package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
)

// installReleaseGeo keeps the previous pair for `masir daemon rollback`. Prepare
// every backup before replacing either file. The returned rollback also handles
// first installs, where there was no previous data.
func installReleaseGeo(staged, assets string, preserve ...bool) (func() error, error) {
	if err := os.MkdirAll(assets, 0o755); err != nil {
		return nil, err
	}
	keep := len(preserve) > 0 && preserve[0]
	if !keep {
		if err := geopack.Record(staged); err != nil {
			return nil, err
		}
	}
	names := []string{"geoip.dat", "geosite.dat", geopack.InstalledName}
	existed := map[string]bool{}
	// Keep the older rollback pair intact until this update has succeeded. Failed
	// attempts must not pair an older binary backup with newer geo backups.
	backupDir := filepath.Join(staged, "previous-geo")
	if err := os.RemoveAll(backupDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return nil, err
	}
	previous := map[string]bool{}
	for _, name := range names {
		dst := filepath.Join(assets, name)
		for _, suffix := range []string{"", ".previous"} {
			if _, err := os.Stat(dst + suffix); err == nil {
				if suffix == "" {
					existed[name] = true
				} else {
					previous[name] = true
				}
				if err := copyFile(dst+suffix, filepath.Join(backupDir, name+suffix), 0o644); err != nil {
					return nil, err
				}
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
	var changed []string
	rollback := func() error {
		var result error
		for _, name := range changed {
			dst := filepath.Join(assets, name)
			var err error
			if existed[name] {
				err = replaceGeoFile(filepath.Join(backupDir, name), dst)
			} else {
				err = os.Remove(dst)
			}
			if err != nil && !os.IsNotExist(err) {
				result = errors.Join(result, fmt.Errorf("restore %s: %w", name, err))
			}
		}
		for _, name := range names {
			dst := filepath.Join(assets, name) + ".previous"
			var err error
			if previous[name] {
				err = replaceGeoFile(filepath.Join(backupDir, name+".previous"), dst)
			} else {
				err = os.Remove(dst)
			}
			if err != nil && !os.IsNotExist(err) {
				result = errors.Join(result, err)
			}
		}
		return result
	}
	for _, name := range names {
		dst := filepath.Join(assets, name) + ".previous"
		var err error
		if existed[name] {
			err = replaceGeoFile(filepath.Join(backupDir, name), dst)
		} else {
			err = os.Remove(dst)
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.Join(err, rollback())
		}
	}
	if keep {
		return rollback, nil
	}
	for _, name := range names {
		// Include failed replacements: Windows may have removed the destination before
		// the final rename failed.
		changed = append(changed, name)
		if err := replaceGeoFile(filepath.Join(staged, name), filepath.Join(assets, name)); err != nil {
			return nil, errors.Join(fmt.Errorf("install %s: %w", name, err), rollback())
		}
	}
	return rollback, nil
}

func replaceGeoFile(src, dst string) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp, 0o644); err != nil {
		return err
	}
	defer os.Remove(tmp)
	want, err := fileSHA256(src)
	if err != nil {
		return err
	}
	got, err := fileSHA256(tmp)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("installed geo data doesn't match the verified release")
	}
	preserveOwner(tmp, dst)
	return renameGeoFile(tmp, dst)
}
