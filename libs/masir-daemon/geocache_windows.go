package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func geoCacheDir(config string, privileged bool) (string, error) {
	if !privileged {
		dir := filepath.Join(config, "geo-cache")
		return dir, os.MkdirAll(dir, 0o700)
	}
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", err
	}
	// UAC's limited token has the same SID: ownership by the person alone cannot
	// protect an elevated daemon's cache. Require an explicit, admin-only DACL.
	want, err := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "masir-geo-cache")
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: want}
	if err := windows.CreateDirectory(p, &sa); err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return "", err
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid privileged geo cache directory")
	}
	got, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	// Windows may record auto-inheritance bookkeeping even for a protected DACL.
	// Ignore those flags when comparing; retain the protected bit and every ACE.
	if err := got.SetControl(windows.SE_DACL_AUTO_INHERITED|windows.SE_DACL_AUTO_INHERIT_REQ, 0); err != nil {
		return "", err
	}
	if got.String() != want.String() {
		return "", errors.New("privileged geo cache permissions differ from the protected template")
	}
	return dir, nil
}
