package systemdns

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/Gozargah/Masir/libs/masir-core/paths"
	"golang.org/x/sys/unix"
)

func rootLock(dir string) (func(), error) { return lockDirectory(dir, 0) }
func lockDirectory(dir string, uid int) (func(), error) {
	if err := paths.EnsureRootDir(dir, uid, 0700); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(dir, "lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "system-dns-lock")
	fail := func(err error) (func(), error) { f.Close(); return nil, err }
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fail(err)
	}
	if int(st.Uid) != uid || st.Mode&0022 != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return fail(errors.New("unsafe system DNS lock"))
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(errors.New("another daemon owns macOS system DNS"))
	}
	return func() { _ = f.Close() }, nil
}
