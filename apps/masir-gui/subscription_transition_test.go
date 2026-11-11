package main

import (
	"testing"
	"time"
)

// Sibling page paths crossfade in MyGo. Navigation into a subscription must
// instead move horizontally, then reverse when Back returns to the list.
func TestSubscriptionPagesSlideForwardAndBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    pagePath
		content string
	}{
		{"add", addSubscriptionPath, "SUBSCRIPTION URL"},
		{"details", subscriptionDetailsPath, "Last refreshed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(ukServers())
			a.motion.instant = false
			tt := tester(a)
			a.openPage(subscriptionsPath)
			tt.Frame()
			tt.Advance(time.Second)
			a.subscriptionPage.id = "s1"
			fromHeight := a.windowHeight()
			a.openPage(tc.path)
			if got := a.windowHeight(); got != fromHeight {
				t.Fatalf("height jumped at navigation: got %v want %v", got, fromHeight)
			}
			tt.Frame()
			tt.Advance(60 * time.Millisecond)
			height := a.windowHeight()
			toHeight := pageOf(tc.path).height
			if height <= min(fromHeight, toHeight) || height >= max(fromHeight, toHeight) {
				t.Fatalf("height did not animate: from %v via %v to %v", fromHeight, height, toHeight)
			}
			during, ok := tt.Find(tc.content)
			if !ok {
				t.Fatalf("missing incoming %q", tc.content)
			}
			tt.Advance(time.Second)
			settled, ok := tt.Find(tc.content)
			if !ok {
				t.Fatalf("missing settled %q", tc.content)
			}
			if during.X <= settled.X+10 {
				t.Fatalf("page faded instead of sliding: during x=%v settled x=%v", during.X, settled.X)
			}
			a.goBack()
			tt.Frame()
			tt.Advance(60 * time.Millisecond)
			returning, ok := tt.Find("Open marzban")
			if !ok {
				t.Fatal("returning list not built")
			}
			tt.Advance(time.Second)
			done, ok := tt.Find("Open marzban")
			if !ok {
				t.Fatal("list not restored")
			}
			if returning.X >= done.X-10 {
				t.Fatalf("Back faded instead of sliding: during x=%v settled x=%v", returning.X, done.X)
			}
		})
	}
}

func TestResetRouterForgetsSubpageMotion(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	a.openPage(subscriptionsPath)
	tt.Frame()
	a.openPage(addSubscriptionPath)
	tt.Frame()
	a.resetRouter()
	if a.subpageMove || !a.motion.detail.settled() {
		t.Fatal("reset kept the previous subpage transition")
	}
}
