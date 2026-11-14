package systemdns

import (
	"context"
	"slices"
	"testing"
)

// Exercise the actual no-cgo ABI without changing any system preferences.
func TestNativeCoreFoundationAndNetworkNotifications(t *testing.T) {
	n, err := newNative()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.1.1.1", "2001:4860:4860::8888", "Wi-Fi فارسی"}
	a := n.array(want)
	defer n.release(a)
	if got := n.strings(a); !slices.Equal(got, want) {
		t.Fatalf("CF round trip: %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	end, err := n.watch(ctx, make(chan struct{}, 1))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	end()
}

func TestNativeReadNetworkServices(t *testing.T) {
	n, err := newNative()
	if err != nil {
		t.Fatal(err)
	}
	name := n.str("Masir read-only DNS test")
	defer n.release(name)
	prefs := n.prefsCreate(0, name, 0)
	if prefs == 0 {
		t.Fatal(n.err("read preferences"))
	}
	defer n.release(prefs)
	services := n.servicesCopy(prefs)
	if services == 0 {
		t.Fatal(n.err("read network services"))
	}
	defer n.release(services)
	dns := n.str("DNS")
	defer n.release(dns)
	key := n.str("ServerAddresses")
	defer n.release(key)
	for i := int64(0); i < n.arrayCount(services); i++ {
		s := n.arrayGet(services, i)
		if n.text(n.serviceID(s)) == "" {
			t.Fatal("empty service ID")
		}
		if p := n.protocolGet(s, dns); p != 0 {
			if cfg := n.protocolConfig(p); cfg != 0 {
				_ = n.strings(n.dictGet(cfg, key))
			}
			n.release(p)
		}
	}
}
