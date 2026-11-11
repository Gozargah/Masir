//go:build !linux && !darwin && !windows

package service

// Supervised is false where masir has no service manager to be started by.
func Supervised() bool { return false }
