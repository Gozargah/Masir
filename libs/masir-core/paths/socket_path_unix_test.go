//go:build !windows

package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureRootDir(t *testing.T) {
	me := os.Getuid()
	dir := filepath.Join(t.TempDir(), "run", "masir")
	if err := EnsureRootDir(dir, me, 0o755); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("created %v, %v; want mode 0755", fi, err)
	}
	if err := EnsureRootDir(dir, me, 0o755); err != nil {
		t.Fatalf("existing directory: %v", err)
	}
	if err := EnsureRootDir(dir, me+1, 0o755); err == nil {
		t.Error("accepted a directory owned by someone else")
	}
	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRootDir(dir, me, 0o755); err == nil {
		t.Error("accepted a group-writable directory")
	}
}

func TestEnsureRootDirRefusesASymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "masir")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRootDir(link, os.Getuid(), 0o755); err == nil {
		t.Error("accepted a symlink")
	}
}

func TestSocketsListPrivilegedFirst(t *testing.T) {
	socks, err := Sockets()
	if err != nil || len(socks) != 2 {
		t.Fatalf("Sockets = %v, %v", socks, err)
	}
	if socks[0] != PrivilegedSocketPath(os.Getuid()) || filepath.Base(socks[1]) != "daemon.sock" {
		t.Fatalf("Sockets = %v", socks)
	}
}

// A root process must only look at root's own sockets. It used to share os.Getuid()
// with the search while the daemon it started served SUDO_UID, so the daemon answered
// on a socket the client never checked and every command timed out.
func TestSocketsForUsesTheCallersUID(t *testing.T) {
	root, err := SocketsFor(0)
	if err != nil {
		t.Fatal(err)
	}
	if root[0] != PrivilegedSocketPath(0) {
		t.Fatalf("root's sockets = %v", root)
	}
	for _, s := range root {
		if s == PrivilegedSocketPath(1000) {
			t.Fatalf("root's sockets include uid 1000's: %v", root)
		}
	}
	user, err := SocketsFor(1000)
	if err != nil {
		t.Fatal(err)
	}
	if user[0] != PrivilegedSocketPath(1000) {
		t.Fatalf("uid 1000's sockets = %v", user)
	}
}
