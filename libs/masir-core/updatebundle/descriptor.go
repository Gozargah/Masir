// Package updatebundle describes release-authorized service files inside a desktop payload.
package updatebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
)

const DescriptorName = "service-update.json"
const SignatureName = "service-update.json.minisig"
const Role = "masir-service-update-v1"
const MaxFileSize int64 = 256 << 20

type File struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Descriptor struct {
	Role    string          `json:"role"`
	Version string          `json:"version"`
	OS      string          `json:"os"`
	Arch    string          `json:"arch"`
	Files   map[string]File `json:"files"`
}

func Names(goos string) []string {
	binary := "masir"
	if goos == "windows" {
		binary += ".exe"
	}
	return []string{binary, "geoip.dat", "geosite.dat"}
}
func Create(dir, version, goos, arch string) ([]byte, error) {
	d := Descriptor{Role: Role, Version: version, OS: goos, Arch: arch, Files: map[string]File{}}
	for _, name := range Names(goos) {
		f, err := Open(dir, name)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, MaxFileSize+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if n == 0 || n > MaxFileSize {
			return nil, fmt.Errorf("invalid component size: %s", name)
		}
		d.Files[name] = File{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	return json.Marshal(d)
}

// Open returns the original component bytes, regardless of desktop storage format.
// The signed descriptor and the service's import protocol continue to describe the
// expanded .dat files, so existing services independently verify the same hashes.
func Open(dir, name string) (io.ReadCloser, error) {
	if name == "geoip.dat" || name == "geosite.dat" {
		return geopack.Open(dir, name)
	}
	if name != "masir" && name != "masir.exe" {
		return nil, fmt.Errorf("invalid component name: %s", name)
	}
	return os.Open(filepath.Join(dir, name))
}
