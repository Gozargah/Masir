//go:build linux

package main

import (
	"strings"
	"testing"
)

// The error that sent this whole investigation astray — a denied dial reported as
// "permission denied" — must point at SELinux, not at the generic file-permission
// advice. While SELinux enforces, anyway; without it the generic advice stands.
func TestExplainDialDenialNamesSELinux(t *testing.T) {
	old := selinuxEnforcing
	t.Cleanup(func() { selinuxEnforcing = old })

	msg := `transport: Error while dialing: dial tcp 127.0.0.1:40489: connect: permission denied`

	selinuxEnforcing = func() bool { return true }
	adv := explain(msg)
	if !strings.Contains(adv.text, "SELinux") {
		t.Fatalf("enforcing advice = %q; want it to name SELinux", adv.text)
	}
	if len(adv.cmds) != 1 || adv.cmds[0] != "masir daemon label" {
		t.Fatalf("enforcing advice cmds = %v; want [masir daemon label]", adv.cmds)
	}

	selinuxEnforcing = func() bool { return false }
	if adv := explain(msg); strings.Contains(adv.text, "SELinux") {
		t.Fatalf("permissive advice = %q; SELinux blocks nothing there", adv.text)
	}
}
