package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/Gozargah/Masir/libs/masir-core/paths"
	"github.com/Gozargah/Masir/libs/masir-core/service"
	"golang.org/x/sys/unix"
)

func stopOfflineService() error {
	bootout()
	if exec.Command("launchctl", "print", "system/"+service.Label).Run() == nil {
		return errors.New("service did not stop for offline update")
	}
	return nil
}
func startOfflineService() error {
	if err := migrateOfflineServiceDefinition(); err != nil {
		return err
	}
	applyFileIcon(service.BinaryPath(), service.InstallDir())
	return bootstrap()
}

// The old plist remains authoritative until a protected executable is ready.
// Preserve its served user and settings, and only change the executable argument.
func migrateOfflineServiceDefinition() error {
	path := service.DefinitionPath()
	in, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	st, err := in.Stat()
	if err != nil {
		in.Close()
		return err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || stat.Uid != 0 || stat.Nlink != 1 || st.Mode().Perm()&0022 != 0 || st.Size() > 65536 {
		in.Close()
		return errors.New("installed launchd definition is not a protected regular file")
	}
	data, readErr := io.ReadAll(io.LimitReader(in, 65537))
	err = errors.Join(readErr, in.Close())
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return errors.New("installed launchd definition is oversized")
	}
	next, err := service.RewriteLegacyDefinition(data)
	if err != nil {
		return err
	}
	if bytes.Equal(data, next) {
		return nil
	}
	// The directory must be root-controlled, and the helper's installer has already
	// authenticated/published the executable before it invokes this recovery hook.
	if err := paths.EnsureRootDir(filepath.Dir(path), 0, 0755); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(path), ".masir-plist-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, writeErr := out.Write(next)
	err = errors.Join(writeErr, out.Chmod(0644), out.Sync(), out.Close())
	if err != nil {
		return err
	}
	return os.Rename(out.Name(), path)
}
