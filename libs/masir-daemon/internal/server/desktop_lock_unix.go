//go:build !windows && !darwin

package server

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/Gozargah/Masir/libs/masir-core/service"
	"golang.org/x/sys/unix"
)

func lockOfflineService() (offlineServiceControl, error) {
	if !rootControlled(service.BinaryPath()) {
		return offlineServiceControl{}, errors.New("installed service is not root-controlled")
	}
	unlock, err := lockServiceUpdateFile()
	if err != nil {
		return offlineServiceControl{}, err
	}
	return offlineServiceControl{close: unlock, version: readOfflineVersion, publish: func() error { return nil }}, nil
}

func lockServiceUpdateFile() (func(), error) {
	fd, err := unix.Open(filepath.Join(service.InstallDir(), ".offline-update.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "offline-update-lock")
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another service update is running")
	}
	return func() { f.Close() }, nil
}
