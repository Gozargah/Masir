package server

import (
	"fmt"
	"net"
	"testing"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/store"
)

// busyPort holds a port open for the duration of the test and returns it.
func busyPort(t *testing.T) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return l.Addr().(*net.TCPAddr).Port, func() {
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func inbound(tag string, port any) store.Item {
	return store.Item{
		Tag:    tag,
		Config: []byte(fmt.Sprintf(`{"protocol":"socks","listen":"127.0.0.1","port":%v}`, port)),
	}
}

// A taken proxy port must be reported before Xray starts: a start that fails on the
// bind crashes the daemon inside xray-core's commander (see probePorts).
func TestProbePortsRejectsABusyProxyPort(t *testing.T) {
	port, release := busyPort(t)

	var st store.State
	st.Settings.ProxyPort = port
	if err := probePorts(st); err == nil {
		t.Fatal("a taken proxy port was accepted")
	}

	release()
	if err := probePorts(st); err != nil {
		t.Fatalf("a free proxy port was refused: %v", err)
	}
}

func TestProbePortsRejectsABusyInboundPort(t *testing.T) {
	port, release := busyPort(t)

	st := store.State{Inbounds: []store.Item{inbound("clash", port)}}
	if err := probePorts(st); err == nil {
		t.Fatal("a taken inbound port was accepted")
	}

	release()
	if err := probePorts(st); err != nil {
		t.Fatalf("a free inbound port was refused: %v", err)
	}
}

func TestProbePortsDoesNotProbeWireGuardAsTCP(t *testing.T) {
	port, release := busyPort(t)
	defer release()

	wireguard := store.Item{
		Tag:    "wireguard",
		Config: []byte(fmt.Sprintf(`{"protocol":"wireguard","listen":"127.0.0.1","port":%d}`, port)),
	}
	st := store.State{Inbounds: []store.Item{wireguard}}
	if err := probePorts(st); err != nil {
		t.Fatalf("a WireGuard inbound sharing a TCP port was refused: %v", err)
	}
}

func TestProbePortsAllowsWireGuardOnProxyPort(t *testing.T) {
	port := freePort(t)
	var st store.State
	st.Settings.ProxyPort = port
	st.Inbounds = []store.Item{{
		Tag:    "wireguard",
		Config: []byte(fmt.Sprintf(`{"protocol":"wireguard","listen":"127.0.0.1","port":%d}`, port)),
	}}
	if err := probePorts(st); err != nil {
		t.Fatalf("a WireGuard inbound sharing the proxy's TCP port was refused: %v", err)
	}
}

// Xray's port field may be a quoted number; a valid config must not be rejected.
func TestProbePortsReadsAQuotedPort(t *testing.T) {
	port, release := busyPort(t)
	defer release()

	st := store.State{Inbounds: []store.Item{inbound("clash", fmt.Sprintf("%q", fmt.Sprint(port)))}}
	if err := probePorts(st); err == nil {
		t.Fatal("a taken quoted inbound port was accepted")
	}
}

// Two inbounds on one port, or an inbound on the proxy's port, fail when Xray binds the
// second one — after its API feature is up. The probe keeps its listeners open to catch
// that here instead.
func TestProbePortsRejectsTwoInboundsOnOnePort(t *testing.T) {
	port := freePort(t)
	st := store.State{Inbounds: []store.Item{inbound("a", port), inbound("b", port)}}
	if err := probePorts(st); err == nil {
		t.Fatal("two inbounds on one port were accepted")
	}
}

func TestProbePortsRejectsAnInboundOnTheProxyPort(t *testing.T) {
	port := freePort(t)
	var st store.State
	st.Settings.ProxyPort = port
	st.Inbounds = []store.Item{inbound("clash", port)}
	if err := probePorts(st); err == nil {
		t.Fatal("an inbound on the proxy port was accepted")
	}
}

// Ranges, lists and unix sockets have no single TCP port to probe; Xray handles them.
func TestProbePortsLeavesRangesAndUnixSocketsToXray(t *testing.T) {
	st := store.State{Inbounds: []store.Item{
		inbound("range", `"1000-2000"`),
		inbound("list", `"80,443"`),
		{Tag: "unix", Config: []byte(`{"protocol":"socks","listen":"/tmp/masir-test.sock","port":0}`)},
	}}
	if err := probePorts(st); err != nil {
		t.Fatalf("a range, a list or a unix socket was refused: %v", err)
	}
}
