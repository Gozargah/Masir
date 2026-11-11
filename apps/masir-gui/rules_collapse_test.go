package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"github.com/egoist/mygo/ui"
)

func multiRuleDaemon() *fakeDaemon {
	f := ukServers()
	f.rules = append(f.rules, &pb.RoutingRule{RuleTag: "r4", RuleJson: `{"ip":["1.1.1.1"]}`, Target: "proxy"})
	return f
}

func openTestRules(t *testing.T, f *fakeDaemon) (*app, *ui.Tester) {
	t.Helper()
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click("RULES"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	return a, tt
}

func TestRuleSectionCollapseAndExpand(t *testing.T) {
	f := multiRuleDaemon()
	a, tt := openTestRules(t, f)

	if !tt.HasText(ltr("Spotify")) {
		t.Fatalf("initially Spotify should be visible: %q", tt.Texts())
	}

	// Click chevron to collapse Applications
	if err := tt.Click("Collapse Applications"); err != nil {
		t.Fatalf("could not click collapse Applications chevron: %v", err)
	}
	tt.Advance(time.Second)

	if tt.HasText(ltr("Spotify")) {
		t.Fatal("Spotify should be hidden after collapsing Applications")
	}
	if !a.isRuleSectionCollapsed("Applications") {
		t.Fatal("Applications should be recorded as collapsed")
	}

	// Click header to expand Applications
	if err := tt.Click(ltr("APPLICATIONS (1)")); err != nil {
		t.Fatalf("could not click header to expand: %v", err)
	}
	tt.Advance(time.Second)

	if !tt.HasText(ltr("Spotify")) {
		t.Fatal("Spotify should be visible after expanding Applications")
	}
	if a.isRuleSectionCollapsed("Applications") {
		t.Fatal("Applications should be recorded as expanded")
	}
}

func TestRuleSectionCollapseSearchShowsMatchingRules(t *testing.T) {
	f := multiRuleDaemon()
	a, tt := openTestRules(t, f)

	// Collapse Applications
	if err := tt.Click("Collapse Applications"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)

	if tt.HasText(ltr("Spotify")) {
		t.Fatal("Spotify should be hidden when collapsed")
	}

	// Search for Spotify
	a.ruleQuery = "spotify"
	tt.Frame()
	tt.Advance(time.Second)

	if !tt.HasText(ltr("Spotify")) {
		t.Fatal("Spotify should be found by search even if its section is collapsed")
	}

	// Clear search
	a.ruleQuery = ""
	tt.Frame()
	tt.Advance(time.Second)

	if tt.HasText(ltr("Spotify")) {
		t.Fatal("Spotify should be hidden again after search is cleared")
	}
}

func TestRulesToolbarHasNoCollapseAllButton(t *testing.T) {
	_, tt := openTestRules(t, multiRuleDaemon())
	if tt.HasText("Collapse All") || tt.HasText("Expand All") {
		t.Fatal("rules toolbar should not offer bulk folding")
	}
}

func TestRuleSectionsCollapsePersistenceAcrossLaunches(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "gui-settings.json")

	f := multiRuleDaemon()
	a1 := newTestApp(f)
	a1.guiSettingsFile = settingsPath
	tt1 := tester(a1)
	if err := tt1.Click("RULES"); err != nil {
		t.Fatal(err)
	}
	tt1.Advance(time.Second)

	// Collapse Applications and Domains
	if err := tt1.Click("Collapse Applications"); err != nil {
		t.Fatal(err)
	}
	if err := tt1.Click("Collapse Domains"); err != nil {
		t.Fatal(err)
	}
	tt1.Advance(time.Second)

	// Close section to flush
	a1.toggle(sectionRules)
	tt1.Advance(time.Second)
	a1.flushGUISettings()

	// Verify json file
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings: %v", err)
	}
	var s guiSettings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(s.CollapsedRuleSections) != 2 {
		t.Fatalf("expected 2 collapsed rule sections, got %+v", s.CollapsedRuleSections)
	}

	// Launch a new app instance
	a2 := newTestApp(f)
	a2.guiSettingsFile = settingsPath
	a2.loadGUISettings()
	tt2 := tester(a2)
	if err := tt2.Click("RULES"); err != nil {
		t.Fatal(err)
	}
	tt2.Advance(time.Second)

	if tt2.HasText(ltr("Spotify")) {
		t.Fatal("Spotify in Applications should remain collapsed after reload")
	}
	if tt2.HasText(ltr("bank.test")) {
		t.Fatal("bank.test in Domains should remain collapsed after reload")
	}
	if !tt2.HasText(ltr("google +1")) {
		t.Fatal("google +1 in Website Categories should remain visible")
	}
}
