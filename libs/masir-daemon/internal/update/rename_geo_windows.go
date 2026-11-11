package update

import (
	"github.com/Gozargah/Masir/libs/masir-core/fileops"
	"os"
)

func renameGeoFile(src, dst string) error {
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return fileops.Rename(src, dst)
}
