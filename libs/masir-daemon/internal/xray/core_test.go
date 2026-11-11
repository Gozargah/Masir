package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/xtls/xray-core/core"
	"net"
	"strings"
	"testing"
	"time"
)

func TestBootstrapPreservesPolicy(t *testing.T) {
	p, err := ParseProfile([]byte(`{"policy":{"levels":{"0":{"connIdle":300,"bufferSize":10}},"system":{"custom":{"x":1},"statsInboundUplink":false}},"outbounds":[{"tag":"direct","protocol":"freedom"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Policy struct {
			Levels map[string]map[string]int  `json:"levels"`
			System map[string]json.RawMessage `json:"system"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Policy.Levels["0"]["connIdle"] != 300 || got.Policy.Levels["0"]["bufferSize"] != 10 || string(got.Policy.System["custom"]) != `{"x":1}` {
		t.Fatalf("policy entries lost: %s", raw)
	}
	for _, key := range []string{"statsInboundUplink", "statsInboundDownlink", "statsOutboundUplink", "statsOutboundDownlink"} {
		if string(got.Policy.System[key]) != "true" {
			t.Errorf("%s = %s, want true", key, got.Policy.System[key])
		}
	}
}

func TestBootstrapRejectsNonObjectPolicy(t *testing.T) {
	for _, policy := range []string{`[]`, `{"system":[]}`} {
		p, err := ParseProfile([]byte(`{"policy":` + policy + `}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.bootstrap(); err == nil {
			t.Errorf("bootstrap accepted policy %s", policy)
		}
	}
}

const baseOutbounds = `{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}`

func mustFreePort(t *testing.T) int {
	t.Helper()
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func profile(t *testing.T, inbounds, outbounds, rules string) *Profile {
	t.Helper()
	p, err := ParseProfile([]byte(fmt.Sprintf(`{"inbounds":[%s],"outbounds":[%s],"routing":{"rules":[%s]}}`, inbounds, outbounds, rules)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// profileWithFallback is a config like compose builds: the catch-all points at a
// balancer whose selector is the outbound the fallback currently takes.
func profileWithFallback(t *testing.T, port int, target string) *Profile {
	t.Helper()
	outs := baseOutbounds + `,{"tag":"alt","protocol":"freedom"}`
	p, err := ParseProfile([]byte(fmt.Sprintf(
		`{"inbounds":[%s],"outbounds":[%s],"routing":{"balancers":[{"tag":"fb","selector":[%q]}],"rules":[{"ruleTag":"last","network":"tcp,udp","balancerTag":"fb"}]}}`,
		socks(port), outs, target)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func socks(port int) string {
	return fmt.Sprintf(`{"tag":"socks-in","listen":"127.0.0.1","port":%d,"protocol":"socks"}`, port)
}

func startCore(t *testing.T, p *Profile) *Core {
	t.Helper()
	c := New("")
	apply(t, c, p)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func apply(t *testing.T, c *Core, p *Profile) {
	t.Helper()
	if err := c.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func wantRoute(t *testing.T, c *Core, domain, outbound string, matched bool) {
	t.Helper()
	res, err := c.TestRoute(context.Background(), RouteQuery{Domain: domain, Port: 443})
	if err != nil {
		t.Fatalf("TestRoute(%s): %v", domain, err)
	}
	if res.OutboundTag != outbound || res.Matched != matched {
		t.Fatalf("TestRoute(%s) = %+v, want {%s %v}", domain, res, outbound, matched)
	}
}

func listening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func TestRuleChangesApplyLive(t *testing.T) {
	in := socks(mustFreePort(t))
	outs := baseOutbounds + `,{"tag":"alt","protocol":"freedom"}`
	c := startCore(t, profile(t, in, baseOutbounds, ""))
	inst := c.inst

	apply(t, c, profile(t, in, outs, ""))
	wantRoute(t, c, "example.com", "direct", false)

	r1 := `{"ruleTag":"r1","domain":["full:example.com"],"outboundTag":"alt"}`
	apply(t, c, profile(t, in, outs, r1))
	wantRoute(t, c, "example.com", "alt", true)
	wantRoute(t, c, "other.com", "direct", false)

	apply(t, c, profile(t, in, outs, `{"ruleTag":"r0","domain":["full:example.com"],"outboundTag":"block"},`+r1))
	wantRoute(t, c, "example.com", "block", true)

	apply(t, c, profile(t, in, outs, r1))
	wantRoute(t, c, "example.com", "alt", true)

	if c.inst != inst {
		t.Fatal("xray restarted; expected live changes")
	}
}

// A mode switch retargets the catch-all's balancer: no rule is touched and Xray keeps
// every connection it is carrying.
func TestFallbackSwitchAppliesLive(t *testing.T) {
	port := mustFreePort(t)
	c := startCore(t, profileWithFallback(t, port, "direct"))
	inst := c.inst
	wantRoute(t, c, "example.com", "direct", true)

	apply(t, c, profileWithFallback(t, port, "alt"))
	wantRoute(t, c, "example.com", "alt", true)

	apply(t, c, profileWithFallback(t, port, "direct"))
	wantRoute(t, c, "example.com", "direct", true)

	if c.inst != inst {
		t.Fatal("xray restarted; a mode switch must not rebuild it")
	}
}

func TestInboundChangesApplyLive(t *testing.T) {
	in := socks(mustFreePort(t))
	c := startCore(t, profile(t, in, baseOutbounds, ""))
	inst := c.inst
	port, port2 := mustFreePort(t), mustFreePort(t)
	extra := func(p int) string {
		return fmt.Sprintf(`,{"tag":"socks-2","listen":"127.0.0.1","port":%d,"protocol":"socks"}`, p)
	}

	apply(t, c, profile(t, in+extra(port), baseOutbounds, ""))
	if !listening(port) {
		t.Fatal("added inbound is not listening")
	}
	apply(t, c, profile(t, in+extra(port2), baseOutbounds, ""))
	if listening(port) || !listening(port2) {
		t.Fatal("changed inbound did not move ports")
	}
	apply(t, c, profile(t, in, baseOutbounds, ""))
	if listening(port2) {
		t.Fatal("removed inbound still listening")
	}

	if c.inst != inst {
		t.Fatal("xray restarted; expected live changes")
	}
}

func TestApplyFailureRestoresPrevious(t *testing.T) {
	in := socks(mustFreePort(t))
	c := startCore(t, profile(t, in, baseOutbounds, ""))
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	clash := fmt.Sprintf(`,{"tag":"clash","listen":"127.0.0.1","port":%d,"protocol":"socks"}`, busy.Addr().(*net.TCPAddr).Port)

	if err := c.Apply(context.Background(), profile(t, in+clash, baseOutbounds, "")); err == nil {
		t.Fatal("expected apply to fail on a busy port")
	}
	if !c.Running() || len(c.cur.inbounds) != 1 {
		t.Fatal("previous profile was not restored")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	p, err := ParseProfile([]byte(`{
		"log": {"loglevel":"warning"},
		"inbounds":  [{"tag":"in","port":1080,"protocol":"socks"}],
		"outbounds": [{"tag":"direct","protocol":"freedom"}],
		"routing":   {"domainStrategy":"AsIs","balancers":[{"tag":"fb","selector":["direct"]}],
		              "rules":[{"domain":["full:a.com"],"outboundTag":"direct"},{"network":"tcp,udp","balancerTag":"fb"}]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(apiInboundTag)) || bytes.Contains(raw, []byte(`"api"`)) {
		t.Fatalf("daemon plumbing leaked into xray.json:\n%s", raw)
	}
	back, err := ParseProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if back.needsRestart(p) || !sameItems(back.inbounds, p.inbounds) ||
		!sameItems(back.outbounds, p.outbounds) || !sameItems(back.rules, p.rules) ||
		back.fallback != p.fallback {
		t.Fatalf("round trip changed the profile:\n%s", raw)
	}
}

func TestCommonPrefix(t *testing.T) {
	mk := func(tag, out string) item {
		return item{Tag: tag, JSON: marshal(map[string]any{"ruleTag": tag, "outboundTag": out})}
	}
	cases := []struct {
		name string
		a, b []item
		want int
	}{
		{"same", []item{mk("a", "x"), mk("b", "y")}, []item{mk("a", "x"), mk("b", "y")}, 2},
		{"empty", nil, []item{mk("a", "x")}, 0},
		{"last changed", []item{mk("a", "x"), mk("b", "y")}, []item{mk("a", "x"), mk("b", "z")}, 1},
		{"middle changed", []item{mk("a", "x"), mk("b", "y"), mk("c", "z")}, []item{mk("a", "x"), mk("b", "q"), mk("c", "z")}, 1},
		{"inserted", []item{mk("a", "x"), mk("c", "z")}, []item{mk("a", "x"), mk("b", "y"), mk("c", "z")}, 1},
	}
	for _, c := range cases {
		if got := commonPrefix(c.a, c.b); got != c.want {
			t.Errorf("%s: commonPrefix = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTestRouteWhenStopped(t *testing.T) {
	if _, err := New("").TestRoute(context.Background(), RouteQuery{Domain: "a.com"}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("got %v, want ErrNotRunning", err)
	}
}

func TestOutboundReplaceAndRemoveApplyLive(t *testing.T) {
	c := startCore(t, profile(t, "", baseOutbounds, ""))
	inst := c.inst
	for _, protocol := range []string{"freedom", "blackhole"} {
		apply(t, c, profile(t, "", baseOutbounds+fmt.Sprintf(`,{"tag":"extra","protocol":%q}`, protocol), ""))
		handler := c.outbounds.GetHandler("extra")
		if handler == nil || !strings.Contains(handler.ProxySettings().Type, protocol) {
			t.Fatalf("extra handler: %v", handler)
		}
	}
	apply(t, c, profile(t, "", baseOutbounds, ""))
	if c.outbounds.GetHandler("extra") != nil || c.inst != inst {
		t.Fatal("outbound removal failed or restarted instance")
	}
}

func TestCancelledApplyPreservesInstance(t *testing.T) {
	p := profile(t, "", baseOutbounds, "")
	c := startCore(t, p)
	inst := c.inst
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Apply(ctx, profile(t, "", baseOutbounds+`,{"tag":"extra","protocol":"freedom"}`, "")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply: %v", err)
	}
	if c.inst != inst || c.cur != p {
		t.Fatal("cancelled apply changed running profile")
	}
}

func TestBootstrapHasNoControlAPI(t *testing.T) {
	p, err := ParseProfile([]byte(`{"api":{"tag":"masir-api","services":["HandlerService"]},"outbounds":[{"tag":"direct","protocol":"freedom"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"api"`)) || bytes.Contains(raw, []byte(apiInboundTag)) {
		t.Fatalf("control API in bootstrap: %s", raw)
	}
}

func TestMissingFeaturesReturnActionableError(t *testing.T) {
	c := New("")
	if err := c.bindFeatures(new(core.Instance)); err == nil || !strings.Contains(err.Error(), "inbound manager unavailable") {
		t.Fatalf("bindFeatures: %v", err)
	}
	if c.inbounds != nil || c.outbounds != nil || c.routing != nil || c.stats != nil {
		t.Fatal("failed binding published features")
	}
}

func TestFailedLiveOutboundAndRuleRestorePrevious(t *testing.T) {
	for _, kind := range []string{"outbound", "rule"} {
		t.Run(kind, func(t *testing.T) {
			rule := `{"ruleTag":"r1","domain":["full:example.com"],"outboundTag":"block"}`
			previous := profile(t, "", baseOutbounds+`,{"tag":"extra","protocol":"freedom"}`, rule)
			c := startCore(t, previous)
			var bad *Profile
			if kind == "outbound" {
				bad = profile(t, "", baseOutbounds+`,{"tag":"extra","protocol":"unknown-protocol"}`, rule)
			} else {
				bad = profile(t, "", baseOutbounds, `{"ruleTag":"bad","network":"tcp"}`)
			}
			if err := c.Apply(context.Background(), bad); err == nil {
				t.Fatal("accepted invalid live change")
			}
			if !c.Running() || c.cur != previous || c.outbounds.GetHandler("extra") == nil {
				t.Fatal("previous profile not restored")
			}
			wantRoute(t, c, "example.com", "block", true)
		})
	}
}
