package updatebundle

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
)

func TestCompressedComponentsKeepExistingSignedDescriptor(t *testing.T) {
	raw, packed := t.TempDir(), t.TempDir()
	for _, name := range Names(runtime.GOOS) {
		if err := os.WriteFile(filepath.Join(raw, name), []byte("component "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := geopack.Pack(raw, packed); err != nil {
		t.Fatal(err)
	}
	bin := Names(runtime.GOOS)[0]
	b, _ := os.ReadFile(filepath.Join(raw, bin))
	os.WriteFile(filepath.Join(packed, bin), b, 0o644)
	a, err := Create(raw, "1.0.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	b, err = Create(packed, "1.0.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("wire descriptor changed:\n%s\n%s", a, b)
	}
}
