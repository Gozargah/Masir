package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/Gozargah/Masir/libs/masir-core/fileops"
	"io"
	"os"
	"path/filepath"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/service"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
)

// Older Windows installers used the elevated account as owner. Its UAC limited
// token can change that directory's DACL. Authenticate the legacy executable by
// publisher-pinned bytes, never by executing it, then publish a fresh protected
// directory. Re-ACLing the original would leave existing writable handles alive.
func snapshotLegacyWindows(dir *os.File, parent string) (control offlineServiceControl, err error) {
	snapshot, err := geopack.NewStaging(parent)
	if err != nil {
		return control, err
	}
	published := false
	defer func() {
		if err != nil {
			os.RemoveAll(snapshot)
		}
	}()
	copyOne := func(name string, limit int64, optional bool) error {
		in, e := offlineWindowsOpen(filepath.Join(dir.Name(), name), false)
		if optional && os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		defer in.Close()
		return offlineSnapshotFile(in, filepath.Join(snapshot, name), limit, false)
	}
	if err = copyOne("masir.exe", 256<<20, false); err != nil {
		return control, err
	}
	bin, err := os.Open(filepath.Join(snapshot, "masir.exe"))
	if err != nil {
		return control, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, bin)
	bin.Close()
	if err != nil {
		return control, err
	}
	version := updatebundle.LegacyWindowsVersion(hex.EncodeToString(hash.Sum(nil)))
	if version == "" {
		return control, errors.New("legacy service is user-owned and is not an authenticated supported release; reinstall the service")
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		if err = copyOne(name, geopack.MaxFileSize, false); err != nil {
			return control, err
		}
	}
	if err = copyOne(geopack.InstalledName, 16384, true); err != nil {
		return control, err
	}
	// Missing provenance means custom data. A malformed record must fail closed.
	if _, err = geopack.Managed(snapshot); err != nil {
		return control, err
	}
	control.snapshot = snapshot
	control.version = func(ctx context.Context) (string, error) {
		if published {
			return readOfflineVersion(ctx)
		}
		return version, nil
	}
	control.close = func() { dir.Close(); os.RemoveAll(snapshot) }
	control.publish = func() error {
		if published {
			return nil
		}
		// Re-snapshot assets after the supervisor has exited, before exposing them.
		for _, name := range []string{"geoip.dat", "geosite.dat", geopack.InstalledName} {
			if e := os.Remove(filepath.Join(snapshot, name)); e != nil && !os.IsNotExist(e) {
				return e
			}
			limit := geopack.MaxFileSize
			if name == geopack.InstalledName {
				limit = 16384
			}
			if e := copyOne(name, limit, name == geopack.InstalledName); e != nil {
				return e
			}
		}
		fresh := snapshot + ".installed"
		if e := geopack.EnsureInstalledDirectory(fresh); e != nil {
			return e
		}
		defer os.RemoveAll(fresh)
		for _, name := range []string{"masir.exe", "geoip.dat", "geosite.dat", geopack.InstalledName} {
			in, e := os.Open(filepath.Join(snapshot, name))
			if os.IsNotExist(e) && name == geopack.InstalledName {
				continue
			}
			if e != nil {
				return e
			}
			e = offlineSnapshotFile(in, filepath.Join(fresh, name), 256<<20, true)
			in.Close()
			if e != nil {
				return e
			}
		}
		// The old tree may contain hostile reparse points. Quarantine it without ever
		// traversing/deleting it as administrator. Program Files controls both names.
		dir.Close()
		quarantine := snapshot + ".legacy"
		if e := fileops.Rename(service.InstallDir(), quarantine); e != nil {
			return e
		}
		if e := fileops.Rename(fresh, service.InstallDir()); e != nil {
			// Do not restart the unsafe tree. A retry can authenticate it again after an
			// administrator restores its name; the error names the recovery directory.
			return fmt.Errorf("publish protected service (legacy files at %s): %w", quarantine, e)
		}
		published = true
		return nil
	}
	return control, nil
}
func offlineSnapshotFile(in *os.File, path string, limit int64, installed bool) error {
	var out *os.File
	var e error
	if installed {
		out, e = geopack.CreateInstalledFile(path, 0755)
	} else {
		out, e = geopack.CreateStagedFile(path, 0755)
	}
	if e != nil {
		return e
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, limit+1))
	if copyErr == nil && (n == 0 || n > limit) {
		copyErr = errors.New("invalid legacy component size")
	}
	return errors.Join(copyErr, out.Close())
}
