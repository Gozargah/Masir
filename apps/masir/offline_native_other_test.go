//go:build !darwin

package main

import "testing"

func nativeAssertProtectedMigration(*testing.T, []byte) {}
func nativeRejectAlteredLegacy(t *testing.T, _, _ string) {
	t.Fatal("legacy layout probe is macOS-only")
}

func nativePrepareLegacyCustomData(t *testing.T) { t.Fatal("legacy layout probe is macOS-only") }

func nativeReproduceReleasedGuard(t *testing.T, _, _, _ string) {
	t.Fatal("released guard probe is macOS-only")
}
