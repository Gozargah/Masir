//go:build !windows

package update

import "os"

func renameGeoFile(src, dst string) error { return os.Rename(src, dst) }
