package main

import (
	"strings"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"github.com/egoist/mygo/ui"
)

func TestErrorLayouts(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme+"/details", func(t *testing.T) {
			a := newTestApp(ukServers())
			a.subscriptions.subscriptions[0].LastError = "The provider could not be reached. Existing servers have been kept."
			a.openSubscription("s1")
			tt := ui.NewTester(a.view, panelWidth, 560)
			tt.SetDark(dark)
			tt.Frame()
			tt.Advance(time.Second)
			errorBox, ok := tt.Find("Last attempt: Never")
			if !ok {
				t.Fatal("missing error detail")
			}
			list, ok := tt.Find("Subscription server group")
			if !ok {
				t.Fatal("missing list")
			}
			if errorBox.Y+errorBox.H > list.Y {
				t.Fatalf("error bottom %.1f overlaps list top %.1f", errorBox.Y+errorBox.H, list.Y)
			}
		})
		t.Run(theme+"/import", func(t *testing.T) {
			a := newTestApp(ukServers())
			a.openPage(addSubscriptionPath)
			a.subURL = "https://provider.example/sub"
			var content, footer ui.Rect
			tt := ui.NewTester(func(c *ui.Context) {
				a.view(c)
				content, footer = a.importContent.Bounds(c), a.importFooter.Bounds(c)
			}, panelWidth, addSubscriptionHeight)
			tt.SetDark(dark)
			for _, message := range []string{"Provider unreachable.", strings.Repeat("Provider returned an unexpected response. ", 10), ""} {
				a.subError = message
				tt.Frame()
				tt.Advance(time.Second)
				tt.SetSize(panelWidth, int(a.restHeight()))
				tt.Frame()
				url, ok := tt.Find("Subscription link fields")
				if !ok {
					t.Fatal("missing URL")
				}
				form, ok := tt.Find("Subscription import form")
				if !ok {
					t.Fatal("missing form")
				}
				if url.Y+url.H > form.Y+form.H {
					t.Fatalf("URL cropped by form: URL %v, form %v", url, form)
				}
				action, ok := tt.Find("Import Subscription")
				if !ok {
					t.Fatal("missing action")
				}
				if message != "" {
					if content.Y+content.H > footer.Y+0.01 {
						t.Fatalf("full content clipped: content %v footer %v height %.2f", content, footer, a.restHeight())
					}
					err, ok := tt.Find("Subscription import error")
					if !ok {
						t.Fatal("missing error")
					}
					if err.Y+err.H > form.Y+form.H+0.5 {
						t.Fatalf("alert clipped by viewport: alert bottom %.2f, viewport bottom %.2f, page %.2f", err.Y+err.H, form.Y+form.H, a.restHeight())
					}
					if err.Y < url.Y+url.H || err.Y+err.H > action.Y {
						t.Fatalf("error outside space between URL and action: URL %v error %v action %v", url, err, action)
					}
				}
				if message != "" && a.restHeight() <= addSubscriptionHeight {
					t.Fatalf("error did not grow page: height %.1f", a.restHeight())
				}
			}
		})
		t.Run(theme+"/settings-update", func(t *testing.T) {
			a := newTestApp(ukServers())
			a.openPage(settingsPath)
			a.updates.state.phase = updateFailed
			a.updates.state.err = "Signature verification failed"
			a.updates.state.rel = &pb.ReleaseInfo{Available: true, LatestVersion: "v0.2.0"}
			tt := ui.NewTester(a.view, panelWidth, settingsHeight)
			tt.SetDark(dark)
			tt.Frame()
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 300, 0, 2000)
			tt.Advance(time.Second)
			update, ok := tt.Find("Update failed")
			if !ok {
				t.Fatal("missing update")
			}
			ignore, ok := tt.Find("Ignore this update")
			if !ok {
				t.Fatal("missing ignore")
			}
			if update.W < panelWidth/2 || abs32(ignore.X+ignore.W-(panelWidth-tw(4)-1)) > 1 {
				t.Fatalf("Settings update does not fill the row: update %v ignore %v", update, ignore)
			}
			if abs32(update.Y+update.H/2-ignore.Y-ignore.H/2) > 1 || ignore.X < update.X+update.W {
				t.Fatalf("update and ignore are not on one row: %v %v", update, ignore)
			}
		})
	}
}
