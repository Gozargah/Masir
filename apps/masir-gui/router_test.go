package main

import (
	"testing"
	"time"
)

// Closing the panel forgets the page it was on: the next open starts at the
// main page, and Back has nowhere to go.
func TestClosingResetsTheRouter(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	tt.Frame()
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if got := a.router.Path(); got != string(settingsPath) {
		t.Fatalf("router at %q before close, want /settings", got)
	}

	s := &shell{a: a}
	s.stop()
	if got := a.router.Path(); got != string(mainPath) {
		t.Fatalf("router at %q after close, want /", got)
	}
	if a.router.CanGoBack() || a.router.CanGoForward() {
		t.Fatal("close kept the page history")
	}
}

// A router made after the popover closed keeps the page motion: the app's
// transition is installed on it again, and both pages stay built long enough
// for the panel's page spring.
func TestANewRouterKeepsThePageMotion(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	tt.Frame()
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	a.resetRouter()
	if a.router.Animation == nil || a.router.Animation.Duration != pageMotionDuration {
		t.Fatalf("new router animation = %v, want %v", a.router.Animation, pageMotionDuration)
	}
}
