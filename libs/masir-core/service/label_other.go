//go:build !linux

package service

// SelinuxEnforcing is false off Linux: no SELinux, nothing to enforce.
func SelinuxEnforcing() bool { return false }

// EnsureBinaryLabel relabels the service binary for SELinux; only Linux has SELinux.
// Everywhere else the install is already correct as written.
func EnsureBinaryLabel(string) error { return nil }

// LabelOK is always true without SELinux: there is no label to need.
func LabelOK(string) bool { return true }

// LabelBinary has no work off Linux.
func LabelBinary(string) error { return nil }

// RememberBinaryLabel has no work off Linux.
func RememberBinaryLabel(string) {}
