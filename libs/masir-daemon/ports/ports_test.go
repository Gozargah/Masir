package ports

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/store"
)

// busyPort holds a port open and returns it with the call that releases it.
func busyPort(t *testing.T) (int, func()) { return busyPortOn(t, "127.0.0.1") }

// busyPortOn holds a port on host. A wildcard bind is the one that collides with a
// wildcard bind: macOS lets a specific address and a wildcard share a port, so a probe
// for AllowLAN only meets the listener Xray itself makes.
func busyPortOn(t *testing.T, host string) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	return l.Addr().(*net.TCPAddr).Port, func() { _ = l.Close() }
}

func inbound(tag string, port any) store.Item {
	return store.Item{
		Tag:    tag,
		Config: []byte(fmt.Sprintf(`{"protocol":"socks","listen":"127.0.0.1","port":%v}`, port)),
	}
}

// A taken proxy port must be reported before Xray starts: a start that fails on the bind
// crashes the daemon inside xray-core's commander (see package docs).
func TestBusyRejectsABusyProxyPort(t *testing.T) {
	port, release := busyPort(t)

	var st store.State
	st.Settings.ProxyPort = port
	if err := Busy(st); err == nil {
		t.Fatal("a taken proxy port was accepted")
	}

	release()
	if err := Busy(st); err != nil {
		t.Fatalf("a free proxy port was refused: %v", err)
	}
}

// The error is the one a person reads, so it names the port the way Xray does: the same
// message the banner showed, with the same host AllowLAN asks for.
func TestBusyNamesThePortTheWayXrayDoes(t *testing.T) {
	port, release := busyPortOn(t, "0.0.0.0")
	defer release()

	var st store.State
	st.Settings.ProxyPort = port
	st.Settings.AllowLAN = true
	err := Busy(st)
	if err == nil {
		t.Fatal("a taken proxy port was accepted")
	}
	for _, want := range []string{"proxy: ", "0.0.0.0:", fmt.Sprint(port)} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestBusyRejectsABusyInboundPort(t *testing.T) {
	port, release := busyPort(t)

	st := store.State{Inbounds: []store.Item{inbound("clash", port)}}
	if err := Busy(st); err == nil {
		t.Fatal("a taken inbound port was accepted")
	}

	release()
	if err := Busy(st); err != nil {
		t.Fatalf("a free inbound port was refused: %v", err)
	}
}

func TestBusyDoesNotProbeWireGuardAsTCP(t *testing.T) {
	port, release := busyPort(t)
	defer release()

	wireguard := store.Item{
		Tag:    "wireguard",
		Config: []byte(fmt.Sprintf(`{"protocol":"wireguard","listen":"127.0.0.1","port":%d}`, port)),
	}
	st := store.State{Inbounds: []store.Item{wireguard}}
	if err := Busy(st); err != nil {
		t.Fatalf("a WireGuard inbound sharing a TCP port was refused: %v", err)
	}
}

// Xray's port field may be a quoted number; a valid config must not be rejected.
func TestBusyReadsAQuotedPort(t *testing.T) {
	port, release := busyPort(t)
	defer release()

	st := store.State{Inbounds: []store.Item{inbound("clash", fmt.Sprintf("%q", fmt.Sprint(port)))}}
	if err := Busy(st); err == nil {
		t.Fatal("a taken quoted inbound port was accepted")
	}
}

// Two inbounds on one port, or an inbound on the proxy's port, fail when Xray binds the
// second one — after its API feature is up. The probe keeps its listeners open to catch
// that here instead.
func TestBusyRejectsTwoInboundsOnOnePort(t *testing.T) {
	port := freePort(t)
	st := store.State{Inbounds: []store.Item{inbound("a", port), inbound("b", port)}}
	if err := Busy(st); err == nil {
		t.Fatal("two inbounds on one port were accepted")
	}
}

func TestBusyRejectsAnInboundOnTheProxyPort(t *testing.T) {
	port := freePort(t)
	var st store.State
	st.Settings.ProxyPort = port
	st.Inbounds = []store.Item{inbound("clash", port)}
	if err := Busy(st); err == nil {
		t.Fatal("an inbound on the proxy port was accepted")
	}
}

// Ranges, lists and unix sockets have no single TCP port to probe; Xray handles them.
func TestBusyLeavesRangesAndUnixSocketsToXray(t *testing.T) {
	st := store.State{Inbounds: []store.Item{
		inbound("range", `"1000-2000"`),
		inbound("list", `"80,443"`),
		{Tag: "unix", Config: []byte(`{"protocol":"socks","listen":"/tmp/masir-test.sock","port":0}`)},
	}}
	if err := Busy(st); err != nil {
		t.Fatalf("a range, a list or a unix socket was refused: %v", err)
	}
}

// WaitFree is what makes a handover safe: the port a daemon is letting go of is taken for
// a moment, and starting the next daemon inside that moment is the bug it exists to stop.
func TestWaitFreeWaitsForThePortToBeReleased(t *testing.T) {
	port, release := busyPort(t)
	var st store.State
	st.Settings.ProxyPort = port

	// Released after the wait has started, as an exiting Xray does.
	time.AfterFunc(150*time.Millisecond, release)
	if err := WaitFree(st, 5*time.Second); err != nil {
		t.Fatalf("the port was released but the wait failed: %v", err)
	}
}

func TestWaitFreeReportsATimeoutWithThePortInIt(t *testing.T) {
	port, release := busyPort(t)
	defer release()
	var st store.State
	st.Settings.ProxyPort = port

	err := WaitFree(st, 200*time.Millisecond)
	if err == nil {
		t.Fatal("a port nobody released passed the wait")
	}
	if !contains(err.Error(), fmt.Sprint(port)) {
		t.Fatalf("the timeout doesn't name the port: %v", err)
	}
}

func TestWaitFreeReturnsAtOnceWhenNothingIsTaken(t *testing.T) {
	var st store.State
	st.Settings.ProxyPort = freePort(t)
	start := time.Now()
	if err := WaitFree(st, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("a free port took %s", elapsed)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// freePort returns a port nothing holds, for the cases that must not collide with the
// machine's own listeners.
func freePort(t *testing.T) int {
	t.Helper()
	port, release := busyPort(t)
	release()
	return port
}
