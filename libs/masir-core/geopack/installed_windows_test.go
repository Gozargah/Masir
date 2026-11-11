package geopack

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestFreshWindowsInstallExcludesTheLimitedToken(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("elevated Windows fixture")
	}
	dir := filepath.Join(t.TempDir(), "service")
	if e := EnsureInstalledDirectory(dir); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "masir.exe")
	f, e := CreateInstalledFile(path, 0755)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	for _, path := range []string{dir, path} {
		sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if e != nil {
			t.Fatal(e)
		}
		if e := CheckInstalledSecurity(sd); e != nil {
			t.Fatalf("%s: %v", path, e)
		}
	}
	unsafe := filepath.Join(t.TempDir(), "user-owned")
	if e := os.Mkdir(unsafe, 0755); e != nil {
		t.Fatal(e)
	}
	// Explicitly give the account ownership: its limited token can rewrite this DACL.
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	if e := windows.SetNamedSecurityInfo(unsafe, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, u.User.Sid, nil, nil, nil); e != nil {
		t.Fatal(e)
	}
	if e := EnsureInstalledDirectory(unsafe); e == nil {
		t.Fatal("accepted existing user-owned directory")
	}
}
