//go:build !darwin && !linux && !windows

package main

func installService(string, string) error { return errNoService }

func uninstallService() error { return errNoService }

func restartService() error { return errNoService }

// serviceDefinition is empty: this OS has no service manager file to keep in sync.
func serviceDefinition(string, int) string { return "" }

func applyServiceDefinition(string, string, bool) error { return nil }

func serviceDefinitionPath() string { return "" }
