package server

import (
	"golang.org/x/sys/windows"
	"testing"
)

func desktopStagingTestAllowed() bool { return windows.GetCurrentProcessToken().IsElevated() }
func TestDesktopStagingUsesAdminOwnershipAndProtectedDACL(t *testing.T) {
	sa, err := desktopSecurityAttributes()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sa.SecurityDescriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		t.Fatal("staging owner is not Administrators")
	}
	control, _, err := sa.SecurityDescriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("staging inherits user ACL entries")
	}
	acl, _, err := sa.SecurityDescriptor.DACL()
	if err != nil || acl == nil {
		t.Fatal("staging has no restrictive DACL")
	}
}
