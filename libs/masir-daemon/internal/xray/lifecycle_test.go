package xray

import (
	"context"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestControlLifecycleImmediateCloseAndFailedStart(t *testing.T) {
	before := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(before)
	for range 20 {
		c := New("")
		good := profile(t, "", baseOutbounds, "")
		if err := c.Apply(context.Background(), good); err != nil {
			t.Fatal(err)
		}
		busy, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		bad := profile(t, socks(busy.Addr().(*net.TCPAddr).Port), baseOutbounds, "")
		err = c.Apply(context.Background(), bad)
		busy.Close()
		if err == nil {
			c.Close()
			t.Fatal("accepted occupied inbound")
		}
		if !c.Running() {
			t.Fatal("failed start lost the previous running profile")
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectManagementIgnoresUserRoutingAndClearsFeatures(t *testing.T) {
	c := New("")
	defer c.Close()
	p := profile(t, "", baseOutbounds, `{"ruleTag":"block-all","network":"tcp,udp","outboundTag":"block"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.Traffic(ctx); err != nil || !ok {
		t.Fatalf("Traffic: %v %v", ok, err)
	}
	inst := c.inst
	apply(t, c, profile(t, "", baseOutbounds, ""))
	if c.inst != inst {
		t.Fatal("management required restart")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.inst != nil || c.inbounds != nil || c.outbounds != nil || c.routing != nil || c.balancer != nil || c.stats != nil {
		t.Fatal("features survived shutdown")
	}
	if _, ok, err := c.Traffic(ctx); ok || err != nil {
		t.Fatalf("stopped Traffic: %v %v", ok, err)
	}
	apply(t, c, p)
	wantRoute(t, c, "example.com", "block", true)
}

func TestCounterReadsDuringRepeatedRestart(t *testing.T) {
	c := New("")
	defer c.Close()
	p := profile(t, "", baseOutbounds, "")
	apply(t, c, p)
	var readers sync.WaitGroup
	done := make(chan struct{})
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				if _, _, err := c.Traffic(context.Background()); err != nil {
					t.Error(err)
					return
				}
				if _, _, _, err := c.OutboundTraffic(context.Background()); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	defer func() { close(done); readers.Wait() }()
	for range 20 {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		apply(t, c, p)
	}
}
