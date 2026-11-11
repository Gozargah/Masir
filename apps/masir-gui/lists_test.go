package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// manyRules makes n rules across the sections, each with its own JSON.
func manyRules(n int) []*pb.RoutingRule {
	rules := make([]*pb.RoutingRule, n)
	for i := range rules {
		switch i % 4 {
		case 0:
			rules[i] = &pb.RoutingRule{RuleTag: fmt.Sprintf("r%d", i), RuleJson: fmt.Sprintf(`{"domain":["full:site%d.test"]}`, i), Target: "proxy"}
		case 1:
			rules[i] = &pb.RoutingRule{RuleTag: fmt.Sprintf("r%d", i), RuleJson: fmt.Sprintf(`{"domain":["geosite:cat%d"]}`, i), Target: "proxy"}
		case 2:
			rules[i] = &pb.RoutingRule{RuleTag: fmt.Sprintf("r%d", i), RuleJson: fmt.Sprintf(`{"ip":["10.%d.0.0/16"]}`, i%256), Target: "direct"}
		default:
			rules[i] = &pb.RoutingRule{RuleTag: fmt.Sprintf("r%d", i), RuleJson: fmt.Sprintf(`{"port":["%d"]}`, 1000+i), Target: "block"}
		}
	}
	return rules
}

// manyConfigs makes n configurations for a subscription.
func manyConfigs(n int) []*pb.SubscriptionConfig {
	cfgs := make([]*pb.SubscriptionConfig, n)
	for i := range cfgs {
		cfgs[i] = &pb.SubscriptionConfig{Id: fmt.Sprintf("x%d", i), Name: fmt.Sprintf("Node %d", i), Protocol: "vless"}
	}
	return cfgs
}

// TestRulesListBuildsOnlyVisibleRows checks that opening the rules list doesn't build every
// rule: the list builds what it shows and a few around it, and still reaches the last one.
func TestRulesListBuildsOnlyVisibleRows(t *testing.T) {
	f := ukServers()
	f.rules = manyRules(200)
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click("RULES"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)

	first, last := a.rulesList.Visible()
	if first < 0 || last < 0 {
		t.Fatalf("the rules list wasn't laid out: %d..%d", first, last)
	}
	if built := last - first + 1; built > 24 {
		t.Fatalf("built %d rows of 200; want only the visible rows", built)
	}

	a.rulesList.ScrollToEnd()
	tt.Advance(time.Second)
	if !tt.HasText("1199") {
		t.Fatalf("the list lost its last rule: %q", tt.Texts())
	}
}

// TestServerPickerBuildsOnlyVisibleRows checks the servers picker the same way.
func TestServerPickerBuildsOnlyVisibleRows(t *testing.T) {
	f := ukServers()
	f.configs["s1"] = append(f.configs["s1"], manyConfigs(160)...)
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click(ltr("United Kingdom")); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)

	first, last := a.serversList.Visible()
	if first < 0 || last < 0 {
		t.Fatalf("the servers list wasn't laid out: %d..%d", first, last)
	}
	if built := last - first + 1; built > 24 {
		t.Fatalf("built %d rows of 160; want only the visible rows", built)
	}
}

// The subscription name stays visible while scrolling within its server group.
func TestServerPickerKeepsSubscriptionHeaderSticky(t *testing.T) {
	f := ukServers()
	f.configs["s1"] = append(f.configs["s1"], manyConfigs(100)...)
	a := newTestApp(f)
	a.open = sectionServers
	tt := tester(a)
	before, ok := tt.Find(ltr("MARZBAN"))
	if !ok {
		t.Fatal("missing subscription header before scrolling")
	}
	a.serversList.ScrollTo(40, ui.Start)
	tt.Frame()
	tt.Advance(time.Second)
	first, _ := a.serversList.Visible()
	if first < 10 {
		t.Fatal("list did not scroll")
	}
	after, ok := tt.Find(ltr("MARZBAN"))
	if !ok || abs32(after.Y-before.Y) > 1 {
		t.Fatalf("subscription header did not stay pinned: before=%v after=%v found=%v", before, after, ok)
	}
}

func TestServerPickerScrollRestoration(t *testing.T) {
	f := ukServers()
	f.configs["s1"] = append(f.configs["s1"], manyConfigs(100)...)
	f.rules = manyRules(100)
	a := newTestApp(f)
	a.open = sectionServers
	tt := tester(a)
	tt.Advance(time.Second)

	a.serversList.ScrollTo(40, ui.Start)
	tt.Frame()
	tt.Advance(time.Second)
	firstS, _ := a.serversList.Visible()
	if firstS < 30 {
		t.Fatalf("servers list did not scroll: first=%d", firstS)
	}

	// Switch to rules
	a.toggle(sectionRules)
	tt.Advance(time.Second)
	a.rulesList.ScrollTo(50, ui.Start)
	tt.Frame()
	tt.Advance(time.Second)
	firstR, _ := a.rulesList.Visible()
	if firstR < 40 {
		t.Fatalf("rules list did not scroll: first=%d", firstR)
	}

	// Switch back to servers
	a.toggle(sectionServers)
	tt.Advance(time.Second)
	firstSAfter, _ := a.serversList.Visible()
	if firstSAfter < 30 {
		t.Fatalf("servers list did not restore scroll: first=%d (expected near %d)", firstSAfter, firstS)
	}

	// Switch back to rules
	a.toggle(sectionRules)
	tt.Advance(time.Second)
	firstRAfter, _ := a.rulesList.Visible()
	if firstRAfter < 40 {
		t.Fatalf("rules list did not restore scroll: first=%d (expected near %d)", firstRAfter, firstR)
	}
}

func TestScrollRestorationPersistsAcrossLaunches(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "gui-settings.json")

	f := ukServers()
	f.configs["s1"] = append(f.configs["s1"], manyConfigs(100)...)
	f.rules = manyRules(100)

	a1 := newTestApp(f)
	a1.guiSettingsFile = settingsPath
	a1.open = sectionServers
	tt1 := tester(a1)
	tt1.Advance(time.Second)

	// Scroll servers
	a1.serversList.ScrollTo(42, ui.Start)
	tt1.Frame()
	tt1.Advance(time.Second)
	first1, _ := a1.serversList.Visible()
	if first1 < 30 {
		t.Fatalf("servers list did not scroll: first=%d", first1)
	}

	// Switch to rules and scroll rules
	a1.toggle(sectionRules)
	tt1.Advance(time.Second)
	a1.rulesList.ScrollTo(55, ui.Start)
	tt1.Frame()
	tt1.Advance(time.Second)
	ruleFirst1, _ := a1.rulesList.Visible()
	if ruleFirst1 < 40 {
		t.Fatalf("rules list did not scroll: first=%d", ruleFirst1)
	}

	// Close rules to flush settings
	a1.toggle(sectionRules)
	tt1.Advance(time.Second)
	a1.flushGUISettings()

	// Verify file was written
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("could not read settings file: %v", err)
	}
	var s guiSettings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	t.Logf("saved settings: %+v", s)
	if s.ServerScrollRow <= 0 || s.ServerScrollKey == "" {
		t.Fatalf("server scroll position not saved: %+v", s)
	}
	if s.RuleScrollRow <= 0 || s.RuleScrollKey == "" {
		t.Fatalf("rule scroll position not saved: %+v", s)
	}

	// Now launch a fresh app instance simulating a restart
	a2 := newTestApp(f)
	a2.guiSettingsFile = settingsPath
	a2.loadGUISettings()
	t.Logf("a2 loaded: key=%q row=%d restored=%v", a2.serverScrollKey, a2.serverScrollRow, a2.serverScrollRestored)
	tt2 := tester(a2)

	// Open servers list
	a2.toggle(sectionServers)
	tt2.Frame()
	tt2.Advance(time.Second)
	first2, _ := a2.serversList.Visible()
	t.Logf("a2 after toggle: first=%d restored=%v lastTop=%d", first2, a2.serverScrollRestored, a2.lastServerTop)
	if first2 < 30 {
		t.Fatalf("fresh app servers list did not restore scroll: first=%d (expected near %d)", first2, first1)
	}

	// Switch to rules list
	a2.toggle(sectionRules)
	tt2.Frame()
	tt2.Advance(time.Second)
	ruleFirst2, _ := a2.rulesList.Visible()
	if ruleFirst2 < 40 {
		t.Fatalf("fresh app rules list did not restore scroll: first=%d (expected near %d)", ruleFirst2, ruleFirst1)
	}
}

func TestFindScrollRowByKey(t *testing.T) {
	items := []serverListItem{
		{group: &serverGroup{id: "sub1", name: "Sub 1"}},
		{srv: server{key: serverKey{sub: "sub1", cfg: "c1"}}},
		{srv: server{key: serverKey{sub: "sub1", cfg: "c2"}}},
		{group: &serverGroup{id: "sub2", name: "Sub 2"}},
		{srv: server{key: serverKey{sub: "sub2", cfg: "c3"}}},
	}

	// Exact match by server key
	if idx := findServerScrollRow(items, "s:sub2:c3", 0); idx != 4 {
		t.Fatalf("expected 4, got %d", idx)
	}
	// Exact match by group header
	if idx := findServerScrollRow(items, "g:sub2", 0); idx != 3 {
		t.Fatalf("expected 3, got %d", idx)
	}
	// Fallback to group header if server is missing (e.g. collapsed)
	if idx := findServerScrollRow(items, "s:sub2:deleted_server", 0); idx != 3 {
		t.Fatalf("expected fallback to group index 3, got %d", idx)
	}
	// Fallback to row index if key missing
	if idx := findServerScrollRow(items, "s:unknown:unknown", 2); idx != 2 {
		t.Fatalf("expected fallback row 2, got %d", idx)
	}
	// Clamp to max index
	if idx := findServerScrollRow(items, "s:unknown:unknown", 100); idx != 4 {
		t.Fatalf("expected clamped index 4, got %d", idx)
	}
}

func TestServerPickerSearchDoesNotOverwriteScroll(t *testing.T) {
	f := ukServers()
	f.configs["s1"] = append(f.configs["s1"], manyConfigs(100)...)
	a := newTestApp(f)
	a.open = sectionServers
	tt := tester(a)
	tt.Advance(time.Second)

	a.serversList.ScrollTo(45, ui.Start)
	tt.Frame()
	tt.Advance(time.Second)
	first, _ := a.serversList.Visible()
	if first < 30 {
		t.Fatalf("servers list did not scroll: first=%d", first)
	}

	// User types a query that only matches 2 items
	a.query = "Node 10"
	tt.Frame()
	tt.Advance(time.Second)
	searchFirst, _ := a.serversList.Visible()
	if searchFirst > 5 {
		t.Fatalf("expected filtered list at top, got %d", searchFirst)
	}

	// User clears search
	a.query = ""
	tt.Frame()
	tt.Advance(time.Second)
	afterClear, _ := a.serversList.Visible()
	if afterClear < 30 {
		t.Fatalf("expected scroll position restored after clearing search, got %d", afterClear)
	}
}

func TestRulesListSearchDoesNotOverwriteScroll(t *testing.T) {
	f := ukServers()
	f.rules = manyRules(100)
	a := newTestApp(f)
	a.open = sectionRules
	tt := tester(a)
	tt.Advance(time.Second)

	a.rulesList.ScrollTo(50, ui.Start)
	tt.Frame()
	tt.Advance(time.Second)
	first, _ := a.rulesList.Visible()
	if first < 40 {
		t.Fatalf("rules list did not scroll: first=%d", first)
	}

	// User types a query
	a.ruleQuery = "cat10"
	tt.Frame()
	tt.Advance(time.Second)

	// User clears search
	a.ruleQuery = ""
	tt.Frame()
	tt.Advance(time.Second)
	afterClear, _ := a.rulesList.Visible()
	if afterClear < 40 {
		t.Fatalf("expected rules scroll position restored after clearing search, got %d", afterClear)
	}
}




