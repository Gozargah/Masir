//go:build darwin || linux

package main

import (
	"errors"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// Removing the service unloads its daemon, and a supervisor's stop answers before that
// daemon has closed Xray's listeners. The app must wait for the ports before it points
// itself at the bundle's masir: starting that daemon inside the gap is what reported
// "proxy: listen tcp 0.0.0.0:7890: bind: address already in use" on a machine whose only
// proxy was the one being removed.
func TestRemovingTheServiceWaitsForItsPortsBeforeSwitching(t *testing.T) {
	oldRemove, oldWait := removeServiceFn, daemonPortWait
	t.Cleanup(func() { removeServiceFn, daemonPortWait = oldRemove, oldWait })

	a, home := autoTestApp(t)
	var order []string
	removeServiceFn = func(string) error {
		order = append(order, "remove")
		return nil
	}
	daemonPortWait = func() error {
		order = append(order, "ports")
		return nil
	}

	a.removeDaemonService()

	if len(order) != 2 || order[0] != "remove" || order[1] != "ports" {
		t.Fatalf("order = %v, want the removal then the port wait", order)
	}
	if a.serviceBusy {
		t.Fatal("the app stayed busy after the removal finished")
	}
	// The bundle's daemon is the one to start from now: the service's copy went with it.
	if want := daemonBinary(); a.binary != want {
		t.Fatalf("binary = %q, want %q (built in %s)", a.binary, want, home)
	}
	if !hasNotice(a, "The background service is removed") {
		t.Fatalf("notices = %v", a.notices)
	}
}

// A port that stays taken is reported: the daemon that replaces the service would come up
// without the proxy the person was using, and silence would make that look intended.
func TestRemovingTheServiceSurfacesAPortThatStayedTaken(t *testing.T) {
	oldRemove, oldWait := removeServiceFn, daemonPortWait
	t.Cleanup(func() { removeServiceFn, daemonPortWait = oldRemove, oldWait })

	a, _ := autoTestApp(t)
	removeServiceFn = func(string) error { return nil }
	daemonPortWait = func() error {
		return errors.New("proxy: listen tcp 0.0.0.0:7890: bind: address already in use")
	}
	before := a.binary

	a.removeDaemonService()

	if !hasNotice(a, "Couldn't remove the background service: proxy: listen tcp 0.0.0.0:7890: bind: address already in use") {
		t.Fatalf("notices = %v", a.notices)
	}
	if a.binary != before {
		t.Fatal("the app switched daemons although the ports were never free")
	}
	if hasNotice(a, "The background service is removed") {
		t.Fatalf("success reported for a failed removal: %v", a.notices)
	}
}

// A failed unload never reaches the wait: there is nothing to hand over yet.
func TestRemovingTheServiceSkipsTheWaitWhenTheUnloadFailed(t *testing.T) {
	oldRemove, oldWait := removeServiceFn, daemonPortWait
	t.Cleanup(func() { removeServiceFn, daemonPortWait = oldRemove, oldWait })

	a, _ := autoTestApp(t)
	removeServiceFn = func(string) error { return errors.New("permission wasn't granted") }
	waited := false
	daemonPortWait = func() error {
		waited = true
		return nil
	}

	a.removeDaemonService()

	if waited {
		t.Fatal("waited for ports of a service that was never removed")
	}
	if !hasNotice(a, "Couldn't remove the background service: permission wasn't granted") {
		t.Fatalf("notices = %v", a.notices)
	}
}

// The service ran as root, so Tunnel mode went with it. A person who had the tunnel on
// must be told the connection is now a proxy, instead of reading a switch that still says
// Tunnel.
func TestRemovingTheServiceExplainsTheTunnelDowngrade(t *testing.T) {
	oldRemove, oldWait := removeServiceFn, daemonPortWait
	t.Cleanup(func() { removeServiceFn, daemonPortWait = oldRemove, oldWait })

	f := ukServers()
	f.status.Settings.Tun = true
	a := newTestApp(f)
	a.binary = "/opt/masir/masir"
	removeServiceFn = func(string) error { return nil }
	daemonPortWait = func() error { return nil }

	a.removeDaemonService()

	if !hasNotice(a, "Tunnel mode needs the background service. Masir reconnects through the proxy until you install it again.") {
		t.Fatalf("notices = %v", a.notices)
	}
}

// Proxy-only users lose nothing, so they get no note about a tunnel they never used.
func TestRemovingTheServiceSaysNothingAboutATunnelThatWasOff(t *testing.T) {
	oldRemove, oldWait := removeServiceFn, daemonPortWait
	t.Cleanup(func() { removeServiceFn, daemonPortWait = oldRemove, oldWait })

	f := ukServers()
	f.status.Settings.Tun = false
	a := newTestApp(f)
	a.binary = "/opt/masir/masir"
	removeServiceFn = func(string) error { return nil }
	daemonPortWait = func() error { return nil }

	a.removeDaemonService()

	if hasNotice(a, "Tunnel mode needs the background service. Masir reconnects through the proxy until you install it again.") {
		t.Fatalf("notices = %v", a.notices)
	}
}

// The note reads the state the daemon reports, not a copy: the settings panel can change
// Tunnel mode between two removals.
func TestNoteHandoverFollowsTheReportedSetting(t *testing.T) {
	a := newTestApp(ukServers())
	a.status.Settings = &pb.Settings{Tun: false}
	a.noteHandover()
	if len(a.notices) != 0 {
		t.Fatalf("notices = %v", a.notices)
	}
	a.status.Settings = &pb.Settings{Tun: true}
	a.noteHandover()
	if !hasNotice(a, "Tunnel mode needs the background service. Masir reconnects through the proxy until you install it again.") {
		t.Fatalf("notices = %v", a.notices)
	}
}

func hasNotice(a *app, message string) bool {
	for _, notice := range a.notices {
		if notice == message {
			return true
		}
	}
	return false
}
