package main

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestListHeaderIsOpaque checks that the header pinned over a list hides the rows
// scrolling under it: a translucent tint would let their text show through.
func TestListHeaderIsOpaque(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tt := ui.NewTester(func(c *ui.Context) {
			if p := paletteFor(c); p.listHeader.A != 255 {
				t.Errorf("dark=%v: the list header's alpha is %d, want opaque", dark, p.listHeader.A)
			}
		}, 10, 10)
		tt.SetDark(dark)
		tt.Frame()
	}
}

// The material owns the complete popover shape, including the arrow. A content
// background would tint only the body and flatten the glass behind it.
func TestMaterialKeepsContentTransparent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, path := range []pagePath{mainPath, subscriptionDetailsPath} {
			a := newTestApp(ukServers())
			if path == subscriptionDetailsPath {
				a.openSubscription("s1")
			}
			tt := ui.NewTester(a.view, panelWidth, 560)
			tt.SetDark(dark)
			tt.SetVibrancy(true)
			tt.Frame()
			_, _, _, alpha := tt.Image().At(5, 280).RGBA()
			if alpha != 0 {
				t.Fatalf("dark=%v page=%s: content background covers native glass (alpha=%d)", dark, path, alpha)
			}
			tt.SetVibrancy(false)
			tt.Frame()
			_, _, _, alpha = tt.Image().At(5, 280).RGBA()
			if alpha != 65535 {
				t.Fatalf("dark=%v page=%s: window without material has no opaque background", dark, path)
			}
		}
	}
}

// Small status labels need darker ink in light mode; using the vivid switch
// green for these labels made them disappear into their pale badge surfaces.
func TestLightPaletteTextAndSwitchContrast(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		p := paletteFor(c)
		white := ui.Hex("#ffffff")
		for _, material := range []ui.Color{ui.Hex("#b8bdc5"), white} {
			card := p.card.Over(material)
			for _, state := range []struct {
				name      string
				ink, fill ui.Color
			}{
				{"healthy", p.healthy, p.healthySurface},
				{"warning", p.degraded, p.degradedSurface},
				{"error", p.errorC, p.errorSurface},
			} {
				bg := state.fill.Over(card)
				if ratio := textContrast(state.ink, bg); ratio < 4.5 {
					t.Errorf("%s badge contrast %.2f:1; want 4.5:1", state.name, ratio)
				}
			}
			if ratio := textContrast(p.muted.Over(material), material); ratio < 4.5 {
				t.Errorf("secondary text contrast %.2f:1; want 4.5:1", ratio)
			}
		}
		if ratio := textContrast(white, p.switchOn); ratio < 3 {
			t.Errorf("white switch knob contrast %.2f:1; want 3:1", ratio)
		}
		if ratio := textContrast(white, p.accent); ratio < 4.5 {
			t.Errorf("primary button text contrast %.2f:1; want 4.5:1", ratio)
		}
	}, 10, 10)
	tt.SetDark(false)
	tt.Frame()
}

func textContrast(a, b ui.Color) float64 {
	luminance := func(c ui.Color) float64 {
		linear := func(v uint8) float64 {
			x := float64(v) / 255
			if x <= 0.04045 {
				return x / 12.92
			}
			return math.Pow((x+0.055)/1.055, 2.4)
		}
		return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
	}
	x, y := luminance(a), luminance(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}

func TestSubscriptionServerHeaderIsDarkerThanItsList(t *testing.T) {
	for _, dark := range []bool{false, true} {
		a := newTestApp(ukServers())
		a.subscriptionPage.id = "s1"
		tt := ui.NewTester(func(c *ui.Context) { a.subscriptionDetailsView(c, paletteFor(c)) }, panelWidth, 560)
		tt.SetDark(dark)
		tt.Frame()
		header, ok := tt.Find("Subscription server header")
		if !ok {
			t.Fatal("missing server header")
		}
		list, ok := tt.Find("Subscription servers")
		if !ok {
			t.Fatal("missing server list")
		}
		im := tt.Image()
		r, g, b, _ := im.At(int(header.X+header.W/2), int(header.Y+header.H/2)).RGBA()
		lr, lg, lb, _ := im.At(int(list.X+list.W/2), int(list.Y+list.H-12)).RGBA()
		if r+g+b >= lr+lg+lb {
			t.Fatalf("dark=%v: header surface isn't darker than server rows", dark)
		}
	}
}

// Native dark glass becomes brighter over a white window. The nested input
// surface and its focused placeholder must keep their contrast there too.
func TestFocusedDarkPlaceholderOverBrightMaterial(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		p := paletteFor(c)
		background, _, placeholder := inputColors(p, true)
		for _, material := range []ui.Color{ui.Hex("#303034"), ui.Hex("#808080")} {
			surface := background.Over(p.card.Over(material))
			if ratio := textContrast(placeholder.Over(surface), surface); ratio < 4.5 {
				t.Errorf("focused placeholder contrast %.2f:1 over %v; want 4.5:1", ratio, material)
			}
		}
	}, 10, 10)
	tt.SetDark(true)
	tt.Frame()
}

func TestDarkPinnedBadgeContrastOverNativeMaterial(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		if !c.Theme().Dark {
			return
		}
		p := paletteFor(c)
		for _, material := range []ui.Color{ui.Hex("#1e1e20"), ui.Hex("#60646c"), ui.Hex("#a0a4ac"), ui.Hex("#ffffff")} {
			background := p.pinnedSurface.Over(p.card.Over(material))
			if ratio := textContrast(p.pinned, background); ratio < 4.5 {
				t.Errorf("pinned badge contrast %.2f:1 over %v, want at least 4.5:1", ratio, material)
			}
		}
	}, 10, 10)
	tt.SetDark(true)
	tt.Frame()
}
