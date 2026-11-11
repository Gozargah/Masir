//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strconv"

	"github.com/Gozargah/Masir/libs/masir-core/service"
)

// serviceDefinitionStale reports whether the installed service definition differs from
// the one the service's binary carries. The app reads both without privileges: the unit
// and the plist are world-readable, and `daemon unit` only prints.
func serviceDefinitionStale() bool {
	path := service.DefinitionPath()
	if path == "" {
		return false
	}
	out, err := exec.Command(service.BinaryPath(), "daemon", "unit", "--user", strconv.Itoa(os.Getuid())).Output()
	if err != nil || len(out) == 0 {
		return false // an older service binary without `unit`
	}
	cur, err := os.ReadFile(path)
	return err == nil && string(cur) != string(out)
}
