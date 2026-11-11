package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// aimMotion points the springs at where the state says things should be, steps them by
// the frame's own time, and asks for the next frame while they move. The frame clock is
// the display's, so the view and the window change together and only as often as they
// can be seen. A frame may build the view more than once; only its first pass steps the
// springs.
func (a *app) aimMotion(c *ui.Context) {
	pageTarget := b2f(!pageOf(a.path).main)
	changed := a.motion.aim(b2f(a.open == sectionServers), b2f(a.open == sectionRules), pageTarget)
	now := c.Now()
	first := a.lastMotion.IsZero() || now.After(a.lastMotion)
	if first {
		// A page opening shows for the first time: hold its spring for this frame and build
		// it unseen instead, so its first layout and paint are not the frame that starts
		// the slide.
		if !a.motion.instant && pageTarget == 1 && a.motion.page.x == 0 && !a.warmed[a.path] {
			a.warmed[a.path] = true
			a.lastMotion = now
			c.AnimationFrame()
			return
		}
	}
	moving := !a.motion.settled()
	if a.holdPage {
		// The frame the pages change: lay both out at the start of the spring
		// and go no further, so the popover's resize is spent before the pages
		// move instead of eating the first half of the slide.
		a.holdPage = false
		a.pinMotion = true
		c.AnimationFrame()
		return
	}
	if a.pinMotion {
		// That frame took the resize; the next frame is the clock's new zero,
		// so the spring's first step is one frame, not the whole block.
		a.pinMotion = false
		a.lastMotion = now
		c.AnimationFrame()
		return
	}
	switch {
	case a.lastMotion.IsZero():
		a.lastMotion = now
	case now.After(a.lastMotion):
		dt := float32(min(now.Sub(a.lastMotion).Seconds(), 0.05))
		a.lastMotion = now
		a.motion.step(dt)
		switch {
		case a.frameSize && !a.motion.settled():
			// The window follows the springs as they move: the height grows and shrinks
			// with the page or list instead of jumping to the end. Resizing the window makes
			// MyGo draw a frame at once, which builds the view again; ask for a height at
			// most once a display frame. Asking every 6 ms builds the page two or three
			// times for a frame the person sees, which a heavy page such as settings cannot
			// afford.
			if a.onHeight != nil && (a.lastSize.IsZero() || now.Sub(a.lastSize) >= heightStep) {
				a.lastSize = now
				a.onHeight(a.windowHeight())
			}
		case moving && a.motion.settled():
			// The content has come to rest: its exact height may differ from the estimate
			// it grew to, so settle the window on it once, now that nothing moves.
			a.requestHeight()
		}
	}
	if a.frameSize && changed && a.motion.settled() && a.onHeight != nil {
		// An instant change (tests) has nothing to follow; ask for the exact height.
		a.requestHeight()
	}
	if !a.motion.settled() {
		c.AnimationFrame()
	}
}

// heightStep is how often a window that follows the springs takes a new size:
// one per 60 Hz frame. Faster than that draws frames the display never shows.
const heightStep = 16 * time.Millisecond

// requestHeight tells the window how tall the content will be once at rest. On a window
// that follows the springs, a transition is sized frame by frame instead, so the window and
// the content move together; only a change with no movement is asked for here.
func (a *app) requestHeight() {
	if a.onHeight == nil {
		return
	}
	if a.frameSize && a.willMove() {
		return
	}
	h := a.restHeight()
	if h <= 0 || abs32(h-a.askedHeight) < 0.5 {
		return
	}
	a.askedHeight = h
	a.onHeight(h)
}

// willMove reports whether the state asks for a spring to move that is not there yet.
func (a *app) willMove() bool {
	return a.motion.servers.x != b2f(a.open == sectionServers) ||
		a.motion.rules.x != b2f(a.open == sectionRules) ||
		a.motion.page.x != b2f(!pageOf(a.path).main) || !a.motion.detail.settled()
}

// windowHeight is the window's height for the springs as they are: the main page with its
// lists as far open as they are, blended with the page sliding over it. It is what a
// window that follows the animation every frame is set to, so the window grows and shrinks
// with the content instead of jumping to the end.
func (a *app) windowHeight() float32 {
	if a.subpageMove {
		return a.fromHeight + (a.toHeight-a.fromHeight)*a.motion.detail.x
	}
	base := a.baseHeight
	if base == 0 {
		base = defaultPanelHeight
	}
	main := base + a.motion.servers.x*a.naturalHeight("servers") + a.motion.rules.x*a.naturalHeight("rules")
	if a.motion.settled() && a.mainHeight > 0 {
		main = a.mainHeight
	}
	return main + (a.subHeight()-main)*a.motion.page.x
}

// naturalHeight is how tall a list is when fully open: as measured the last time it was
// shown, else an estimate from what it holds.
func (a *app) naturalHeight(key string) float32 {
	if h := a.natural[key]; h > 0 {
		return h
	}
	switch key {
	case "servers":
		rows := float32(len(a.filteredServers()))
		return 46 + min(300, rows*42+8)
	default:
		rows := float32(len(a.rules.rules))
		return 46 + min(167, rows*44+60)
	}
}

// restHeight is how tall the panel is with everything at rest: the page shown, with an
// open list as tall as it measured (or is estimated to be just before it opens).
func (a *app) restHeight() float32 {
	if !pageOf(a.path).main {
		return a.subHeight()
	}
	if a.mainHeight > 0 && a.open == a.measuredOpen && a.motion.listsSettled() {
		return a.mainHeight // the content itself says how tall it is
	}
	base := a.baseHeight
	if base == 0 {
		base = defaultPanelHeight
	}
	if a.open == sectionServers {
		base += a.naturalHeight("servers")
	}
	if a.open == sectionRules {
		base += a.naturalHeight("rules")
	}
	return base
}

func (a *app) subHeight() float32 {
	if a.lastSub == addSubscriptionPath && a.importHeight > 0 {
		return a.importHeight
	}
	if a.lastSub == editSubscriptionPath && a.subscriptionPage.editHeight > 0 {
		return a.subscriptionPage.editHeight
	}
	return pageOf(a.lastSub).height
}

// heightForOpen is how tall the panel starts. It is the height the content actually had
// the last time it was drawn, or, on the first open of a session, the height the previous
// launch remembered. Opening therefore doesn't resize the panel a frame later. Building
// the view in a headless tester here (as an earlier version did) measured with another
// text engine and made the panel open at a height it never drew.
func (a *app) heightForOpen() float32 {
	if !pageOf(a.path).main {
		return a.restHeight()
	}
	if a.mainHeight > 0 && a.open == a.measuredOpen {
		return a.mainHeight
	}
	if a.baseHeight > 0 { // the main page with both lists closed, as it opens
		return a.baseHeight
	}
	return defaultPanelHeight
}

// settleHeight gives the window the height the page just drew, when nothing is moving.
// Data arrives while the panel is open (a status, subscriptions, a banner, the update
// control) and nothing springs, so without this the window would keep the height from
// the frame that opened it. Layers call it after they have recorded their bounds.
func (a *app) settleHeight() {
	if a.onHeight == nil || !a.motion.settled() {
		return
	}
	if a.willMove() {
		// The click that just changed the page or a list is drawn in this frame with
		// the old springs; the height it measured belongs to the state that is leaving.
		// The transition asks for the target height, and the frame that settles asks
		// for the exact one.
		return
	}
	if h := a.restHeight(); h > 0 && abs32(h-a.askedHeight) > 0.5 {
		a.askedHeight = h
		a.onHeight(h)
	}
}
