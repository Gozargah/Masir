package geopack

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestElevatedGeoStagingHasAdministratorOwnership(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires an elevated Windows token")
	}
	dir, err := NewStaging(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "geoip.dat")
	f, err := createResource(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, name := range []string{dir, path} {
		sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		owner, _, err := sd.Owner()
		if err != nil || !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			t.Fatalf("unsafe owner for %s: %v", name, err)
		}
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("unprotected DACL for %s: %v", name, err)
		}
	}
}
