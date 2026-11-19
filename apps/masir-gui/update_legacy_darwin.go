package main

import (
	"os"
	"path/filepath"
	"strconv"
)

func importLegacyDesktop(dir string, authorized func()) error {
	return runAdmin(authorized, filepath.Join(dir, "masir"), "daemon", "import-desktop", "--from", dir, "--user", strconv.Itoa(os.Getuid()))
}
