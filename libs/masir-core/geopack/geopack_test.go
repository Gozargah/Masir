package geopack

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	source, packed := t.TempDir(), t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(source, name), bytes.Repeat([]byte(name), 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Pack(source, packed); err != nil {
		t.Fatal(err)
	}
	return source, packed
}

func TestRawAndPackedResourcesHaveIdenticalBytes(t *testing.T) {
	source, packed := fixture(t)
	for _, dir := range []string{source, packed} {
		for _, name := range names {
			f, err := Open(dir, name)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			want, _ := os.ReadFile(filepath.Join(source, name))
			if !bytes.Equal(got, want) {
				t.Fatal("expanded bytes differ")
			}
		}
	}
	second := t.TempDir()
	if err := Pack(source, second); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ManifestName, "geoip.dat.gz", "geosite.dat.gz"} {
		a, _ := os.ReadFile(filepath.Join(packed, name))
		b, _ := os.ReadFile(filepath.Join(second, name))
		if !bytes.Equal(a, b) {
			t.Fatal("non-deterministic pack:", name)
		}
	}
}

func TestCorruptPackNeverPublishesCache(t *testing.T) {
	for _, kind := range []string{"truncated", "checksum", "hash", "oversize", "missing metadata", "missing second file"} {
		t.Run(kind, func(t *testing.T) {
			_, packed := fixture(t)
			path := filepath.Join(packed, "geosite.dat.gz")
			switch kind {
			case "truncated", "checksum":
				b, _ := os.ReadFile(path)
				if kind == "truncated" {
					b = b[:len(b)-5]
				} else {
					b[len(b)-8] ^= 1
				}
				os.WriteFile(path, b, 0o644)
			case "missing metadata":
				os.Remove(filepath.Join(packed, ManifestName))
			case "missing second file":
				os.Remove(path)
			default:
				m, _, err := readManifest(packed)
				if err != nil {
					t.Fatal(err)
				}
				f := m.Files["geosite.dat"]
				if kind == "hash" {
					f.SHA256 = string(bytes.Repeat([]byte("0"), 64))
				} else {
					f.Size = 1
				}
				m.Files["geosite.dat"] = f
				b, _ := json.Marshal(m)
				os.WriteFile(filepath.Join(packed, ManifestName), b, 0o644)
			}
			cache := t.TempDir()
			if _, err := Cached(packed, cache); err == nil {
				t.Fatal("accepted corrupt pack")
			}
			entries, _ := os.ReadDir(cache)
			if len(entries) != 0 {
				t.Fatal("published partial cache")
			}
		})
	}
}

func TestCacheReusesPairAndPreservesCustomDataAcrossUpdates(t *testing.T) {
	source, packed := fixture(t)
	base := t.TempDir()
	dir, err := Cached(packed, base)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "geoip.dat")
	before, _ := os.Stat(path)
	again, err := Cached(packed, base)
	if err != nil || again != dir {
		t.Fatalf("reuse: %s %v", again, err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) {
		t.Fatal("unnecessarily expanded cache again")
	}
	os.WriteFile(path, []byte("broken"), 0o644)
	if _, err := Cached(packed, base); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want, _ := os.ReadFile(filepath.Join(source, "geoip.dat"))
	if string(got) != "broken" {
		t.Fatal("overwrote a modified cache")
	}
	os.WriteFile(filepath.Join(source, "geoip.dat"), []byte("new release"), 0o644)
	newPack := t.TempDir()
	if err := Pack(source, newPack); err != nil {
		t.Fatal(err)
	}
	newDir, err := Cached(newPack, base)
	if err != nil || newDir != dir {
		t.Fatalf("new release cache: %s %v", newDir, err)
	}
	// Legacy caches without the active pointer also keep manual replacements.
	os.Remove(filepath.Join(base, activeCacheName))
	if legacy, err := Cached(newPack, base); err != nil || legacy != dir {
		t.Fatalf("legacy custom cache: %s %v", legacy, err)
	}
	// Restoring the original pair permits managed updates again.
	os.WriteFile(path, want, 0o644)
	if next, err := Cached(newPack, base); err != nil || next == dir {
		t.Fatalf("managed cache did not update: %s %v", next, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removed previous release cache")
	}
}
