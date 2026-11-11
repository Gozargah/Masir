package main

import (
	"os"
	"path/filepath"
	"strconv"
)

func importLegacyDesktop(dir string) error {
	return runAdmin(filepath.Join(dir, "masir"), "daemon", "import-desktop", "--from", dir, "--user", strconv.Itoa(os.Getuid()))
}
