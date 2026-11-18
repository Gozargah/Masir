package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/Gozargah/Masir/libs/masir-core/client"
	"github.com/Gozargah/Masir/libs/masir-core/paths"
	"github.com/Gozargah/Masir/libs/masir-core/service"
	masird "github.com/Gozargah/Masir/libs/masir-daemon"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

var fetchPackage = masird.FetchDesktopPackage

// desktopCommands is the hidden `masir desktop` group the packaged Linux app runs.
func desktopCommands() []*cobra.Command {
	fetch := leaf("fetch-package", "Download and verify a desktop package", func(a args) error {
		return fetchDesktopPackage(os.Stdout, a)
	}, cobra.NoArgs,
		flagSpec{names: []string{"version"}, help: "The release version"},
		flagSpec{names: []string{"format"}, help: "deb or rpm"},
		flagSpec{names: []string{"dir"}, help: "Where to put the files"},
		flagSpec{names: []string{"proxy"}, help: "A running local proxy to try when direct fails"})
	fetch.Hidden = true
	install := leaf("install-package", "Install a verified desktop package as root", installPackageCommand, cobra.NoArgs,
		flagSpec{names: []string{"package"}, help: "The downloaded package"},
		flagSpec{names: []string{"checksums"}, help: "Its signed checksum list"},
		flagSpec{names: []string{"signature"}, help: "The list's signature"},
		flagSpec{names: []string{"user"}, help: "The uid the service serves"})
	install.Hidden = true
	desktop := group("desktop", "Desktop app helpers", fetch, install)
	desktop.Hidden = true
	return []*cobra.Command{desktop}
}

// installPackageCommand is `masir desktop install-package`. There is no self-elevation:
// the app runs it through pkexec, and by hand the update is `apt install`.
func installPackageCommand(a args) error {
	pkg, sums, sig, user := a.str("package"), a.str("checksums"), a.str("signature"), a.str("user")
	if pkg == "" || sums == "" || sig == "" {
		return errors.New("install-package needs --package, --checksums and --signature")
	}
	if _, err := strconv.ParseUint(user, 10, 32); err != nil {
		return errors.New("--user names the uid the service serves")
	}
	if !elevated() {
		return errors.New("install-package runs as root")
	}
	// The app may close its pipes while this runs: a failed write must not kill root
	// halfway through, so SIGPIPE turns into EPIPE instead.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	return installDesktopPackage(pkg, sums, sig, user, os.Stdout)
}

// releaseBase is the release host, or the loopback override a test or development
// build may set. It never lets MASIR_DESKTOP_RELEASE_BASE point at a remote host.
func releaseBase() (string, error) {
	v := os.Getenv("MASIR_DESKTOP_RELEASE_BASE")
	if v == "" {
		return masird.DesktopReleaseBase, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", fmt.Errorf("MASIR_DESKTOP_RELEASE_BASE: %w", err)
	}
	if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("MASIR_DESKTOP_RELEASE_BASE must be on the loopback address, not %q", u.Hostname())
	}
	return v, nil
}

// fetchDesktopPackage writes only the protocol the app parses to out: progress lines,
// then the package, checksum and signature paths. Errors go to stderr through report.
func fetchDesktopPackage(out io.Writer, a args) error {
	version, format, dir := a.str("version"), a.str("format"), a.str("dir")
	if version == "" || dir == "" || (format != "deb" && format != "rpm") {
		return errors.New("fetch-package needs --version, --dir and --format deb|rpm")
	}
	base, err := releaseBase()
	if err != nil {
		return err
	}
	var last time.Time
	pkg, sums, sig, err := fetchPackage(appCtx, base, version, format, runtime.GOARCH, dir, a.str("proxy"), func(done, total int64) {
		if now := time.Now(); now.Sub(last) >= 100*time.Millisecond || (total > 0 && done >= total) {
			last = now
			fmt.Fprintf(out, "progress %d %d\n", done, total)
		}
	})
	switch {
	case errors.Is(err, masird.ErrReleaseUnreachable):
		return coded(exitUnreachable, err)
	case errors.Is(err, masird.ErrPackageMismatch):
		return coded(exitMismatch, err)
	case err != nil:
		return err
	}
	fmt.Fprintf(out, "package %s\nchecksums %s\nsignature %s\n", pkg, sums, sig)
	return nil
}

// packagedBinary is the CLI the .deb and .rpm install. After the package manager runs it
// is the new version, so the service is imported from it rather than from this process.
const packagedBinary = "/opt/masir-desktop/bin/masir"

// The size caps keep a root copy of a person's file bounded.
const (
	maxPackageSize   = 256 << 20
	maxChecksumsSize = 1 << 20
	maxSignatureSize = 2048
)

// Seams, so tests run the whole order of checks without root, dpkg or a service.
var (
	packageStagingParent    = "/var/lib"
	packageStagingOwned     = rootOwnedDir
	verifyPackage           = masird.VerifyDesktopPackage
	installedPackageVersion = queryInstalledPackage
	packageMetadata         = readPackageMetadata
	runPackageManager       = execPackageManager
	serviceInstalled        = service.Installed
	serviceVersion          = installedServiceVersion
	runningServiceVersion   = liveServiceVersion
	restartServiceNow       = restartInstalledService
	syncPackageServiceUnit  = reconcilePackageServiceUnit
	runBundled              = execBundled
)

// installDesktopPackage installs the package the app downloaded, as root. Nothing the
// person can still write is trusted: the three files are copied into a root-only
// directory, and that copy is verified, parsed and installed. The service then takes
// the new bundle's components through the new CLI, and its unit is reconciled here so
// the app never asks for a second password.
func installDesktopPackage(pkg, sums, sig, user string, out io.Writer) (err error) {
	// Every failure carries a code the app reads; nothing ends as a bare 1, or as 130
	// when a signal arrived while the package manager ran.
	defer func() {
		var ce *codedError
		if err != nil && !errors.As(err, &ce) {
			err = coded(exitInstallFailed, err)
		}
	}()
	// The password was accepted: the app leaves "Enter Password…".
	fmt.Fprintln(out, "authorized")

	if err := packageStagingOwned(packageStagingParent); err != nil {
		return fmt.Errorf("refusing to stage the update in %s: %w", packageStagingParent, err)
	}
	// One install at a time: the cleanup below would delete a running one's staging.
	// The kernel drops the lock when this process ends, however it ends.
	lock, err := lockPackageInstall(packageStagingParent)
	if err != nil {
		return err
	}
	installLock = lock
	defer func() { installLock = nil; lock.Close() }()
	// A killed run leaves its staging directory behind; the parent is root's alone.
	if old, _ := filepath.Glob(filepath.Join(packageStagingParent, "masir-desktop.update-*")); len(old) > 0 {
		for _, d := range old {
			os.RemoveAll(d)
		}
	}
	dir, err := os.MkdirTemp(packageStagingParent, "masir-desktop.update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	staged := filepath.Join(dir, "package")
	for _, f := range []struct {
		from, to string
		max      int64
	}{
		{pkg, staged, maxPackageSize},
		{sums, filepath.Join(dir, "checksums"), maxChecksumsSize},
		{sig, filepath.Join(dir, "signature"), maxSignatureSize},
	} {
		if err := copyPrivate(f.from, f.to, f.max); err != nil {
			return coded(exitMismatch, err)
		}
	}
	list, err := os.ReadFile(filepath.Join(dir, "checksums"))
	if err != nil {
		return err
	}
	signature, err := os.ReadFile(filepath.Join(dir, "signature"))
	if err != nil {
		return err
	}
	version, format, err := verifyPackage(list, signature, staged, runtime.GOARCH)
	if err != nil {
		return coded(exitMismatch, err)
	}
	version = strings.TrimPrefix(version, "v")
	// apt and dnf take a local file by its extension.
	named := filepath.Join(dir, masird.DesktopPackageName(version, format, runtime.GOARCH))
	if err := os.Rename(staged, named); err != nil {
		return err
	}

	// Only a verified file is handed to a parser.
	name, inside, err := packageMetadata(format, named)
	if err != nil {
		return coded(exitMismatch, err)
	}
	if name != "masir-desktop" || strings.TrimPrefix(inside, "v") != version {
		return coded(exitMismatch, fmt.Errorf("the package is %s %s, not masir-desktop %s", name, inside, version))
	}
	installed, complete, err := installedPackageVersion(format)
	if err != nil {
		return coded(exitInstallFailed, err)
	}
	// A package dpkg holds unpacked or half-configured may be finished at its own
	// version, rather than leaving the person stuck on "already installed"; it is never
	// replaced by an older one.
	if complete && !newerVersion(version, installed) || !complete && newerVersion(installed, version) {
		return coded(exitNotNewer, fmt.Errorf("masir-desktop %s is installed; %s isn't newer", installed, version))
	}

	if output, err := runPackageManager(format, named); err != nil {
		if packageManagerBusy(format, output) {
			return coded(exitInstallerBusy, errors.New("another installer is running"))
		}
		return coded(exitInstallFailed, fmt.Errorf("%s: %s", err, lastLine(output)))
	}

	// No service: the app restarts its bundled daemon itself.
	if !serviceInstalled() {
		return nil
	}
	// Already current on disk (the drift case): importing would only restart it for
	// nothing. But an earlier update can replace the binary without restarting the
	// process, and the app waits for the running daemon: restart one that answers with
	// an older version. One that doesn't answer is stopped, which the person may have
	// chosen, or still starting from the current binary: leave it alone.
	if current, err := serviceVersion(); err == nil && !newerVersion(version, current) {
		running, runningErr := runningServiceVersion(user)
		restarted, err := syncPackageServiceUnit(user)
		if err != nil {
			return coded(exitUnitPending, err)
		}
		if !restarted && runningErr == nil && newerVersion(version, running) {
			if err := restartServiceNow(); err != nil {
				return coded(exitServicePending, err)
			}
		}
		return nil
	}
	if err := runBundled("daemon", "import-desktop", "--from", filepath.Dir(packagedBinary), "--user", user); err != nil {
		return coded(exitServicePending, err)
	}
	if err := runBundled("daemon", "sync-unit", "--user", user); err != nil {
		return coded(exitUnitPending, err)
	}
	return nil
}

func reconcilePackageServiceUnit(user string) (bool, error) {
	var restarted bool
	err := syncServiceUnitWith(user, func(path, text string) error {
		var err error
		restarted, err = applyLinuxServiceDefinition(path, text, false)
		return err
	})
	return restarted, err
}

// lockPackageInstall takes the install lock in parent, or fails as a busy installer
// when another install-package holds it. Its name doesn't match the staging glob.
func lockPackageInstall(parent string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(parent, "masir-desktop.update.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, coded(exitInstallerBusy, errors.New("another Masir update is installing"))
		}
		return nil, err
	}
	return f, nil
}

// copyPrivate copies a regular file, not a link, device or pipe, into a new 0600 file,
// reading at most max bytes.
func copyPrivate(from, to string, max int64) error {
	// Look before opening: opening a device can have side effects of its own.
	before, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a regular file", from)
	}
	// O_NONBLOCK so a pipe swapped in now can't hold root; it doesn't affect regular files.
	src, err := os.OpenFile(from, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Size() > max {
		return fmt.Errorf("%s isn't a regular file of at most %d bytes", from, max)
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(dst, io.LimitReader(src, max+1))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = fmt.Errorf("%s grew past %d bytes", from, max)
	}
	return err
}

// rootOwnedDir refuses a staging parent anyone but root could rename things in.
func rootOwnedDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || st.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("it isn't a directory only root can write")
	}
	return nil
}

// newerVersion compares plain release versions. MyGo writes a pre-release .deb version
// with "~", which isn't semver; releases are plain numbers, and apt refuses a downgrade.
func newerVersion(a, b string) bool {
	canon := func(v string) string { return "v" + strings.TrimPrefix(v, "v") }
	return semver.IsValid(canon(a)) && semver.Compare(canon(a), canon(b)) > 0
}

// queryInstalledPackage returns the installed version, and whether it is fully
// installed: a failed configure leaves a .deb half-configured.
func queryInstalledPackage(format string) (version string, complete bool, err error) {
	var cmd *exec.Cmd
	if format == "deb" {
		cmd = rootCommand("/usr/bin/dpkg-query", "-W", "-f=${Version}\t${db:Status-Status}", "masir-desktop")
	} else {
		cmd = rootCommand("/usr/bin/rpm", "-q", "--queryformat", "%{VERSION}", "masir-desktop")
	}
	b, err := cmd.Output()
	if err != nil {
		return "", false, fmt.Errorf("masir-desktop isn't installed as a %s: %w", format, err)
	}
	if format == "deb" {
		return debInstalledVersion(string(b))
	}
	return strings.TrimSpace(string(b)), true, nil
}

// debInstalledVersion reads dpkg-query's "<version>\t<status>". Pending triggers still
// mean installed; any other state is one apt should be allowed to finish.
func debInstalledVersion(out string) (version string, complete bool, err error) {
	version, status, ok := strings.Cut(strings.TrimSpace(out), "\t")
	if !ok || version == "" || status == "" {
		return "", false, fmt.Errorf("unexpected dpkg-query output %q", out)
	}
	switch status {
	case "installed", "triggers-pending", "triggers-awaited":
		return version, true, nil
	}
	return version, false, nil
}

func readPackageMetadata(format, path string) (name, version string, err error) {
	var cmd *exec.Cmd
	if format == "deb" {
		cmd = rootCommand("/usr/bin/dpkg-deb", "--showformat=${Package}\n${Version}", "--show", path)
	} else {
		cmd = rootCommand("/usr/bin/rpm", "-qp", "--queryformat", "%{NAME}\n%{VERSION}", path)
	}
	b, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("read the package: %w", err)
	}
	name, version, _ = strings.Cut(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(name), strings.TrimSpace(version), nil
}

func execPackageManager(format, path string) (string, error) {
	var cmd *exec.Cmd
	if format == "deb" {
		// Lock timeout 0: a busy dpkg fails now and the app says so, instead of hanging.
		cmd = rootCommand("/usr/bin/apt-get", "install", "-y", "--no-install-recommends", "-o", "DPkg::Lock::Timeout=0", path)
		cmd.Env = append(cmd.Env, "DEBIAN_FRONTEND=noninteractive")
	} else {
		// ponytail: untested on Fedora; dnf may wait on its lock or want --nogpgcheck for
		// an unsigned local RPM (the minisign check above already covers this exact copy).
		cmd = rootCommand("/usr/bin/dnf", "install", "-y", path)
	}
	out, err := cmd.CombinedOutput()
	os.Stderr.Write(out)
	return string(out), err
}

func packageManagerBusy(format, output string) bool {
	if format == "deb" {
		return strings.Contains(output, "Could not get lock") || strings.Contains(output, "Unable to acquire the dpkg frontend lock")
	}
	return strings.Contains(strings.ToLower(output), "lock")
}

// installedServiceVersion asks the service's binary, which prints "masir version <v>".
// liveServiceVersion asks the running service that serves user for its version, over
// that user's privileged socket.
func liveServiceVersion(user string) (string, error) {
	uid, err := strconv.Atoi(user)
	if err != nil {
		return "", err
	}
	conn, c, err := client.DialSocket(paths.PrivilegedSocketPath(uid))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.GetStatus(ctx, &pb.GetStatusRequest{})
	if err != nil {
		return "", err
	}
	return res.GetStatus().GetVersion(), nil
}

// restartInstalledService restarts the running service through systemd, which runs the
// binary on disk. try-restart never starts a unit that isn't running.
func restartInstalledService() error {
	if out, err := rootCommand("/usr/bin/systemctl", "try-restart", "masird.service").CombinedOutput(); err != nil {
		return fmt.Errorf("restart the service: %w: %s", err, lastLine(string(out)))
	}
	return nil
}

func installedServiceVersion() (string, error) {
	b, err := rootCommand(service.BinaryPath(), "--version").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) != 3 || fields[0] != "masir" || fields[1] != "version" {
		return "", errors.New("the service binary has no version")
	}
	return fields[2], nil
}

// execBundled runs the package's (new) CLI. Its output is buffered, then copied to
// stderr: stdout is the protocol the app reads, and a closed pipe must not kill the
// child between stopping and starting the service.
func execBundled(args ...string) error {
	cmd := rootCommand(packagedBinary, args...)
	out, err := cmd.CombinedOutput()
	os.Stderr.Write(out)
	if err != nil {
		return fmt.Errorf("%s %s: %w", packagedBinary, strings.Join(args, " "), err)
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// rootCommand runs a tool by absolute path with a fixed environment: root must not take
// PATH, or anything else, from the person who started it (sudo -E, su without -).
func rootCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LC_ALL=C"}
	if installLock != nil {
		// apt, dpkg and the service import hold the install lock too: flock lasts
		// until every copy of the descriptor closes, so a killed install-package
		// can't let the next one delete staging they still use.
		cmd.ExtraFiles = []*os.File{installLock}
	}
	return cmd
}

// installLock is the lock install-package holds while it runs, nil otherwise.
var installLock *os.File
