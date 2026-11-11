package server

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/identity"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/subs"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/xray"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func TestSelectConfigCollectsPreviousProxyTraffic(t *testing.T) {
	s := newTestServer(t)
	subID, cfgs := addSub(t, s, "Next")
	if err := s.subs.SetActive(subs.Active{SubscriptionID: "old", ConfigID: "old", Outbound: []byte(`{"protocol":"freedom"}`)}); err != nil {
		t.Fatal(err)
	}
	on(t, s)
	if _, err := s.SetFallback(context.Background(), &pb.SetFallbackRequest{Fallback: pb.Fallback_FALLBACK_PROXY}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRouting(context.Background(), &pb.AddRoutingRequest{RuleTag: "test-proxy", RuleJson: `{"ip":["127.0.0.1/32"]}`, Target: "proxy"}); err != nil {
		t.Fatal(err)
	}
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()
	conn, err := s.xray.Dial(context.Background(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("traffic for the old server")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, make([]byte, len("traffic for the old server"))); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, _, ok, err := s.xray.OutboundTraffic(context.Background())
		if err != nil || !ok {
			t.Fatalf("outbound stats: ok=%v err=%v", ok, err)
		}
		if raw["proxy"].Up > 0 && raw["proxy"].Down > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("proxy traffic was not counted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := s.SelectConfig(context.Background(), &pb.SelectConfigRequest{SubscriptionId: subID, ConfigId: cfgs["Next"]}); err != nil {
		t.Fatal(err)
	}
	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()
	if got := s.cumulativeServer["old/old"]; got.Up == 0 || got.Down == 0 {
		t.Fatalf("previous server did not receive its pending traffic: %+v", got)
	}
	if got := s.cumulativeServer[subID+"/"+cfgs["Next"]]; got.Up != 0 || got.Down != 0 {
		t.Fatalf("new server received old traffic: %+v", got)
	}
}

func TestWaitingCollectorDoesNotHoldStateLock(t *testing.T) {
	s := newTestServer(t)
	s.collectMu.Lock()
	done := make(chan struct{})
	go func() { s.collectTraffic(context.Background()); close(done) }()
	// Once the collector has had time to reach collectMu, status must remain readable.
	time.Sleep(50 * time.Millisecond)
	read := make(chan struct{})
	go func() { _ = s.status(); close(read) }()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		s.collectMu.Unlock()
		t.Fatal("status blocked behind traffic collection")
	}
	s.collectMu.Unlock()
	<-done
}

func TestRateIsBytesPerSecondBetweenReadings(t *testing.T) {
	t0 := time.Now()
	prev := TrafficReading{Up: 1000, Down: 5000, Valid: true}
	cur := TrafficReading{Up: 1500, Down: 9000, Valid: true}
	got := rate(prev, t0, cur, t0.Add(500*time.Millisecond))
	if got == nil || got.UplinkBps != 1000 || got.DownlinkBps != 8000 || got.UplinkTotal != 1500 || got.DownlinkTotal != 9000 {
		t.Fatalf("got %+v", got)
	}
}

func TestRateWaitsForAFirstReading(t *testing.T) {
	if rate(TrafficReading{}, time.Time{}, TrafficReading{Up: 1, Valid: true}, time.Now()) != nil {
		t.Fatal("a rate without an earlier reading")
	}
}

func TestRateCountsFromZeroAfterARestart(t *testing.T) {
	t0 := time.Now()
	prev := TrafficReading{Up: 9000, Down: 9000, Valid: true}
	cur := TrafficReading{Up: 100, Down: 300, Valid: true}
	got := rate(prev, t0, cur, t0.Add(time.Second))
	if got.UplinkBps != 100 || got.DownlinkBps != 300 {
		t.Fatalf("got %+v", got)
	}
}

func TestOutboundTrafficAccumulationAndRestart(t *testing.T) {
	s := New(xray.New(""), identity.Identity{UID: os.Getuid(), GID: os.Getgid(), Name: "tester"})

	// Reading 1 (generation 1, fresh start)
	raw1 := map[string]xray.Traffic{
		"proxy":  {Up: 100, Down: 200},
		"direct": {Up: 50, Down: 60},
	}
	s.updateTraffic(raw1, 1, true, "sub1", "cfg1", nil)

	s.trafficMu.Lock()
	if got := s.cumulativeOutbound["proxy"]; got.Up != 100 || got.Down != 200 {
		t.Fatalf("expected proxy 100/200, got %+v", got)
	}
	if got := s.cumulativeServer["sub1/cfg1"]; got.Up != 100 || got.Down != 200 {
		t.Fatalf("expected server sub1/cfg1 100/200, got %+v", got)
	}
	s.trafficMu.Unlock()

	// Reading 2 (generation 1, delta accumulation)
	raw2 := map[string]xray.Traffic{
		"proxy":  {Up: 150, Down: 250}, // +50 up, +50 down
		"direct": {Up: 70, Down: 80},   // +20 up, +20 down
	}
	s.updateTraffic(raw2, 1, true, "sub1", "cfg1", nil)

	s.trafficMu.Lock()
	if got := s.cumulativeOutbound["proxy"]; got.Up != 150 || got.Down != 250 {
		t.Fatalf("expected proxy 150/250, got %+v", got)
	}
	if got := s.cumulativeServer["sub1/cfg1"]; got.Up != 150 || got.Down != 250 {
		t.Fatalf("expected server sub1/cfg1 150/250, got %+v", got)
	}
	s.trafficMu.Unlock()

	// Reading 3 (generation 2, Xray restarted, server changed to sub1/cfg2)
	// New instance counters start from 0
	raw3 := map[string]xray.Traffic{
		"proxy":  {Up: 40, Down: 60},
		"direct": {Up: 10, Down: 15},
	}
	s.updateTraffic(raw3, 2, true, "sub1", "cfg2", nil)

	s.trafficMu.Lock()
	// Outbound proxy should be 150 + 40 = 190 up, 250 + 60 = 310 down
	if got := s.cumulativeOutbound["proxy"]; got.Up != 190 || got.Down != 310 {
		t.Fatalf("expected proxy 190/310 after restart, got %+v", got)
	}
	// Old server sub1/cfg1 should retain its traffic (survived restart)
	if got := s.cumulativeServer["sub1/cfg1"]; got.Up != 150 || got.Down != 250 {
		t.Fatalf("expected sub1/cfg1 to retain 150/250, got %+v", got)
	}
	// New server sub1/cfg2 should have 40/60
	if got := s.cumulativeServer["sub1/cfg2"]; got.Up != 40 || got.Down != 60 {
		t.Fatalf("expected sub1/cfg2 40/60, got %+v", got)
	}
	s.trafficMu.Unlock()

	// Reading 4: Pinned server rule tag
	pinnedTag := "sub-sub2-cfg3"
	tagToServer := map[string]string{pinnedTag: "sub2/cfg3"}
	raw4 := map[string]xray.Traffic{
		"proxy":   {Up: 40, Down: 60},
		pinnedTag: {Up: 300, Down: 400},
	}
	s.updateTraffic(raw4, 2, true, "sub1", "cfg2", tagToServer)

	s.trafficMu.Lock()
	if got := s.cumulativeServer["sub2/cfg3"]; got.Up != 300 || got.Down != 400 {
		t.Fatalf("expected pinned server sub2/cfg3 300/400, got %+v", got)
	}
	s.trafficMu.Unlock()

	// Reading 5: Stopped / Idle (ok = false)
	s.updateTraffic(nil, 0, false, "sub1", "cfg2", nil)
	s.trafficMu.Lock()
	// Cumulative values must remain intact
	if got := s.cumulativeOutbound["proxy"]; got.Up != 190 || got.Down != 310 {
		t.Fatalf("expected proxy to remain 190/310 when stopped, got %+v", got)
	}
	if got := s.cumulativeServer["sub1/cfg1"]; got.Up != 150 || got.Down != 250 {
		t.Fatalf("expected sub1/cfg1 to remain 150/250 when stopped, got %+v", got)
	}
	s.trafficMu.Unlock()
}

func TestGetOutboundTrafficRPC(t *testing.T) {
	s := New(xray.New(""), identity.Identity{UID: os.Getuid(), GID: os.Getgid(), Name: "tester"})

	s.trafficMu.Lock()
	s.cumulativeOutbound["direct"] = xray.Traffic{Up: 10, Down: 20}
	s.cumulativeOutbound["proxy"] = xray.Traffic{Up: 100, Down: 200}
	s.cumulativeServer["s1/c2"] = xray.Traffic{Up: 30, Down: 40}
	s.cumulativeServer["s1/c1"] = xray.Traffic{Up: 50, Down: 60}
	s.trafficMu.Unlock()

	resp, err := s.GetOutboundTraffic(context.Background(), &pb.GetOutboundTrafficRequest{})
	if err != nil {
		t.Fatalf("GetOutboundTraffic error: %v", err)
	}

	if len(resp.Outbounds) != 2 {
		t.Fatalf("expected 2 outbounds, got %d", len(resp.Outbounds))
	}
	// Verify sorted order
	if resp.Outbounds[0].Tag != "direct" || resp.Outbounds[1].Tag != "proxy" {
		t.Fatalf("unexpected outbounds order: %+v", resp.Outbounds)
	}
	if resp.Outbounds[1].UplinkBytes != 100 || resp.Outbounds[1].DownlinkBytes != 200 {
		t.Fatalf("unexpected proxy bytes: %+v", resp.Outbounds[1])
	}

	if len(resp.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(resp.Servers))
	}
	// Verify sorted by (SubscriptionId, ConfigId)
	if resp.Servers[0].SubscriptionId != "s1" || resp.Servers[0].ConfigId != "c1" {
		t.Fatalf("expected first server s1/c1, got %+v", resp.Servers[0])
	}
	if resp.Servers[0].UploadBytes != 50 || resp.Servers[0].DownloadBytes != 60 {
		t.Fatalf("unexpected s1/c1 bytes: %+v", resp.Servers[0])
	}
	if resp.Servers[1].SubscriptionId != "s1" || resp.Servers[1].ConfigId != "c2" {
		t.Fatalf("expected second server s1/c2, got %+v", resp.Servers[1])
	}
}

func TestOutboundTrafficInStatusAndSubscriptionsRPC(t *testing.T) {
	s := newTestServer(t)
	subID, cfgs := addSub(t, s, "ServerA")
	cfgID := cfgs["ServerA"]

	// Select the server as active
	if _, err := s.SelectConfig(context.Background(), &pb.SelectConfigRequest{
		SubscriptionId: subID,
		ConfigId:       cfgID,
	}); err != nil {
		t.Fatalf("SelectConfig error: %v", err)
	}

	// Populate cumulative traffic
	s.trafficMu.Lock()
	s.cumulativeOutbound["proxy"] = xray.Traffic{Up: 500, Down: 1000}
	s.cumulativeServer[subID+"/"+cfgID] = xray.Traffic{Up: 500, Down: 1000}
	s.trafficMu.Unlock()

	// 1. GetStatus
	statusResp, err := s.GetStatus(context.Background(), &pb.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus error: %v", err)
	}
	var foundProxy bool
	for _, ot := range statusResp.Status.OutboundTraffic {
		if ot.Tag == "proxy" {
			foundProxy = true
			if ot.UplinkBytes != 500 || ot.DownlinkBytes != 1000 {
				t.Fatalf("unexpected proxy bytes in status: %+v", ot)
			}
		}
	}
	if !foundProxy {
		t.Fatalf("proxy not found in status outbound_traffic: %+v", statusResp.Status.OutboundTraffic)
	}

	// 2. ListSubscriptions(include_configs: true)
	listResp, err := s.ListSubscriptions(context.Background(), &pb.ListSubscriptionsRequest{IncludeConfigs: true})
	if err != nil {
		t.Fatalf("ListSubscriptions error: %v", err)
	}
	if len(listResp.Subscriptions) == 0 || len(listResp.Subscriptions[0].Configs) == 0 {
		t.Fatalf("expected at least one subscription with configs: %+v", listResp)
	}
	cfg := listResp.Subscriptions[0].Configs[0]
	if cfg.UploadBytes != 500 || cfg.DownloadBytes != 1000 {
		t.Fatalf("expected 500/1000 upload/download bytes on config, got %d/%d", cfg.UploadBytes, cfg.DownloadBytes)
	}

	// 3. GetActiveConfig
	actResp, err := s.GetActiveConfig(context.Background(), &pb.GetActiveConfigRequest{})
	if err != nil {
		t.Fatalf("GetActiveConfig error: %v", err)
	}
	if actResp.Config == nil || actResp.Config.UploadBytes != 500 || actResp.Config.DownloadBytes != 1000 {
		t.Fatalf("expected 500/1000 upload/download bytes on active config, got %+v", actResp.Config)
	}
}

func TestTrafficSurvivesXrayPowerCycle(t *testing.T) {
	s := newTestServer(t)
	subID, cfgs := addSub(t, s, "Alpha")
	cfgID := cfgs["Alpha"]

	if _, err := s.SelectConfig(context.Background(), &pb.SelectConfigRequest{
		SubscriptionId: subID,
		ConfigId:       cfgID,
	}); err != nil {
		t.Fatalf("SelectConfig error: %v", err)
	}

	on(t, s)
	if !s.xray.Running() {
		t.Fatal("expected Xray to be running")
	}

	// Simulate traffic collected while running
	s.trafficMu.Lock()
	s.cumulativeOutbound["proxy"] = xray.Traffic{Up: 2048, Down: 4096}
	s.cumulativeServer[subID+"/"+cfgID] = xray.Traffic{Up: 2048, Down: 4096}
	s.trafficMu.Unlock()

	// Power off (stops Xray)
	if _, err := s.SetPower(context.Background(), &pb.SetPowerRequest{On: false}); err != nil {
		t.Fatalf("SetPower(false) error: %v", err)
	}
	if s.xray.Running() {
		t.Fatal("expected Xray to be stopped")
	}

	// Traffic should still be reported when stopped/idle
	respOff, err := s.GetOutboundTraffic(context.Background(), &pb.GetOutboundTrafficRequest{})
	if err != nil {
		t.Fatalf("GetOutboundTraffic when stopped error: %v", err)
	}
	if len(respOff.Servers) != 1 || respOff.Servers[0].UploadBytes != 2048 || respOff.Servers[0].DownloadBytes != 4096 {
		t.Fatalf("unexpected traffic while stopped: %+v", respOff)
	}

	// Power on (restarts Xray)
	on(t, s)
	if !s.xray.Running() {
		t.Fatal("expected Xray to be running again")
	}

	// Traffic must survive the power cycle
	respOn, err := s.GetOutboundTraffic(context.Background(), &pb.GetOutboundTrafficRequest{})
	if err != nil {
		t.Fatalf("GetOutboundTraffic when restarted error: %v", err)
	}
	if len(respOn.Servers) != 1 || respOn.Servers[0].UploadBytes != 2048 || respOn.Servers[0].DownloadBytes != 4096 {
		t.Fatalf("traffic did not survive restart: %+v", respOn)
	}
}
