package main

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"github.com/egoist/mygo/ui"
)

// TestErrorSnapshots captures existing UI with synthetic failure data. It does not
// contact a provider, change the running daemon, or assert pixel correctness.
func TestErrorSnapshots(t *testing.T) {
	dir := os.Getenv("MASIR_ERROR_SNAPSHOTS")
	if dir == "" {
		t.Skip("set MASIR_ERROR_SNAPSHOTS to capture the error-state inventory")
	}
	type scenario struct {
		ID, View, Title, Note, Source string
		setup                         func(*app)
		scroll                        float32
		toast                         string
	}
	main := func(a *app) {}
	settings := func(a *app) { a.openPage(settingsPath) }
	rules := func(a *app) { a.open = sectionRules }
	servers := func(a *app) { a.open = sectionServers }
	details := func(a *app) { a.openSubscription("s1") }
	edit := func(a *app) { a.openSubscription("s1"); a.openSubscriptionEdit() }
	add := func(a *app) { a.subURL = "https://provider.example/sub"; a.openPage(addSubscriptionPath) }
	check := func(internet, proxy, tested bool) func(*app) {
		return func(a *app) {
			a.status.Settings.Power, a.status.XrayRunning = true, true
			a.status.ProxyRunning, a.status.ProxyAddress = tested, "127.0.0.1:7890"
			a.connection = connectionState{checked: true, internetOK: internet, proxyOK: proxy, proxyTested: tested, identity: a.connectionIdentity(), resultUntil: time.Now().Add(time.Hour)}
		}
	}
	failedUpdate := func(a *app) {
		a.updates.state.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true}
		a.updates.state.phase, a.updates.state.err = updateFailed, "Release signature verification failed"
	}
	shots := []scenario{
		{ID: "01", View: "Main", Title: "Connection failed banner", Source: "main_view.go:153", setup: func(a *app) {
			a.status.Settings.Power = true
			a.status.LastError = "failed to start Xray: listen tcp 127.0.0.1:7890: bind: address already in use"
		}},
		{ID: "02", View: "Main", Title: "Daemon unavailable", Note: "The current UI presents this as Starting, even when the daemon is unavailable.", Source: "main_view.go:58", setup: func(a *app) { a.reachable = false }},
		{ID: "03", View: "Main", Title: "Background service stopped", Source: "main_view.go:51", setup: func(a *app) {
			a.reachable = false
			a.status.ServiceInstalled = true
			a.status.ServiceEnabled = true
			a.status.ServiceRunning = false
		}},
		{ID: "04", View: "Main", Title: "Proxy probe failed", Source: "connection.go:90", setup: check(true, false, true)},
		{ID: "05", View: "Main", Title: "Internet probe failed", Source: "connection.go:92", setup: check(false, true, true)},
		{ID: "06", View: "Main", Title: "Both connection probes failed", Source: "connection.go:94", setup: check(false, false, true)},
		{ID: "07", View: "Main", Title: "Proxy unavailable", Source: "connection.go:85", setup: check(true, false, false)},
		{ID: "08", View: "Main", Title: "Selected server removed", Source: "connection.go:54", setup: func(a *app) { a.status.ActiveStale = true }},
		{ID: "09", View: "Main", Title: "Active subscription expired", Source: "connection.go:63", setup: func(a *app) { a.subscriptions.subscriptions[0].Usage = &pb.Usage{ExpireAtMs: 1} }},
		{ID: "10", View: "Main", Title: "Active subscription traffic exhausted", Source: "connection.go:66", setup: func(a *app) {
			a.subscriptions.subscriptions[0].Usage = &pb.Usage{DownloadBytes: 100e9, TotalBytes: 100e9}
		}},
		{ID: "11", View: "Main", Title: "Connect action failed", Source: "daemon.go:27", setup: main, toast: "Couldn't connect: permission denied"},
		{ID: "12", View: "Servers", Title: "Refresh failed with cached servers", Source: "servers_view.go:211", setup: func(a *app) { servers(a); a.subscriptions.subscriptions[0].LastError = "Provider could not be reached" }},
		{ID: "13", View: "Servers", Title: "Latency probe timeout", Source: "widgets.go:225", setup: func(a *app) {
			servers(a)
			for _, s := range a.subscriptions.servers {
				a.latency[s.key] = -1
			}
		}},
		{ID: "14", View: "Servers", Title: "Server selection failed", Source: "optimistic.go:276", setup: servers, toast: "Couldn't select that server: the server is no longer available"},
		{ID: "15", View: "Subscriptions", Title: "Refresh failed, existing nodes kept", Source: "subscriptions_view.go:223", setup: func(a *app) {
			a.subscriptions.subscriptions[0].LastError = "Provider could not be reached"
			a.openPage(subscriptionsPath)
		}},
		{ID: "16", View: "Subscriptions", Title: "Refresh failed, no servers available", Source: "subscriptions_view.go:226", setup: func(a *app) {
			s := a.subscriptions.subscriptions[0]
			s.LastError = "Provider returned no usable servers"
			s.Configs = nil
			s.ConfigCount = 0
			a.openPage(subscriptionsPath)
		}},
		{ID: "17", View: "Subscriptions", Title: "Expired subscription", Source: "subscriptions_view.go:195", setup: func(a *app) {
			a.subscriptions.subscriptions[0].Usage = &pb.Usage{ExpireAtMs: 1}
			a.openPage(subscriptionsPath)
		}},
		{ID: "18", View: "Subscriptions", Title: "Traffic allowance exhausted", Source: "subscriptions_view.go:209", setup: func(a *app) {
			a.subscriptions.subscriptions[0].Usage = &pb.Usage{DownloadBytes: 100e9, TotalBytes: 100e9}
			a.openPage(subscriptionsPath)
		}},
		{ID: "19", View: "Subscription details", Title: "Provider refresh failed", Source: "subscription_details_view.go:118", setup: func(a *app) {
			a.subscriptions.subscriptions[0].LastError = "The provider could not be reached. Existing servers have been kept."
			details(a)
		}},
		{ID: "20", View: "Subscription details", Title: "Subscription action failed", Source: "subscriptions_view.go:143", setup: func(a *app) {
			details(a)
			a.subscriptionPage.err = "Couldn't remove the subscription: permission denied"
		}},
		{ID: "21", View: "Subscription details", Title: "Subscription no longer available", Source: "subscription_details_view.go:23", setup: func(a *app) { details(a); a.subscriptions.subscriptions = nil }},
		{ID: "22", View: "Subscription details", Title: "No servers after import", Source: "subscription_details_view.go:70", setup: func(a *app) {
			a.subscriptions.subscriptions[0].Configs = nil
			a.subscriptions.subscriptions[0].ConfigCount = 0
			details(a)
		}},
		{ID: "23", View: "Subscription details", Title: "Node latency timeout", Source: "subscription_details_view.go:174", setup: func(a *app) {
			details(a)
			for _, s := range a.subscriptions.servers {
				a.latency[s.key] = -1
			}
		}},
		{ID: "24", View: "Edit subscription", Title: "Invalid refresh interval", Source: "subscriptions_view.go:102", setup: func(a *app) {
			edit(a)
			a.subscriptionPage.interval = "abc"
			a.subscriptionPage.err = "Enter an interval in whole minutes, or 0 for automatic."
		}},
		{ID: "25", View: "Edit subscription", Title: "Invalid subscription URL", Source: "subscriptions_view.go:107", setup: func(a *app) {
			edit(a)
			a.subscriptionPage.url = "provider.example/sub"
			a.subscriptionPage.err = "Enter an HTTP or HTTPS subscription URL."
		}},
		{ID: "26", View: "Edit subscription", Title: "Save failed", Source: "subscriptions_view.go:143", setup: func(a *app) {
			edit(a)
			a.subscriptionPage.err = "The daemon is unavailable. Try again when it reconnects."
		}},
		{ID: "27", View: "Edit subscription", Title: "Subscription disappeared while editing", Source: "subscription_edit_view.go:24", setup: func(a *app) { edit(a); a.subscriptions.subscriptions = nil }},
		{ID: "28", View: "Add subscription", Title: "Provider import failed", Source: "add_subscription_view.go:17", setup: func(a *app) { add(a); a.subError = "Couldn't reach the provider. Check the link and try again." }},
		{ID: "29", View: "Add subscription", Title: "Long technical import error", Note: "Representative raw RPC detail, to inspect wrapping and truncation.", Source: "add_subscription_view.go:18", setup: func(a *app) {
			add(a)
			a.subError = "Get https://provider.example/sub: context deadline exceeded (Client.Timeout exceeded while awaiting headers). The provider may be offline or blocked on this network. Check your subscription URL and internet connection, then try importing again."
		}},
		{ID: "30", View: "Rules", Title: "Global rules change failed", Source: "optimistic.go:284", setup: rules, toast: "Couldn't change the rules: permission denied"},
		{ID: "31", View: "Rules", Title: "Individual rule change failed", Source: "optimistic.go:293", setup: rules, toast: "Couldn't change that rule: the rule is no longer available"},
		{ID: "32", View: "Rules", Title: "Rule removal failed", Source: "optimistic.go:303", setup: rules, toast: "Couldn't remove that rule: permission denied"},
		{ID: "33", View: "Rules", Title: "Add rule unavailable", Source: "rules_view.go:58", setup: rules, toast: "Adding rules from the panel isn't available yet"},
		{ID: "34", View: "Rules", Title: "Edit rule unavailable", Source: "ctxmenu.go:108", setup: rules, toast: "Editing rules isn't available yet"},
		{ID: "35", View: "Settings", Title: "Invalid port number", Source: "settings.go:272", setup: func(a *app) { settings(a); a.portText = "80" }, toast: "The port must be a number from 1024 to 65535"},
		{ID: "36", View: "Settings", Title: "Setting save failed", Source: "settings.go:94", setup: settings, toast: "Couldn't save the setting: permission denied"},
		{ID: "37", View: "Settings", Title: "Bootstrap DNS validation failed", Source: "settings.go:107", setup: settings, scroll: 430, toast: "Bootstrap DNS needs at least one address"},
		{ID: "38", View: "Settings", Title: "Config read failed", Source: "settings.go:65", setup: settings, scroll: 650, toast: "Couldn't read the config: the daemon is unavailable"},
		{ID: "39", View: "Settings", Title: "Service disabled by the system", Source: "settings_view.go:445", setup: func(a *app) {
			a.status.ServiceInstalled = true
			a.status.ServiceRunning = false
			a.status.ServiceEnabled = false
			settings(a)
		}, scroll: 1200},
		{ID: "40", View: "Settings", Title: "Service stopped", Source: "settings_view.go:433", setup: func(a *app) {
			a.status.ServiceInstalled = true
			a.status.ServiceRunning = false
			a.status.ServiceEnabled = true
			settings(a)
		}, scroll: 1200},
		{ID: "41", View: "Settings", Title: "Service installation failed", Source: "install.go:250", setup: settings, scroll: 1200, toast: "Couldn't install the background service: authorization was denied"},
		{ID: "42", View: "Settings", Title: "Command line installation failed", Source: "install.go:212", setup: settings, scroll: 1200, toast: "Couldn't install the command line tool: permission denied"},
		{ID: "43", View: "Settings", Title: "Restore defaults failed", Source: "settings.go:196", setup: settings, scroll: 1200, toast: "Couldn't restore the defaults: the daemon is unavailable"},
		{ID: "44", View: "Updates", Title: "Update failed, retry control", Note: "The error detail lives in the hover tooltip; the resting control only says Update failed.", Source: "update_ui.go:118", setup: failedUpdate},
		{ID: "45", View: "Updates", Title: "Update failed toast", Source: "updates.go:526", setup: failedUpdate, toast: "The update failed: Release signature verification failed"},
		{ID: "46", View: "Settings", Title: "Update failure in settings", Source: "settings_view.go:48", setup: func(a *app) { settings(a); failedUpdate(a) }, scroll: 1600},
	}
	type entry struct{ ID, View, Title, Note, Source, Dark, Light string }
	var index []entry
	for _, s := range shots {
		e := entry{ID: s.ID, View: s.View, Title: s.Title, Note: s.Note, Source: s.Source, Dark: s.ID + "-dark.png", Light: s.ID + "-light.png"}
		index = append(index, e)
		for _, theme := range []string{"dark", "light"} {
			t.Run(s.ID+"/"+theme, func(t *testing.T) {
				a := newTestApp(ukServers())
				a.down, a.up = 120<<10, 8<<10
				tt := ui.NewTester(a.view, panelWidth, 600)
				tt.SetDark(theme == "dark")
				a.serviceChecked = time.Now() // preserve the synthetic service state when Settings opens
				s.setup(a)
				a.cli, a.cliChecked = cliStatus{}, true // deterministic, without a machine-specific installation path
				tt.Frame()
				tt.Advance(time.Second)
				tt.SetSize(panelWidth, int(a.restHeight()))
				tt.Advance(time.Second)
				tt.Frame()
				if s.scroll > 0 {
					tt.Scroll(panelWidth/2, 300, 0, s.scroll)
					tt.Advance(time.Second)
				}
				if s.toast != "" {
					switch s.ID {
					case "33", "34", "35", "37":
						a.notifyWarning(s.toast)
					default:
						a.notifyError(s.toast)
					}
					tt.Frame()
					tt.Advance(250 * time.Millisecond)
				}
				tt.Frame()
				if a.path == mainPath && a.mainHeight > float32(tt.Image().Bounds().Dy())+1 {
					t.Fatalf("main content clipped: content height %.1f, capture height %d", a.mainHeight, tt.Image().Bounds().Dy())
				}
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				f, err := os.Create(filepath.Join(dir, s.ID+"-"+theme+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, tt.Image())
				closeErr := f.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
			})
		}
	}
	b, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
}
