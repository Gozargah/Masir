package update

import (
	"aead.dev/minisign"
	"encoding/json"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDesktopDescriptorAuthorizesOnlyOurReleaseComponents(t *testing.T) {
	pub, key := newKey(t)
	trust(t, pub)
	dir := t.TempDir()
	for _, name := range updatebundle.Names(runtime.GOOS) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := updatebundle.Create(dir, "0.2.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDesktopDescriptor(data, minisign.Sign(key, data), "0.1.0"); err != nil {
		t.Fatal(err)
	}
	_, other := newKey(t)
	for _, kind := range []string{"other key", "altered metadata", "downgrade", "wrong role", "wrong platform", "missing file", "oversized file", "unsigned development"} {
		t.Run(kind, func(t *testing.T) {
			var d updatebundle.Descriptor
			json.Unmarshal(data, &d)
			current := "0.1.0"
			signer := key
			switch kind {
			case "other key":
				signer = other
			case "downgrade":
				current = "0.3.0"
			case "wrong role":
				d.Role = "other-product"
			case "wrong platform":
				d.Arch = "other"
			case "missing file":
				delete(d.Files, "geoip.dat")
			case "oversized file":
				f := d.Files["geoip.dat"]
				f.Size = updatebundle.MaxFileSize + 1
				d.Files["geoip.dat"] = f
			}
			b, _ := json.Marshal(d)
			sig := minisign.Sign(signer, b)
			if kind == "altered metadata" {
				b = append(b, ' ')
			}
			if kind == "unsigned development" {
				trust(t)
				t.Setenv("MASIR_UPDATE_ALLOW_UNSIGNED", "1")
			}
			if _, err := VerifyDesktopDescriptor(b, sig, current); err == nil {
				t.Fatal("accepted unauthorized components")
			}
		})
	}
}
