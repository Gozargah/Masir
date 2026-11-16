package server

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/store"
)

// A daemon that boots while the one it replaces is still closing Xray must wait for the
// port instead of reporting a failure. A supervisor's stop answers first, and the app
// starts this daemon the moment the removal returns, so the busy port is masir's own
// proxy on its way out — the "address already in use" a person saw after removing the
// service.
func TestBootWaitsForThePortThePreviousDaemonIsReleasing(t *testing.T) {
	port, release := busyPort(t)
	var st store.State
	st.Settings.ProxyPort = port

	s := newTestServer(t)
	s.state.Settings.Power = true
	s.state.Settings.ProxyPort = port
	s.heldPorts.Store(true)

	// Released after the wait has started, as an exiting Xray closes its listener.
	time.AfterFunc(150*time.Millisecond, release)
	done := make(chan error, 1)
	go func() { done <- s.handoverPatience(st, probePorts(st)) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a port on its way out was reported as taken: %v", err)
		}
	case <-time.After(handoverWait + 5*time.Second):
		t.Fatal("the wait never returned")
	}
}

// The patience is for a handover only. A config change made while the daemon runs (a new
// proxy port, an inbound added) reports the busy port at once: there is no previous
// daemon to blame, and waiting would only hide another program holding it.
func TestABusyPortDuringAChangeIsReportedAtOnce(t *testing.T) {
	port, release := busyPort(t)
	defer release()

	s := newTestServer(t)
	s.heldPorts.Store(false) // Boot has finished; nothing is being handed over
	var st store.State
	st.Settings.ProxyPort = port

	start := time.Now()
	err := s.handoverPatience(st, probePorts(st))
	if err == nil {
		t.Fatal("a busy port during a change was accepted")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("a change waited %s for a port, want an immediate report", elapsed)
	}
}

// The wait happens once per daemon. A port that stays taken is another program's, and a
// retry must say so immediately rather than sit through five more seconds each time.
func TestTheHandoverWaitIsUsedUpOnce(t *testing.T) {
	old := handoverWait
	handoverWait = 200 * time.Millisecond
	t.Cleanup(func() { handoverWait = old })

	port, release := busyPort(t)
	defer release()

	s := newTestServer(t)
	s.heldPorts.Store(true)
	var st store.State
	st.Settings.ProxyPort = port

	if err := s.handoverPatience(st, probePorts(st)); err == nil {
		t.Fatal("a port nobody released was accepted")
	}
	if s.heldPorts.Load() {
		t.Fatal("the daemon would wait again on the next apply")
	}
	start := time.Now()
	if err := s.handoverPatience(st, probePorts(st)); err == nil {
		t.Fatal("the second apply accepted the port")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("the second apply waited %s, want none", elapsed)
	}
}

// A free port needs no patience at all: the common case must not add a delay to boot.
func TestAFreePortDoesNotWait(t *testing.T) {
	s := newTestServer(t)
	s.heldPorts.Store(true)
	var st store.State
	st.Settings.ProxyPort = freePort(t)

	start := time.Now()
	if err := s.handoverPatience(st, probePorts(st)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("a free port took %s", elapsed)
	}
	if !s.heldPorts.Load() {
		t.Fatal("the daemon used up its patience on a free port")
	}
}

// A boot that can't get the port still reports it, naming the port: the person has to
// know what to quit.
func TestAHandoverThatNeverFinishesNamesThePort(t *testing.T) {
	old := handoverWait
	handoverWait = 200 * time.Millisecond
	t.Cleanup(func() { handoverWait = old })

	port, release := busyPort(t)
	defer release()

	s := newTestServer(t)
	s.heldPorts.Store(true)
	var st store.State
	st.Settings.ProxyPort = port

	err := s.handoverPatience(st, probePorts(st))
	if err == nil {
		t.Fatal("a port held by someone else was accepted")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Fatalf("the failure doesn't name the port: %v", err)
	}
	if !strings.Contains(err.Error(), "still taken") {
		t.Fatalf("the failure doesn't say it waited: %v", err)
	}
}

// The wait is bounded: a port held by another program fails the boot, it doesn't hang it.
func TestTheHandoverWaitIsBounded(t *testing.T) {
	if handoverWait > 30*time.Second {
		t.Fatalf("handoverWait = %s; a boot must not wait that long", handoverWait)
	}
}
