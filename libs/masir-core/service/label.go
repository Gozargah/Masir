package service

import (
	"fmt"
	"os"
	"runtime"
)

// BinaryReady reports whether the installed Linux service binary has the mode
// and SELinux label it needs to start. Other platforms do not need these checks.
func BinaryReady(bin string) bool {
	if runtime.GOOS != "linux" {
		return true
	}
	info, err := os.Stat(bin)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o100 != 0 && LabelOK(bin)
}

// EnsureBinaryReady repairs the mode and label in place when necessary.
func EnsureBinaryReady(bin string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	info, err := os.Stat(bin)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("service binary %s is not a regular file", bin)
	}
	if info.Mode().Perm()&0o100 == 0 {
		if err := os.Chmod(bin, 0o755); err != nil {
			return err
		}
	}
	if LabelOK(bin) {
		return nil
	}
	return EnsureBinaryLabel(bin)
}
