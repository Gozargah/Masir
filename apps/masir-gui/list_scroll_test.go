package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNestedListContainsScrollingAtBothEdges(t *testing.T) {
	var parent ui.ScrollState
	parent.Y = 60
	var items ui.ListState
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Label("Outer pane").TrackScroll(&parent).Height(180).Children(func() {
			ui.Box(c).Height(100).Shrink(0)
			containListScroll(ui.List(c, &items, 20, func(i int) { ui.Text(c, fmt.Sprintf("Item %d", i)).Height(24).Shrink(0) })).Label("Inner list").Height(80).Shrink(0)
			ui.Box(c).Height(300).Shrink(0)
		})
	}, 260, 200)
	tt.Frame()
	tt.Frame()
	list, ok := tt.Find("Inner list")
	if !ok {
		t.Fatal("missing inner list")
	}
	before := parent.Y
	tt.Scroll(50, list.Y+20, 0, -60)
	tt.Frame()
	if parent.Y != before {
		t.Fatalf("upward edge scroll escaped the list: parent %v -> %v", before, parent.Y)
	}
	tt.Scroll(50, list.Y+20, 0, 60)
	tt.Frame()
	first, _ := items.Visible()
	if first == 0 {
		t.Fatal("containment disabled normal list scrolling")
	}
	tt.Scroll(50, list.Y+20, 0, 5000)
	tt.Frame()
	before = parent.Y
	tt.Scroll(50, list.Y+20, 0, 60)
	tt.Frame()
	if parent.Y != before {
		t.Fatalf("downward edge scroll escaped the list: parent %v -> %v", before, parent.Y)
	}
}
