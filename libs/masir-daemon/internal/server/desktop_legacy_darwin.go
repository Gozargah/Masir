package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	"golang.org/x/sys/unix"
)

func snapshotLegacyDarwin(source, target string) (c offlineServiceControl, err error) {
	root, err := openLegacyDarwinDirectory(source)
	if err != nil {
		return c, err
	}
	defer func() {
		if err != nil {
			root.Close()
		}
	}()
	snapshot, err := os.MkdirTemp(target, ".legacy-snapshot-")
	if err != nil {
		return c, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(snapshot)
		}
	}()
	copyOne := func(name string, limit int64, optional bool) error {
		in, e := openLegacyDarwinFile(root, name, limit)
		if optional && os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := geopack.CreateStagedFile(filepath.Join(snapshot, name), 0600)
		if e != nil {
			return e
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, limit+1))
		if copyErr == nil && (n == 0 || n > limit) {
			copyErr = errors.New("invalid legacy component size")
		}
		return errors.Join(copyErr, out.Close())
	}
	if err = copyOne("Masir", updatebundle.MaxFileSize, false); err != nil {
		return c, err
	}
	bin, err := os.Open(filepath.Join(snapshot, "Masir"))
	if err != nil {
		return c, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, bin)
	bin.Close()
	if err != nil {
		return c, err
	}
	version := updatebundle.LegacyDarwinVersion(hex.EncodeToString(hash.Sum(nil)))
	if version == "" {
		return c, errors.New("legacy service is outside a root-controlled directory and is not an authenticated supported release; remove and reinstall the service")
	}
	names := []string{"geoip.dat", "geosite.dat", geopack.InstalledName, "AppIcon.rsrc", "FinderInfo.bin"}
	copyAssets := func() error {
		for _, name := range names {
			if e := os.Remove(filepath.Join(snapshot, name)); e != nil && !os.IsNotExist(e) {
				return e
			}
			limit := geopack.MaxFileSize
			optional := false
			switch name {
			case geopack.InstalledName:
				limit = 16384
				optional = true
			case "AppIcon.rsrc", "FinderInfo.bin":
				limit = 2 << 20
				optional = true
			}
			if e := copyOne(name, limit, optional); e != nil {
				return e
			}
		}
		_, e := geopack.Managed(snapshot)
		return e
	}
	if err = copyAssets(); err != nil {
		return c, err
	}
	published := false
	c.snapshot = snapshot
	c.close = func() { root.Close(); os.RemoveAll(snapshot) }
	c.version = func(ctx context.Context) (string, error) {
		if published {
			return readOfflineVersion(ctx)
		}
		return version, nil
	}
	c.publish = func() error {
		if published {
			return nil
		}
		if e := copyAssets(); e != nil {
			return e
		}
		// Prepare every file before publishing. Nothing is executed from the legacy
		// path, and no write follows it. The source root handle survives parent swaps.
		all := append([]string{"Masir"}, names...)
		var pending []string
		defer func() {
			for _, p := range pending {
				os.Remove(p)
			}
		}()
		for _, name := range all {
			in, e := os.Open(filepath.Join(snapshot, name))
			if os.IsNotExist(e) && name != "Masir" && name != "geoip.dat" && name != "geosite.dat" {
				continue
			}
			if e != nil {
				return e
			}
			tmp, e := os.CreateTemp(target, ".migration-")
			if e != nil {
				in.Close()
				return e
			}
			pending = append(pending, tmp.Name())
			mode := os.FileMode(0644)
			if name == "Masir" {
				mode = 0755
			}
			_, copyErr := io.Copy(tmp, in)
			e = errors.Join(copyErr, in.Close(), tmp.Chmod(mode), tmp.Sync(), tmp.Close())
			if e != nil {
				return e
			}
		}
		next := 0
		for _, name := range all {
			if _, e := os.Stat(filepath.Join(snapshot, name)); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return e
			}
			if e := os.Rename(pending[next], filepath.Join(target, name)); e != nil {
				return fmt.Errorf("publish protected legacy %s: %w", name, e)
			}
			next++
		}
		published = true
		return nil
	}
	return c, nil
}

// A directory handle confines all reads despite a replaceable /usr/local parent.
// Reject symlinks, hard links, devices and oversized files before any elevated copy.
func openLegacyDarwinFile(root *os.File, name string, limit int64) (*os.File, error) {
	if name == "." || name == ".." || filepath.Base(name) != name {
		return nil, errors.New("legacy component must be a fixed direct child")
	}
	fd, e := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: e}
	}
	f := os.NewFile(uintptr(fd), name)
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || stat.Nlink != 1 || st.Size() <= 0 || st.Size() > limit {
		f.Close()
		return nil, errors.New("legacy component is not a bounded single-link regular file")
	}
	return f, nil
}

func openLegacyDarwinDirectory(path string) (*os.File, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: e}
	}
	return os.NewFile(uintptr(fd), path), nil
}
