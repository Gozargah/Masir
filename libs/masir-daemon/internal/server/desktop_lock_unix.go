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
	fd, err := unix.Open(filepath.Join(service.InstallDir(), ".offline-update.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return offlineServiceControl{}, err
	}
	f := os.NewFile(uintptr(fd), "offline-update-lock")
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return offlineServiceControl{}, errors.New("another offline service update is running")
	}
	return offlineServiceControl{close: func() { f.Close() }, version: readOfflineVersion, publish: func() error { return nil }}, nil
}
