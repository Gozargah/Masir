//go:build !mygo_patched

package main

// setPageTransition leaves the router to its own transition. The panel's own
// needs the page-transition patch in patches/, which the packaged build applies
// and compiles with the mygo_patched tag (see scripts/build-gui.sh).
func setPageTransition(*app) {}
