//go:build !windows

package daemon

import (
	"os"
	"path/filepath"

	"github.com/Gozargah/Masir/libs/masir-core/paths"
)

func geoCacheDir(config string, privileged bool) (string, error) {
	if privileged {
		// Never put root's expanded resources in a user-controlled directory.
		if err := paths.EnsureRootDir(paths.PrivilegedRunDir, 0, 0o755); err != nil {
			return "", err
		}
		dir := filepath.Join(paths.PrivilegedRunDir, "geo")
		return dir, paths.EnsureRootDir(dir, 0, 0o700)
	}
	dir := filepath.Join(config, "geo-cache")
	return dir, os.MkdirAll(dir, 0o700)
}
