package main

import (
	"bytes"
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestLatencyBadgeDimsTextAndPulsesOnlyDot(t *testing.T) {
	testingProbe := false
	tt := ui.NewTester(func(c *ui.Context) {
		latencyBadge(c, "probe", 180, testingProbe, paletteFor(c))
	}, 150, 40)
	tt.Advance(time.Second)
	rest := append([]byte(nil), tt.Image().Pix...)

	testingProbe = true
	tt.Frame()
	if !tt.HasText("180") || bytes.Equal(rest, tt.Image().Pix) {
		t.Fatal("testing state should dim the previous reading")
	}
	pending := append([]byte(nil), tt.Image().Pix...)
	tt.Advance(latencyPulsePeriod / 2)
	frame := tt.Image()
	var changed image.Rectangle
	for y := frame.Rect.Min.Y; y < frame.Rect.Max.Y; y++ {
		for x := frame.Rect.Min.X; x < frame.Rect.Max.X; x++ {
			i := frame.PixOffset(x, y)
			if !bytes.Equal(pending[i:i+4], frame.Pix[i:i+4]) {
				changed = changed.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if changed.Empty() || changed.Dx() > 5 || changed.Dy() > 5 {
		t.Fatalf("expected only the dot to pulse, changed bounds = %v", changed)
	}

	testingProbe = false
	tt.Frame()
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("dot kept pulsing after the test finished")
	}
	frame = tt.Image()
	for y := frame.Rect.Min.Y; y < frame.Rect.Max.Y; y++ {
		for x := frame.Rect.Min.X; x < frame.Rect.Max.X; x++ {
			i := frame.PixOffset(x, y)
			if !image.Pt(x, y).In(changed) && !bytes.Equal(rest[i:i+4], frame.Pix[i:i+4]) {
				t.Fatal("finishing a test should restore the text opacity")
			}
		}
	}
}

func TestLatencyBadgePulseHonorsReducedMotion(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		latencyBadge(c, "probe", 180, true, paletteFor(c))
	}, 150, 40)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("testing dot pulsed with reduced motion enabled")
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
