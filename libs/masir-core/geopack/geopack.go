// Package geopack stores the desktop's geo resources compressed, while presenting
// the original verified .dat bytes to Xray, service installation and signed updates.
package geopack

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const ManifestName = "geo.json"
const CacheMarker = "bundled-geo.json"
const MaxFileSize int64 = 256 << 20

var names = [...]string{"geoip.dat", "geosite.dat"}
var cacheMu sync.Mutex

// NewStaging creates a private directory beneath a caller-controlled parent.
// On elevated Windows runs its owner and DACL exclude the UAC limited token.
func NewStaging(base string) (string, error) { return makeStage(base) }

// CreateStagedFile exclusively creates a file in a private staging directory, with
// Administrators ownership and a protected DACL on elevated Windows processes.
func CreateStagedFile(path string, mode os.FileMode) (*os.File, error) {
	return createResource(path, mode)
}

// CreateInstalledFile creates public installed binaries/assets. Elevated Windows
// writers use Administrators ownership and allow ordinary users read/execute only.
func CreateInstalledFile(path string, mode os.FileMode) (*os.File, error) {
	return createInstalledResource(path, mode)
}

// EnsureInstalledDirectory creates/protects a service directory before installation.
func EnsureInstalledDirectory(path string) error { return ensureInstalledDirectory(path) }

type fileInfo struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Version int                 `json:"version"`
	Files   map[string]fileInfo `json:"files"`
}

// Packed recognizes even an incomplete pack, so missing metadata cannot silently
// fall back to stale uncompressed resources left by an earlier build.
func Packed(dir string) bool {
	if _, err := os.Lstat(filepath.Join(dir, ManifestName)); !os.IsNotExist(err) {
		return true
	}
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dir, name+".gz")); !os.IsNotExist(err) {
			return true
		}
	}
	return false
}

// Pack writes a deterministic compressed pair and metadata into an empty build
// directory. Its caller publishes the resources only after this succeeds.
func Pack(source, dest string) error {
	m := manifest{Version: 1, Files: map[string]fileInfo{}}
	for _, name := range names {
		in, err := os.Open(filepath.Join(source, name))
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dest, name+".gz"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			in.Close()
			return err
		}
		z, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(z, h), io.LimitReader(in, MaxFileSize+1))
		err = errors.Join(copyErr, in.Close(), z.Close(), out.Close())
		if err != nil {
			return err
		}
		if n <= 0 || n > MaxFileSize {
			return fmt.Errorf("invalid geo size: %s", name)
		}
		m.Files[name] = fileInfo{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, ManifestName), data, 0o644)
}

func readManifest(dir string) (manifest, []byte, error) {
	return readRecord(dir, ManifestName)
}

func readRecord(dir, name string) (manifest, []byte, error) {
	var m manifest
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return m, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil {
		return m, nil, err
	}
	if len(data) > 16384 {
		return m, nil, errors.New("oversized geo metadata")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, nil, err
	}
	if m.Version != 1 || len(m.Files) != len(names) {
		return m, nil, errors.New("invalid geo metadata")
	}
	for _, name := range names {
		f := m.Files[name]
		h, err := hex.DecodeString(f.SHA256)
		if f.Size <= 0 || f.Size > MaxFileSize || err != nil || len(h) != sha256.Size {
			return m, nil, fmt.Errorf("invalid geo metadata: %s", name)
		}
	}
	return m, data, nil
}

// Open supports old raw resources and the new packed format. Packed readers verify
// expanded size, SHA-256 and gzip integrity at EOF. Callers must read to EOF and
// discard staged output on any error before publishing or using it.
func Open(dir, name string) (io.ReadCloser, error) {
	if name != names[0] && name != names[1] {
		return nil, errors.New("unknown geo resource")
	}
	if !Packed(dir) {
		return os.Open(filepath.Join(dir, name))
	}
	m, _, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, name+".gz"))
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxFileSize {
		f.Close()
		return nil, fmt.Errorf("invalid compressed geo resource: %s", name)
	}
	z, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	want := m.Files[name]
	return &verifiedReader{reader: io.LimitReader(z, want.Size+1), gzip: z, file: f, hash: sha256.New(), want: want}, nil
}

type verifiedReader struct {
	reader io.Reader
	gzip   *gzip.Reader
	file   *os.File
	hash   hash.Hash
	want   fileInfo
	size   int64
}

func (r *verifiedReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.hash.Write(p[:n])
	r.size += int64(n)
	if r.size > r.want.Size {
		return n, errors.New("geo resource exceeds its declared size")
	}
	if err == io.EOF && (r.size != r.want.Size || hex.EncodeToString(r.hash.Sum(nil)) != r.want.SHA256) {
		return n, errors.New("geo resource hash or size mismatch")
	}
	return n, err
}

func (r *verifiedReader) Close() error { return errors.Join(r.gzip.Close(), r.file.Close()) }

// Expand prepares both files in a private, empty directory. The caller owns cleanup
// and must not expose the directory if either extraction fails.
func Expand(source, dest string) error {
	return copyPair(source, dest, false)
}

// CopyRawPair snapshots active custom data without interpreting packaging sidecars.
// The destination must be private and empty; failures must never be published.
func CopyRawPair(source, dest string) error {
	return copyPair(source, dest, true)
}

func copyPair(source, dest string, raw bool) error {
	for _, name := range names {
		var in io.ReadCloser
		var err error
		if raw {
			in, err = os.Open(filepath.Join(source, name))
		} else {
			in, err = Open(source, name)
		}
		if err != nil {
			return err
		}
		out, err := createResource(filepath.Join(dest, name), 0o644)
		if err != nil {
			in.Close()
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, MaxFileSize+1))
		if copyErr == nil && (n == 0 || n > MaxFileSize) {
			copyErr = fmt.Errorf("invalid geo size: %s", name)
		}
		if err := errors.Join(copyErr, in.Close(), out.Close()); err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
	}
	return nil
}

// Cached expands once per packed pair, outside the app bundle. Every reuse checks
// the complete cached bytes. Publish only a fully verified pair, and leave no partial
// cache after failure. base must be private to the daemon's process identity.
func Cached(source, base string) (string, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	m, data, err := readManifest(source)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	dest := filepath.Join(base, hex.EncodeToString(sum[:]))
	if custom, err := customCache(base); err != nil {
		return "", err
	} else if custom != "" {
		return custom, rememberCache(base, custom)
	}
	valid := func(dir string) bool {
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return false
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return false
		}
		defer root.Close()
		for _, name := range names {
			fi, err := root.Lstat(name)
			if err != nil || !fi.Mode().IsRegular() || fi.Size() != m.Files[name].Size {
				return false
			}
			f, err := root.Open(name)
			if err != nil {
				return false
			}
			h := sha256.New()
			n, err := io.Copy(h, io.LimitReader(f, m.Files[name].Size+1))
			f.Close()
			if err != nil || n != m.Files[name].Size || hex.EncodeToString(h.Sum(nil)) != m.Files[name].SHA256 {
				return false
			}
		}
		f, err := root.Open(CacheMarker)
		if err != nil {
			return false
		}
		defer f.Close()
		marker, err := io.ReadAll(io.LimitReader(f, int64(len(data))+1))
		return err == nil && string(marker) == string(data)
	}
	if valid(dest) {
		return dest, rememberCache(base, dest)
	}
	stage, err := makeStage(base)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := Expand(source, stage); err != nil {
		return "", err
	}
	marker, err := createResource(filepath.Join(stage, CacheMarker), 0o600)
	if err != nil {
		return "", err
	}
	_, writeErr := marker.Write(data)
	if err := errors.Join(writeErr, marker.Close()); err != nil {
		return "", err
	}
	if !valid(stage) {
		return "", errors.New("bundled geo resources changed during extraction")
	}
	if valid(dest) { // another starter finished while this one unpacked
		return dest, nil
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(stage, dest); err != nil {
		if valid(dest) {
			return dest, nil
		}
		return "", err
	}
	return dest, rememberCache(base, dest)
}
