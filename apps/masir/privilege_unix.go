//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// privilegeName is what TUN needs, in the words of this OS.
const privilegeName = "root"

// elevationNote explains, before it happens, how masir gets privileges.
const elevationNote = "masir starts its daemon as root for this session; sudo asks for your password."

// privilegeOwner is who may change the service's files.
const privilegeOwner = "root"

// elevationAsk finishes "To do X, ..." with how masir gets the rights.
const elevationAsk = "sudo asks for your password"

// enableTip says how to stop being asked, once a session daemon runs as root.
const enableTip = "Tip: `masir daemon enable` starts the daemon as root at boot, so connecting won't ask for your password again."

// elevationHint is how a privileged step announces itself.
const elevationHint = "sudo may ask for your password"

// elevated is a variable so tests can be root without being root.
var elevated = func() bool { return os.Geteuid() == 0 }

// rootRefusal explains why masir won't act as a root client. The daemon serves one
// regular user (identity.Resolve refuses root), so a root client reads /root's config
// directory and looks for root's socket while the daemon it starts serves SUDO_UID:
// the two never meet, and the command waits out a timeout it can't win.
func rootRefusal(path string) error {
	if !elevated() || mayRunAsRoot(path) {
		return nil
	}
	return refuseClient(path)
}

// refuseClient is the failure for a root client, with the command to run instead.
func refuseClient(path string) error {
	return advised("This needs your normal user, not root: masir asks for permission itself.",
		"Run it again without sudo:", withoutSudo(path))
}

// rootRestartRefusal refuses `masir daemon restart` as root when no service is installed:
// with nothing supervising it, that command starts a session root daemon this client
// can't reach. With a service, daemonRestart never asks.
func rootRestartRefusal() error {
	if !elevated() {
		return nil
	}
	return refuseClient("masir daemon restart")
}

// canElevateSilently reports whether becoming root won't prompt: already root, or sudo
// still remembers the password (or is set up not to ask).
func canElevateSilently() bool {
	return elevated() || exec.Command("sudo", "-n", "true").Run() == nil
}

// startElevatedDaemon starts `masir daemon run` as root in the background, serving
// user, its output going to log. It isn't a service: it lasts until it's stopped or
// the machine restarts.
//
// sudo runs on this terminal, so it asks for the password here if it needs to and
// accepts one it remembers: sudo remembers per terminal, so a detached sudo would
// always find none. Its -b flag then starts the daemon in the background and returns.
func startElevatedDaemon(user string, log *os.File) error {
	exe, err := self()
	if err != nil {
		return err
	}
	argv := []string{exe, "daemon", "run", "--user", user}
	if elevated() {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr = detached()
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	cmd := exec.Command("sudo", append([]string{"-b", "--"}, argv...)...)
	cmd.Stdin = os.Stdin // the password prompt itself goes to the terminal
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		// sudo running and exiting non-zero means the prompt was cancelled or the
		// password was wrong; sudo missing or unable to start is a broken setup.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fail("root permission wasn't granted")
		}
		return fmt.Errorf("run sudo: %w", err)
	}
	return nil
}

// elevate re-runs masir with args as root, through sudo.
func elevate(args ...string) error { return asRoot(args...) }

// currentUser is who the service should serve: this user's uid.
func currentUser() string { return strconv.Itoa(os.Getuid()) }

// stopServiceForInstall is a no-op: on Unix, a running binary can be renamed over.
func stopServiceForInstall() {}

// asRootExe runs exe with args as root, attached to the terminal for the password prompt.
// It is for a binary other than this one: after an update the CLI in hand may not be the
// copy that should write the service definition.
func asRootExe(exe string, args ...string) error {
	return sudo(append([]string{exe}, args...)...)
}
