package server

import (
	"encoding/json"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDesktopRetryRecognizesOnlyIdenticalInstalledComponents(t *testing.T) {
	dir := t.TempDir()
	names := updatebundle.Names(runtime.GOOS)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := updatebundle.Create(dir, "0.1.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	var d updatebundle.Descriptor
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, names[0])
	if !desktopInstalledMatches(d, binary, dir) {
		t.Fatal("identical payload would overwrite rollback backups")
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("changed"), 0755); err != nil {
			t.Fatal(err)
		}
		if desktopInstalledMatches(d, binary, dir) {
			t.Fatalf("changed %s was treated as identical", name)
		}
		os.WriteFile(filepath.Join(dir, name), []byte(name), 0755)
	}
}
