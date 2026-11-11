//go:build darwin

package main

import "github.com/ebitengine/purego/objc"

// tintPopoverGlass runs on the main thread after the popover opens. Tint the
// public NSGlassEffectView that AppKit uses for the whole popover, including its
// arrow, rather than painting an overlay over just the content. Older macOS
// versions have no glass view and keep their native visual effect material.
func tintPopoverGlass(handle uintptr, dark bool) bool {
	glass := objc.GetClass("NSGlassEffectView")
	if handle == 0 || glass == 0 {
		return false
	}
	sel := objc.RegisterName

	var tint objc.ID
	if dark {
		tint = objc.ID(objc.GetClass("NSColor")).Send(sel("colorWithSRGBRed:green:blue:alpha:"), float64(0), float64(0), float64(0), float64(.18))
	}
	var apply func(objc.ID) bool
	apply = func(view objc.ID) bool {
		if view == 0 {
			return false
		}
		if view.Send(sel("isKindOfClass:"), glass) != 0 {
			view.Send(sel("setTintColor:"), tint)
			return true
		}
		children := view.Send(sel("subviews"))
		for i, n := uintptr(0), uintptr(children.Send(sel("count"))); i < n; i++ {
			if apply(children.Send(sel("objectAtIndex:"), i)) {
				return true
			}
		}
		return false
	}
	window := objc.ID(handle)
	return apply(window.Send(sel("contentView")).Send(sel("superview")))
}
