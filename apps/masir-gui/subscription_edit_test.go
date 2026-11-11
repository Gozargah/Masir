package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSubscriptionEditFitsContentAndResizesForErrors(t *testing.T) {
	a := newTestApp(ukServers())
	a.openSubscription("s1")
	a.openSubscriptionEdit()
	var requested float32
	a.onHeight = func(h float32) { requested = h }
	tt := tester(a)
	tt.Advance(time.Second)
	normal := a.restHeight()
	if normal >= pageOf(subscriptionDetailsPath).height || normal < 250 {
		t.Fatalf("edit didn't fit its form: %v", normal)
	}
	assertFit := func() {
		t.Helper()
		tt.SetSize(panelWidth, int(a.restHeight()+0.5))
		tt.Frame()
		content, ok := tt.Find("Subscription edit content")
		if !ok {
			t.Fatal("missing edit content")
		}
		if gap := a.restHeight() - content.Y - content.H; abs32(gap-pageInset) > 1 {
			t.Fatalf("space below edit content=%v; want %v", gap, pageInset)
		}
		if abs32(requested-a.restHeight()) > 1 || abs32(a.windowHeight()-a.restHeight()) > 1 {
			t.Fatalf("height not synchronized: requested=%v animated=%v content=%v", requested, a.windowHeight(), a.restHeight())
		}
	}
	assertFit()
	a.subscriptionPage.interval = "invalid"
	a.saveSubscription()
	tt.Frame()
	tt.Advance(time.Second)
	if a.restHeight() <= normal {
		t.Fatal("validation error did not grow the edit page")
	}
	assertFit()
	a.subscriptionPage.err = ""
	tt.Frame()
	tt.Advance(time.Second)
	if abs32(a.restHeight()-normal) > 1 {
		t.Fatal("edit page did not shrink when the error cleared")
	}
	assertFit()
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if a.path != subscriptionDetailsPath || a.restHeight() != pageOf(subscriptionDetailsPath).height {
		t.Fatal("Cancel did not restore subscription details and its height")
	}
}

func TestSubscriptionEditSlidesIntoNestedRouteAndBack(t *testing.T) {
	a := newTestApp(ukServers())
	a.motion.instant = false
	a.frameSize = true
	var sizes []float32
	a.onHeight = func(h float32) { sizes = append(sizes, h) }
	tt := tester(a)
	a.openSubscription("s1")
	tt.Frame()
	tt.Advance(time.Second)
	from := a.windowHeight()
	sizes = nil
	a.openSubscriptionEdit()
	if a.path != editSubscriptionPath || a.windowHeight() != from {
		t.Fatal("edit navigation jumped or used wrong route")
	}
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	during, ok := tt.Find("Save changes")
	if !ok {
		t.Fatal("incoming edit page missing")
	}
	if h := a.windowHeight(); h >= from || h <= a.restHeight() {
		t.Fatalf("height did not animate from %v via %v to %v", from, h, a.restHeight())
	}
	tt.Advance(time.Second)
	settled, ok := tt.Find("Save changes")
	if !ok || during.X <= settled.X+10 {
		t.Fatal("edit faded instead of sliding in")
	}
	intermediate := false
	for _, h := range sizes {
		intermediate = intermediate || (h > a.restHeight() && h < from)
	}
	if !intermediate {
		t.Fatalf("window wasn't resized with the edit-page spring: %v", sizes)
	}
	editHeight := a.windowHeight()
	a.goBack()
	if a.windowHeight() != editHeight {
		t.Fatal("Back jumped to the details height")
	}
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	returning, ok := tt.Find("Traffic")
	if !ok {
		t.Fatal("returning details missing")
	}
	tt.Advance(time.Second)
	done, ok := tt.Find("Traffic")
	if !ok || returning.X >= done.X-10 {
		t.Fatal("details faded instead of sliding back")
	}
	if a.path != subscriptionDetailsPath || a.windowHeight() != from {
		t.Fatal("Back did not restore details and its height")
	}
}

// An edit page is a normal route and remains usable without transition motion.
func TestSubscriptionEditWithReducedMotion(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	a.openSubscription("s1")
	tt.Frame()
	tt.Advance(time.Second)
	a.openSubscriptionEdit()
	tt.Frame()
	tt.Advance(time.Second)
	if a.path != editSubscriptionPath || !tt.HasText("Save changes") || a.restHeight() >= 560 {
		t.Fatal("reduced-motion edit route did not fit")
	}
}
