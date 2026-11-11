package server

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsLegacySnapshotRejectsUntrustedExecutable(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("elevated Windows fixture")
	}
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "masir.exe"), []byte("not a publisher-authenticated executable"), 0755); e != nil {
		t.Fatal(e)
	}
	d, e := offlineWindowsOpen(dir, true)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	if _, e := snapshotLegacyWindows(d, t.TempDir()); e == nil || !strings.Contains(e.Error(), "not an authenticated") {
		t.Fatalf("untrusted executable accepted: %v", e)
	}
}
func TestWindowsOfflineSnapshotRejectsLinks(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if e := os.WriteFile(source, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(dir, "hardlink")
	if e := os.Link(source, link); e != nil {
		t.Fatal(e)
	}
	if f, e := offlineWindowsOpen(link, false); e == nil {
		f.Close()
		t.Fatal("accepted multiply-linked resource")
	}
	os.Remove(link)
	symlink := filepath.Join(dir, "symlink")
	if e := os.Symlink(source, symlink); e != nil {
		t.Skipf("symlinks unavailable: %v", e)
	}
	if f, e := offlineWindowsOpen(symlink, false); e == nil {
		f.Close()
		t.Fatal("followed reparse point")
	}
}

// Reproduce the original directory sharing policy and verify the replacement
// policy permits child renames while continuing to pin the directory's own name.
func TestWindowsPinnedDirectoryAllowsVerifiedFileReplacement(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "service")
	if e := os.Mkdir(dir, 0755); e != nil {
		t.Fatal(e)
	}
	source, target := filepath.Join(dir, "verified.new"), filepath.Join(dir, "installed")
	if e := os.WriteFile(source, []byte("verified"), 0600); e != nil {
		t.Fatal(e)
	}
	p, e := windows.UTF16PtrFromString(dir)
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	oldError := os.Rename(source, target)
	windows.CloseHandle(h)
	if !errors.Is(oldError, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("old directory policy did not reproduce sharing conflict: %v", oldError)
	}
	d, e := offlineWindowsOpen(dir, true)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	if e := os.Rename(source, target); e != nil {
		t.Fatalf("our directory pin blocks child replacement: %v", e)
	}
	if e := os.Rename(dir, dir+".swapped"); !errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("directory pin failed to protect its name: %v", e)
	}
	got, e := os.ReadFile(target)
	if e != nil || string(got) != "verified" {
		t.Fatalf("replacement bytes changed: %v", e)
	}
}
