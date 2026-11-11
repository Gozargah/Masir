//go:build windows || (!darwin && !linux && !windows)

package main

// applyServiceUnit does nothing here: Windows keeps the service's configuration in the
// SCM, and the remaining systems have no service manager file to keep in sync.
func applyServiceUnit() error { return nil }
