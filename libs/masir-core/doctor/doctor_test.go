package doctor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seams restores every package-level seam after the test.
func seams(t *testing.T) {
	t.Helper()
	oldListen, oldDial, oldLookup, oldRead := listen, dial, lookupHost, readFile
	oldConfig, oldProbe := configDir, probeProxy
	oldRegistry := registry
	t.Cleanup(func() {
		listen, dial, lookupHost, readFile = oldListen, oldDial, oldLookup, oldRead
		configDir, probeProxy, registry = oldConfig, oldProbe, oldRegistry
	})
}

func TestDaemonError(t *testing.T) {
	seams(t)
	if r := checkDaemonError(context.Background(), Env{DaemonRunning: true}); r.State != OK {
		t.Fatalf("no error = %+v, want OK", r)
	}
	if r := checkDaemonError(context.Background(), Env{DaemonRunning: true, Power: true, LastError: "proxy: bind: address already in use"}); r.State != Fail {
		t.Fatalf("connected with error = %+v, want Fail", r)
	}
	if r := checkDaemonError(context.Background(), Env{DaemonRunning: true, LastError: "proxy: bind: address already in use"}); r.State != Warn {
		t.Fatalf("idle with error = %+v, want Warn", r)
	}
	if r := checkDaemonError(context.Background(), Env{DaemonRunning: true, LastError: "proxy: bind: address already in use"}); !strings.Contains(r.Detail, "address already in use") {
		t.Fatalf("the daemon's own words must appear: %+v", r)
	}
}

func TestXrayIdleAndBroken(t *testing.T) {
	seams(t)
	if r := checkXray(context.Background(), Env{DaemonRunning: true, Power: false}); r.State != Skip {
		t.Fatalf("idle daemon = %+v, want Skip", r)
	}
	if r := checkXray(context.Background(), Env{DaemonRunning: true, Power: true, XrayRunning: false}); r.State != Fail {
		t.Fatalf("connected without Xray = %+v, want Fail", r)
	}
	if r := checkXray(context.Background(), Env{DaemonRunning: true, Power: true, XrayRunning: true, XrayVersion: "26.1.1"}); r.State != OK {
		t.Fatalf("running = %+v, want OK", r)
	}
}

func TestVersionsSkew(t *testing.T) {
	seams(t)
	if r := checkVersions(context.Background(), Env{DaemonRunning: true, CLIVersion: "0.1.10", DaemonVersion: "0.1.9"}); r.State != Warn {
		t.Fatalf("skew = %+v, want Warn", r)
	}
	if r := checkVersions(context.Background(), Env{DaemonRunning: true, CLIVersion: "0.1.10", DaemonVersion: "0.1.10"}); r.State != OK {
		t.Fatalf("same version = %+v, want OK", r)
	}
	if r := checkVersions(context.Background(), Env{DaemonRunning: false}); r.State != Skip {
		t.Fatalf("daemon down = %+v, want Skip", r)
	}
}

func TestProxyPortStates(t *testing.T) {
	seams(t)
	free := func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	busy := func(string, string) (net.Listener, error) { return nil, errors.New("bind: address already in use") }
	cases := []struct {
		name     string
		listen   func(string, string) (net.Listener, error)
		xray     bool
		want     State
		inDetail string
	}{
		{"xray up, port taken", busy, true, OK, "in use by masir"},
		{"xray up, port free", free, true, Warn, "nothing is listening"},
		{"xray down, port free", free, false, OK, "is free"},
		{"xray down, port taken", busy, false, Fail, "Another process holds"},
	}
	for _, c := range cases {
		listen = c.listen
		r := checkProxyPort(context.Background(), Env{ProxyAddress: "127.0.0.1:10808", XrayRunning: c.xray})
		if r.State != c.want || !strings.Contains(r.Detail, c.inDetail) {
			t.Errorf("%s: %+v, want state %v containing %q", c.name, r, c.want, c.inDetail)
		}
		if r.State != OK && r.Fix == "" {
			t.Errorf("%s: a problem without a fix: %+v", c.name, r)
		}
	}
}

func TestProxyReach(t *testing.T) {
	seams(t)
	probeProxy = func(string) (int, time.Duration, error) {
		return 0, 50 * time.Millisecond, errors.New("connection refused")
	}
	if r := checkProxyReach(context.Background(), Env{ProxyAddress: "127.0.0.1:10808", XrayRunning: true}); r.State != Warn {
		t.Fatalf("failed request = %+v, want Warn", r)
	}
	probeProxy = func(string) (int, time.Duration, error) { return 204, 120 * time.Millisecond, nil }
	if r := checkProxyReach(context.Background(), Env{ProxyAddress: "127.0.0.1:10808", XrayRunning: true}); r.State != OK || !strings.Contains(r.Detail, "120 ms") {
		t.Fatalf("good request = %+v, want OK with the timing", r)
	}
}

func TestDNS(t *testing.T) {
	seams(t)
	lookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("timeout") }
	if r := checkDNS(context.Background(), Env{}); r.State != Fail {
		t.Fatalf("failed lookup = %+v, want Fail", r)
	}
	lookupHost = func(context.Context, string) ([]string, error) { return []string{"93.184.216.34"}, nil }
	if r := checkDNS(context.Background(), Env{}); r.State != OK {
		t.Fatalf("ok lookup = %+v, want OK", r)
	}
}

func TestTunnel(t *testing.T) {
	seams(t)
	if r := checkTunnel(context.Background(), Env{TunRunning: false}); r.State != Skip {
		t.Fatalf("off = %+v, want Skip", r)
	}
	// The loopback address exists on every platform, so it stands in for a
	// gateway that is really there.
	if r := checkTunnel(context.Background(), Env{TunRunning: true, TunGateway: "127.0.0.1/24"}); r.State != OK {
		t.Fatalf("present gateway = %+v, want OK", r)
	}
	if r := checkTunnel(context.Background(), Env{TunRunning: true, TunGateway: "203.0.113.7/24"}); r.State != Fail {
		t.Fatalf("missing gateway = %+v, want Fail", r)
	}
}

func TestRouting(t *testing.T) {
	seams(t)
	dir := t.TempDir()
	configDir = func() (string, error) { return dir, nil }
	if r := checkRouting(context.Background(), Env{}); r.State != Skip {
		t.Fatalf("no file = %+v, want Skip", r)
	}
	if err := os.WriteFile(filepath.Join(dir, "routing.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := checkRouting(context.Background(), Env{}); r.State != Fail {
		t.Fatalf("broken file = %+v, want Fail", r)
	}
	if err := os.WriteFile(filepath.Join(dir, "routing.json"), []byte(`{"rules":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := checkRouting(context.Background(), Env{}); r.State != OK {
		t.Fatalf("valid file = %+v, want OK", r)
	}
}

func TestRunOrderPanicAndWorst(t *testing.T) {
	seams(t)
	registry = func() []Check {
		return []Check{
			{ID: "first", Title: "First", Run: func(context.Context, Env) Result { return Result{State: OK, Detail: "fine"} }},
			{ID: "boom", Title: "Boom", Run: func(context.Context, Env) Result { panic("boom") }},
			{ID: "slow", Title: "Slow", Run: func(ctx context.Context, env Env) Result {
				<-ctx.Done()
				return Result{State: Skip, Detail: "out of time"}
			}},
		}
	}
	reports := Run(context.Background(), Env{})
	if len(reports) != 3 || reports[0].ID != "first" || reports[1].ID != "boom" {
		t.Fatalf("reports = %+v, want registration order", reports)
	}
	if reports[1].State != Fail || !strings.Contains(reports[1].Detail, "crashed") {
		t.Fatalf("panic guard = %+v", reports[1])
	}
	if reports[2].State != Skip {
		t.Fatalf("timeout = %+v, want Skip from the check itself", reports[2])
	}
	if got := Worst(reports); got != Fail {
		t.Fatalf("worst = %v, want Fail", got)
	}
}

// Every registered check must carry a title and say something in every state;
// a silent check is worse than no check.
func TestEveryCheckSpeaks(t *testing.T) {
	seams(t)
	for _, c := range Checks() {
		if c.ID == "" || c.Title == "" || c.Run == nil || c.Group == "" {
			t.Errorf("incomplete check: %+v", c)
			continue
		}
		r := c.Run(context.Background(), Env{})
		if r.Detail == "" {
			t.Errorf("%s: empty detail in state %v", c.ID, r.State)
		}
		if (r.State == Fail || r.State == Warn) && r.Fix == "" {
			t.Errorf("%s: %v without a fix", c.ID, r.State)
		}
	}
}
