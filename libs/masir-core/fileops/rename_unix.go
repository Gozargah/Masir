//go:build !windows

package fileops

import "os"

// Rename renames a verified file, tolerating short-lived sharing locks on Windows.
func Rename(source, target string) error { return os.Rename(source, target) }
