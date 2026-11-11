package main

import (
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// multiSubDaemon creates a fakeDaemon with 3 subscriptions for testing groups.
func multiSubDaemon() *fakeDaemon {
	f := ukServers()
	f.subs = []*pb.Subscription{
		{Id: "s1", Name: "marzban", Configs: []*pb.SubscriptionConfig{
			{Id: "c1", Name: "United Kingdom", Protocol: "vless"},
			{Id: "c2", Name: "England", Protocol: "vless"},
		}},
		{Id: "s2", Name: "Germany VPN", Configs: []*pb.SubscriptionConfig{
			{Id: "c3", Name: "Frankfurt 1", Protocol: "vmess"},
			{Id: "c4", Name: "Frankfurt 2", Protocol: "vmess"},
		}},
		{Id: "s3", Name: "US Fast", Configs: []*pb.SubscriptionConfig{
			{Id: "c5", Name: "New York", Protocol: "shadowsocks"},
			{Id: "c6", Name: "Los Angeles", Protocol: "shadowsocks"},
		}},
	}
	f.configs = map[string][]*pb.SubscriptionConfig{
		"s1": f.subs[0].Configs,
		"s2": f.subs[1].Configs,
		"s3": f.subs[2].Configs,
	}
	return f
}

// Clicking the chevron button or node count collapses and expands its nodes.
func TestGroupHeaderCollapseAndExpand(t *testing.T) {
	f := multiSubDaemon()
	a := newTestApp(f)
	a.open = sectionServers
	tt := tester(a)

	if !tt.HasText("Frankfurt 1") || !tt.HasText("Frankfurt 2") {
		t.Fatal("initially all nodes should be visible")
	}

	// Click chevron to collapse Germany VPN
	if err := tt.Click("Collapse Germany VPN"); err != nil {
		t.Fatalf("could not click collapse chevron: %v", err)
	}
	tt.Advance(time.Second)

	if tt.HasText("Frankfurt 1") || tt.HasText("Frankfurt 2") {
		t.Fatal("Germany VPN nodes should be hidden after collapse")
	}
	if !a.isGroupCollapsed("s2") {
		t.Fatal("s2 should be recorded as collapsed")
	}

	// Click chevron to expand Germany VPN
	if err := tt.Click("Expand Germany VPN"); err != nil {
		t.Fatalf("could not click expand chevron: %v", err)
	}
	tt.Advance(time.Second)

	if !tt.HasText("Frankfurt 1") || !tt.HasText("Frankfurt 2") {
		t.Fatal("Germany VPN nodes should be visible after expand")
	}
	if a.isGroupCollapsed("s2") {
		t.Fatal("s2 should not be recorded as collapsed")
	}

	// Click node count of s1 (first '2 nodes') to toggle collapse
	if err := tt.Click("2 nodes"); err != nil {
		t.Fatalf("could not click node count: %v", err)
	}
	tt.Advance(time.Second)
	if !a.isGroupCollapsed("s1") {
		t.Fatal("clicking node count should toggle collapse on s1")
	}
}

// When a group containing the active server is folded, the active row stays visible.
func TestActiveServerStaysVisibleWhenGroupFolded(t *testing.T) {
	f := multiSubDaemon()
	// c1 (United Kingdom) is active in s1
	f.status.ActiveSubscriptionId = "s1"
	f.status.ActiveConfigId = "c1"
	a := newTestApp(f)
	a.open = sectionServers
	tt := tester(a)

	if !tt.HasText(ltr("United Kingdom")) || !tt.HasText(ltr("England")) {
		t.Fatal("initially s1 nodes should be visible")
	}

	// Collapse s1
	if err := tt.Click("Collapse marzban"); err != nil {
		t.Fatalf("could not click collapse: %v", err)
	}
	tt.Advance(time.Second)

	// Active server (United Kingdom) must still be visible
	if !tt.HasText(ltr("United Kingdom")) {
		t.Fatal("active server should stay visible when group is folded")
	}
	// Non-active server (England) must be hidden
	if tt.HasText(ltr("England")) {
		t.Fatal("non-active server should be hidden when group is folded")
	}
}

// Search matches inside folded groups and reveals matching rows without discarding folds.
func TestSearchRevealsRowsInFoldedGroupsWithoutDiscardingFolds(t *testing.T) {
	f := multiSubDaemon()
	a := newTestApp(f)
	a.open = sectionServers
	tt := tester(a)

	// Fold s2 (Germany VPN)
	a.toggleGroupCollapse("s2")
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText("Frankfurt 1") {
		t.Fatal("Frankfurt 1 should be hidden while s2 is folded")
	}

	// Search for Frankfurt
	a.query = "frankfurt"
	tt.Frame()
	tt.Advance(time.Second)

	if !tt.HasText("Frankfurt 1") || !tt.HasText("Frankfurt 2") {
		t.Fatal("matching rows in folded group should be revealed during search")
	}

	// Clear search
	a.query = ""
	tt.Frame()
	tt.Advance(time.Second)

	if tt.HasText("Frankfurt 1") {
		t.Fatal("clearing search should restore the folded state")
	}
	if !a.isGroupCollapsed("s2") {
		t.Fatal("s2 fold state was discarded")
	}
}

// The toolbar leaves folding to the individual group headers.
func TestServerToolbarHasNoCollapseAllButton(t *testing.T) {
	a := newTestApp(multiSubDaemon())
	a.open = sectionServers
	tt := tester(a)
	if tt.HasText("Collapse all") || tt.HasText("Expand all") {
		t.Fatal("server toolbar should not offer bulk folding")
	}
}

// Folded state survives subscription refreshes and latency tests.
func TestFoldedStateSurvivesRefreshesAndLatencyTests(t *testing.T) {
	d := &subscriptionLatencyDaemon{featureDaemon: &featureDaemon{fakeDaemon: multiSubDaemon()}}
	a := newTestApp(d.fakeDaemon)
	a.d = d
	a.open = sectionServers
	tt := tester(a)

	a.toggleGroupCollapse("s3")
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText("New York") {
		t.Fatal("New York should be hidden after folding s3")
	}

	// Latency test
	a.testServers()
	tt.Frame()
	tt.Advance(time.Second)

	if !a.isGroupCollapsed("s3") || tt.HasText("New York") {
		t.Fatal("latency test reopened folded group")
	}

	// Refresh lists
	a.refreshLists()
	tt.Frame()
	tt.Advance(time.Second)

	if !a.isGroupCollapsed("s3") || tt.HasText("New York") {
		t.Fatal("list refresh reopened folded group")
	}
}
