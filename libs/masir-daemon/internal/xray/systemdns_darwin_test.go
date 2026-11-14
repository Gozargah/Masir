package xray

import (
	"context"
	"slices"
	"testing"
)

type fakeMacManager struct {
	starts, stops int
	name          string
	servers       []string
}

func (m *fakeMacManager) Start(_ context.Context, name string, servers []string) error {
	m.starts++
	m.name = name
	m.servers = servers
	return nil
}
func (m *fakeMacManager) Stop() error { m.stops++; return nil }
func TestMacDNSFollowsTunInboundNotProxyState(t *testing.T) {
	ctx := context.Background()
	backend := new(fakeMacManager)
	m := &macDNS{manager: backend}
	tun := profile(t, `{"tag":"tun-in","protocol":"tun","settings":{"name":"utun42","dns":["1.1.1.1","2606:4700:4700::1111"]}}`, baseOutbounds, "")
	if err := m.after(ctx, tun); err != nil {
		t.Fatal(err)
	}
	if backend.name != "utun42" || !slices.Equal(backend.servers, []string{"1.1.1.1", "2606:4700:4700::1111"}) {
		t.Fatal(backend)
	}
	if err := m.before(tun); err != nil {
		t.Fatal(err)
	}
	if err := m.after(ctx, tun); err != nil {
		t.Fatal(err)
	}
	if backend.starts != 1 || backend.stops != 0 {
		t.Fatal("unchanged TUN restarted DNS")
	}
	proxyOnly := profile(t, socks(mustFreePort(t)), baseOutbounds, "")
	if err := m.before(proxyOnly); err != nil {
		t.Fatal(err)
	}
	if backend.stops != 1 {
		t.Fatal("DNS not restored before TUN removal")
	}
	if err := m.after(ctx, proxyOnly); err != nil {
		t.Fatal(err)
	}
	if backend.starts != 1 {
		t.Fatal("proxy-only profile changed DNS")
	}
	if err := m.after(ctx, tun); err != nil {
		t.Fatal(err)
	}
	if backend.starts != 2 {
		t.Fatal("TUN reconnect did not reinstate DNS")
	}
}
