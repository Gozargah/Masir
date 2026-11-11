package main

import (
	"errors"
	"testing"
)

// packaged makes the test binary look like a build from mygo build, which is
// the only kind of build that disconnects on quit.
func packaged(t *testing.T) {
	t.Helper()
	t.Setenv("MYGO_ENV", "production")
}

// Quitting a connected app turns masir off as `masir disconnect` does.
func TestQuittingDisconnects(t *testing.T) {
	packaged(t)
	f := ukServers()
	f.status.Settings.Power = true
	a := newTestApp(f)

	a.disconnectOnQuit()

	if !f.called("power off") {
		t.Fatalf("quitting did not turn masir off: %v", f.calls)
	}
	// `masir disconnect` only changes power, so Always-on Proxy keeps its port up.
	if f.called("tun") {
		t.Errorf("a quit changed the tunnel setting: %v", f.calls)
	}
}

// Quitting with Disconnect on Quit turned off leaves masir on.
func TestQuittingDisabledKeepsMasirOn(t *testing.T) {
	packaged(t)
	f := ukServers()
	f.status.Settings.Power = true
	a := newTestApp(f)
	a.setDisconnectOnQuit(false)

	a.disconnectOnQuit()

	if f.called("power off") {
		t.Fatalf("quitting with disconnect on quit disabled turned masir off: %v", f.calls)
	}
}

// A quit while masir is off writes nothing at all.
func TestQuittingWhenOffSendsNothing(t *testing.T) {
	packaged(t)
	f := ukServers()
	a := newTestApp(f)

	a.disconnectOnQuit()

	if f.called("power off") || f.called("tun") {
		t.Fatalf("quitting while off called the daemon: %v", f.calls)
	}
}

// An update relaunches the app, which is not the person leaving: masir stays on.
func TestAnUpdateRelaunchKeepsMasirOn(t *testing.T) {
	packaged(t)
	f := ukServers()
	f.status.Settings.Power = true
	a := newTestApp(f)
	a.updates.state.relaunching = true

	a.disconnectOnQuit()

	if f.called("power off") {
		t.Fatalf("an update relaunch turned masir off: %v", f.calls)
	}
}

// The quit must not wait on a daemon that cannot answer: a failed disconnect is logged
// and the app still exits.
func TestAFailedQuitDisconnectDoesNotHoldTheQuit(t *testing.T) {
	packaged(t)
	f := ukServers()
	f.status.Settings.Power = true
	f.failPow = errors.New("the daemon went away")
	a := newTestApp(f)

	a.disconnectOnQuit() // returns instead of panicking or waiting

	if !f.called("power off") {
		t.Fatalf("the disconnect was not attempted: %v", f.calls)
	}
}

// A development build quits without turning masir off: the next run should
// find the daemon it was using.
func TestQuittingInDevKeepsMasirOn(t *testing.T) {
	t.Setenv("MYGO_ENV", "development")
	f := ukServers()
	f.status.Settings.Power = true
	a := newTestApp(f)

	a.disconnectOnQuit()

	if f.called("power off") {
		t.Fatalf("a dev quit turned masir off: %v", f.calls)
	}
}
