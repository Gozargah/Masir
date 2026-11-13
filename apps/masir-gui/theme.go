package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// palette holds the colors of the design, light and dark: the same tokens the panel has
// always used, as translucent surfaces over the system's material.
type palette struct {
	// Text hierarchy.
	text, muted, tertiary, quaternary ui.Color
	// Surfaces and layers.
	card, cardHover, cardActive              ui.Color
	elevated, popup, subdued, inset          ui.Color
	listHeader                               ui.Color // opaque, for a pinned header over scrolling rows
	borderSubtle, borderDefault, borderFocus ui.Color
	// Status.
	healthy, healthySurface   ui.Color
	switchOn                  ui.Color // control fill stays vivid independently of readable status text
	degraded, degradedSurface ui.Color
	errorC, errorSurface      ui.Color
	errorBorder               ui.Color
	neutral, neutralSurface   ui.Color
	// Destinations and accent.
	direct, directSurface ui.Color
	pinned, pinnedSurface ui.Color
	accent, accentHover   ui.Color
	accentSurface         ui.Color
	// Scroll bar thumb.
	scrollbar, scrollbarHover ui.Color
	dark                      bool
}

func paletteFor(c *ui.Context) palette {
	if c.Theme().Dark {
		return palette{
			dark: true,
			text: ui.Hex("#f5f5f7"), muted: ui.RGBA(255, 255, 255, 0.65), tertiary: ui.RGBA(255, 255, 255, 0.56), quaternary: ui.RGBA(255, 255, 255, 0.25),
			card: ui.RGBA(255, 255, 255, 0.07), cardHover: ui.RGBA(255, 255, 255, 0.05), cardActive: ui.RGBA(255, 255, 255, 0.08),
			elevated: ui.RGBA(30, 30, 30, 0.85), popup: ui.Hex("#232325"), subdued: ui.RGBA(0, 0, 0, 0.25), inset: ui.RGBA(255, 255, 255, 0.06),
			listHeader:   ui.Hex("#1f1f21"),
			borderSubtle: ui.RGBA(255, 255, 255, 0.05), borderDefault: ui.RGBA(255, 255, 255, 0.10), borderFocus: ui.Hex("#0a84ff"),
			switchOn: ui.Hex("#00d688"), healthy: ui.Hex("#00d688"), healthySurface: ui.RGBA(0, 192, 120, 0.16),
			degraded: ui.Hex("#ff9f0a"), degradedSurface: ui.RGBA(255, 149, 0, 0.16),
			errorC: ui.Hex("#ff453a"), errorSurface: ui.RGBA(255, 69, 58, 0.16), errorBorder: ui.RGBA(255, 69, 58, 0.25),
			neutral: ui.Hex("#8e8e93"), neutralSurface: ui.RGBA(255, 255, 255, 0.08),
			direct: ui.Hex("#94a3b8"), directSurface: ui.RGBA(148, 163, 184, 0.15),
			pinned: ui.Hex("#a9d4ff"), pinnedSurface: ui.RGBA(20, 53, 84, 0.88),
			accent: ui.Hex("#0a84ff"), accentHover: ui.Hex("#0066d6"), accentSurface: ui.RGBA(10, 132, 255, 0.15),
			scrollbar: ui.RGBA(255, 255, 255, 0.28), scrollbarHover: ui.RGBA(255, 255, 255, 0.45),
		}
	}
	return palette{
		text: ui.Hex("#20242b"), muted: ui.RGBA(20, 24, 32, 0.76), tertiary: ui.RGBA(20, 24, 32, 0.68), quaternary: ui.RGBA(20, 24, 32, 0.4),
		card: ui.RGBA(255, 255, 255, 0.60), cardHover: ui.RGBA(20, 24, 32, 0.06), cardActive: ui.RGBA(20, 24, 32, 0.10),
		elevated: ui.RGBA(250, 251, 253, 0.94), popup: ui.Hex("#f8f9fb"), subdued: ui.RGBA(20, 24, 32, 0.045), inset: ui.RGBA(20, 24, 32, 0.05),
		listHeader:   ui.Hex("#e4e7eb"),
		borderSubtle: ui.RGBA(20, 24, 32, 0.08), borderDefault: ui.RGBA(20, 24, 32, 0.13), borderFocus: ui.Hex("#0066d6"),
		switchOn: ui.Hex("#009e64"), healthy: ui.Hex("#006b44"), healthySurface: ui.RGBA(0, 130, 80, 0.10),
		degraded: ui.Hex("#8b4a00"), degradedSurface: ui.RGBA(220, 150, 0, 0.13),
		errorC: ui.Hex("#b01d1d"), errorSurface: ui.RGBA(220, 38, 38, 0.10), errorBorder: ui.RGBA(190, 30, 30, 0.20),
		neutral: ui.Hex("#68707c"), neutralSurface: ui.RGBA(20, 24, 32, 0.065),
		direct: ui.Hex("#475569"), directSurface: ui.RGBA(100, 116, 139, 0.10),
		pinned: ui.Hex("#005fc7"), pinnedSurface: ui.RGBA(0, 100, 210, 0.10),
		accent: ui.Hex("#0066d6"), accentHover: ui.Hex("#0059bd"), accentSurface: ui.RGBA(0, 100, 210, 0.12),
		scrollbar: ui.RGBA(20, 24, 32, 0.32), scrollbarHover: ui.RGBA(20, 24, 32, 0.48),
	}
}

// Sizes of the panel, in DIPs.
const (
	panelWidth  = 300
	panelRadius = 14
	cardRadius  = 9.75 // rounded-xl

	// The type scale.
	sizeTitle   = 14
	sizeBody    = 13
	sizeSubhead = 12
	sizeCaption = 11
	sizeMicro   = 10

	// Heights of the screens that don't size themselves to their content.
	settingsHeight         = 520
	addSubscriptionHeight  = 268
	subscriptionEditHeight = 342 // first-layout estimate; the form reports its actual height

	// defaultPanelHeight is how tall the main page opens before it has ever drawn: the
	// closed page at the current layout. The height the last launch drew replaces it.
	defaultPanelHeight = 240
)

// tw converts Tailwind spacing units to DIPs: the design sets its root font size to 13px, so
// a unit (0.25rem) is 3.25 and p-2.5, gap-1.5 and w-7 are not what they are at 16px.
func tw(n float32) float32 { return n * 3.25 }

// The motion of the design: 200ms on CSS's "ease"-like curve for the accordions and the
// pages, and a springier curve for the switch.
const motionDuration = 200 * time.Millisecond

// pageMotionDuration is how long the router keeps both pages of a change
// built. The app places them with its own page spring, which settles in about
// 170ms; this leaves room for a blocked frame or two before the router drops
// the page going out.
const pageMotionDuration = 300 * time.Millisecond

var (
	easeApp    = ui.CubicBezier(0.25, 0.1, 0.25, 1)
	easeSwitch = ui.CubicBezier(0.16, 1, 0.3, 1)
)

func appMotion(d time.Duration) ui.Animation { return ui.Animation{Duration: d, Ease: easeApp} }

// tuneTheme makes scroll bars belong to the panel: thin, and in the palette's own color
// rather than the stock one. The theme is copied so the change is the app's only.
func (a *app) tuneTheme(c *ui.Context, p palette) {
	t := *c.Theme()
	if t.ScrollbarWidth == 4 && t.Scrollbar == p.scrollbar {
		return
	}
	t.ScrollbarWidth = 4
	t.Scrollbar = p.scrollbar
	c.SetTheme(&t)
}
