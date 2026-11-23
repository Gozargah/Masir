package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstalledDesktopAppsMasksHiddenOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux desktop entries")
	}
	binary, err := exec.LookPath("ls")
	if err != nil {
		t.Fatal(err)
	}
	local, system := t.TempDir(), t.TempDir()
	write := func(dir, file, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte("[Desktop Entry]\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(system, "foo.desktop", "Name=System\nExec="+binary+"\n")
	if got := installedDesktopApps([]string{local, system}); len(got) != 1 {
		t.Fatalf("expected the system app, got %v", got)
	}
	write(local, "foo.desktop", "Hidden=true\n")
	if got := installedDesktopApps([]string{local, system}); len(got) != 0 {
		t.Fatalf("hidden desktop ID reappeared: %v", got)
	}
}
