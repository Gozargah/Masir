//go:build !darwin

package main

import "testing"

func nativeAssertProtectedInstallation(*testing.T) {}

func nativeRejectUnsupportedLegacy(t *testing.T, _, _ string) {
	t.Fatal("legacy layout probe is macOS-only")
}
