package main

import "github.com/egoist/mygo/ui"

// Lists own wheel/trackpad gestures even at their edges. Normal movement stays
// with MyGo; only an outward gesture at a boundary is consumed here.
func containListScroll(list ui.Element) ui.Element {
	scroll := ui.Local(list, "scroll-containment", func() ui.ScrollState { return ui.ScrollState{} })
	return list.TrackScroll(scroll).HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputScroll {
			return false
		}
		return ev.DY < 0 && scroll.Y <= 0 || ev.DY > 0 && scroll.Y >= scroll.MaxY
	})
}
