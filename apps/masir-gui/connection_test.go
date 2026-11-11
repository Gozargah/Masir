package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
)

type featureDaemon struct {
	*fakeDaemon
	tun                   []bool
	tunErr                error
	probes                []string
	probeErr              error
	internetErr, proxyErr error
	refreshErr            error
	edited                *pb.AlterSubscriptionRequest
	removed               string
}

func (f *featureDaemon) SetTunEnabled(_ context.Context, r *pb.SetTunEnabledRequest, _ ...grpc.CallOption) (*pb.SetTunEnabledResponse, error) {
	f.tun = append(f.tun, r.Enabled)
	return &pb.SetTunEnabledResponse{}, f.tunErr
}
func (f *featureDaemon) TestLatency(_ context.Context, r *pb.TestLatencyRequest, _ ...grpc.CallOption) (*pb.TestLatencyResponse, error) {
	f.probes = append(f.probes, r.XrayConfigJson)
	err := f.probeErr
	if err == nil {
		if strings.Contains(r.XrayConfigJson, "freedom") {
			err = f.internetErr
		} else {
			err = f.proxyErr
		}
	}
	return &pb.TestLatencyResponse{LatencyMs: 42}, err
}

func TestSingleRoutingControlAndCompactActions(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	for _, want := range []string{"Tunnel", "Direct", "Traffic uses the tunnel unless a rule matches.", "Test connection", "Subscriptions"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q: %q", want, tt.Texts())
		}
	}
	for _, text := range []string{"Proxy only", "Default route", "Tunnel: Off", "Daemon uptime:"} {
		if tt.HasText(text) {
			t.Errorf("unexpected %q", text)
		}
	}
	if err := tt.Click("Direct"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Traffic goes direct unless a rule matches.") {
		t.Fatal("direct caption missing")
	}
}

func TestBothDefaultRoutesConnectWithTunnel(t *testing.T) {
	for _, route := range []pb.Fallback{pb.Fallback_FALLBACK_PROXY, pb.Fallback_FALLBACK_DIRECT} {
		f := &featureDaemon{fakeDaemon: ukServers()}
		f.status.Settings.Fallback = route
		a := newTestApp(f.fakeDaemon)
		a.d = f
		a.setPower(true)
		if len(f.tun) != 1 || !f.tun[0] || !f.called("power on") {
			t.Fatalf("route=%v tun=%v calls=%v", route, f.tun, f.calls)
		}
	}
}

func TestTunnelRefusalDoesNotTurnPowerOn(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers(), tunErr: errors.New("tunnel failed")}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.setPower(true)
	if f.called("power on") {
		t.Fatal("power enabled after tunnel refusal")
	}
	if tester(a).HasText("Use proxy only") {
		t.Fatal("proxy-only option returned")
	}
}

func TestDisconnectPreservesTunnelChoice(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	f.status.Settings.Tun = true
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.setPower(false)
	if len(f.tun) != 0 || !f.called("power off") {
		t.Fatalf("tun=%v calls=%v", f.tun, f.calls)
	}
}

func TestConnectionHealthIncludesAllActiveWarnings(t *testing.T) {
	f := ukServers()
	f.status.ActiveStale = true
	f.subs[0].Usage = &pb.Usage{ExpireAtMs: 1, TotalBytes: 100, UploadBytes: 40, DownloadBytes: 60}
	a := newTestApp(f)
	if got := a.connectionHealth(time.Now()); len(got) != 3 {
		t.Fatalf("warnings=%v", got)
	}
	a.status.ActiveSubscriptionId = "other"
	a.status.ActiveStale = false
	if got := a.connectionHealth(time.Now()); len(got) != 0 {
		t.Fatalf("unrelated subscription warning: %v", got)
	}
}

func TestConnectionProbesActualListenerAndReportsRPCFailure(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	f.status.ProxyRunning = true
	f.status.ProxyAddress = "0.0.0.0:7890"
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.testConnection()
	if len(f.probes) != 2 {
		t.Fatalf("probes=%v", f.probes)
	}
	if !strings.Contains(f.probes[0], "freedom") || !strings.Contains(f.probes[1], "127.0.0.1") || !strings.Contains(f.probes[1], "socks") {
		t.Fatalf("probes=%v", f.probes)
	}
	for _, p := range f.probes {
		if !json.Valid([]byte(p)) {
			t.Fatal("invalid probe config")
		}
	}
	f.probeErr = errors.New("offline")
	a.testConnection()
	if a.connection.testing || a.connection.internetOK || a.connection.proxyOK {
		t.Fatalf("state=%+v", a.connection)
	}
}

func TestConnectWaitsDuringAnUpdate(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	a.updates.state.phase = updateDownloading
	a.setPower(true)
	if f.called("power on") || f.called("tun") {
		t.Fatal("connection changed during update")
	}
}

func TestConnectionIndicatorOutcomes(t *testing.T) {
	a := newTestApp(ukServers())
	for _, tc := range []struct {
		internet, proxy, tested bool
		want                    string
	}{
		{true, true, true, "Internet & proxy are working"},
		{true, false, true, "Internet works · Proxy failed"},
		{false, true, true, "Proxy works · Internet failed"},
		{false, false, true, "Connection check failed"},
		{true, false, false, "Internet is working"},
	} {
		a.connection = connectionState{checked: true, internetOK: tc.internet, proxyOK: tc.proxy, proxyTested: tc.tested, identity: a.connectionIdentity()}
		text, _ := a.connectionMessage()
		if text != tc.want {
			t.Fatalf("got %q want %q", text, tc.want)
		}
	}
}

func TestHeaderChecksConnectionWithoutExtraStatusLine(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	f.status.Settings.Power = true
	f.status.XrayRunning = true
	f.status.ProxyRunning = true
	f.status.ProxyAddress = "127.0.0.1:7890"
	a := newTestApp(f.fakeDaemon)
	a.d = f
	tt := tester(a)
	if err := tt.Click("Test connection"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Connected") || !a.connection.checked || !a.connection.proxyOK {
		t.Fatalf("check failed: %q", tt.Texts())
	}
	f.probeErr = errors.New("offline")
	if err := tt.Click("Test connection"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Connection issue") {
		t.Fatalf("missing issue status: %q", tt.Texts())
	}
	if tt.HasText("42 ms") || tt.HasText("Internet: 42 ms") {
		t.Fatal("connection timing shown")
	}
}

func TestConnectionResultIsDiscardedAfterDisconnect(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	release := held(a)
	a.testConnection()
	a.setUnreachable()
	release()
	if a.connection.checked || a.connection.testing {
		t.Fatalf("stale result survived: %+v", a.connection)
	}
}

func TestHeaderClickChecksBothPathsAndShowsEachOutcome(t *testing.T) {
	for _, tc := range []struct {
		name            string
		internet, proxy bool
		title, subtitle string
	}{
		{"healthy", true, true, "Connected", "Internet OK · Proxy OK"},
		{"proxy failure", true, false, "Connection issue", "Internet OK · Proxy failed"},
		{"internet failure", false, true, "Connection issue", "Internet failed · Proxy OK"},
		{"both fail", false, false, "Connection issue", "Internet failed · Proxy failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &featureDaemon{fakeDaemon: ukServers()}
			f.status.Settings.Power = true
			f.status.XrayRunning = true
			f.status.ProxyRunning = true
			f.status.ProxyAddress = "127.0.0.1:7890"
			if !tc.internet {
				f.internetErr = errors.New("no internet")
			}
			if !tc.proxy {
				f.proxyErr = errors.New("proxy unavailable")
			}
			a := newTestApp(f.fakeDaemon)
			a.d = f
			tt := tester(a)
			if err := tt.Click("Test connection"); err != nil {
				t.Fatal(err)
			}
			if len(f.probes) != 2 {
				t.Fatalf("expected two separate probes, got %d", len(f.probes))
			}
			if !tt.HasText(tc.title) || !tt.HasText(tc.subtitle) {
				t.Fatalf("wrong result: %q", tt.Texts())
			}
			if bounds, ok := tt.Find(tc.subtitle); !ok || bounds.W < 100 || bounds.X+bounds.W > panelWidth-65 {
				t.Fatalf("connection result clipped: %+v", bounds)
			}
		})
	}
}

func TestMissingProxyCannotReportAHealthyConnectedState(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	f.status.Settings.Power = true
	f.status.XrayRunning = true
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.testConnection()
	title, subtitle, _, _ := a.statusText()
	if title != "Connection issue" || subtitle != "Internet OK · Proxy unavailable" {
		t.Fatalf("title=%q subtitle=%q", title, subtitle)
	}
	if len(f.probes) != 1 || a.connection.proxyOK {
		t.Fatal("reported a proxy result without a listener")
	}
}

func TestHeaderResultReturnsToLiveSession(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		a := newTestApp(ukServers())
		a.status.Settings.Power, a.status.XrayRunning = true, true
		now := time.Now()
		a.since = now.Add(-83 * time.Second)
		hold := 3 * time.Second
		if !healthy {
			hold = 5 * time.Second
		}
		a.connection = connectionState{checked: true, internetOK: true, proxyOK: healthy, proxyTested: true, identity: a.connectionIdentity(), resultUntil: now.Add(hold)}
		_, subtitle, _, _ := a.statusTextAt(now)
		if subtitle != a.connectionResult() {
			t.Fatalf("result missing: %q", subtitle)
		}
		title, subtitle, _, _ := a.statusTextAt(now.Add(hold))
		if subtitle != sessionClock(83*time.Second+hold) {
			t.Fatalf("did not restore advancing session: %q", subtitle)
		}
		if !healthy && title != "Connection issue" {
			t.Fatal("failed check lost its warning")
		}
		a.connection.testing = true
		title, subtitle, _, _ = a.statusTextAt(now)
		if title != "Connected" || subtitle != "Checking connection…" {
			t.Fatalf("checking changed connection state: %q / %q", title, subtitle)
		}
	}
}

func TestSessionClock(t *testing.T) {
	for _, tc := range []struct {
		elapsed time.Duration
		want    string
	}{
		{-time.Second, "00:00:00"},
		{time.Second, "00:00:01"},
		{83 * time.Second, "00:01:23"},
		{25*time.Hour + 2*time.Minute + 3*time.Second, "25:02:03"},
	} {
		if got := sessionClock(tc.elapsed); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

// The scheduler must restore the timer without a click or a daemon event.
func TestHeaderResultExpiresWithoutInput(t *testing.T) {
	a := newTestApp(ukServers())
	a.status.Settings.Power, a.status.XrayRunning = true, true
	initialized := false
	tt := ui.NewTester(func(c *ui.Context) {
		a.palette = paletteFor(c)
		if !initialized {
			initialized = true
			a.since = c.Now().Add(-83 * time.Second)
			a.connection = connectionState{checked: true, internetOK: true, proxyOK: true, proxyTested: true, identity: a.connectionIdentity(), resultUntil: c.Now().Add(3 * time.Second)}
		}
		a.header(c, a.palette)
	}, panelWidth, 60)
	tt.Frame()
	tt.Advance(2 * time.Second)
	if !tt.HasText("Internet OK · Proxy OK") {
		t.Fatal("result disappeared too soon")
	}
	tt.Advance(2 * time.Second)
	if !tt.HasText("00:01:27") {
		t.Fatalf("timer did not return on its own: %v", tt.Texts())
	}
	tt.Advance(time.Second)
	if !tt.HasText("00:01:28") {
		t.Fatalf("timer stopped ticking: %v", tt.Texts())
	}
}
