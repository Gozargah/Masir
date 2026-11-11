package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestStatusAnimationPaintsWithoutRebuildingPanel(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "connecting"
		if running {
			name = "connected"
		}
		t.Run(name, func(t *testing.T) {
			f := ukServers()
			f.status.Settings.Power = true
			f.status.XrayRunning = running
			a := newTestApp(f)
			builds := 0
			tt := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, panelWidth, 520)
			tt.Advance(5 * time.Second)
			before := builds
			pixels := append([]byte(nil), tt.Image().Pix...)
			tt.Advance(300 * time.Millisecond)
			if builds != before {
				t.Errorf("animation rebuilt the panel %d times", builds-before)
			}
			if bytes.Equal(pixels, tt.Image().Pix) {
				t.Error("status animation stopped moving")
			}
			if !tt.FramePending() {
				t.Error("status animation stopped requesting frames")
			}
			f.status.Settings.Power = false
			f.status.XrayRunning = false
			a.applyStatus(f.status)
			tt.Frame()
			tt.Advance(time.Second)
			if tt.FramePending() {
				t.Error("disconnected dot kept animating")
			}
		})
	}
}

func TestStatusAnimationHonorsReducedMotion(t *testing.T) {
	f := ukServers()
	f.status.Settings.Power = true
	f.status.XrayRunning = true
	a := newTestApp(f)
	tt := tester(a)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("status animation ignores reduced motion")
	}
	tt.SetPreferences(ui.Preferences{})
	tt.Advance(time.Second)
	if !tt.FramePending() {
		t.Fatal("status animation did not resume")
	}
}
