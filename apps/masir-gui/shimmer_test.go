package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestLatencyShimmerPaintsWithoutRebuilding(t *testing.T) {
	builds := 0
	testingProbe := true
	ms := int64(180)
	tt := ui.NewTester(func(c *ui.Context) { builds++; latencyBadge(c, "probe", ms, testingProbe, paletteFor(c)) }, 150, 40)
	tt.Advance(time.Second)
	before := builds
	pixels := append([]byte(nil), tt.Image().Pix...)
	tt.Advance(300 * time.Millisecond)
	if builds != before {
		t.Errorf("shimmer rebuilt the badge %d times", builds-before)
	}
	if bytes.Equal(pixels, tt.Image().Pix) {
		t.Error("shimmer stopped moving")
	}
	testingProbe = false
	ms = 240
	tt.Frame()
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Error("completed probe kept animating")
	}
	if !tt.HasText("240") {
		t.Errorf("lost latency result: %v", tt.Texts())
	}
}

func TestTrafficUpdatesExplainPanelRebuilds(t *testing.T) {
	a := newTestApp(ukServers())
	a.status.Settings.Power = true
	a.status.XrayRunning = true
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, panelWidth, 520)
	tt.Advance(5 * time.Second)
	before := builds
	tt.Advance(time.Second)
	if builds != before {
		t.Fatalf("unchanged panel rebuilt %d times", builds-before)
	}
	a.down = 123456
	tt.Frame()
	before = builds
	tt.Advance(300 * time.Millisecond)
	t.Logf("changed traffic speed caused %d animation rebuilds over 300 ms", builds-before)
	if builds == before {
		t.Fatal("traffic digits did not animate")
	}
	before = builds
	tt.Advance(time.Second)
	if builds != before {
		t.Error("traffic digit animation failed to settle")
	}
}

func TestLatencyShimmerHonorsReducedMotion(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) { latencyBadge(c, "probe", 180, true, paletteFor(c)) }, 150, 40)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("shimmer ignores reduced motion")
	}
	tt.SetPreferences(ui.Preferences{})
	tt.Advance(time.Second)
	if !tt.FramePending() {
		t.Fatal("shimmer did not resume")
	}
	if err := tt.Click("Test latency"); err != nil {
		t.Fatal(err)
	}
}
