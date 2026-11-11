package server

import (
	"bytes"
	"context"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyDarwinSnapshotsRefuseLinksDevicesAndOversize(t *testing.T) {
	dir := t.TempDir()
	root, e := openLegacyDarwinDirectory(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := os.WriteFile(filepath.Join(dir, "raw"), []byte("geo"), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := openLegacyDarwinFile(root, "raw", 3)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	if e := os.Symlink("raw", filepath.Join(dir, "symlink")); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name  string
		limit int64
	}{{"raw", 2}, {"symlink", 3}} {
		if f, e := openLegacyDarwinFile(root, tc.name, tc.limit); e == nil {
			f.Close()
			t.Fatalf("accepted %s", tc.name)
		}
	}
	if e := os.Link(filepath.Join(dir, "raw"), filepath.Join(dir, "hardlink")); e != nil {
		t.Fatal(e)
	}
	if f, e := openLegacyDarwinFile(root, "hardlink", 3); e == nil {
		f.Close()
		t.Fatal("accepted multiply-linked file")
	}
	if f, e := openLegacyDarwinFile(root, ".", 3); e == nil {
		f.Close()
		t.Fatal("accepted directory")
	}
}
func TestLegacyDarwinSnapshotRejectsUnknownExecutable(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if e := os.WriteFile(filepath.Join(source, "Masir"), []byte("untrusted executable"), 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := snapshotLegacyDarwin(source, target); e == nil {
		t.Fatal("accepted an unrecognized executable")
	}
	entries, e := os.ReadDir(target)
	if e != nil || len(entries) != 0 {
		t.Fatalf("failed verification left a snapshot: %v %v", entries, e)
	}
}

func TestLegacyDarwinSnapshotPinsSourceDirectory(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source, "geoip.dat"), []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	root, e := openLegacyDarwinDirectory(source)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := os.Rename(source, source+".moved"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source, "geoip.dat"), []byte("substituted"), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := openLegacyDarwinFile(root, "geoip.dat", 100)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	b, e := io.ReadAll(f)
	if e != nil || string(b) != "original" {
		t.Fatalf("snapshot followed replaced parent: %q %v", b, e)
	}
	link := filepath.Join(parent, "link")
	if e := os.Symlink(source, link); e != nil {
		t.Fatal(e)
	}
	if d, e := openLegacyDarwinDirectory(link); e == nil {
		d.Close()
		t.Fatal("accepted symlinked legacy directory")
	}
}

func TestLegacyDarwinSnapshotPublishesVerifiedLocalFixture(t *testing.T) {
	fixture := os.Getenv("MASIR_DARWIN_LEGACY_FIXTURE")
	if fixture == "" {
		t.Skip("provide an authenticated legacy fixture directory")
	}
	source, target := t.TempDir(), t.TempDir()
	names := []string{"Masir", "geoip.dat", "geosite.dat", "installed-geo.json", "AppIcon.rsrc", "FinderInfo.bin"}
	before := map[string][]byte{}
	for _, name := range names {
		b, e := os.ReadFile(filepath.Join(fixture, name))
		if os.IsNotExist(e) && name != "Masir" && name != "geoip.dat" && name != "geosite.dat" {
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		before[name] = b
		if e := os.WriteFile(filepath.Join(source, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	c, e := snapshotLegacyDarwin(source, target)
	if e != nil {
		t.Fatal(e)
	}
	defer c.close()
	v, e := c.version(context.Background())
	if e != nil || v == "" {
		t.Fatalf("authenticated version unavailable: %q %v", v, e)
	}
	// Replace the source path after verification. Publishing must still read through
	// the original directory handle and must never execute this substituted tree.
	moved := source + ".original"
	if e := os.Rename(source, moved); e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(moved)
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source, "geoip.dat"), []byte("substituted"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := c.publish(); e != nil {
		t.Fatal(e)
	}
	if e := c.publish(); e != nil {
		t.Fatalf("repeat publication not idempotent: %v", e)
	}
	for name, want := range before {
		got, e := os.ReadFile(filepath.Join(target, name))
		if e != nil || !bytes.Equal(got, want) {
			t.Fatalf("published %s changed: %v", name, e)
		}
		original, e := os.ReadFile(filepath.Join(moved, name))
		if e != nil || !bytes.Equal(original, want) {
			t.Fatalf("source %s was modified: %v", name, e)
		}
	}
}

func TestLegacyDarwinSnapshotRejectsFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if e := unix.Mkfifo(filepath.Join(dir, "fifo"), 0600); e != nil {
		t.Fatal(e)
	}
	root, e := openLegacyDarwinDirectory(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	done := make(chan error, 1)
	go func() {
		f, e := openLegacyDarwinFile(root, "fifo", 100)
		if f != nil {
			f.Close()
		}
		done <- e
	}()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("accepted FIFO")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("legacy resource open blocked on FIFO")
	}
}
