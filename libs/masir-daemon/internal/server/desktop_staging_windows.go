package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UAC's unelevated token belongs to the same account. A user-owned config folder or
// a user-owned object is therefore insufficient, even with an apparently private DACL.
// Use an admin-owned parent, protected DACLs, and Administrators ownership at creation.
const desktopStagingSDDL = "O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

func desktopSecurityAttributes() (*windows.SecurityAttributes, error) {
	sd, err := windows.SecurityDescriptorFromString(desktopStagingSDDL)
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}
func (s *Server) newDesktopStaging() (string, error) {
	if !s.id.Privileged {
		return "", errors.New("desktop service import requires elevation")
	}
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", err
	}
	sa, err := desktopSecurityAttributes()
	if err != nil {
		return "", err
	}
	for range 10 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		dir := filepath.Join(base, "masir-update-"+hex.EncodeToString(random[:]))
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
	return "", errors.New("could not create protected desktop staging")
}
func openDesktopComponent(path string, _ os.FileMode) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	sa, err := desktopSecurityAttributes()
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
