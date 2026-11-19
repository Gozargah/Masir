package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// TestSnapshots writes what the panel looks like to PNG files when MASIR_SNAPSHOTS names
// a directory, for looking at without running the app. It checks nothing itself.
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("MASIR_SNAPSHOTS")
	if dir == "" {
		t.Skip("set MASIR_SNAPSHOTS to a directory to write the pictures to")
	}
	shots := []struct {
		name string
		dark bool
		do   func(a *app, tt *ui.Tester)
	}{
		{"daemon-update", true, func(a *app, tt *ui.Tester) {
			a.version = "0.2.0"
			a.status.Version = "0.1.0"
			a.updates.state.bundleOffer = true
			a.updates.state.phase = updateAvailable
		}},
		{"update-enter-password", true, func(a *app, tt *ui.Tester) {
			a.updates.state.bundleSync = true
			a.updates.state.phase = updateAuthorizing
		}},
		{"rolling-mid", true, func(a *app, tt *ui.Tester) {
			a.latency[serverKey{"s1", "c1"}] = 2075
			a.rules.rules = append(a.rules.rules, a.rules.rules[0])
			tt.Frame()
			tt.Advance(110 * time.Millisecond) // part way through the roll
		}},

		{"latency-shimmer", true, func(a *app, tt *ui.Tester) {
			a.open = sectionServers
			for _, s := range a.subscriptions.servers {
				a.testing[s.key] = true
			}
			tt.Frame()
			tt.Advance(550 * time.Millisecond)
		}},
		{"connected-breathe", true, func(a *app, tt *ui.Tester) {
			a.status.Settings.Power, a.status.XrayRunning = true, true
			tt.Frame()
			tt.Advance(1200 * time.Millisecond)
		}},
		{"connecting-pulse", true, func(a *app, tt *ui.Tester) {
			a.status.Settings.Power, a.status.XrayRunning = true, false
			tt.Frame()
			tt.Advance(time.Second)
		}},
		{"connection-disconnected", true, func(a *app, tt *ui.Tester) {}},
		{"header-session-dark", true, headerSessionSnapshot},
		{"header-session-light", false, headerSessionSnapshot},
		{"header-result-expired", true, func(a *app, tt *ui.Tester) {
			headerSessionSnapshot(a, tt)
			a.connection = connectionState{checked: true, internetOK: true, proxyOK: true, proxyTested: true, identity: a.connectionIdentity(), resultUntil: time.Now().Add(-time.Second)}
		}},
		{"connection-tunnel", true, func(a *app, tt *ui.Tester) {
			a.status.Settings.Power = true
			a.status.Settings.Tun = true
			a.status.XrayRunning = true
			a.status.TunRunning = true
			a.status.ProxyRunning = true
			a.status.ProxyAddress = "127.0.0.1:7890"
			a.status.UptimeSeconds = 3661
		}},
		{"connection-healthy", true, func(a *app, tt *ui.Tester) {
			a.status.Settings.Power = true
			a.status.XrayRunning = true
			a.status.TunRunning = true
			a.status.ProxyRunning = true
			a.status.ProxyAddress = "127.0.0.1:7890"
			a.connection = connectionState{resultUntil: time.Now().Add(3 * time.Second), checked: true, internetOK: true, proxyOK: true, proxyTested: true, identity: a.connectionIdentity()}
		}},
		{"connection-proxy-failed", true, func(a *app, tt *ui.Tester) {
			a.status.Settings.Power = true
			a.status.XrayRunning = true
			a.status.TunRunning = true
			a.status.ProxyRunning = true
			a.status.ProxyAddress = "127.0.0.1:7890"
			a.connection = connectionState{resultUntil: time.Now().Add(3 * time.Second), checked: true, internetOK: true, proxyTested: true, identity: a.connectionIdentity()}
		}},
		{"connection-testing", true, func(a *app, tt *ui.Tester) {
			a.status.Settings.Power = true
			a.status.XrayRunning = true
			a.connection.testing = true
		}},
		{"servers-refresh-failed", true, func(a *app, tt *ui.Tester) {
			a.subscriptions.subscriptions[0].LastError = "Provider could not be reached"
			a.open = sectionServers
		}},
		{"subscriptions-scrollbar", true, func(a *app, tt *ui.Tester) {
			subscriptionSnapshotData(a)
			a.openPage(subscriptionsPath)
			tt.Frame()
			tt.Advance(time.Second)
			tt.Move(panelWidth-5, 200)
		}},

		{"rule-pinned-dark", true, func(a *app, tt *ui.Tester) { a.newRule(); a.rulePage.target = "config:s1/c2" }},
		{"rule-pinned-light", false, func(a *app, tt *ui.Tester) { a.newRule(); a.rulePage.target = "config:s1/c2" }},
		{"rule-new-dark", true, func(a *app, tt *ui.Tester) { a.newRule() }},
		{"rule-new-light", false, func(a *app, tt *ui.Tester) { a.newRule() }},
		{"rule-edit-dark", true, func(a *app, tt *ui.Tester) { a.editRule(a.rules.rules[0]) }},
		{"rule-edit-light", false, func(a *app, tt *ui.Tester) { a.editRule(a.rules.rules[0]) }},
		{"rule-delete-confirmation", true, func(a *app, tt *ui.Tester) {
			a.editRule(a.rules.rules[0])
			a.rulePage.removing = true
		}},
		{"rule-swipe-confirmation", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("RULES")
			sw := a.swipeFor("r1")
			sw.x, sw.open, sw.confirmRemove = -swipeWidth, true, true
		}},
		{"rule-tooltip-dark", true, func(a *app, tt *ui.Tester) { a.editRule(a.rules.rules[0]) }},
		{"rule-tooltip-light", false, func(a *app, tt *ui.Tester) { a.editRule(a.rules.rules[0]) }},
		{"rule-details", true, func(a *app, tt *ui.Tester) { a.openRule(a.rules.rules[0]) }},
		{"rule-condition", true, func(a *app, tt *ui.Tester) {
			a.newRule()
			a.rulePage.expanded = "domain"
			a.rulePage.domainMode = 1
			a.rulePage.values["domain"] = "domain:example.com\nfull:work.example.org"
		}},
		{"rule-destination", true, func(a *app, tt *ui.Tester) { a.newRule(); a.rulePage.expanded = "destination" }},
		{"rule-apps", true, func(a *app, tt *ui.Tester) {
			a.newRule()
			a.ruleApps = ruleAppState{selected: map[string]bool{"/Applications/Safari.app/": true}, all: []ruleApp{{Name: "Safari", Path: "/Applications/Safari.app/", Icon: "/Applications/Safari.app", Running: true}, {Name: "Terminal", Path: "/System/Applications/Utilities/Terminal.app/", Icon: "/System/Applications/Utilities/Terminal.app", Running: true}}}
			a.filterRuleApps()
			a.rulePage.expanded = "process"
		}},
		{"subscriptions-overview-dark", true, func(a *app, tt *ui.Tester) { subscriptionSnapshotData(a); a.openPage(subscriptionsPath) }},
		{"subscriptions-overview-light", false, func(a *app, tt *ui.Tester) { subscriptionSnapshotData(a); a.openPage(subscriptionsPath) }},
		{"subscriptions-healthy", true, func(a *app, tt *ui.Tester) {
			s := a.subscriptions.subscriptions[0]
			s.Url = "https://provider.example/sub"
			s.UpdateIntervalMinutes = 720
			s.Usage = &pb.Usage{DownloadBytes: 40e9, TotalBytes: 100e9, ExpireAtMs: time.Now().Add(30 * 24 * time.Hour).UnixMilli()}
			a.openPage(subscriptionsPath)
		}},
		{"subscriptions-empty", true, func(a *app, tt *ui.Tester) { a.subscriptions.subscriptions = nil; a.openPage(subscriptionsPath) }},
		{"subscriptions-failing", true, func(a *app, tt *ui.Tester) {
			a.subscriptions.subscriptions[0].LastError = "Provider could not be reached. Existing nodes kept."
			a.openPage(subscriptionsPath)
		}},
		{"subscriptions-expired", true, func(a *app, tt *ui.Tester) {
			a.subscriptions.subscriptions[0].Usage = &pb.Usage{ExpireAtMs: 1, DownloadBytes: 100e9, TotalBytes: 100e9}
			a.openPage(subscriptionsPath)
		}},
		{"subscription-details", true, func(a *app, tt *ui.Tester) { a.openSubscription("s1") }},
		{"subscription-details-light", false, func(a *app, tt *ui.Tester) { a.openSubscription("s1") }},
		{"subscription-details-inactive", true, func(a *app, tt *ui.Tester) {
			a.status.ActiveSubscriptionId = "other"
			a.subscriptions.subscriptions[0].UpdateIntervalMinutes = 15
			a.openSubscription("s1")
		}},
		{"subscription-details-failed", true, func(a *app, tt *ui.Tester) {
			a.subscriptions.subscriptions[0].LastError = "The provider could not be reached. Existing servers have been kept."
			a.openSubscription("s1")
		}},
		{"subscription-details-populated", true, func(a *app, tt *ui.Tester) {
			s := a.subscriptions.subscriptions[0]
			s.Url = "https://primary.example/sub"
			s.UpdateIntervalMinutes = 15
			s.UpdatedAtMs = time.Now().Add(-3 * time.Minute).UnixMilli()
			s.Usage = &pb.Usage{DownloadBytes: 42e9, TotalBytes: 100e9, ExpireAtMs: time.Now().Add(30 * 24 * time.Hour).UnixMilli()}
			s.Configs = append(s.Configs, manyConfigs(20)...)
			a.openSubscription("s1")
		}},
		{"subscriptions-refreshing", true, func(a *app, tt *ui.Tester) {
			subscriptionSnapshotData(a)
			a.subscriptionRefreshing = map[string]bool{"s1": true}
			a.openPage(subscriptionsPath)
		}},
		{"subscription-edit", true, func(a *app, tt *ui.Tester) {
			a.openSubscription("s1")
			a.openSubscriptionEdit()
			a.subscriptionPage.name = "marzban"
			a.subscriptionPage.url = "https://provider.example/sub"
			a.subscriptionPage.interval = "0"
		}},
		{"subscription-edit-error", true, func(a *app, tt *ui.Tester) {
			a.openSubscription("s1")
			a.openSubscriptionEdit()
			a.subscriptionPage.err = "Enter an interval in whole minutes, or 0 for automatic."
		}},
		{"main-light", false, func(a *app, tt *ui.Tester) {}},
		{"main-dark", true, func(a *app, tt *ui.Tester) {}},
		{"servers-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click(ltr("United Kingdom")) }},
		{"rules-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click("RULES") }},
		{"settings-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click("Settings") }},
		{"settings-middle", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 300, 0, 430)
			tt.Advance(time.Second)
		}},
		{"settings-logging", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 300, 0, 650)
			tt.Advance(time.Second)
		}},
		{"settings-select", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 300, 0, 430)
			tt.Advance(time.Second)
			_ = tt.Click("Warning") // the log level select's trigger
			tt.Advance(100 * time.Millisecond)
		}},
		{"update-available", true, func(a *app, tt *ui.Tester) {
			a.updates.state.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.updates.state.phase = updateAvailable
		}},
		{"update-downloading", true, func(a *app, tt *ui.Tester) {
			a.updates.state.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.updates.state.job = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_DOWNLOADING, BytesDone: 42, BytesTotal: 100}
			a.updates.state.parts, a.updates.state.phase = 2, updateDownloading
		}},
		{"update-paused", true, func(a *app, tt *ui.Tester) {
			a.updates.state.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.updates.state.job = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_PAUSED, BytesDone: 42, BytesTotal: 100}
			a.updates.state.parts, a.updates.state.phase = 2, updatePaused
		}},
		{"update-app-downloading", true, func(a *app, tt *ui.Tester) {
			a.updates.state.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.updates.state.parts, a.updates.state.phase = 2, updateAppDownloading
			a.updates.state.progress, a.updates.state.total = 74, 100
		}},
		{"update-package-downloading", true, func(a *app, tt *ui.Tester) {
			a.updates.state.block, a.updates.state.pkgSync, a.updates.state.parts = blockPackageDeb, true, 1
			a.updates.state.phase = updateAppDownloading
			a.updates.state.progress, a.updates.state.total = 42, 100
		}},
		{"update-package-verifying", true, func(a *app, tt *ui.Tester) {
			a.updates.state.block, a.updates.state.pkgSync, a.updates.state.parts = blockPackageDeb, true, 1
			a.updates.state.phase = updateVerifying
		}},
		{"update-ready", true, func(a *app, tt *ui.Tester) {
			a.updates.state.phase = updateReady
		}},
		{"update-package-password", true, func(a *app, tt *ui.Tester) {
			a.updates.state.block, a.updates.state.pkgSync, a.updates.state.parts = blockPackageDeb, true, 1
			a.updates.state.phase = updateAuthorizing
		}},
		{"update-package-installing", true, func(a *app, tt *ui.Tester) {
			a.updates.state.block, a.updates.state.pkgSync, a.updates.state.parts = blockPackageDeb, true, 1
			a.updates.state.phase = updateAppInstalling
		}},
		{"update-package-restarting", true, func(a *app, tt *ui.Tester) {
			a.updates.state.block, a.updates.state.pkgSync, a.updates.state.parts = blockPackageDeb, true, 1
			a.updates.state.phase = updateRestarting
		}},
		{"update-package-failed", true, func(a *app, tt *ui.Tester) {
			a.updates.state.block, a.updates.state.pkgSync, a.updates.state.parts = blockPackageDeb, true, 1
			a.updates.state.pkgSync = false
			a.updates.state.phase = updateFailed
			a.updates.state.err = "Another installer is running. Try again in a minute."
		}},
		{"settings-update", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			a.updates.state.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.updates.state.phase = updateAvailable
			tt.Scroll(panelWidth/2, 400, 0, 1200)
			tt.Advance(time.Second)
		}},
		{"system-dark", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 420, 0, 1200)
			tt.Advance(100 * time.Millisecond)
		}},
		{"add-dark", true, func(a *app, tt *ui.Tester) { a.openPage(addSubscriptionPath) }},
		{"add-error", false, func(a *app, tt *ui.Tester) {
			a.subURL = "https://provider.example/sub"
			a.subError = "Couldn't reach the provider. Check the link and try again."
			a.openPage(addSubscriptionPath)
		}},
	}

	for _, dark := range []bool{true, false} {
		themeName := "light"
		if dark {
			themeName = "dark"
		}
		for _, d := range ruleConditions {
			shots = append(shots, struct {
				name string
				dark bool
				do   func(*app, *ui.Tester)
			}{
				"rule-expanded-" + d.key + "-" + themeName, dark, func(a *app, tt *ui.Tester) {
					a.newRule()
					a.rulePage.more = true
					a.rulePage.expanded = d.key
					switch d.key {
					case "domain":
						a.rulePage.domainMode = 1
						a.rulePage.values[d.key] = "domain:example.com\nfull:www.example.org"
					case "ip":
						a.rulePage.ipMode = 1
						a.rulePage.values[d.key] = "1.1.1.1\n10.0.0.0/8"
					case "source":
						a.rulePage.values[d.key] = "192.168.1.0/24"
					case "port", "sourcePort":
						a.rulePage.values[d.key] = "443"
					case "network":
						a.rulePage.values[d.key] = "tcp"
					case "protocol":
						a.rulePage.values[d.key] = "tls"
					case "process":
						a.rulePage.values[d.key] = "/Applications/Safari.app/"
						a.ruleApps = ruleAppState{selected: map[string]bool{"/Applications/Safari.app/": true}, all: []ruleApp{{Name: "Safari", Path: "/Applications/Safari.app/", Icon: "/Applications/Safari.app", Running: true}}}
						a.filterRuleApps()
					}
					if d.key != "process" && d.key != "domain" {
						tt.SetSize(panelWidth, 620)
						tt.Frame()
						tt.Advance(time.Second)
						tt.Scroll(285, 420, 0, 1000)
						tt.Frame()
					}
				},
			})
		}
		shots = append(shots, struct {
			name string
			dark bool
			do   func(*app, *ui.Tester)
		}{
			"geoip-picker-" + themeName, dark, func(a *app, tt *ui.Tester) {
				a.newRule()
				a.rulePage.expanded = "ip"
				a.rulePage.values["ip"] = "geoip:ir\n1.1.1.1"
				a.ruleGeoIP = ruleGeoState{loaded: true, dirty: true, known: map[string]bool{}}
				for _, code := range []string{"ir", "cn", "de", "private", "telegram"} {
					a.ruleGeoIP.all = append(a.ruleGeoIP.all, geoCatalogOption("ip", code, 240))
					a.ruleGeoIP.known[code] = true
				}
			},
		})
		for _, view := range []string{"all", "search", "loading", "error"} {
			shots = append(shots, struct {
				name string
				dark bool
				do   func(*app, *ui.Tester)
			}{"geo-picker-" + view + "-" + themeName, dark, func(a *app, tt *ui.Tester) {
				a.newRule()
				a.rulePage.expanded, a.rulePage.domainMode = "domain", 0
				a.rulePage.values["domain"] = "geosite:category-ads-all\ngeosite:google"
				a.ruleGeo = ruleGeoState{loaded: true, dirty: true, known: map[string]bool{}}
				for _, code := range []string{"category-ads-all", "category-ai-!cn", "apple", "google", "ir", "netflix", "youtube"} {
					a.ruleGeo.all = append(a.ruleGeo.all, geoWebsiteOption(code, 1200))
					a.ruleGeo.known[code] = true
				}
				switch view {
				case "search":
					a.ruleGeo.query = "iran"
				case "loading":
					a.ruleGeo.all = nil
					a.ruleGeo.loaded = false
					a.ruleGeo.loading = true
				case "error":
					a.ruleGeo.all = nil
					a.ruleGeo.err = "Couldn't load website lists. Try refreshing the list."
				}
				tt.SetSize(panelWidth, 620)
			}})
		}
		shots = append(shots, struct {
			name string
			dark bool
			do   func(*app, *ui.Tester)
		}{"rule-then-" + themeName, dark, func(a *app, tt *ui.Tester) {
			a.newRule()
			a.rulePage.expanded = "destination"
			tt.SetSize(panelWidth, 620)
			tt.Frame()
			tt.Advance(time.Second)
			tt.Scroll(285, 420, 0, 1000)
			tt.Frame()
		}})
	}

	for _, dark := range []bool{true, false} {
		themeName := "light"
		if dark {
			themeName = "dark"
		}
		for _, mode := range []string{"apps", "selected", "manual", "search-empty", "loading", "error", "long-name"} {
			shots = append(shots, struct {
				name string
				dark bool
				do   func(*app, *ui.Tester)
			}{"app-picker-" + mode + "-" + themeName, dark, func(a *app, tt *ui.Tester) {
				a.newRule()
				appPickerTestFixture(a)
				a.ruleApps.all = append(a.ruleApps.all, ruleApp{Name: "Terminal", Path: "/System/Applications/Utilities/Terminal.app/", Icon: "/System/Applications/Utilities/Terminal.app", Running: true})
				a.rulePage.values["process"] = "/Applications/Browser.app/"
				a.syncRuleAppSelection()
				switch mode {
				case "selected":
					a.addPickedRuleApps([]string{"/Tools/Worker"}, true)
				case "manual":
					a.ruleApps.tab = ruleAppsManual
					a.rulePage.values["process"] += "\n/Tools/Worker/\nmy-worker"
					a.syncRuleAppSelection()
				case "search-empty":
					a.ruleApps.query = "nothing-matches"
				case "loading":
					a.ruleApps.all = nil
					a.ruleApps.loading = true
				case "error":
					a.ruleApps.all = nil
					a.ruleApps.err = "Couldn't read installed applications. Choose an app or folder, or use Manual."
				case "long-name":
					a.ruleApps.all[0].Name = "An application with a very long name that must stay inside its row"
				}
				a.filterRuleApps()
			}})
		}
	}
	for _, s := range shots {
		a := newTestApp(ukServers())
		a.down, a.up = 120<<10, 8<<10
		tt := ui.NewTester(a.view, panelWidth, 600)
		dark := s.dark
		switch os.Getenv("MASIR_SNAPSHOT_THEME") {
		case "light":
			dark = false
		case "dark":
			dark = true
		}
		tt.SetDark(dark)
		s.do(a, tt)
		if s.name != "rolling-mid" {
			tt.Frame()              // fixtures change state directly; paint-only animations do not rebuild it
			tt.Advance(time.Second) // let the panels finish opening
		}
		tt.SetSize(panelWidth, int(a.restHeight()))
		if s.name != "rolling-mid" {
			tt.Advance(time.Second)
		} else {
			tt.Frame()
		}
		if s.name == "rule-tooltip-dark" || s.name == "rule-tooltip-light" {
			if b, ok := tt.Find("Delete rule"); ok {
				tt.Move(b.X+b.W/2, b.Y+b.H/2)
				tt.Advance(time.Second)
			}
		}
		tt.Frame()
		f, err := os.Create(filepath.Join(dir, s.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

// Keep documentation providers fictional, with subscription URLs under .example.
func subscriptionSnapshotData(a *app) {
	now := time.Now()
	a.subscriptions.subscriptions = []*pb.Subscription{
		{Id: "s1", Name: "Primary", Url: "https://primary.example/sub", ConfigCount: 8, UpdateIntervalMinutes: 15, UpdatedAtMs: now.Add(-3 * time.Minute).UnixMilli(), Usage: &pb.Usage{DownloadBytes: 794.1e9}},
		{Id: "s2", Name: "Work", Url: "https://work.example/sub", ConfigCount: 5, UpdateIntervalMinutes: 15, UpdatedAtMs: now.Add(-time.Minute).UnixMilli(), Usage: &pb.Usage{DownloadBytes: 82.4e9, TotalBytes: 100e9, ExpireAtMs: now.Add(3 * 24 * time.Hour).UnixMilli()}},
		{Id: "s3", Name: "Backup", Url: "https://backup.example/sub", ConfigCount: 9, UpdateIntervalMinutes: 60, UpdatedAtMs: now.Add(-2 * time.Hour).UnixMilli(), LastError: "The provider could not be reached"},
		{Id: "s4", Name: "My subscription with a very long descriptive name", Url: "https://example.com/sub", ConfigCount: 1},
	}
}

func headerSessionSnapshot(a *app, tt *ui.Tester) {
	a.status.Settings.Power = true
	a.status.XrayRunning = true
	a.status.ProxyRunning = true
	a.status.ProxyAddress = "127.0.0.1:7890"
	a.since = time.Now().Add(-81 * time.Second)
}
