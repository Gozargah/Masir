package main

import (
	"fmt"
	"os/exec"
	"os/user"
	"path/filepath"
	"syscall"
)

func importLegacyDesktop(dir string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(dir, "masir.exe"), "daemon", "import-desktop", "--from", dir, "--user", u.Uid)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("offline service upgrade: %s: %w", out, err)
	}
	return nil
}
