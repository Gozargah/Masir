package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"github.com/egoist/mygo/ui"
	"google.golang.org/grpc"
)

func (f *featureDaemon) UpdateSubscription(_ context.Context, r *pb.UpdateSubscriptionRequest, _ ...grpc.CallOption) (*pb.UpdateSubscriptionResponse, error) {
	f.log("refresh " + r.Id)
	if f.refreshErr != nil {
		f.subs[0].LastError = f.refreshErr.Error()
		f.subs[0].CheckedAtMs = time.Now().UnixMilli()
	}
	return &pb.UpdateSubscriptionResponse{}, f.refreshErr
}
func (f *featureDaemon) AlterSubscription(_ context.Context, r *pb.AlterSubscriptionRequest, _ ...grpc.CallOption) (*pb.AlterSubscriptionResponse, error) {
	f.edited = r
	f.subs[0].Name = r.GetName()
	f.subs[0].Url = r.GetUrl()
	f.subs[0].CustomUpdateIntervalMinutes = r.GetCustomUpdateIntervalMinutes()
	return &pb.AlterSubscriptionResponse{Subscription: f.subs[0]}, nil
}
func (f *featureDaemon) RemoveSubscription(_ context.Context, r *pb.RemoveSubscriptionRequest, _ ...grpc.CallOption) (*pb.RemoveSubscriptionResponse, error) {
	f.removed = r.Id
	f.subs = nil
	f.configs = nil
	return &pb.RemoveSubscriptionResponse{}, nil
}

func TestSubscriptionStatesAndEntryPoints(t *testing.T) {
	for _, state := range []string{"empty", "healthy", "failing", "expired"} {
		t.Run(state, func(t *testing.T) {
			f := ukServers()
			f.subs[0].Url = "https://provider.example/sub/token"
			f.subs[0].UpdateIntervalMinutes = 720
			want := "provider.example"
			switch state {
			case "empty":
				f.subs = nil
				f.configs = nil
				want = "No subscriptions yet. Add a subscription to import servers."
			case "failing":
				f.subs[0].LastError = "provider offline"
				want = "Refresh failed: provider offline"
			case "expired":
				f.subs[0].Usage = &pb.Usage{ExpireAtMs: 1}
				want = "Expired"
			}
			a := newTestApp(f)
			tt := tester(a)
			if state == "empty" {
				if err := tt.Click("Subscriptions"); err != nil {
					t.Fatal(err)
				}
			} else {
				a.toggle(sectionServers)
				tt.Frame()
				tt.Advance(time.Second)
				if state == "failing" && !tt.HasText(want) {
					t.Fatal("refresh failure hidden in picker")
				}
				if err := tt.Click("Subscriptions"); err != nil {
					t.Fatal(err)
				}
			}
			tt.Advance(time.Second)
			if state == "failing" {
				want = "Refresh failed · Existing nodes kept"
			}
			if a.path != subscriptionsPath || !tt.HasText(want) {
				t.Fatalf("path=%s texts=%q", a.path, tt.Texts())
			}
			if state != "empty" {
				if err := tt.Click("Open marzban"); err != nil {
					t.Fatal(err)
				}
				if a.path != subscriptionDetailsPath || a.subscriptionPage.id != "s1" {
					t.Fatal("details did not open")
				}
			}
		})
	}
}

func TestGroupHeaderOpensItsSubscriptionAndKeepsSameNamesSeparate(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	a.toggle(sectionServers)
	tt := tester(a)
	if err := tt.Click("Open marzban subscription"); err != nil {
		t.Fatal(err)
	}
	if a.path != subscriptionDetailsPath || a.subscriptionPage.id != "s1" {
		t.Fatal("wrong subscription opened")
	}
	nodes := []server{{key: serverKey{"s1", "c1"}, subName: "same"}, {key: serverKey{"s2", "c1"}, subName: "same"}}
	if got := orderedGroups(nodes); len(got) != 2 {
		t.Fatal("same-named subscriptions merged")
	}
}

func TestSubscriptionRefreshFailureKeepsNodesAndIsVisible(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers(), refreshErr: errors.New("provider offline")}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openSubscription("s1")
	a.subscriptionAction("refresh", nil)
	if len(a.subscriptions.servers) != 2 || a.subscriptionPage.err != "provider offline" {
		t.Fatalf("state=%+v", a.subscriptionPage)
	}
	if got := a.subscriptions.subscriptions[0].LastError; got != "provider offline" {
		t.Fatalf("fetch error=%q", got)
	}
}

func TestSubscriptionEditValidatesAndUsesAlterRPC(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openSubscription("s1")
	a.openSubscriptionEdit()
	st := &a.subscriptionPage
	st.name = "Renamed"
	st.url = "https://example.com/sub"
	st.interval = "nope"
	a.saveSubscription()
	if f.edited != nil || st.err == "" {
		t.Fatal("invalid interval submitted")
	}
	st.interval = "60"
	a.saveSubscription()
	if f.edited == nil || f.edited.Id != "s1" || f.edited.GetName() != "Renamed" || f.edited.GetCustomUpdateIntervalMinutes() != 60 || a.path != subscriptionDetailsPath {
		t.Fatalf("request=%v state=%+v", f.edited, st)
	}
}

func TestSubscriptionRemovalRequiresConfirmationAndUsesRemoveRPC(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openSubscription("s1")
	tt := tester(a)
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	if f.removed != "" {
		t.Fatal("removed without confirmation")
	}
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	if f.removed != "s1" || a.path != mainPath {
		t.Fatalf("removed=%s path=%s", f.removed, a.path)
	}
	wantToast := `Subscription "marzban" deleted`
	if !tt.HasText(wantToast) {
		t.Fatalf("expected toast %q in UI, got texts: %v", wantToast, tt.Texts())
	}
	if _, ok := tt.Find("Notification: Notice"); !ok {
		t.Fatalf("expected 'Notification: Notice' (info toast) in UI")
	}
}

func TestSubscriptionRemovalFromSubscriptionsListReturnsToMainWithToast(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openPage(subscriptionsPath)
	a.openSubscription("s1")
	tt := tester(a)
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	if f.removed != "s1" {
		t.Fatalf("expected removed 's1', got %s", f.removed)
	}
	if a.path != mainPath {
		t.Fatalf("expected to return to mainPath, got %s", a.path)
	}
	wantToast := `Subscription "marzban" deleted`
	if !tt.HasText(wantToast) {
		t.Fatalf("expected toast text %q in UI, got texts: %v", wantToast, tt.Texts())
	}
	if _, ok := tt.Find("Notification: Notice"); !ok {
		t.Fatalf("expected 'Notification: Notice' (info toast) in UI")
	}
}

func TestSubscriptionRemovalCancelKeep(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openSubscription("s1")
	tt := tester(a)
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	if !a.subscriptionPage.removing {
		t.Fatal("expected removing confirmation to be open")
	}
	if err := tt.Click("Keep"); err != nil {
		t.Fatal(err)
	}
	if a.subscriptionPage.removing {
		t.Fatal("expected removing confirmation to be closed after Keep")
	}
	if f.removed != "" {
		t.Fatalf("removed=%s; expected nothing removed", f.removed)
	}
}

func TestSameNamedSubscriptionsHaveDistinctPickerKeys(t *testing.T) {
	f := ukServers()
	f.subs = append(f.subs, &pb.Subscription{Id: "s2", Name: "marzban", ConfigCount: 1})
	f.configs["s2"] = []*pb.SubscriptionConfig{{Id: "c1", Name: "Other server"}}
	a := newTestApp(f)
	a.open = sectionServers
	tester(a).Frame()
	seen := map[any]bool{}
	for i, item := range a.serverItems {
		if item.group == nil {
			continue
		}
		key := a.serversList.Key(i)
		if seen[key] {
			t.Fatalf("same-named subscriptions share picker key %v", key)
		}
		seen[key] = true
	}
}

func TestSubscriptionCardExpiryWording(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		want      string
	}{
		{"expired", -time.Hour, "Expired"},
		{"soon", 4 * time.Hour, "Expires within 24 hours"},
		{"more than a day", 26 * time.Hour, "Expires in 2 days"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(ukServers())
			sub := &pb.Subscription{Id: "expiry", Name: "Expiry", Usage: &pb.Usage{ExpireAtMs: time.Now().Add(tc.remaining).UnixMilli()}}
			tt := ui.NewTester(func(c *ui.Context) { a.subscriptionCard(c, paletteFor(c), sub) }, int(panelWidth), 300)
			if !tt.HasText(tc.want) {
				t.Fatalf("missing %q: %q", tc.want, tt.Texts())
			}
		})
	}
}

func TestSubscriptionDetailsFallsBackToProviderName(t *testing.T) {
	f := ukServers()
	f.subs[0].Name = ""
	f.subs[0].Url = "https://provider.example/sub"
	a := newTestApp(f)
	a.subscriptionPage.id = "s1"
	tt := ui.NewTester(func(c *ui.Context) { a.subscriptionDetailsView(c, paletteFor(c)) }, int(panelWidth), 560)
	if _, ok := tt.Find("provider.example"); !ok {
		t.Fatalf("missing provider title: %q", tt.Texts())
	}
}

func TestSubscriptionCardRefreshStaysOnListAndPreventsDuplicates(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openPage(subscriptionsPath)
	release := held(a)
	tt := tester(a)
	for range 2 {
		if err := tt.Click("Refresh marzban"); err != nil {
			t.Fatal(err)
		}
		if a.path != subscriptionsPath {
			t.Fatal("refresh opened Details")
		}
	}
	if !a.subscriptionRefreshing["s1"] || f.count("refresh s1") != 0 {
		t.Fatal("missing pending refresh")
	}
	release()
	tt.Frame()
	if a.subscriptionRefreshing["s1"] || f.count("refresh s1") != 1 {
		t.Fatalf("refresh state=%v calls=%v", a.subscriptionRefreshing, f.calls)
	}
}

func TestRefreshAllSubscriptionsFromHeader(t *testing.T) {
	f := &featureDaemon{fakeDaemon: ukServers()}
	f.subs = append(f.subs, &pb.Subscription{Id: "s2", Name: "Other"})
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openPage(subscriptionsPath)
	release := held(a)
	tt := tester(a)
	refresh, ok := tt.Find("Refresh all subscriptions")
	add, hasAdd := tt.Find("Add Subscription")
	if !ok || !hasAdd || refresh.X+refresh.W > add.X || refresh.Y != add.Y {
		t.Fatalf("refresh and add should sit side by side: refresh=%v add=%v", refresh, add)
	}
	for range 2 {
		if err := tt.Click("Refresh all subscriptions"); err != nil {
			t.Fatal(err)
		}
	}
	release()
	tt.Frame()
	if a.path != subscriptionsPath || f.count("refresh s1") != 1 || f.count("refresh s2") != 1 {
		t.Fatalf("path=%s calls=%v", a.path, f.calls)
	}
}

func TestSubscriptionCardMarksOnlyActiveOwner(t *testing.T) {
	a := newTestApp(ukServers())
	for _, id := range []string{"s1", "s2"} {
		sub := &pb.Subscription{Id: id, Name: id}
		tt := ui.NewTester(func(c *ui.Context) { a.subscriptionCard(c, paletteFor(c), sub) }, int(panelWidth), 200)
		if got, want := tt.HasText("Active"), id == "s1"; got != want {
			t.Fatalf("%s active=%v want=%v", id, got, want)
		}
	}
}

func TestSubscriptionDetailsSelectsItsServersWithoutLeaving(t *testing.T) {
	f := ukServers()
	f.subs = append(f.subs, &pb.Subscription{Id: "s2", Name: "Other"})
	f.configs["s2"] = []*pb.SubscriptionConfig{{Id: "c1", Name: "Other subscription node"}}
	a := newTestApp(f)
	a.openSubscription("s1")
	release := held(a)
	tt := tester(a)
	if tt.HasText("Other subscription node") {
		t.Fatal("showing another subscription's servers")
	}
	name := f.configs["s1"][1].Name
	if err := tt.Click("Use " + name); err != nil {
		t.Fatal(err)
	}
	key, ok := a.activeKey()
	if !ok || key != (serverKey{"s1", "c2"}) || a.path != subscriptionDetailsPath {
		t.Fatalf("selection=%v path=%s", key, a.path)
	}
	release()
	if !f.called("select s1/c2") {
		t.Fatalf("calls=%v", f.calls)
	}
	if err := tt.Click("Edit subscription"); err != nil {
		t.Fatal(err)
	}
	if a.path != editSubscriptionPath || !tt.HasText("Save changes") {
		t.Fatal("header edit action didn't open form")
	}
}

type subscriptionLatencyDaemon struct {
	*featureDaemon
	requests []*pb.TestSubscriptionLatencyRequest
}

func (f *subscriptionLatencyDaemon) TestSubscriptionLatency(_ context.Context, r *pb.TestSubscriptionLatencyRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.TestSubscriptionLatencyResponse], error) {
	f.requests = append(f.requests, r)
	return &subscriptionLatencyStream{ids: append([]string(nil), r.ConfigIds...)}, nil
}

type subscriptionLatencyStream struct {
	grpc.ClientStream
	ids []string
}

func (s *subscriptionLatencyStream) Recv() (*pb.TestSubscriptionLatencyResponse, error) {
	if len(s.ids) == 0 {
		return nil, io.EOF
	}
	id := s.ids[0]
	s.ids = s.ids[1:]
	return &pb.TestSubscriptionLatencyResponse{ConfigId: id, Result: &pb.LatencyResult{LatencyMs: 42, TestedAtMs: 2}}, nil
}
func TestSubscriptionDetailsTestsOnlyItsNodes(t *testing.T) {
	f := &subscriptionLatencyDaemon{featureDaemon: &featureDaemon{fakeDaemon: ukServers()}}
	f.subs = append(f.subs, &pb.Subscription{Id: "s2", Name: "Other"})
	f.configs["s2"] = []*pb.SubscriptionConfig{{Id: "c3", Name: "Other node"}}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openSubscription("s1")
	tt := tester(a)
	if err := tt.Click("Test all " + subscriptionName(f.subs[0]) + " configs"); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || f.requests[0].SubscriptionId != "s1" || !reflect.DeepEqual(f.requests[0].ConfigIds, []string{"c1", "c2"}) {
		t.Fatalf("requests=%v", f.requests)
	}
	if a.latency[serverKey{"s1", "c2"}] != 42 || len(a.testing) != 0 {
		t.Fatalf("results=%v pending=%v", a.latency, a.testing)
	}
	if a.path != subscriptionDetailsPath {
		t.Fatal("test left Details")
	}
}
func TestSubscriptionDetailsVirtualizesServers(t *testing.T) {
	f := ukServers()
	f.configs["s1"] = manyConfigs(1000)
	a := newTestApp(f)
	a.openSubscription("s1")
	tt := tester(a)
	first, last := a.subscriptionNodes.Visible()
	if first < 0 || last < 0 || last-first > 24 {
		t.Fatalf("built rows %d..%d", first, last)
	}
	a.subscriptionNodes.ScrollToEnd()
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText("Node 999") {
		first, last = a.subscriptionNodes.Visible()
		t.Fatalf("last server missing, visible %d..%d: %q", first, last, tt.Texts())
	}
	list, ok := tt.Find("Subscription servers")
	if !ok {
		t.Fatal("missing server list")
	}
	footer, ok := tt.Find("Last refreshed")
	if !ok || footer.Y < list.Y+list.H {
		t.Fatal("refresh footer scrolled into server list")
	}
	if !tt.HasText("Remove") || !tt.HasText("Refresh marzban") {
		t.Fatal("footer actions disappeared after scrolling")
	}
	a.openSubscription("s1")
	tt.Frame()
	if !tt.HasText("Node 0") {
		t.Fatal("reopening Details kept old scroll position")
	}
}

func TestSubscriptionNodeLatencyActionDoesNotSelectServer(t *testing.T) {
	f := &subscriptionLatencyDaemon{featureDaemon: &featureDaemon{fakeDaemon: ukServers()}}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.openSubscription("s1")
	tt := tester(a)
	if err := tt.Click("Test latency"); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || !reflect.DeepEqual(f.requests[0].ConfigIds, []string{"c1"}) {
		t.Fatalf("requests=%v", f.requests)
	}
	if f.called("select s1/c1") || a.path != subscriptionDetailsPath {
		t.Fatal("latency action also selected server or left Details")
	}
}

func TestSubscriptionDetailsKeepsActiveRemarksAndRefreshSummary(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			f := ukServers()
			f.subs[0].UpdateIntervalMinutes = 15
			if !active {
				f.status.ActiveSubscriptionId = "other"
			}
			a := newTestApp(f)
			a.subscriptionPage.id = "s1"
			tt := ui.NewTester(func(c *ui.Context) { a.subscriptionDetailsView(c, paletteFor(c)) }, int(panelWidth), 560)
			if active {
				for _, text := range []string{"Refresh: every 15 mins", ltr("England"), "Active"} {
					if !tt.HasText(text) {
						t.Fatalf("missing %q: %q", text, tt.Texts())
					}
				}
				if tt.HasText("Auto-refresh") {
					t.Fatal("separate auto-refresh title still present")
				}
			} else if !tt.HasText("Auto-refresh") || !tt.HasText("Every 15 mins") || tt.HasText("Active") {
				t.Fatalf("inactive summary=%q", tt.Texts())
			}
		})
	}
}

func TestImportActionStaysVisibleWhenErrorGrows(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, message := range []string{"", "The provider could not be reached.", strings.Repeat("The provider returned an unexpected response. ", 10)} {
			a := newTestApp(ukServers())
			a.subError = message
			tt := ui.NewTester(func(c *ui.Context) { a.addSubscriptionView(c, paletteFor(c)) }, panelWidth, addSubscriptionHeight)
			tt.SetDark(dark)
			tt.Frame()
			action, ok := tt.Find("Import Subscription")
			if !ok || action.Y < 0 || action.Y+action.H > addSubscriptionHeight {
				t.Fatalf("dark=%v: error pushed Import outside the panel: %v", dark, action)
			}
			form, ok := tt.Find("Subscription import form")
			if !ok || action.Y < form.Y+form.H {
				t.Fatal("Import action scrolls with the error")
			}
		}
	}
}

func TestBackFromSubscriptionEditReturnsToDetails(t *testing.T) {
	a := newTestApp(ukServers())
	a.openPage(subscriptionsPath)
	a.openSubscription("s1")
	tt := tester(a)
	if err := tt.Click("Edit subscription"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	a.subscriptionPage.name = "Unsaved name"
	a.subscriptionPage.err = "Invalid URL"
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if a.path != subscriptionDetailsPath || a.subscriptionPage.err != "" || !tt.HasText("Subscription servers") || tt.HasText("Save changes") {
		t.Fatalf("Back skipped Details: path=%s state=%+v", a.path, a.subscriptionPage)
	}
	if a.selectedSubscription().Name != "marzban" {
		t.Fatal("Back saved the draft")
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if a.path != subscriptionsPath {
		t.Fatalf("second Back didn't return to list: %s", a.path)
	}
}

func TestSubscriptionEditAutoFocusesName(t *testing.T) {
	a := newTestApp(ukServers())
	a.openSubscription("s1")
	tt := tester(a)
	for range 2 {
		if err := tt.Click("Edit subscription"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(time.Second)
		originalURL := a.subscriptionPage.url
		tt.Type("Renamed")
		if !strings.Contains(a.subscriptionPage.name, "Renamed") || a.subscriptionPage.url != originalURL {
			t.Fatalf("typing didn't go to the name field: %+v", a.subscriptionPage)
		}
		if err := tt.Click("Back"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(time.Second)
	}
}

func TestSubscriptionDetailsIgnoresPickerCollapse(t *testing.T) {
	a := newTestApp(ukServers())
	a.toggleGroupCollapse("s1")
	a.openSubscription("s1")
	tt := tester(a)
	if !tt.HasText(ltr("Deutschland")) {
		t.Fatal("subscription details hid servers because the picker group was collapsed")
	}
	for _, action := range []string{"Collapse marzban", "Expand marzban"} {
		if _, ok := tt.Find(action); ok {
			t.Fatalf("subscription details offered %q", action)
		}
	}
}
