package main

import (
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestIconTileCentersWhatItHolds checks that an icon and a flag sit in the middle of their
// rounded square, with the same room above and below, whatever the drawing's height.
func TestIconTileCentersWhatItHolds(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Children(func() {
			iconTile(c, paletteFor(c), func() {
				ui.Image(c, iconGlobe).Size(18, 18).Label("icon")
			})
			iconTile(c, paletteFor(c), func() {
				flagElement(c, "🇩🇪", 18).Label("flag")
			})
		})
	}, 300, 100)
	tt.Frame()

	side := tw(7)
	for _, tc := range []struct {
		label string
		w, h  float32
	}{
		{"icon", 18, 18},
		{"flag", 18, 18},
	} {
		r, ok := tt.Find(tc.label)
		if !ok {
			t.Fatalf("no %s in the tile", tc.label)
		}
		if r.W != tc.w || r.H != tc.h {
			t.Errorf("%s is %v×%v, want %v×%v", tc.label, r.W, r.H, tc.w, tc.h)
		}
		// The tile's 1-unit border leaves its content, and the drawing centers in it.
		top, bottom := r.Y-1, side-1-(r.Y+r.H)
		if abs32(top-bottom) > 0.01 {
			t.Errorf("%s has %v above it and %v below it", tc.label, top, bottom)
		}
	}
}

func TestPickerAppIconMatchesRulesListAlignment(t *testing.T) {
	a := newTestApp(ukServers())
	path := "/Example.app/"
	pixels := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for y := 8; y < 88; y++ {
		for x := 8; x < 88; x++ {
			pixels.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	a.apps.known[path] = &appMeta{name: "Example", icon: ui.NewBitmap(pixels)}
	a.ruleApps.selected = map[string]bool{}
	rule := &pb.RoutingRule{RuleTag: "example", RuleJson: `{"process":["/Example.app/"]}`, Target: "direct"}
	tt := ui.NewTester(func(c *ui.Context) {
		p := paletteFor(c)
		ui.Column(c).Children(func() {
			a.ruleAppRow(c, p, ruleApp{Name: "Example", Path: path, Icon: path})
			ui.Row(c).Label("Rule icon row").Height(44).AlignItems(ui.Center).Children(func() { a.ruleContent(c, p, rule) })
		})
	}, 300, 120)
	tt.Frame()
	tt.Frame()
	picker, ok := tt.Find("Picker application icon")
	if !ok {
		t.Fatal("missing picker bitmap")
	}
	reference, ok := tt.Find("Rule application icon")
	if !ok {
		t.Fatal("missing rule bitmap")
	}
	if picker.W != reference.W || picker.H != reference.H {
		t.Fatalf("picker icon is %vx%v, rule icon is %vx%v", picker.W, picker.H, reference.W, reference.H)
	}
	row, _ := tt.Find("Select Example")
	ruleRow, _ := tt.Find("Rule icon row")
	pickerOffset := picker.Y + picker.H/2 - row.Y - row.H/2
	referenceOffset := reference.Y + reference.H/2 - ruleRow.Y - ruleRow.H/2
	if abs32(pickerOffset-referenceOffset) > 0.5 {
		t.Fatalf("bitmap vertical offset: picker=%v rules=%v", pickerOffset, referenceOffset)
	}
	inkBounds := func(r ui.Rect) image.Rectangle {
		bounds := image.Rectangle{Min: image.Pt(1000, 1000)}
		for y := int(r.Y); y < int(r.Y+r.H); y++ {
			for x := int(r.X); x < int(r.X+r.W); x++ {
				rr, gg, bb, _ := tt.Image().At(x, y).RGBA()
				if rr > 50000 && gg < 5000 && bb < 5000 {
					bounds.Min.X = min(bounds.Min.X, x)
					bounds.Min.Y = min(bounds.Min.Y, y)
					bounds.Max.X = max(bounds.Max.X, x+1)
					bounds.Max.Y = max(bounds.Max.Y, y+1)
				}
			}
		}
		return bounds
	}
	pickerInk, ruleInk := inkBounds(row), inkBounds(ruleRow)
	if pickerInk.Empty() || ruleInk.Empty() {
		t.Fatal("bitmap did not render")
	}
	if pickerInk.Size() != ruleInk.Size() {
		t.Fatalf("different rendered icon sizes: picker=%v rules=%v", pickerInk, ruleInk)
	}
	pickerCenter := float32(pickerInk.Min.Y+pickerInk.Max.Y)/2 - row.Y - row.H/2
	ruleCenter := float32(ruleInk.Min.Y+ruleInk.Max.Y)/2 - ruleRow.Y - ruleRow.H/2
	if abs32(pickerCenter-ruleCenter) > 0.5 {
		t.Fatalf("rendered icon offset: picker=%v rules=%v", pickerCenter, ruleCenter)
	}

}
