package server

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/Gozargah/Masir/libs/masir-core/paths"
	"github.com/Gozargah/Masir/libs/masir-core/service"
	"golang.org/x/sys/unix"
)

func lockOfflineService() (offlineServiceControl, error) {
	// /usr/local/lib is commonly owned by Homebrew's user. Never change its owner
	// or relax the root-controlled target check; all new writes go under /Library.
	parent := filepath.Dir(service.InstallDir())
	if e := paths.EnsureRootDir(parent, 0, 0755); e != nil {
		return offlineServiceControl{}, e
	}
	if !rootControlled(parent) {
		return offlineServiceControl{}, errors.New("service helper parent is not root-controlled")
	}
	if e := paths.EnsureRootDir(service.InstallDir(), 0, 0755); e != nil {
		return offlineServiceControl{}, e
	}
	fd, e := unix.Open(filepath.Join(service.InstallDir(), ".offline-update.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return offlineServiceControl{}, e
	}
	f := os.NewFile(uintptr(fd), "offline-update-lock")
	fail := func(e error) (offlineServiceControl, error) { f.Close(); return offlineServiceControl{}, e }
	var st unix.Stat_t
	if e := unix.Fstat(fd, &st); e != nil {
		return fail(e)
	}
	if st.Uid != 0 || st.Mode&0022 != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return fail(errors.New("unsafe offline service lock"))
	}
	if e := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fail(errors.New("another offline service update is running"))
	}
	if service.LegacyLocation() {
		if !rootControlled(service.DefinitionPath()) {
			return fail(errors.New("installed launchd definition is not root-controlled"))
		}
		c, e := snapshotLegacyDarwin(service.LegacyDarwinDir, service.InstallDir())
		if e != nil {
			return fail(e)
		}
		closeSnapshot := c.close
		c.close = func() { closeSnapshot(); f.Close() }
		return c, nil
	}
	if !rootControlled(service.BinaryPath()) {
		return fail(errors.New("installed service is not root-controlled"))
	}
	return offlineServiceControl{close: func() { f.Close() }, version: readOfflineVersion, publish: func() error { return nil }}, nil
}
