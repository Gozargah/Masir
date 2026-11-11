//go:build windows

package service

import "golang.org/x/sys/windows/svc"

// Supervised reports whether the Service Control Manager started this process, rather
// than a person or another masir.
func Supervised() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}
