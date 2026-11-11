//go:build !windows

package server

import (
	"errors"
	"os"
)

func (s *Server) newDesktopStaging() (string, error) {
	base := s.updateDir(s.dir, "desktop-import")
	if base == "" {
		return "", errors.New("no protected staging directory")
	}
	return os.MkdirTemp(base, "payload-")
}
func openDesktopComponent(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
}
