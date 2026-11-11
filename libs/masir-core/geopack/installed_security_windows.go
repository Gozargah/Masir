package geopack

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

// TrustedInstaller is the privileged Windows resource-protection principal:
// https://learn.microsoft.com/en-us/archive/msdn-magazine/2008/november/access-control-understanding-windows-file-and-registry-permissions
func installedPrivilegedSID(sid *windows.SID) bool {
	return sid != nil && (sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.String() == "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464")
}
func CheckInstalledSecurity(sd *windows.SECURITY_DESCRIPTOR) error {
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	if !installedPrivilegedSID(owner) {
		return errors.New("service files must be owned by Administrators or SYSTEM")
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	if acl == nil {
		return errors.New("service has an unrestricted DACL")
	}
	const deleteChild = 0x0040 // FILE_DELETE_CHILD (not exposed by x/sys/windows).
	const writes = deleteChild | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e := windows.GetAce(acl, i, &ace); e != nil {
			return e
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("unsupported service ACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if uint32(ace.Mask)&writes != 0 && !installedPrivilegedSID(sid) {
			return errors.New("service is writable without administrator permission")
		}
	}
	return nil
}
