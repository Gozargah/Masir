package geopack

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func privateAttributes() (*windows.SecurityAttributes, error) {
	// Ownership matters as well as the DACL: UAC's limited token has the same
	// account SID and can rewrite permissions on objects owned by that SID.
	sd, err := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

func makeStage(base string) (string, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return os.MkdirTemp(base, ".geo-")
	}
	sa, err := privateAttributes()
	if err != nil {
		return "", err
	}
	for range 10 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		dir := filepath.Join(base, ".geo-"+hex.EncodeToString(random[:]))
		p, err := windows.UTF16PtrFromString(dir)
		if err != nil {
			return "", err
		}
		if err := windows.CreateDirectory(p, sa); err == windows.ERROR_ALREADY_EXISTS {
			continue
		} else if err != nil {
			return "", err
		}
		return dir, nil
	}
	return "", errors.New("could not create protected geo staging")
}

func createResource(path string, mode os.FileMode) (*os.File, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	}
	sa, err := privateAttributes()
	if err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// Installed assets are public but must never be owned by the UAC limited token.
const installedSDDL = "O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GRGX;;;BU)"

func installedSecurity() (*windows.SecurityAttributes, error) {
	sd, e := windows.SecurityDescriptorFromString(installedSDDL)
	if e != nil {
		return nil, e
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}
func createInstalledResource(path string, mode os.FileMode) (*os.File, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return createResource(path, mode)
	}
	sa, e := installedSecurity()
	if e != nil {
		return nil, e
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(h), path), nil
}
func ensureInstalledDirectory(path string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return os.MkdirAll(path, 0755)
	}
	sa, e := installedSecurity()
	if e != nil {
		return e
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	if e := windows.CreateDirectory(p, sa); e != nil && e != windows.ERROR_ALREADY_EXISTS {
		return e
	}
	// Never follow a junction while applying administrator ownership.
	h, e := windows.CreateFile(p, windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if e := windows.GetFileInformationByHandle(h, &info); e != nil {
		return e
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("service directory is a reparse point")
	}
	existing, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	return CheckInstalledSecurity(existing)
}
