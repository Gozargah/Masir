package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
)

func TestPrepareAssetsKeepsServiceDataAndExpandsBundledData(t *testing.T) {
	raw, packed, config := t.TempDir(), t.TempDir(), t.TempDir()
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		os.WriteFile(filepath.Join(raw, name), []byte(name), 0o644)
	}
	got, err := prepareAssets(raw, config, false)
	if err != nil || got != raw {
		t.Fatalf("service data: %s %v", got, err)
	}
	if err := geopack.Pack(raw, packed); err != nil {
		t.Fatal(err)
	}
	got, err = prepareAssets(packed, config, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != filepath.Join(config, "geo-cache") {
		t.Fatalf("cache path %s", got)
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		b, err := os.ReadFile(filepath.Join(got, name))
		if err != nil || string(b) != name {
			t.Fatalf("expanded %s: %s %v", name, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(packed, "geoip.dat")); !os.IsNotExist(err) {
		t.Fatal("modified bundle")
	}
}
