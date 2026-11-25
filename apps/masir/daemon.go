package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/Gozargah/Masir/apps/masir/internal/ui"
	"github.com/Gozargah/Masir/libs/masir-core/client"
	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/paths"
	"github.com/Gozargah/Masir/libs/masir-core/service"
	masird "github.com/Gozargah/Masir/libs/masir-daemon"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

var errNoService = errors.New("installing the masir daemon as a service isn't supported on this OS yet")

func daemonStart(args) error {
	if service.Installed() { // the service is the supervisor; start it, not a second daemon
		s := ui.Spin("Starting the masir service (" + elevationHint + ")")
		if err := restartService(); err != nil {
			s.Fail(err.Error())
			return errReported
		}
		s.Stop("The masir service is running.")
		return nil
	}
	if daemonAlive() {
		ui.Info("The masir daemon is already running.")
		return nil
	}
	s := ui.Spin("Starting the masir daemon")
	if err := ensureDaemon(false); err != nil {
		failWith(s, "", err)
		return errReported
	}
	s.Stop("Daemon started in the background.")
	return nil
}

func daemonStop(args) error {
	c, ok := runningDaemon()
	if !ok {
		ui.Info("The masir daemon isn't running.")
		return nil
	}
	ctx, cancel := rpc()
	defer cancel()
	if _, err := c.Shutdown(ctx, &pb.ShutdownRequest{}); err != nil {
		return err
	}
	ui.Success("Daemon stopped. Proxy and TUN are off until it starts again.")
	return nil
}

func daemonRestart(a args) error {
	if service.Installed() {
		s := ui.Spin("Restarting the masir service (" + elevationHint + ")")
		if err := restartService(); err != nil {
			s.Fail(err.Error())
			return errReported
		}
		s.Stop("Service restarted.")
		return nil
	}
	// Without a service this starts a session daemon, which a root client can never
	// reach (see rootRefusal); the app and the service manager are the ways to do that.
	if err := rootRestartRefusal(); err != nil {
		return err
	}
	if c, ok := runningDaemon(); ok {
		ctx, cancel := rpc()
		_, err := c.Shutdown(ctx, &pb.ShutdownRequest{})
		cancel()
		if err != nil {
			return err
		}
		if err := waitFor(func() bool { return !daemonAlive() }, 10*time.Second, "the daemon didn't stop"); err != nil {
			return err
		}
	}
	return daemonStart(a)
}

func daemonEnable(a args) error {
	user := a.str("user")
	if user == "" {
		user = currentUser()
	}
	return enableService(user, a.boolean("confirmed", false))
}

// daemonLabel relabels the system service's copy of masir for SELinux and restarts
// the service so the new domain applies: the one-command repair when an update or a
// system relabel left the binary with a label that starts the daemon confined
// (docs/selinux.md).
func daemonLabel(args) error {
	if !service.Installed() {
		return fail("no service is installed, so there is no service binary to relabel")
	}
	if !elevated() {
		return elevate("daemon", "label")
	}
	if err := service.EnsureBinaryLabel(service.BinaryPath()); err != nil {
		return err
	}
	if err := restartService(); err != nil {
		return err
	}
	ui.Success("The service binary is labeled for SELinux and the service restarted.")
	return nil
}

// daemonRollback restores the service's previous binary after an update that installed
// but didn't start. `masir update` keeps it beside the service's copy.
func daemonRollback(args) error {
	if !service.Installed() {
		return fail("no service is installed, so there is no previous service version to restore")
	}
	if !elevated() {
		return elevate("daemon", "rollback")
	}
	prev := service.BinaryPath() + ".previous"
	if _, err := os.Stat(prev); err != nil {
		return fail("there is no previous version to restore")
	}
	if err := copyFile(prev, service.BinaryPath(), 0o755); err != nil {
		return err
	}
	if same, err := sameContent(prev, service.BinaryPath()); err != nil || !same {
		return fail("the restored copy doesn't match the saved version")
	}
	// Combined updates save the active geo pair beside the service binary. Older
	// binary-only updates have no pair to restore.
	assets := filepath.Dir(service.BinaryPath())
	ip := filepath.Join(assets, "geoip.dat")
	site := filepath.Join(assets, "geosite.dat")
	if _, err := os.Stat(ip + ".previous"); err == nil {
		if _, err := os.Stat(site + ".previous"); err == nil {
			for _, dst := range []string{ip, site} {
				if err := copyFile(dst+".previous", dst, 0o644); err != nil {
					return err
				}
			}
			meta := filepath.Join(assets, geopack.InstalledName)
			if _, err := os.Stat(meta + ".previous"); err == nil {
				if err := copyFile(meta+".previous", meta, 0o600); err != nil {
					return err
				}
			} else if os.IsNotExist(err) {
				if err := os.Remove(meta); err != nil && !os.IsNotExist(err) {
					return err
				}
			} else {
				return err
			}
		}
	}
	// The restore rewrote the binary, so the SELinux label of the fresh copy applies.
	if err := labelServiceBinary(service.BinaryPath()); err != nil {
		return err
	}
	if err := restartService(); err != nil {
		return err
	}
	ui.Success("The previous masir version is running.")
	return nil
}

// daemonDisable removes the service in the order that leaves nothing holding the ports.
//
// The service's daemon has to stop before anything else: a supervisor's own stop
// (launchd's bootout, systemd's disable --now, the SCM's Delete) answers as soon as the
// job is gone, while the daemon it stopped still holds the proxy port and the TUN. The
// app starts the bundled daemon the moment this returns, so a return that didn't wait for
// the ports is what showed "proxy: listen tcp 0.0.0.0:7890: bind: address already in
// use" on a machine whose only proxy was the one being removed.
//
// --user names the user the service serves, so root finds that user's settings; --ports
// waits for the ports those settings ask for.
func daemonDisable(a args) error {
	user := a.str("user")
	if !elevated() {
		if user == "" {
			user = currentUser()
		}
		// The elevated copy is a new process: forward the options the caller chose, or a
		// `--ports=false` here would still wait for ports there.
		argv := []string{"daemon", "disable", "--user", user}
		if !a.boolean("ports", true) {
			argv = append(argv, "--ports=false")
		}
		if a.boolean("stopped", false) {
			argv = append(argv, "--stopped")
		}
		return elevate(argv...)
	}
	// Root can't be the user the service serves: its socket and settings belong to a
	// regular account. sudo records that account in SUDO_UID, which is where the daemon
	// itself looks when it is given no --user, so a bare `sudo masir daemon disable`
	// names the same user it serves. A permission prompt that isn't sudo (the app's
	// osascript or pkexec) records nothing, and names the uid with --user instead.
	if user == "" {
		user = sudoUser()
	}
	if user == "" || user == "0" {
		return advised("This needs your normal user, not root: masir asks for permission itself.",
			"Run it again without sudo:", "masir daemon disable")
	}
	waitPorts := a.boolean("ports", true)
	// Stop it over the socket while it still answers: the reply waits for Xray's TUN and
	// proxy to be down, which is the half a supervisor's stop never waits for.
	if !a.boolean("stopped", false) {
		stopBeforeRemove(user)
	}
	if err := removeServiceFn(); err != nil {
		return err
	}
	if err := removeInstallDir(); err != nil {
		return err
	}
	if waitPorts {
		if err := waitPortsFree(user, removePortTimeout); err != nil {
			// The detail is the bind error itself, which names the port and host: that is
			// what the person needs to find whatever still holds it.
			return &userError{
				headline: "The masir service is removed, but the ports it used are still taken, so the next daemon would start without the proxy.",
				details:  err.Error(),
				advice:   advice{text: "Quit whatever still holds the port, then start masir again:", cmds: []string{"masir connect"}},
			}
		}
	}
	ui.Success("The masir service is removed. The daemon runs in the background again when masir needs it.")
	return nil
}

// The machine calls removing the service makes, replaceable in tests so they never touch
// the machine they run on.
var (
	waitPortsFree    = masird.WaitPortsFree
	removeServiceFn  = uninstallService
	removeInstallDir = func() error { return os.RemoveAll(service.InstallDir()) }
)

// removePortTimeout bounds the wait for the removed service's ports. Xray closes its
// listeners in BeginShutdown, before it answers; the rest is the time a process needs to
// be reaped, and past this the failure is worth reporting rather than hiding.
const removePortTimeout = 15 * time.Second

// stopBeforeRemove asks the service's daemon to exit and waits for its socket. It runs as
// root, so it must name the socket by the uid the service serves: root's own sockets are
// somewhere else (see rootRefusal). Nothing answering is not a failure — a service that
// was already stopped has nothing left to stop.
func stopBeforeRemove(user string) {
	uid, err := strconv.Atoi(user)
	if err != nil {
		return // not a uid: this OS names its sockets another way
	}
	if err := stopTheDaemon(paths.PrivilegedSocketPath(uid)); err != nil {
		ui.Note("The service's daemon didn't answer the stop; removing the service anyway.", ui.Yellow("▲ Service"))
	}
}

// daemonCheck answers a running daemon that is about to install this version: stdin
// holds the user's rules and where the geo data is, stdout gets the rules this version
// can't build. It changes nothing.
//
// The daemon runs it with pipes. Typed at a terminal it would wait on stdin for ever, which
// looks like a hang, so it says what it is instead.
func daemonCheck(args) error {
	if ui.Interactive() {
		return advised("This is an internal command: the daemon runs it on a new version before installing it.",
			"To check your own rules, run:", "masir rules check")
	}
	return masird.CheckRules(os.Stdin, os.Stdout)
}

// daemonRun runs the daemon in this process, in the foreground: it's how the background
// daemon and the root service run (both start this same executable).
func daemonRun(a args) error {
	return runDaemon(masird.Options{User: a.str("user"), Idle: a.boolean("idle", false), Version: version})
}

// ensureDaemon starts the daemon in the background, as this user, unless something
// already answers (a background daemon or the root service). An idle daemon keeps Xray
// down until masir is turned on: commands that only need the daemon for subscriptions,
// rules or updates don't start proxying. `masir daemon start` starts it normally,
// following the saved settings.
//
// An installed service is the supervisor, never a second daemon beside it: the app asks
// the service manager for those, and the CLI must not undo that by spawning its own.
func ensureDaemon(idle bool) error {
	if service.Installed() {
		// The service is the supervisor. Starting anything here would race it for the
		// socket, and a takeover stops its daemon cleanly — which the service manager
		// does not restart (Restart=on-failure treats a clean exit as intended).
		if daemonAlive() {
			return nil
		}
		return advised("The masir service is installed but isn't running.", "Start it with:", "masir daemon start")
	}
	bin, err := self()
	if err != nil {
		return err
	}
	return client.Start(appCtx, bin, idle)
}

// enableService installs the daemon as a privileged service serving user (a uid, or a
// SID on Windows). Run as the user, it re-runs itself with privileges: sudo asks for
// the password, or Windows for permission.
func enableService(user string, confirmed bool) error {
	src, err := self()
	if err != nil {
		return err
	}
	if !elevated() {
		if err := preflightGeoForInstall(src); err != nil {
			return err
		}
		user := installUser()
		// Ask and stop here, as the person: after sudo the daemon is found by the uid it
		// serves, not by this process's own sockets.
		approved, err := stopBeforeInstall(user, confirmed)
		if err != nil {
			return err
		}
		argv := []string{"daemon", "enable", "--user", user}
		if confirmed || approved {
			argv = append(argv, "--confirmed")
		}
		return elevate(argv...)
	}
	if user == "" || user == "0" {
		return advised("This needs your normal user, not root: masir asks for permission itself.", "Run it again without sudo:", "masir daemon enable")
	}
	if !service.Supported() {
		return errNoService
	}
	if err := preflightGeoForInstall(src); err != nil {
		return err
	}
	if _, err := stopBeforeInstall(user, confirmed); err != nil {
		return err
	}
	if _, err := installBinary(src); err != nil {
		return err
	}
	if err := installService(service.BinaryPath(), user); err != nil {
		return err
	}
	ui.Success("masir is installed as a system service. It starts at boot, and TUN mode needs no permission prompt from now on.")
	return nil
}

// preflightInstallGeo validates geo data before stopping any running daemon so a
// missing or incomplete geo pair cannot leave the user's daemon stopped.
func preflightInstallGeo(src string) error {
	dir := service.InstallDir()
	managedGeo, err := geopack.Managed(dir)
	if err != nil {
		if !elevated() && errors.Is(err, os.ErrPermission) {
			// The elevated invocation will re-run this check with root rights before
			// stopping any daemon.
			return nil
		}
		return err
	}
	assets := filepath.Dir(src)
	if geopack.Packed(assets) {
		stage, err := os.MkdirTemp("", "masir-geo-preflight-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		if err := geopack.Expand(assets, stage); err != nil {
			return err
		}
		assets = stage
	}
	_, err = preflightServiceGeo(assets, dir, managedGeo)
	return err
}

// stopBeforeInstall asks before taking down the daemon that would hold the install up,
// then stops it over the socket. The service manager's own stop (launchd's bootout,
// systemd's stop) sends a signal, and that waits on the streams a client keeps open —
// the app holds two — so asking over the socket stops it at once.
//
// It reports whether a running daemon was actually stopped (so the caller knows whether
// stop consent was given), distinguishing that from the no-daemon case.
//
// There is a prompt only in a terminal: the app's Install Service is the person's answer
// already, and its pkexec'd masir has no terminal to ask on. The elevated CLI
// inherits the original confirmation only when consent was given, and still prompts or
// stops a daemon that restarted while sudo was asking for permission.
func stopBeforeInstall(user string, confirmed bool) (bool, error) {
	sock, ok := socketForInstall(user)
	if !ok {
		return false, nil
	}
	if !confirmed {
		stop, err := confirmInstallStop("A masir daemon is already running. Stop it and install the service?", true)
		if err != nil {
			return false, err
		}
		if !stop {
			return false, advised("Nothing was installed: a running daemon owns the socket, and the service can't take it over while it does.",
				"Install it when you're ready:", "masir daemon enable")
		}
	}
	s := ui.Spin("Stopping the masir daemon")
	if err := stopTheDaemon(sock); err != nil {
		s.Fail(err.Error())
		return false, errReported
	}
	s.Stop("Daemon stopped.")
	return true, nil
}

// socketForInstall is where the daemon the install must stop answers. As the person that
// is the live socket; as root it is the service's socket for the uid it serves, which is
// why the lookup takes the uid rather than trusting root's own.
func socketForInstall(user string) (string, bool) {
	if sock, ok := liveDaemonSocket(); ok {
		return sock, true
	}
	if !elevated() {
		return "", false
	}
	uid, err := strconv.Atoi(user)
	if err != nil {
		return "", false
	}
	sock := paths.PrivilegedSocketPath(uid)
	return sock, client.AliveAt(sock)
}

// stopDaemonAt asks the daemon on sock to exit and waits until it has let go of it.
func stopDaemonAt(sock string) error {
	conn, c, err := client.DialSocket(sock)
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(appCtx, 5*time.Second)
	defer cancel()
	// A daemon that exits before answering is stopped too; the socket is what counts.
	_, _ = c.Shutdown(ctx, &pb.ShutdownRequest{})
	return waitFor(func() bool { return !client.AliveAt(sock) }, 10*time.Second, "the daemon didn't stop")
}

// liveDaemonSocket, stopTheDaemon, and preflightGeoForInstall are the machine calls
// an install makes. Tests replace them so they never touch the host machine.
var (
	confirmInstallStop     = ui.Confirm
	liveDaemonSocket       = client.LiveSocket
	stopTheDaemon          = stopDaemonAt
	preflightGeoForInstall = preflightInstallGeo
	installUser            = currentUser
)

// enableTun turns TUN on, elevating the daemon first when it isn't root.
func enableTun() error {
	if err := ensurePrivileged(); err != nil {
		return err
	}
	_, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetTunEnabledResponse, error) {
		return c.SetTunEnabled(ctx, &pb.SetTunEnabledRequest{Enabled: true})
	})
	return err
}

// ensurePrivileged makes sure a privileged daemon is answering, as TUN needs. If the
// one running isn't, it starts the daemon as root (Administrator on Windows) for this
// session, which takes over from the background daemon. It installs nothing: `masir
// daemon enable` is the user's choice, offered once it worked.
func ensurePrivileged() error {
	st, err := currentStatus()
	if err != nil {
		return err
	}
	if st.Privileged {
		return nil
	}
	if !canElevateSilently() { // only warn when a prompt is actually coming
		ui.Note("TUN mode routes all system traffic, which needs "+privilegeName+".\n"+elevationNote,
			ui.Yellow("▲ Elevated Privileges Required"))
	}
	dir, err := paths.ConfigDir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "masird.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = startElevatedDaemon(currentUser(), logf)
	logf.Close()
	if err != nil {
		return err
	}

	cachedClient = nil // the privileged daemon takes over the socket
	s := ui.Spin("Starting the daemon with " + privilegeName)
	err = waitFor(func() bool {
		c, ok := runningDaemon()
		if !ok {
			return false
		}
		ctx, cancel := context.WithTimeout(appCtx, time.Second)
		defer cancel()
		res, err := c.GetStatus(ctx, &pb.GetStatusRequest{})
		return err == nil && res.Status.Privileged
	}, 30*time.Second, "")
	if err != nil && !errors.Is(err, context.Canceled) && appCtx.Err() == nil {
		// A daemon answering somewhere else is the more useful fact than the log path:
		// it is a start this command didn't wait for, or a takeover it should know about.
		failure := "the privileged daemon didn't start; see " + logPath
		if live, ok := liveDaemonSocket(); ok {
			failure = "the privileged daemon didn't start; a daemon answers on " + live
		}
		err = errors.New(failure)
	}
	if err != nil {
		s.Fail(err.Error())
		return errReported
	}
	s.Stop("The daemon runs with " + privilegeName + ".")
	if service.Supported() && !service.Installed() {
		ui.Info(enableTip)
	}
	return nil
}

// installBinary copies this executable and the geo data beside it into service.InstallDir(),
// which only root or Administrators can change: the service must never run a binary
// the user can modify. (On Windows the service writes the Wintun driver it embeds.)
func installBinary(src string) (string, error) {
	dir := service.InstallDir()
	if runtime.GOOS == "darwin" {
		for _, parent := range []string{"/Library", filepath.Dir(dir), dir} {
			if err := paths.EnsureRootDir(parent, 0, 0755); err != nil {
				return "", err
			}
		}
	}
	if err := geopack.EnsureInstalledDirectory(dir); err != nil {
		return "", err
	}
	managedGeo, err := geopack.Managed(dir)
	if err != nil {
		return "", err
	}
	assets := filepath.Dir(src)
	if geopack.Packed(assets) {
		stage, err := geopack.NewStaging(dir)
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(stage)
		if err := geopack.Expand(assets, stage); err != nil {
			return "", err
		}
		assets = stage
	}
	copyGeo, err := preflightServiceGeo(assets, dir, managedGeo)
	if err != nil {
		return "", err
	}
	stopServiceForInstall()
	name := filepath.Base(service.BinaryPath())
	bin := filepath.Join(dir, name)
	if err := copyFile(src, bin, 0o755); err != nil {
		return "", err
	}
	// The service runs this copy as root. A short write or a swapped source must not
	// become the daemon.
	if same, err := sameContent(src, bin); err != nil || !same {
		return "", errors.New("the service's copy of masir doesn't match the running binary")
	}
	os.Remove(filepath.Join(dir, "masird")) // the separate daemon binary of earlier versions
	if runtime.GOOS == "darwin" {
		// An older app wrapper is not part of the service now, and a stale wrapper
		// would keep the wrong name and icon in Login Items. Do not try to remove a
		// lowercase "masir": on the default case-insensitive APFS it is this same file.
		os.RemoveAll(filepath.Join(dir, "Masir.app"))
	}
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		if !copyGeo {
			break
		}
		from := filepath.Join(assets, f)
		to := filepath.Join(dir, f)
		if err := copyFile(from, to, 0o644); err != nil {
			return "", err
		}
		if same, err := sameContent(from, to); err != nil || !same {
			return "", errors.New("the service's copy of " + f + " doesn't match the running data")
		}
	}
	if managedGeo {
		_, ipErr := os.Stat(filepath.Join(dir, "geoip.dat"))
		_, siteErr := os.Stat(filepath.Join(dir, "geosite.dat"))
		if ipErr == nil && siteErr == nil {
			if err := geopack.Record(dir); err != nil {
				return "", err
			}
		}
	}
	// Give the service binary the app's Finder icon, so Login Items shows Masir's icon
	// instead of the generic executable. The files sit beside it; updates re-apply them.
	for _, f := range []string{"AppIcon.rsrc", "FinderInfo.bin"} {
		to := filepath.Join(dir, f)
		for _, from := range []string{
			filepath.Join(filepath.Dir(src), "..", f),
			filepath.Join(filepath.Dir(src), f),
			to,
		} {
			if filepath.Clean(from) == filepath.Clean(to) {
				break
			}
			if fi, err := os.Stat(from); err == nil && !fi.IsDir() {
				if err := copyFile(from, to, 0o644); err != nil {
					return "", err
				}
				break
			}
		}
	}
	applyFileIcon(bin, dir)
	if err := labelServiceBinary(bin); err != nil {
		return "", err
	}
	return bin, nil
}

func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := to + ".new"
	os.Remove(tmp)
	out, err := geopack.CreateInstalledFile(tmp, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	preserveOwner(tmp, to)
	return os.Rename(tmp, to) // replacing in place keeps a running copy intact
}

// asRoot re-runs this binary under sudo with args, attached to the terminal for the prompt.
func asRoot(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return sudo(append([]string{exe}, args...)...)
}

// sudo runs argv as root, attached to the terminal for the password prompt. When the
// command fails, it has already said why (as have sudo and launchctl), so the error
// reports nothing more.
func sudo(argv ...string) error {
	cmd := exec.Command("sudo", append([]string{"--"}, argv...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return errReported
		}
		return fmt.Errorf("sudo %s: %w", filepath.Base(argv[0]), err)
	}
	return nil
}

// daemonUnit prints the service definition this binary would install for the uid, so an
// updater can compare it with the installed one without writing anything.
func daemonUnit(a args) error {
	user := a.str("user")
	if user == "" {
		user = currentUser()
	}
	uid, err := strconv.Atoi(user)
	if err != nil {
		return fail("--user names the uid the service serves")
	}
	bin, err := self()
	if err != nil {
		return err
	}
	fmt.Print(serviceDefinition(bin, uid))
	return nil
}

// daemonSyncUnit rewrites the installed service definition from the service's own binary
// and reloads the service manager, which restarts the service when the file changed so
// the new sandbox applies now. It runs as root.
func daemonSyncUnit(a args) error {
	user := a.str("user")
	if user == "" {
		user = currentUser()
	}
	retry := a.boolean("retry", false)
	if !elevated() {
		flags := []string{"daemon", "sync-unit", "--user", user}
		if retry {
			flags = append(flags, "--retry")
		}
		return elevate(flags...)
	}
	return applySyncedServiceUnit(user, retry)
}

// syncServiceUnit is sync-unit with root rights. It does nothing when the installed
// definition already matches what the service's binary carries.
func syncServiceUnit(user string) error {
	return applySyncedServiceUnit(user, false)
}

func applySyncedServiceUnit(user string, retry bool) error {
	return syncServiceUnitWith(user, func(path, text string) error {
		return applyServiceDefinition(path, text, retry)
	})
}

func syncServiceUnitWith(user string, apply func(string, string) error) error {
	path := serviceDefinitionPath()
	if path == "" {
		return nil // this OS keeps no definition file
	}
	if !service.Installed() {
		return fail("no masir service is installed")
	}
	uid, err := strconv.Atoi(user)
	if err != nil {
		return fail("--user names the uid the service serves")
	}
	// The definition comes from the service's own binary: after an update the masir in
	// hand may still be the old version while the service already runs the new one.
	bin := service.BinaryPath()
	if _, err := os.Stat(bin); err != nil {
		if bin, err = self(); err != nil {
			return err
		}
	}
	out, err := exec.Command(bin, "daemon", "unit", "--user", strconv.Itoa(uid)).Output()
	if err != nil {
		return fmt.Errorf("read the service definition from %s: %w", bin, err)
	}
	if len(out) == 0 {
		return nil
	}
	if err := apply(path, string(out)); err != nil {
		return err
	}
	ui.Success("The masir service definition is up to date.")
	return nil
}

// reconcileServiceUnit updates the installed definition when the service's binary would
// write a different one, asking for root only when it has to. A release that changes the
// unit (capabilities, sandbox) must apply it, not leave it for the next reinstall.
func reconcileServiceUnit() error {
	path := serviceDefinitionPath()
	if path == "" || !service.Installed() {
		return nil
	}
	bin := service.BinaryPath()
	if _, err := os.Stat(bin); err != nil {
		return nil // no service binary yet; `daemon enable` writes the definition
	}
	user := currentUser()
	out, err := exec.Command(bin, "daemon", "unit", "--user", user).Output()
	if err != nil || len(out) == 0 {
		return nil // an older service binary without `unit`; the next enable fixes it
	}
	cur, err := os.ReadFile(path)
	if err == nil && string(cur) == string(out) {
		return nil
	}
	if !elevated() {
		ui.Note("The new masir carries a different service definition; applying it so the sandbox and capabilities take effect ("+elevationAsk+").",
			ui.Yellow("▲ Updating the service definition"))
		// The service's own binary, not this one: after an update the CLI in hand may be
		// the copy that couldn't replace itself.
		return asRootExe(service.BinaryPath(), "daemon", "sync-unit", "--user", user)
	}
	return syncServiceUnit(user)
}

// self is this executable, resolved through symlinks: masir is also the daemon, so the
// background daemon and the service run this same file.
func self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func waitFor(ok func() bool, timeout time.Duration, failure string) error {
	return client.WaitFor(appCtx, ok, timeout, failure)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
