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
	for _, command := range []string{"alias", "/app/bin/alias"} {
		d.Exec = "env FOO=bar flatpak run --command=" + command + " test.App"
		if got, err := d.Value(); err != nil || got != "program" {
			t.Fatalf("resolved %q to %q, %v, want program", command, got, err)
		}
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

// Shapes seen on a real Fedora host: Spotify's /app/bin entry is a bash wrapper
// that spawns the real binary from /app/extra (its /proc exe is confirmed as
// .../spotify), and Game2048's entry is a symlink to an absolute sandbox path.
// Both name a real program; a script that only starts an interpreter does not.
func TestFlatpakWrapperAndSandboxSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux symlinks and executables")
	}
	root := t.TempDir()
	desktop := filepath.Join(root, "export/share/applications/wrapped.desktop")
	writeFixture(t, desktop, "[Desktop Entry]\nExec=/usr/bin/flatpak run --command=wrapped wrap.App\n", 0o644)
	exported := filepath.Join(root, "wrapped.desktop")
	if err := os.Symlink(desktop, exported); err != nil {
		t.Fatal(err)
	}
	d, err := FromDesktop(exported)
	if err != nil {
		t.Fatal(err)
	}

	// Wrapper script: a library is named first, the real program second.
	writeFixture(t, filepath.Join(root, "files/bin/wrapped"),
		"#!/bin/bash\nLD_PRELOAD=/app/lib/preload.so\nSPAWN=/app/extra/bin/real \"$@\" &\n", 0o755)
	writeFixture(t, filepath.Join(root, "files/lib/preload.so"), "not an executable", 0o644)
	if err := os.MkdirAll(filepath.Join(root, "files/extra/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/app/extra/share/real/real", filepath.Join(root, "files/extra/bin/real")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, "files/extra/share/real/real"), "\x7fELF", 0o755)
	if got, err := d.Value(); err != nil || got != "real" {
		t.Fatalf("wrapper resolved to %q, %v; want real", got, err)
	}

	// A script that only starts an interpreter keeps no nameable identity.
	writeFixture(t, filepath.Join(root, "files/bin/wrapped"),
		"#!/usr/bin/python3\nDATA = '/app/share/app'\n", 0o755)
	if _, err := d.Value(); !errors.Is(err, ErrUnresolvable) {
		t.Fatalf("interpreter wrapper should be unresolvable, got %v", err)
	}

	// An absolute sandbox symlink is followed inside the deployment.
	if err := os.Remove(filepath.Join(root, "files/bin/wrapped")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/app/share/Game2048/Game2048", filepath.Join(root, "files/bin/wrapped")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, "files/share/Game2048/Game2048"), "\x7fELF", 0o755)
	if got, err := d.Value(); err != nil || got != "Game2048" {
		t.Fatalf("sandbox symlink resolved to %q, %v; want Game2048", got, err)
	}
}
