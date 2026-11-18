package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestRuleTargetOpensFullyInViewAndLeavesManualScrollingAlone(t *testing.T) {
	a := newTestApp(ukServers())
	a.newRule()
	tt := tester(a)
	tt.SetSize(panelWidth, 620)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Choose destination"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(80 * time.Millisecond)
	tt.Frame()
	if a.rulePage.formScroll.MaxY <= 0 || abs32(a.rulePage.formScroll.Y-a.rulePage.formScroll.MaxY) > 0.5 {
		t.Fatalf("scroll did not follow opening immediately: y=%v max=%v", a.rulePage.formScroll.Y, a.rulePage.formScroll.MaxY)
	}
	tt.Advance(620 * time.Millisecond)
	tt.Frame()
	body, ok := tt.Find("Destination options")
	if !ok {
		t.Fatal("missing target body")
	}
	viewport, _ := tt.Find("Rule form")
	if body.Y < viewport.Y-1 || body.Y+body.H > viewport.Y+viewport.H+1 {
		t.Fatalf("target body needs manual scrolling: body=%+v viewport=%+v", body, viewport)
	}
	if abs32(a.rulePage.formScroll.Y-a.rulePage.formScroll.MaxY) > 0.5 {
		t.Fatalf("target stopped short of the bottom: y=%v max=%v", a.rulePage.formScroll.Y, a.rulePage.formScroll.MaxY)
	}
	if a.rulePage.revealTarget {
		t.Fatal("target reveal never settled")
	}
	tt.Scroll(285, viewport.Y+10, 0, -1000)
	tt.Advance(500 * time.Millisecond)
	later, _ := tt.Find("Destination options")
	if later.Y <= body.Y+1 {
		t.Fatal("auto reveal prevented scrolling away")
	}
}
func TestRuleTargetUsesGroupedServerRowsWithoutChangingActiveServer(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	a.newRule()
	before, _ := a.activeKey()
	tt := tester(a)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.SetSize(panelWidth, 620)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Choose destination"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	s := a.subscriptions.servers[1]
	if _, ok := tt.Find("Target server group " + s.subName); !ok {
		t.Fatal("subscription hierarchy missing")
	}
	if err := tt.Click("Pin " + s.subName + " " + s.cfg.Name); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.rulePage.target != "config:"+s.key.sub+"/"+s.key.cfg {
		t.Fatal("server was not pinned to rule")
	}
	after, _ := a.activeKey()
	if after != before {
		t.Fatal("pinning changed global active server")
	}
	if err := tt.Click("Search servers…"); err != nil {
		t.Fatal(err)
	}
	tt.Type("nothing-matches")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("No matching servers.") {
		t.Fatal("search empty state missing")
	}
	if a.rulePage.target != "config:"+s.key.sub+"/"+s.key.cfg {
		t.Fatal("search lost selected target")
	}
}

func TestTargetListEdgeDoesNotScrollTheRuleForm(t *testing.T) {
	a := newTestApp(ukServers())
	a.newRule()
	tt := tester(a)
	tt.SetSize(panelWidth, 620)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Choose destination"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	list, ok := tt.Find("Target servers")
	if !ok {
		t.Fatal("missing server list")
	}
	before := a.rulePage.formScroll.Y
	if before <= 0 {
		t.Fatal("outer pane did not scroll down for target")
	}
	tt.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, -100)
	tt.Frame()
	if a.rulePage.formScroll.Y != before {
		t.Fatalf("list edge moved rule form: %v -> %v", before, a.rulePage.formScroll.Y)
	}
}

func TestRuleTargetGroupCollapseAndLatencyActions(t *testing.T) {
	f := &subscriptionLatencyDaemon{featureDaemon: &featureDaemon{fakeDaemon: ukServers()}}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.rulePage.target = "config:s1/c1"
	tt := ui.NewTester(func(c *ui.Context) { a.ruleDestinationServers(c, paletteFor(c)) }, panelWidth, 200)
	if err := tt.Click(ltr("MARZBAN")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.isGroupCollapsed("s1") || tt.HasText(ltr("England")) {
		t.Fatal("clicking the subscription name did not hide its configs")
	}
	if err := tt.Click("Expand marzban"); err != nil {
		t.Fatal(err)
	}
	a.ruleDestinationQuery = "deutschland"
	tt.Frame()
	if err := tt.Click("Collapse marzban"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText(ltr("Deutschland")) || a.isGroupCollapsed("s1") {
		t.Fatal("collapse during search should hide the matches for that search")
	}
	if err := tt.Click("Test all marzban configs"); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || f.requests[0].SubscriptionId != "s1" || len(f.requests[0].ConfigIds) != 2 {
		t.Fatalf("group test should include filtered-out configs: %v", f.requests)
	}
	if err := tt.Click("Expand marzban"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Test latency"); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 || len(f.requests[1].ConfigIds) != 1 || f.requests[1].ConfigIds[0] != "c2" {
		t.Fatalf("badge should retest only its config: %v", f.requests)
	}
	if a.rulePage.target != "config:s1/c1" {
		t.Fatal("testing latency changed the rule target")
	}
}
