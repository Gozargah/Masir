//go:build !windows

package main

import (
	"errors"
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

func containsText(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
