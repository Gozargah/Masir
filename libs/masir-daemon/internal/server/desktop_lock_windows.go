package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/service"
	"golang.org/x/sys/windows"
)

func lockOfflineService() (offlineServiceControl, error) {
	pf, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return offlineServiceControl{}, err
	}
	if !strings.EqualFold(filepath.Clean(service.InstallDir()), filepath.Join(pf, "masir")) {
		return offlineServiceControl{}, errors.New("service directory is outside Program Files")
	}
	// Keep the parent from being renamed, and reject junctions and writable parents.
	parent, err := offlineWindowsOpen(pf, true)
	if err != nil {
		return offlineServiceControl{}, err
	}
	defer parent.Close()
	if err := offlineAdminOwned(parent); err != nil {
		return offlineServiceControl{}, err
	}
	sa, err := desktopSecurityAttributes()
	if err != nil {
		return offlineServiceControl{}, err
	}
	p, err := windows.UTF16PtrFromString(filepath.Join(pf, ".masir-offline-update.lock"))
	if err != nil {
		return offlineServiceControl{}, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, sa, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return offlineServiceControl{}, err
	}
	lock := os.NewFile(uintptr(h), "offline-update-lock")
	fail := func(e error) (offlineServiceControl, error) { lock.Close(); return offlineServiceControl{}, e }
	if err := offlineRegular(lock); err != nil {
		return fail(err)
	}
	if err := offlineAdminOwned(lock); err != nil {
		return fail(err)
	}
	var over windows.Overlapped
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &over); err != nil {
		return fail(errors.New("another offline service update is running"))
	}
	dir, err := offlineWindowsOpen(service.InstallDir(), true)
	if err != nil {
		return fail(err)
	}
	bin, err := offlineWindowsOpen(service.BinaryPath(), false)
	if err != nil {
		dir.Close()
		return fail(err)
	}
	trusted := offlineAdminOwned(dir) == nil && offlineAdminOwned(bin) == nil
	bin.Close()
	if trusted {
		return offlineServiceControl{close: func() { dir.Close(); lock.Close() }, version: readOfflineVersion, publish: func() error { return nil }}, nil
	}
	control, err := snapshotLegacyWindows(dir, pf)
	if err != nil {
		dir.Close()
		return fail(err)
	}
	closeSnapshot := control.close
	control.close = func() { closeSnapshot(); lock.Close() }
	return control, nil
}

func desktopAdminControlled(sd *windows.SECURITY_DESCRIPTOR) error {
	return geopack.CheckInstalledSecurity(sd)
}
func offlineAdminOwned(f *os.File) error {
	sd, e := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	return desktopAdminControlled(sd)
}
func offlineRegular(f *os.File) error {
	var info windows.ByHandleFileInformation
	if e := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); e != nil {
		return e
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		return errors.New("offline component must be a regular file with one link")
	}
	return nil
}

// Pin directory names against deletion, but allow directory writers: Windows
// rename opens the destination parent for FILE_ADD_FILE. Denying write sharing
// on our own directory handle can block replacement of every child. Individual
// resource handles still deny write/delete sharing during their byte snapshot.
// Never follow junctions or accept multiply-linked resource files.
func offlineWindowsOpen(path string, dir bool) (*os.File, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	share := uint32(windows.FILE_SHARE_READ)
	if dir {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
		share |= windows.FILE_SHARE_WRITE
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, share, nil, windows.OPEN_EXISTING, flags, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	e = windows.GetFileInformationByHandle(h, &info)
	if e == nil && (info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != dir) {
		e = errors.New("unexpected component type or reparse point")
	}
	if e == nil && !dir {
		e = offlineRegular(f)
	}
	if e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
