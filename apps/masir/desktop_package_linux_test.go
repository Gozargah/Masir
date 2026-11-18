package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	masird "github.com/Gozargah/Masir/libs/masir-daemon"
)

func TestReportUsesACodedExit(t *testing.T) {
	if got := report(coded(11, errors.New("x"))); got != 11 {
		t.Fatalf("coded exit = %d, want 11", got)
	}
	if got := report(errors.New("x")); got != 1 {
		t.Fatalf("plain exit = %d, want 1", got)
	}
}

func TestReleaseBaseOverrideIsLoopbackOnly(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8799", "http://localhost:1", "http://[::1]:2"} {
		t.Setenv("MASIR_DESKTOP_RELEASE_BASE", ok)
		if got, err := releaseBase(); err != nil || got != ok {
			t.Fatalf("%s: got %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"https://evil.example", "http://10.0.0.1:1", "::"} {
		t.Setenv("MASIR_DESKTOP_RELEASE_BASE", bad)
		if got, err := releaseBase(); err == nil {
			t.Fatalf("%s accepted as %q", bad, got)
		}
	}
	t.Setenv("MASIR_DESKTOP_RELEASE_BASE", "")
	if got, err := releaseBase(); err != nil || got != masird.DesktopReleaseBase {
		t.Fatalf("unset: got %q, %v", got, err)
	}
}

// The signing key is baked in at build time, so a real signed release can't be served
// from a test: the download is stubbed and only the command's protocol is checked.
func TestFetchPackagePrintsMachineReadableResult(t *testing.T) {
	old := fetchPackage
	defer func() { fetchPackage = old }()
	var gotProxy string
	fetchPackage = func(_ context.Context, base, version, format, arch, dir, proxy string, progress func(done, total int64)) (string, string, string, error) {
		gotProxy = proxy
		for i := int64(1); i <= 100; i++ { // faster than the throttle
			progress(i, 100)
		}
		return dir + "/p.deb", dir + "/checksums.txt", dir + "/checksums.txt.minisig", nil
	}
	a := args{flags: map[string][]string{"version": {"0.1.7"}, "format": {"deb"}, "dir": {"/d"}, "proxy": {"http://127.0.0.1:1"}}}
	var out bytes.Buffer
	if err := fetchDesktopPackage(&out, a); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	n := len(lines)
	if n < 4 || n > 10 {
		t.Fatalf("got %d lines: %q", n, lines)
	}
	if lines[n-4] != "progress 100 100" || lines[n-3] != "package /d/p.deb" ||
		lines[n-2] != "checksums /d/checksums.txt" || lines[n-1] != "signature /d/checksums.txt.minisig" {
		t.Fatalf("bad tail: %q", lines)
	}
	if gotProxy != "http://127.0.0.1:1" {
		t.Fatalf("proxy = %q", gotProxy)
	}

	fetchPackage = func(context.Context, string, string, string, string, string, string, func(int64, int64)) (string, string, string, error) {
		return "", "", "", masird.ErrPackageMismatch
	}
	err := fetchDesktopPackage(&out, a)
	if code := report(err); code != exitMismatch {
		t.Fatalf("mismatch exit = %d", code)
	}
	a.flags["format"] = []string{"zip"}
	if err := fetchDesktopPackage(&out, a); err == nil {
		t.Fatal("format zip accepted")
	}
}

// installFake stands in for everything install-package touches outside the process:
// the verifier, dpkg, the package manager and the service. Each test changes one part.
type installFake struct {
	in        string // the person's download directory
	staging   string
	pm        []string // paths the package manager was given
	bundled   [][]string
	verified  bool
	pmOutput  string
	pmErr     error
	bundleErr error
}

func newInstallFake(t *testing.T) *installFake {
	t.Helper()
	f := &installFake{in: t.TempDir(), staging: t.TempDir()}
	for _, name := range []string{"masir-desktop-linux-0.1.7-amd64.deb", "checksums-desktop.txt", "checksums-desktop.txt.minisig"} {
		if err := os.WriteFile(filepath.Join(f.in, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	saved := []any{packageStagingParent, packageStagingOwned, verifyPackage, installedPackageVersion, packageMetadata, runPackageManager, serviceInstalled, serviceVersion, runBundled, elevated}
	t.Cleanup(func() {
		packageStagingParent = saved[0].(string)
		packageStagingOwned = saved[1].(func(string) error)
		verifyPackage = saved[2].(func([]byte, []byte, string, string) (string, string, error))
		installedPackageVersion = saved[3].(func(string) (string, error))
		packageMetadata = saved[4].(func(string, string) (string, string, error))
		runPackageManager = saved[5].(func(string, string) (string, error))
		serviceInstalled = saved[6].(func() bool)
		serviceVersion = saved[7].(func() (string, error))
		runBundled = saved[8].(func(...string) error)
		elevated = saved[9].(func() bool)
	})
	packageStagingParent = f.staging
	packageStagingOwned = func(string) error { return nil }
	verifyPackage = func(_, _ []byte, _, _ string) (string, string, error) {
		f.verified = true
		return "0.1.7", "deb", nil
	}
	installedPackageVersion = func(string) (string, error) { return "0.1.6", nil }
	packageMetadata = func(string, string) (string, string, error) { return "masir-desktop", "0.1.7", nil }
	runPackageManager = func(_, path string) (string, error) {
		f.pm = append(f.pm, path)
		return f.pmOutput, f.pmErr
	}
	serviceInstalled = func() bool { return true }
	serviceVersion = func() (string, error) { return "0.1.6", nil }
	runBundled = func(args ...string) error {
		f.bundled = append(f.bundled, args)
		return f.bundleErr
	}
	return f
}

func (f *installFake) install(out io.Writer) error {
	return installDesktopPackage(
		filepath.Join(f.in, "masir-desktop-linux-0.1.7-amd64.deb"),
		filepath.Join(f.in, "checksums-desktop.txt"),
		filepath.Join(f.in, "checksums-desktop.txt.minisig"),
		"1000", out)
}

func exitOf(err error) int {
	if err == nil {
		return 0
	}
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.code
	}
	return 1
}

func TestInstallPackageRejectsWhatIsntTheSignedRelease(t *testing.T) {
	cases := map[string]func(){
		// A test build carries no key, so the real verifier must refuse: it fails closed.
		"real verifier, no key": func() { verifyPackage = masird.VerifyDesktopPackage },
		"bad signature or hash": func() {
			verifyPackage = func(_, _ []byte, _, _ string) (string, string, error) { return "", "", masird.ErrPackageMismatch }
		},
		"wrong package name": func() {
			packageMetadata = func(string, string) (string, string, error) { return "other", "0.1.7", nil }
		},
		"metadata disagrees with the signed version": func() {
			packageMetadata = func(string, string) (string, string, error) { return "masir-desktop", "0.1.8", nil }
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newInstallFake(t)
			change()
			if code := exitOf(f.install(io.Discard)); code != exitMismatch {
				t.Fatalf("exit = %d, want %d", code, exitMismatch)
			}
			if len(f.pm) != 0 {
				t.Fatal("the package manager ran")
			}
		})
	}
}

func TestInstallPackageRejectsADowngrade(t *testing.T) {
	for _, installed := range []string{"0.1.7", "0.1.8"} {
		f := newInstallFake(t)
		installedPackageVersion = func(string) (string, error) { return installed, nil }
		if code := exitOf(f.install(io.Discard)); code != exitNotNewer || len(f.pm) != 0 {
			t.Fatalf("installed %s: exit = %d, package manager runs = %d", installed, code, len(f.pm))
		}
	}
}

// A second install while one runs is a busy installer, and touches nothing of the first.
func TestInstallPackageWaitsForTheRunningInstall(t *testing.T) {
	f := newInstallFake(t)
	running := filepath.Join(f.staging, "masir-desktop.update-running")
	if err := os.Mkdir(running, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := lockPackageInstall(f.staging)
	if err != nil {
		t.Fatal(err)
	}
	if code := exitOf(f.install(io.Discard)); code != exitInstallerBusy || len(f.pm) != 0 {
		t.Fatalf("exit = %d, package manager runs = %d", code, len(f.pm))
	}
	if _, err := os.Stat(running); err != nil {
		t.Fatalf("the running install's staging was removed: %v", err)
	}
	held.Close()
	if err := f.install(io.Discard); err != nil || len(f.pm) != 1 {
		t.Fatalf("after the first install: err = %v, package manager runs = %d", err, len(f.pm))
	}
}

// A package dpkg left half-configured at the target version is repaired, not refused.
func TestInstallPackageRepairsAnIncompleteInstall(t *testing.T) {
	f := newInstallFake(t)
	installedPackageVersion = func(string) (string, error) { return "", nil }
	if err := f.install(io.Discard); err != nil || len(f.pm) != 1 {
		t.Fatalf("err = %v, package manager runs = %d", err, len(f.pm))
	}
}

func TestDebInstalledVersion(t *testing.T) {
	for out, want := range map[string]string{
		"0.1.7\tinstalled\n":      "0.1.7",
		"0.1.7\ttriggers-pending": "0.1.7",
		"0.1.7\thalf-configured":  "",
		"0.1.7\tunpacked":         "",
		"0.1.7\thalf-installed":   "",
		"0.1.7":                   "",
	} {
		if got := debInstalledVersion(out); got != want {
			t.Errorf("%q: got %q, want %q", out, got, want)
		}
	}
}

func TestInstallPackageStagingIsPrivate(t *testing.T) {
	f := newInstallFake(t)
	runPackageManager = func(_, path string) (string, error) {
		f.pm = append(f.pm, path)
		if filepath.Dir(filepath.Dir(path)) != f.staging || strings.HasPrefix(path, f.in) {
			t.Errorf("installing %s, not a copy staged under %s", path, f.staging)
		}
		if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("staging directory: %v %v, want 0700", info.Mode(), err)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != "masir-desktop-linux-0.1.7-amd64.deb" {
			t.Errorf("staged copy = %q, %v", b, err)
		}
		if filepath.Base(path) != "masir-desktop-linux-0.1.7-amd64.deb" {
			t.Errorf("staged name %s: apt needs the .deb name", path)
		}
		return "", nil
	}
	if err := f.install(io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(f.pm) != 1 {
		t.Fatalf("package manager runs = %d", len(f.pm))
	}
	// Only the install lock stays: removing a lock file would race the next install.
	if left, _ := os.ReadDir(f.staging); len(left) != 1 || left[0].Name() != "masir-desktop.update.lock" {
		t.Fatalf("staging left behind: %v", left)
	}
}

func TestInstallPackageRemovesAKilledRunsStaging(t *testing.T) {
	f := newInstallFake(t)
	stale := filepath.Join(f.staging, "masir-desktop.update-123")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.install(io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale staging kept: %v", err)
	}
}

func TestInstallPackageRefusesAnUnsafeStagingParent(t *testing.T) {
	f := newInstallFake(t)
	packageStagingOwned = func(string) error { return errors.New("group-writable") }
	// Every failure is coded, so a signal during the run can't turn it into 130.
	if code := exitOf(f.install(io.Discard)); code != exitInstallFailed {
		t.Fatalf("exit = %d, want %d", code, exitInstallFailed)
	}
	if left, _ := os.ReadDir(f.staging); len(left) != 0 || f.verified {
		t.Fatalf("copied or verified anyway: %v", left)
	}
}

func TestRootOwnedDir(t *testing.T) {
	if err := rootOwnedDir("/"); err != nil && os.Geteuid() == 0 {
		t.Fatalf("/: %v", err)
	}
	if err := rootOwnedDir(t.TempDir()); err == nil && os.Geteuid() != 0 {
		t.Fatal("a directory the person owns was accepted")
	}
}

func TestInstallPackageRefusesALinkOrAPipe(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo"} {
		f := newInstallFake(t)
		pkg := filepath.Join(f.in, "masir-desktop-linux-0.1.7-amd64.deb")
		os.Remove(pkg)
		var err error
		if kind == "symlink" {
			err = os.Symlink(filepath.Join(f.in, "checksums-desktop.txt"), pkg)
		} else {
			err = syscall.Mkfifo(pkg, 0o644) // must not block root waiting for a writer
		}
		if err != nil {
			t.Fatal(err)
		}
		if code := exitOf(f.install(io.Discard)); code != exitMismatch || f.verified {
			t.Fatalf("%s: exit = %d, verified = %v", kind, code, f.verified)
		}
	}
}

func TestInstallPackageReportsABusyInstaller(t *testing.T) {
	f := newInstallFake(t)
	f.pmOutput, f.pmErr = "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 42 (apt)\n", errors.New("exit status 100")
	if code := exitOf(f.install(io.Discard)); code != exitInstallerBusy {
		t.Fatalf("exit = %d, want %d", code, exitInstallerBusy)
	}
	f = newInstallFake(t)
	f.pmOutput, f.pmErr = "E: Sub-process /usr/bin/dpkg returned an error code (1)\n", errors.New("exit status 100")
	if err := f.install(io.Discard); exitOf(err) != exitInstallFailed || !strings.Contains(err.Error(), "returned an error code") {
		t.Fatalf("err = %v, want exit %d with the last line", err, exitInstallFailed)
	}
}

func TestInstallPackageRunsImportThenSyncUnit(t *testing.T) {
	f := newInstallFake(t)
	if err := f.install(io.Discard); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"daemon", "import-desktop", "--from", "/opt/masir-desktop/bin", "--user", "1000"},
		{"daemon", "sync-unit", "--user", "1000"},
	}
	if fmt.Sprint(f.bundled) != fmt.Sprint(want) {
		t.Fatalf("bundled runs = %v, want %v", f.bundled, want)
	}
}

func TestInstallPackageKeepsTheAppWhenTheServiceStepFails(t *testing.T) {
	f := newInstallFake(t)
	f.bundleErr = errors.New("import failed")
	if code := exitOf(f.install(io.Discard)); code != exitServicePending || len(f.pm) != 1 {
		t.Fatalf("exit = %d, package manager runs = %d", code, len(f.pm))
	}
}

func TestInstallPackageSkipsTheServiceWhenItIsCurrentOrAbsent(t *testing.T) {
	f := newInstallFake(t)
	serviceVersion = func() (string, error) { return "0.1.7", nil }
	if err := f.install(io.Discard); err != nil || len(f.bundled) != 0 {
		t.Fatalf("current service: %v, bundled runs %v", err, f.bundled)
	}
	f = newInstallFake(t)
	serviceInstalled = func() bool { return false }
	if err := f.install(io.Discard); err != nil || len(f.bundled) != 0 {
		t.Fatalf("no service: %v, bundled runs %v", err, f.bundled)
	}
}

func TestInstallPackagePrintsAuthorizedFirst(t *testing.T) {
	f := newInstallFake(t)
	var out bytes.Buffer
	if err := f.install(&out); err != nil {
		t.Fatal(err)
	}
	if first, _, _ := strings.Cut(out.String(), "\n"); first != "authorized" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestInstallPackageNeedsRoot(t *testing.T) {
	f := newInstallFake(t)
	elevated = func() bool { return false }
	a := args{flags: map[string][]string{
		"package":   {filepath.Join(f.in, "masir-desktop-linux-0.1.7-amd64.deb")},
		"checksums": {filepath.Join(f.in, "checksums-desktop.txt")},
		"signature": {filepath.Join(f.in, "checksums-desktop.txt.minisig")},
		"user":      {"1000"},
	}}
	if err := installPackageCommand(a); err == nil || f.verified || len(f.pm) != 0 {
		t.Fatalf("err = %v, verified = %v: ran without root", err, f.verified)
	}
	elevated = func() bool { return true }
	for _, user := range []string{"+1000", "-1", "me"} {
		a.flags["user"] = []string{user}
		if err := installPackageCommand(a); err == nil || f.verified {
			t.Fatalf("--user %s accepted", user)
		}
	}
}
