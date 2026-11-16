//go:build !windows

package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/paths"
)

// Removing the service stops its daemon before anything else is touched, and only then
// unloads it. A supervisor's own stop (bootout, disable --now) answers while the daemon
// it stopped still holds the proxy port, and the app starts the bundle daemon the moment
// the removal returns: that ordering is the bug the sequence exists to prevent.
func TestRemovingTheServiceStopsItsDaemonFirst(t *testing.T) {
	oldElevated, oldStop, oldUninstall, oldWait, oldRemove := elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir
	t.Cleanup(func() {
		elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir = oldElevated, oldStop, oldUninstall, oldWait, oldRemove
	})

	var order []string
	elevated = func() bool { return true }
	stopTheDaemon = func(sock string) error {
		order = append(order, "stop "+sock)
		return nil
	}
	removeServiceFn = func() error {
		order = append(order, "uninstall")
		return nil
	}
	waitPortsFree = func(user string, _ time.Duration) error {
		order = append(order, "ports "+user)
		return nil
	}
	removeInstallDir = func() error {
		order = append(order, "remove")
		return nil
	}

	if err := daemonDisable(args{flags: map[string][]string{"user": {"501"}}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop " + paths.PrivilegedSocketPath(501), "uninstall", "remove", "ports 501"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// A port that stays taken is reported, because the daemon that replaces this one would
// otherwise start without the proxy the person was using.
func TestRemovingTheServiceReportsPortsItCouldNotFree(t *testing.T) {
	oldElevated, oldStop, oldUninstall, oldWait, oldRemove := elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir
	t.Cleanup(func() {
		elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir = oldElevated, oldStop, oldUninstall, oldWait, oldRemove
	})

	elevated = func() bool { return true }
	stopTheDaemon = func(string) error { return nil }
	removeServiceFn = func() error { return nil }
	removeInstallDir = func() error { return nil }
	waitPortsFree = func(string, time.Duration) error {
		return errors.New("proxy: listen tcp 0.0.0.0:7890: bind: address already in use")
	}

	err := daemonDisable(args{flags: map[string][]string{"user": {"501"}}})
	if err == nil {
		t.Fatal("a port that stayed taken was reported as a clean removal")
	}
	var ue *userError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %T, want the usual failure format", err)
	}
	if len(ue.advice.cmds) != 1 || ue.advice.cmds[0] != "masir connect" {
		t.Fatalf("advice = %v", ue.advice.cmds)
	}
	if !containsText(err.Error(), "7890") && !containsText(ue.advice.text, "7890") {
		t.Fatalf("the failure doesn't name the port: %v / %v", err, ue.advice)
	}
}

// --ports=false is the escape a script uses when it knows the ports are nobody's business.
func TestRemovingTheServiceCanSkipThePortWait(t *testing.T) {
	oldElevated, oldStop, oldUninstall, oldWait, oldRemove := elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir
	t.Cleanup(func() {
		elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir = oldElevated, oldStop, oldUninstall, oldWait, oldRemove
	})

	elevated = func() bool { return true }
	stopTheDaemon = func(string) error { return nil }
	removeServiceFn = func() error { return nil }
	removeInstallDir = func() error { return nil }
	called := false
	waitPortsFree = func(string, time.Duration) error {
		called = true
		return nil
	}

	if err := daemonDisable(args{flags: map[string][]string{"user": {"501"}, "ports": {"false"}}}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("the port wait ran although it was turned off")
	}
}

// --stopped is what the app uses when it already asked the daemon to stop over the socket:
// asking twice would wait out the timeout the first stop's answer had already covered.
func TestRemovingTheServiceCanSkipTheSocketStop(t *testing.T) {
	oldElevated, oldStop, oldUninstall, oldWait, oldRemove := elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir
	t.Cleanup(func() {
		elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir = oldElevated, oldStop, oldUninstall, oldWait, oldRemove
	})

	elevated = func() bool { return true }
	stopped := false
	stopTheDaemon = func(string) error {
		stopped = true
		return nil
	}
	removeServiceFn = func() error { return nil }
	removeInstallDir = func() error { return nil }
	waitPortsFree = func(string, time.Duration) error { return nil }

	if err := daemonDisable(args{flags: map[string][]string{"user": {"501"}, "stopped": {"true"}}}); err != nil {
		t.Fatal(err)
	}
	if stopped {
		t.Fatal("the daemon was stopped although the caller said it already was")
	}
}

// A removal that ran through sudo must still name the user the service serves. sudo sets
// the real uid to 0 as well, so os.Getuid() is root here; the daemon serves SUDO_UID.
// Naming root made the port wait refuse ("the daemon serves a regular user, not root")
// after the service was already gone, which is how CI caught it.
func TestRemovingTheServiceServesTheUserBehindSudo(t *testing.T) {
	oldElevated, oldStop, oldUninstall, oldWait, oldRemove := elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir
	t.Cleanup(func() {
		elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir = oldElevated, oldStop, oldUninstall, oldWait, oldRemove
	})

	elevated = func() bool { return true }
	stopTheDaemon = func(string) error { return nil }
	removeServiceFn = func() error { return nil }
	removeInstallDir = func() error { return nil }
	var waitedFor string
	waitPortsFree = func(user string, _ time.Duration) error {
		waitedFor = user
		return nil
	}

	// No --user, as the app's permission prompt and a bare `sudo masir daemon disable`
	// both arrive. SUDO_UID is what sudo left behind.
	t.Setenv("SUDO_UID", "501")
	if err := daemonDisable(args{}); err != nil {
		t.Fatal(err)
	}
	if waitedFor != "501" {
		t.Fatalf("waited for user %q, want the user sudo served (501)", waitedFor)
	}
}

// Without --user and without sudo there is no user to name, and the removal says so
// instead of waiting on root's ports.
func TestRemovingTheServiceWithoutAUserSaysWhichOne(t *testing.T) {
	oldElevated, oldStop, oldUninstall, oldWait, oldRemove := elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir
	t.Cleanup(func() {
		elevated, stopTheDaemon, removeServiceFn, waitPortsFree, removeInstallDir = oldElevated, oldStop, oldUninstall, oldWait, oldRemove
	})

	elevated = func() bool { return true }
	stopTheDaemon = func(string) error { return nil }
	removeServiceFn = func() error { return nil }
	removeInstallDir = func() error { return nil }
	waited := false
	waitPortsFree = func(string, time.Duration) error {
		waited = true
		return nil
	}

	t.Setenv("SUDO_UID", "")
	err := daemonDisable(args{})
	if err == nil {
		t.Fatal("a removal with no user to serve succeeded")
	}
	if waited {
		t.Fatal("the port wait ran for a user nobody named")
	}
	var ue *userError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %T, want the usual failure format", err)
	}
	if len(ue.advice.cmds) != 1 || ue.advice.cmds[0] != "masir daemon disable" {
		t.Fatalf("advice = %v, want the command to run without sudo", ue.advice.cmds)
	}
}

// The elevated copy is a new process, so the skip options have to be forwarded with it:
// a `--ports=false` typed at the prompt must not become a port wait after sudo.
func TestRemovingTheServiceForwardsTheSkipOptionsWhenElevating(t *testing.T) {
	oldElevated, oldElevate := elevated, elevate
	t.Cleanup(func() { elevated, elevate = oldElevated, oldElevate })

	elevated = func() bool { return false }
	var argv []string
	elevate = func(args ...string) error {
		argv = args
		return nil
	}

	if err := daemonDisable(args{flags: map[string][]string{"ports": {"false"}, "stopped": {"true"}}}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--ports=false") {
		t.Fatalf("argv = %v, want --ports=false forwarded", argv)
	}
	if !strings.Contains(joined, "--stopped") {
		t.Fatalf("argv = %v, want --stopped forwarded", argv)
	}
	if !strings.Contains(joined, "--user") {
		t.Fatalf("argv = %v, want --user forwarded", argv)
	}
}

// The defaults are not forwarded: the elevated copy waits for ports and stops the daemon,
// which is what an unqualified removal means.
func TestRemovingTheServiceDoesNotForwardDefaults(t *testing.T) {
	oldElevated, oldElevate := elevated, elevate
	t.Cleanup(func() { elevated, elevate = oldElevated, oldElevate })

	elevated = func() bool { return false }
	var argv []string
	elevate = func(args ...string) error {
		argv = args
		return nil
	}

	if err := daemonDisable(args{}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "--ports") {
		t.Fatalf("argv = %v, want no --ports for the default", argv)
	}
	if strings.Contains(joined, "--stopped") {
		t.Fatalf("argv = %v, want no --stopped for the default", argv)
	}
}

func containsText(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
