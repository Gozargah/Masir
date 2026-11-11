//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/Gozargah/Masir/libs/masir-core/service"
)

// applyServiceUnit rewrites the installed unit when the service's binary carries a
// different one, so a release that changes it applies after the update instead of at
// some later reinstall. It asks for permission only when the definition changed.
func applyServiceUnit() error {
	if !serviceDefinitionStale() {
		return nil
	}
	abs, err := filepath.Abs(service.BinaryPath())
	if err != nil {
		return err
	}
	_, err = pkexec(abs, "daemon", "sync-unit", "--user", strconv.Itoa(os.Getuid()))
	return err
}
