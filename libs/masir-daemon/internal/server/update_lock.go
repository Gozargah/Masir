package server

import (
	"errors"
	"github.com/Gozargah/Masir/libs/masir-core/service"
)

// lockUpdate serializes all writers, including the separate elevated desktop
// installer. Hold it for the entire job, not only the final file replacement.
func (s *Server) lockUpdate() (func(), error) {
	if !s.upd.installMu.TryLock() {
		return nil, errors.New("another CLI, geo or desktop update is running")
	}
	if !s.id.Privileged || !service.Installed() {
		return s.upd.installMu.Unlock, nil
	}
	release, err := lockServiceUpdateFile()
	if err != nil {
		s.upd.installMu.Unlock()
		return nil, err
	}
	return func() { release(); s.upd.installMu.Unlock() }, nil
}
