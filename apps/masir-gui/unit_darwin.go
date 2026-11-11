//go:build darwin

package main

import (
	"os"
	"strconv"

	"github.com/Gozargah/Masir/libs/masir-core/service"
)

// applyServiceUnit rewrites the installed plist when the service's binary carries a
// different one, so a release that changes it applies after the update instead of at
// some later reinstall. It asks for permission only when the definition changed.
func applyServiceUnit() error {
	if !serviceDefinitionStale() {
		return nil
	}
	return runAdmin(service.BinaryPath(), "daemon", "sync-unit", "--user", strconv.Itoa(os.Getuid()))
}
