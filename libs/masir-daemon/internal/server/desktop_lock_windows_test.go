package server

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestOfflineDesktopRequiresPrivilegedOwnershipAndWriteAccess(t *testing.T) {
	for _, tc := range []struct {
		name, sddl string
		good       bool
	}{
		{"normal service", "O:BAG:BAD:(A;;FA;;;SY)(A;;FA;;;BA)(A;;GRGX;;;BU)", true},
		{"inherit only creator", "O:BAG:BAD:(A;;FA;;;SY)(A;;FA;;;BA)(A;OICIIO;GA;;;CO)(A;;GRGX;;;BU)", true},
		{"trusted installer", "O:BAG:BAD:(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464)", true},
		{"unprivileged write", "O:BAG:BAD:(A;;FA;;;BA)(A;;GW;;;BU)", false},
		{"unprivileged delete child", "O:BAG:BAD:(A;;FA;;;BA)(A;;0x40;;;BU)", false},
		{"unprivileged owner", "O:BUG:BAD:(A;;FA;;;BA)", false},
		{"unrestricted dacl", "O:BAG:BA", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, e := windows.SecurityDescriptorFromString(tc.sddl)
			if e != nil {
				t.Fatal(e)
			}
			e = desktopAdminControlled(sd)
			if (e == nil) != tc.good {
				t.Fatalf("accepted=%v err=%v", e == nil, e)
			}
		})
	}
}
