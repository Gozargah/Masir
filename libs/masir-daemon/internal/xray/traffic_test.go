package xray

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestSumTrafficCountsAppInboundsOnly(t *testing.T) {
	got := sumTraffic(map[string]int64{
		"inbound>>>masir-proxy>>>traffic>>>uplink":   100,
		"inbound>>>masir-proxy>>>traffic>>>downlink": 900,
		"inbound>>>tun-in>>>traffic>>>uplink":        20,
		"inbound>>>tun-in>>>traffic>>>downlink":      80,
		"inbound>>>masir-api>>>traffic>>>uplink":     5000,
		"inbound>>>masir-api>>>traffic>>>downlink":   5000,
		"inbound>>>dns-bootstrap>>>traffic>>>uplink": 7,
		"outbound>>>proxy>>>traffic>>>uplink":        12345,
		"inbound>>>odd":                              9,
	})
	if got != (Traffic{Up: 120, Down: 980}) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseOutboundTraffic(t *testing.T) {
	got := parseOutboundTraffic(map[string]int64{
		"outbound>>>direct>>>traffic>>>uplink":     200,
		"outbound>>>direct>>>traffic>>>downlink":   800,
		"outbound>>>proxy>>>traffic>>>uplink":      1500,
		"outbound>>>proxy>>>traffic>>>downlink":    4500,
		"inbound>>>masir-proxy>>>traffic>>>uplink": 100,
		"outbound>>>invalid":                       99,
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 outbounds, got %d", len(got))
	}
	if got["direct"] != (Traffic{Up: 200, Down: 800}) {
		t.Errorf("direct: got %+v", got["direct"])
	}
	if got["proxy"] != (Traffic{Up: 1500, Down: 4500}) {
		t.Errorf("proxy: got %+v", got["proxy"])
	}
}

func TestTrafficCountsBytesThroughARunningXray(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()

	port := mustFreePort(t)
	c := startCore(t, profile(t, socks(port), baseOutbounds, ""))

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	// SOCKS5: no auth, then CONNECT to the echo server.
	conn.Write([]byte{5, 1, 0})
	if _, err := io.ReadFull(conn, make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
	addr := echo.Addr().(*net.TCPAddr)
	req := append([]byte{5, 1, 0, 1}, addr.IP.To4()...)
	req = append(req, byte(addr.Port>>8), byte(addr.Port))
	conn.Write(req)
	if _, err := io.ReadFull(conn, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 4096)
	conn.Write(payload)
	if _, err := io.ReadFull(conn, make([]byte, len(payload))); err != nil {
		t.Fatal(err)
	}
	// A direct connection to a plain TCP destination is spliced on Linux (and Android):
	// Xray copies the response with TCPConn.ReadFrom and adds its bytes to the counters
	// only when that copy ends, so close this side before asking. Elsewhere every write
	// is counted as it happens.
	conn.Close()

	// Even then the counters are a moment behind the bytes; sample rather than failing
	// on the first look.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, ok, err := c.Traffic(context.Background())
		if err != nil || !ok {
			t.Fatalf("Traffic: ok=%v err=%v", ok, err)
		}
		outs, _, okOut, errOut := c.OutboundTraffic(context.Background())
		if errOut != nil || !okOut {
			t.Fatalf("OutboundTraffic: ok=%v err=%v", okOut, errOut)
		}
		if got.Up >= 4096 && got.Down >= 4096 && outs["direct"].Up >= 4096 && outs["direct"].Down >= 4096 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("counted inbound %+v outbound %+v for 4096 bytes each way", got, outs)
		}
		time.Sleep(10 * time.Millisecond)
	}

	_ = c.Close()
	if _, ok, _ := c.Traffic(context.Background()); ok {
		t.Fatal("Traffic reports ok after Xray stopped")
	}
	if _, _, ok, _ := c.OutboundTraffic(context.Background()); ok {
		t.Fatal("OutboundTraffic reports ok after Xray stopped")
	}
}

func TestDirectCountersCumulativeAndGeneration(t *testing.T) {
	ctx := context.Background()
	p := profile(t, "", baseOutbounds, "")
	c := startCore(t, p)
	for name, value := range map[string]int64{
		"inbound>>>app>>>traffic>>>uplink":           123,
		"inbound>>>dns-bootstrap>>>traffic>>>uplink": 999,
		"outbound>>>direct>>>traffic>>>downlink":     456,
	} {
		counter, err := c.stats.GetOrRegisterCounter(name)
		if err != nil {
			t.Fatal(err)
		}
		counter.Add(value)
	}
	for range 2 {
		got, ok, err := c.Traffic(ctx)
		if err != nil || !ok || got != (Traffic{Up: 123}) {
			t.Fatalf("Traffic: %+v %v %v", got, ok, err)
		}
	}
	got, gen, ok, err := c.OutboundTraffic(ctx)
	if err != nil || !ok || got["direct"] != (Traffic{Down: 456}) {
		t.Fatalf("OutboundTraffic: %+v %v %v", got, ok, err)
	}
	apply(t, c, profile(t, "", baseOutbounds+`,{"tag":"extra","protocol":"freedom"}`, ""))
	got, liveGen, _, err := c.OutboundTraffic(ctx)
	if err != nil || liveGen != gen || got["direct"].Down != 456 {
		t.Fatalf("live change reset counters/generation: %+v %d %v", got, liveGen, err)
	}
	c.Close()
	apply(t, c, p)
	got, nextGen, _, err := c.OutboundTraffic(ctx)
	if err != nil || nextGen <= gen || got["direct"].Down != 0 {
		t.Fatalf("restart counters/generation: %+v %d %v", got, nextGen, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := c.Traffic(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Traffic cancellation: %v", err)
	}
	if _, _, _, err := c.OutboundTraffic(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("OutboundTraffic cancellation: %v", err)
	}
}
