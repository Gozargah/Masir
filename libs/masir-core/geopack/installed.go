package geopack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const InstalledName = "installed-geo.json"
const activeCacheName = "active-geo"

// Managed compares with the last pair installed by Masir, never the next release.
// Legacy data without provenance, and a partially missing pair, are user-owned.
func Managed(dir string) (bool, error) {
	_, a := os.Lstat(filepath.Join(dir, names[0]))
	_, b := os.Lstat(filepath.Join(dir, names[1]))
	m, _, err := readRecord(dir, InstalledName)
	if err != nil {
		if os.IsNotExist(err) {
			return os.IsNotExist(a) && os.IsNotExist(b), nil
		}
		return false, err
	}
	return matchesPair(dir, m), nil
}

func matchesPair(dir string, m manifest) bool {
	for _, name := range names {
		want := m.Files[name]
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() != want.Size {
			return false
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return false
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, want.Size+1))
		f.Close()
		if err != nil || n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
			return false
		}
	}
	return true
}

// Record is called only after Masir installs a complete, verified pair.
func Record(dir string) error {
	m := manifest{Version: 1, Files: map[string]fileInfo{}}
	for _, name := range names {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, MaxFileSize+1))
		f.Close()
		if err != nil {
			return err
		}
		if n <= 0 || n > MaxFileSize {
			return fmt.Errorf("invalid geo size: %s", name)
		}
		m.Files[name] = fileInfo{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeRecord(dir, InstalledName, b)
}

func writeRecord(dir, name string, data []byte) error {
	stage, err := makeStage(dir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	f, err := createResource(filepath.Join(stage, name), 0o600)
	if err != nil {
		return err
	}
	_, e := f.Write(data)
	if err := errors.Join(e, f.Close()); err != nil {
		return err
	}
	return os.Rename(filepath.Join(stage, name), filepath.Join(dir, name))
}

func rememberCache(base, dir string) error {
	return writeRecord(base, activeCacheName, []byte(filepath.Base(dir)))
}

// Keep the previously selected pair across bundle versions if it was changed.
// Scan legacy caches only when no pointer exists, preserving pre-migration edits.
func customCache(base string) (string, error) {
	var candidates []string
	f, err := os.Open(filepath.Join(base, activeCacheName))
	if err == nil {
		b, e := io.ReadAll(io.LimitReader(f, 65))
		f.Close()
		if e != nil {
			return "", e
		}
		s := string(b)
		decoded, e := hex.DecodeString(s)
		if e != nil || len(s) != 64 || len(decoded) != 32 {
			return "", errors.New("invalid active geo cache")
		}
		candidates = []string{s}
	} else if !os.IsNotExist(err) {
		return "", err
	} else {
		entries, e := os.ReadDir(base)
		if e != nil {
			return "", e
		}
		for _, entry := range entries {
			s := entry.Name()
			if _, e := hex.DecodeString(s); e == nil && len(s) == 64 {
				candidates = append(candidates, s)
			}
		}
	}
	custom := ""
	for _, name := range candidates {
		dir := filepath.Join(base, name)
		fi, e := os.Lstat(dir)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", e
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("invalid geo cache directory")
		}
		m, _, e := readRecord(dir, CacheMarker)
		if e == nil && matchesPair(dir, m) {
			continue
		}
		if custom != "" {
			return "", errors.New("multiple modified geo caches; cannot safely choose a pair")
		}
		custom = dir
	}
	return custom, nil
}
