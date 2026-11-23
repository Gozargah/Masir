package appresolve

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeFixture(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestFlatpakDeploymentExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths and symlinks")
	}
	root := t.TempDir()
	desktop := filepath.Join(root, "export/share/applications/test.desktop")
	writeFixture(t, desktop, "[Desktop Entry]\nExec=env FOO=bar flatpak run --command=alias test.App\n", 0o644)
	writeFixture(t, filepath.Join(root, "files/bin/program"), "\x7fELF", 0o755)
	alias := filepath.Join(root, "files/bin/alias")
	if err := os.Symlink("program", alias); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(root, "test.desktop")
	if err := os.Symlink(desktop, exported); err != nil {
		t.Fatal(err)
	}
	d, err := FromDesktop(exported)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := d.Value(); err != nil || got != "/app/bin/program" {
		t.Fatalf("resolved %q, %v", got, err)
	}
	writeFixture(t, filepath.Join(root, "files/bin/program"), "#!/bin/sh\nexec /app/bin/heroic\n", 0o755)
	if _, err := d.Value(); !errors.Is(err, ErrUnresolvable) {
		t.Fatalf("wrapper should be unresolvable, got %v", err)
	}
}

func TestSnapDeploymentExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux executable permissions")
	}
	mount := t.TempDir()
	root := filepath.Join(mount, "brave", "current")
	writeFixture(t, filepath.Join(root, "meta/snap.yaml"), "apps:\n  brave:\n    command: opt/brave-browser --flag\n", 0o644)
	writeFixture(t, filepath.Join(root, "opt/brave-browser"), "\x7fELF", 0o755)
	if got, err := snapExecutable(mount, "brave"); err != nil || got != "brave-browser" {
		t.Fatalf("resolved %q, %v", got, err)
	}
}

func TestSnapLauncherDirectories(t *testing.T) {
	for _, path := range []string{"/snap/bin/viewer", "/var/lib/snapd/snap/bin/viewer"} {
		if !isSnapBinPath(path) {
			t.Errorf("%q should be recognized as a snap launcher", path)
		}
	}
	if isSnapBinPath("/opt/vendor/snap/bin/viewer") {
		t.Fatal("ordinary executable was classified as a snap launcher")
	}
}
