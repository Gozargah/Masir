package main

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestAllTextEntriesUseTheSameMetricsAndSurface(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) {
			var first, second, manual string
			tt := ui.NewTester(func(c *ui.Context) {
				p := paletteFor(c)
				ui.Column(c).Padding(12).Gap(10).Children(func() {
					field(c, p, "name", "Name", &first, "Name", iconTag, p.muted, nil)
					field(c, p, "url", "Website address", &second, "https://…", iconLink, p.muted, nil)

					labeledInput(c, p, "manual", "Manual", &manual, "Paths", iconTerminal, p.muted, 96, nil)
				})
			}, 300, 320)
			tt.SetDark(dark)
			tt.Frame()
			tt.Frame()
			var fill [3]uint32
			for i, title := range []string{"Name", "Website address", "Manual"} {
				control, ok := tt.Find(title + " input")
				if !ok {
					t.Fatal(title)
				}
				expected := inputHeight
				if title == "Manual" {
					expected = 96
				}
				if control.H != expected {
					t.Fatalf("%s control height=%v want %v", title, control.H, expected)
				}
				icon, ok := tt.Find(title + " icon")
				if !ok || icon.W != 14 || icon.H != 14 {
					t.Fatalf("%s missing consistent leading icon: %+v", title, icon)
				}
				input, ok := tt.Find(title)
				if !ok {
					t.Fatal("missing input")
				}
				if abs32(input.X-icon.X-icon.W-6) > 0.5 {
					t.Fatalf("%s icon gap inconsistent", title)
				}
				if title != "Search…" {
					label, ok := tt.Find(map[string]string{"Name": "NAME", "Website address": "WEBSITE ADDRESS", "Manual": "MANUAL"}[title])
					if !ok {
						t.Fatal("missing label")
					}
					if abs32(control.Y-label.Y-label.H-inputLabelGap) > 0.5 {
						t.Fatalf("%s label gap differs", title)
					}
				}
				r, g, b, _ := tt.Image().At(int(control.X+control.W-5), int(control.Y+control.H/2)).RGBA()
				pixel := [3]uint32{r, g, b}
				if i == 0 {
					fill = pixel
				} else if fill != pixel {
					t.Fatalf("%s input surface differs: %v vs %v", title, pixel, fill)
				}
			}
		})
	}
}

func TestSearchRetainsTransparentUnpaddedStyle(t *testing.T) {
	var query string
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(260).Background(ui.Hex("#536171")).Children(func() {
			ui.Row(c).Label("Search row").Shrink(0).Children(func() { searchInput(c, "search", &query, "Search…", paletteFor(c), false) })
		})
	}, 300, 60)
	tt.Frame()
	tt.Frame()
	row, ok := tt.Find("Search row")
	if !ok || row.H != tw(7) {
		t.Fatalf("search height changed: %+v", row)
	}
	input, ok := tt.Find("Search…")
	if !ok || abs32(input.X-row.X-20) > 0.5 {
		t.Fatalf("search gained left padding: row=%+v input=%+v", row, input)
	}
	for _, point := range [][2]float32{{row.X + 1, row.Y + 1}, {row.X + row.W - 1, row.Y + row.H/2}, {row.X + 1, row.Y + row.H - 1}} {
		r, g, b, _ := tt.Image().At(int(point[0]), int(point[1])).RGBA()
		if r>>8 != 0x53 || g>>8 != 0x61 || b>>8 != 0x71 {
			t.Fatalf("search painted a surface or border at %v: %x %x %x", point, r, g, b)
		}
	}
}
