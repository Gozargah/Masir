package fileops

import (
	"bytes"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenameWaitsForWindowsSharingRelease(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "verified.new"), filepath.Join(dir, "installed")
	want := []byte("verified bytes")
	if e := os.WriteFile(source, want, 0600); e != nil {
		t.Fatal(e)
	}
	p, e := windows.UTF16PtrFromString(source)
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	// Confirm that this is a real sharing-conflict reproduction before testing retry.
	if e := os.Rename(source, target); !errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
		windows.CloseHandle(h)
		t.Fatalf("fixture did not block rename: %v", e)
	}
	released := make(chan struct{})
	go func() { time.Sleep(100 * time.Millisecond); windows.CloseHandle(h); close(released) }()
	e = Rename(source, target)
	<-released
	if e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(target)
	if e != nil || !bytes.Equal(got, want) {
		t.Fatalf("renamed bytes differ: %v", e)
	}
	if _, e := os.Stat(source); !os.IsNotExist(e) {
		t.Fatalf("source remains: %v", e)
	}
}
func TestRenameKeepsPermanentWindowsErrors(t *testing.T) {
	dir := t.TempDir()
	if e := Rename(filepath.Join(dir, "missing"), filepath.Join(dir, "target")); !os.IsNotExist(e) {
		t.Fatalf("lost permanent error: %v", e)
	}
}

func TestRenameTimesOutWithoutChangingLockedBytes(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "verified.new"), filepath.Join(dir, "installed")
	want := []byte("keep verified bytes")
	if e := os.WriteFile(source, want, 0600); e != nil {
		t.Fatal(e)
	}
	p, e := windows.UTF16PtrFromString(source)
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(h)
	if e := Rename(source, target); !errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("lost sharing timeout: %v", e)
	}
	got, e := os.ReadFile(source)
	if e != nil || !bytes.Equal(got, want) {
		t.Fatalf("failed rename changed source: %v", e)
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatalf("failed rename published target: %v", e)
	}
}
