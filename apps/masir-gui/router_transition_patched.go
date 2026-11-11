//go:build mygo_patched

package main

// setPageTransition gives the router the panel's page transition, which
// app.pageTransition places. Only a build with the mygo-router-page-transition
// patch compiles this; the headless tests use the stock router and its
// counterpart beside this file.
func setPageTransition(a *app) {
	a.router.PageTransition = a.pageTransition
}
