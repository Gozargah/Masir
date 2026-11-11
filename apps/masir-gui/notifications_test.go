package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNotificationsDismissAndExpire(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			a := newTestApp(ukServers())
			tt := ui.NewTester(a.view, panelWidth, 240)
			tt.SetDark(dark)
			a.fail("Couldn't connect: ", errors.New("permission denied"))
			tt.Frame()
			_, isError := tt.Find("Notification: Error")
			if !tt.HasText("Couldn't connect: permission denied") || !isError {
				t.Fatal("failure did not render as an error")
			}
			if err := tt.Click("Dismiss notification"); err != nil {
				t.Fatal(err)
			}
			if tt.HasText("Couldn't connect: permission denied") {
				t.Fatal("dismiss left the error visible")
			}
			a.notifyWarning("Check your settings")
			tt.Frame()
			tt.Move(2, 2)
			tt.Advance(5 * time.Second)
			if !tt.HasText("Check your settings") {
				t.Fatal("warning vanished too soon")
			}
			// Hovering pauses the engine's timeout.
			b, _ := tt.Find("Notification: Warning")
			tt.Move(b.X+4, b.Y+4)
			tt.Advance(4 * time.Second)
			if !tt.HasText("Check your settings") {
				t.Fatal("hover did not pause the notification")
			}
			tt.Move(2, 2)
			tt.Advance(2 * time.Second)
			if tt.HasText("Check your settings") {
				t.Fatal("warning did not expire after leaving it")
			}
			a.notifySuccess("Settings saved")
			tt.Frame()
			tt.Move(2, 2)
			tt.Advance(5 * time.Second)
			if tt.HasText("Settings saved") {
				t.Fatal("success did not expire")
			}
		})
	}
}

func TestNotificationsUseNewestAndPreserveEarlierNotice(t *testing.T) {
	a := newTestApp(ukServers())
	tt := ui.NewTester(a.view, panelWidth, 240)
	a.notifyError("First failure")
	a.notifyError("First failure")
	a.notifyWarning("Second warning")
	tt.Frame()
	if !tt.HasText("Second warning") || tt.HasText("First failure") {
		t.Fatal("newest notice is not shown on its own")
	}
	if err := tt.Click("Dismiss notification"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("First failure") {
		t.Fatal("dismiss lost the previous error")
	}
	if err := tt.Click("Dismiss notification"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("First failure") {
		t.Fatal("duplicate error remained")
	}
}

func TestLongNotificationScrollsWithinPanel(t *testing.T) {
	a := newTestApp(ukServers())
	tt := ui.NewTester(a.view, panelWidth, 240)
	a.notifyError(strings.Repeat("The provider could not be reached. ", 30) + "End of error.")
	tt.Frame()
	card, ok := tt.Find("Notification: Error")
	if !ok {
		t.Fatal("missing error")
	}
	if card.X != 12 || card.W != panelWidth-24 || card.Y < 12 || card.Y+card.H > 228.5 {
		t.Fatalf("toast outside panel insets: %v", card)
	}
	close, ok := tt.Find("Dismiss notification")
	if !ok || close.W < 24 || close.H < 24 {
		t.Fatalf("dismiss target too small: %v", close)
	}
	message, ok := tt.Find("Notification message")
	if !ok {
		t.Fatal("missing message")
	}
	tt.Scroll(message.X+message.W/2, message.Y+message.H/2, 0, 3000)
	tt.Advance(time.Second)
	if err := tt.Click("Dismiss notification"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Notification: Error") {
		t.Fatal("long notification could not be dismissed")
	}
}

func TestNotificationKeyboardDismissal(t *testing.T) {
	a := newTestApp(ukServers())
	tt := ui.NewTester(a.view, panelWidth, 240)
	a.notifyError("Keyboard dismissal")
	tt.Frame()
	for i := 0; i < 40 && !tt.Focused("Dismiss notification"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("Dismiss notification") {
		t.Fatal("Tab cannot reach dismiss")
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText("Keyboard dismissal") {
		t.Fatal("Escape did not dismiss the focused notification")
	}
}

func TestNotificationMotionSettles(t *testing.T) {
	a := newTestApp(ukServers())
	a.motion.instant = false
	tt := ui.NewTester(a.view, panelWidth, 240)
	a.notifyError("Connection failed")
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	if !tt.HasText("Connection failed") {
		t.Fatal("notification did not enter")
	}
	if err := tt.Click("Dismiss notification"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	if tt.HasText("Connection failed") {
		t.Fatal("notification exit did not finish")
	}
	tt.Move(2, 2)
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("dismissed notification keeps requesting frames")
	}
}

func TestSingleLineNotificationCentersItsContents(t *testing.T) {
	for _, messageText := range []string{"Settings saved", "Copied " + logFollowCommand} {
		for _, dark := range []bool{false, true} {
			name := "light"
			if dark {
				name = "dark"
			}
			t.Run(name+"/"+messageText, func(t *testing.T) {
				a := newTestApp(ukServers())
				tt := ui.NewTester(a.view, panelWidth, 240)
				tt.SetDark(dark)
				a.notify(messageText)
				tt.Frame()
				card, ok := tt.Find("Notification: Notice")
				if !ok {
					t.Fatal("missing toast")
				}
				icon, ok := tt.Find("Notification status icon")
				if !ok {
					t.Fatal("missing status icon")
				}
				message, ok := tt.Find(messageText)
				if !ok {
					t.Fatal("missing message")
				}
				close, ok := tt.Find("Dismiss notification")
				if !ok {
					t.Fatal("missing dismiss")
				}
				center := card.Y + card.H/2
				for name, r := range map[string]ui.Rect{"icon": icon, "message": message, "dismiss": close} {
					if abs32(r.Y+r.H/2-center) > 0.1 {
						t.Fatalf("%s is not centered: %v, toast %v", name, r, card)
					}
				}
				if gap := message.X - (icon.X + icon.W); abs32(gap-4) > 0.1 {
					t.Fatalf("icon-to-text gap is %.1f, want 4", gap)
				}
			})
		}
	}
}

func TestMultilineNotificationAlignsIconWithFirstLine(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			a := newTestApp(ukServers())
			tt := ui.NewTester(a.view, panelWidth, 240)
			tt.SetDark(dark)
			a.notifyWarning("Adding rules")
			tt.Frame()
			firstLine, ok := tt.Find("Adding rules")
			if !ok {
				t.Fatal("missing first-line reference")
			}
			if err := tt.Click("Dismiss notification"); err != nil {
				t.Fatal(err)
			}
			text := "Adding rules from the panel isn't available yet"
			a.notifyWarning(text)
			tt.Frame()
			icon, ok := tt.Find("Notification status icon")
			if !ok {
				t.Fatal("missing icon")
			}
			message, ok := tt.Find(text)
			if !ok {
				t.Fatal("missing message")
			}
			if message.H < 24 {
				t.Fatalf("fixture did not wrap: %v", message)
			}
			if abs32(icon.Y+icon.H/2-(message.Y+firstLine.H/2)) > 0.1 {
				t.Fatalf("icon is not centered on first line: icon %v, message %v", icon, message)
			}
		})
	}
}
