//go:build !windows

package geopack

import "os"

func makeStage(base string) (string, error) { return os.MkdirTemp(base, ".geo-") }
func createResource(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
}

func createInstalledResource(path string, mode os.FileMode) (*os.File, error) {
	return createResource(path, mode)
}
func ensureInstalledDirectory(path string) error { return os.MkdirAll(path, 0755) }
