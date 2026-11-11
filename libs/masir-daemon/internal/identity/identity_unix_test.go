//go:build !windows

package identity

import (
	"os"
	"os/user"
	"strconv"
	"testing"
)

// A root daemon serves SUDO_UID, not root. That fallback is what a root CLI's daemon
// used, while the CLI itself looked for root's socket: the mismatch this test pins.
func TestResolveAsRootServesTheSudoUser(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: there is no regular user to serve")
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	oldEUID, oldSudo := geteuid, sudoUID
	t.Cleanup(func() { geteuid, sudoUID = oldEUID, oldSudo }) // variables in tests are not parallel

	geteuid = func() int { return 0 }
	sudoUID = func() string { return me.Uid }

	id, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if !id.Privileged {
		t.Error("a root daemon is not privileged")
	}
	if id.UID != os.Getuid() || id.Name != me.Username {
		t.Fatalf("Resolve = %+v, want the sudo user %s", id, me.Username)
	}
}

func TestResolveAsRootWithoutASudoUserRefuses(t *testing.T) {
	oldEUID, oldSudo := geteuid, sudoUID
	t.Cleanup(func() { geteuid, sudoUID = oldEUID, oldSudo })

	geteuid = func() int { return 0 }
	sudoUID = func() string { return "" }

	if _, err := Resolve(""); err == nil {
		t.Fatal("Resolve as root without --user or SUDO_UID succeeded")
	}
}

func TestResolveAsRootRefusesServingRoot(t *testing.T) {
	oldEUID, oldSudo := geteuid, sudoUID
	t.Cleanup(func() { geteuid, sudoUID = oldEUID, oldSudo })

	geteuid = func() int { return 0 }
	sudoUID = func() string { return "0" }

	if _, err := Resolve(""); err == nil {
		t.Fatal("Resolve as root served root")
	}
	if _, err := Resolve(strconv.Itoa(0)); err == nil {
		t.Fatal("Resolve --user 0 served root")
	}
}
