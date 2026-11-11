package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestZeroStateSnapshots captures existing UI for onboarding design review.
// Like TestSnapshots, it writes pictures only; it is not a visual assertion.
func TestZeroStateSnapshots(t *testing.T) {
	dir := os.Getenv("MASIR_SNAPSHOTS")
	if dir == "" {
		t.Skip("set MASIR_SNAPSHOTS to a directory to write the pictures to")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	shots := []struct {
		name      string
		populated bool
		setup     func(*app)
	}{
		{"first-launch", false, func(a *app) {}},
		{"subscriptions-empty", false, func(a *app) { a.openPage(subscriptionsPath) }},
		{"add-subscription", false, func(a *app) { a.openPage(addSubscriptionPath) }},
		{"import-failed", false, func(a *app) {
			a.subURL = "https://provider.example/sub"
			a.subError = "Couldn't reach the provider. Check the link and try again."
			a.openPage(addSubscriptionPath)
		}},
		{"subscription-zero-servers-home", true, func(a *app) {
			for _, s := range a.subscriptions.subscriptions {
				s.Configs, s.ConfigCount = nil, 0
			}
			a.subscriptions.servers = nil
			a.status.ActiveSubscriptionId, a.status.ActiveConfigId, a.status.ActiveConfigName = "", "", ""
		}},
		{"subscription-zero-servers-details", true, func(a *app) {
			s := a.subscriptions.subscriptions[0]
			s.Configs, s.ConfigCount = nil, 0
			a.subscriptions.servers = nil
			a.status.ActiveSubscriptionId, a.status.ActiveConfigId, a.status.ActiveConfigName = "", "", ""
			a.openSubscription(s.Id)
		}},
		{"rules-empty", true, func(a *app) { a.rules.rules = nil; a.open = sectionRules }},
		{"rules-search-empty", true, func(a *app) { a.ruleQuery = "nonexistent"; a.open = sectionRules }},
		{"servers-search-empty", true, func(a *app) { a.query = "nonexistent"; a.open = sectionServers }},
		{"daemon-starting", false, func(a *app) { a.reachable = false }},
		{"server-not-selected", true, func(a *app) {
			a.status.ActiveSubscriptionId, a.status.ActiveConfigId, a.status.ActiveConfigName = "", "", ""
		}},
	}
	for _, shot := range shots {
		for _, dark := range []bool{true, false} {
			theme := "light"
			if dark {
				theme = "dark"
			}
			t.Run(shot.name+"-"+theme, func(t *testing.T) {
				f := ukServers()
				if !shot.populated {
					f.subs, f.configs, f.rules = nil, nil, nil
					f.status.ActiveSubscriptionId, f.status.ActiveConfigId, f.status.ActiveConfigName = "", "", ""
				}
				a := newTestApp(f)
				tt := ui.NewTester(a.view, panelWidth, 600)
				tt.SetDark(dark)
				shot.setup(a)
				tt.Frame()
				tt.Advance(time.Second)
				tt.SetSize(panelWidth, int(a.restHeight()))
				tt.Advance(time.Second)
				tt.Frame()
				out, err := os.Create(filepath.Join(dir, shot.name+"-"+theme+".png"))
				if err != nil {
					t.Fatal(err)
				}
				defer out.Close()
				if err := png.Encode(out, tt.Image()); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
