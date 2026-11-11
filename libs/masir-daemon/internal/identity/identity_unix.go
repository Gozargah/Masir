//go:build !windows

package identity

import (
	"errors"
	"fmt"
	"os"
	"os/user"
)

// The process's identity, replaceable in tests.
var (
	geteuid = os.Geteuid
	sudoUID = func() string { return os.Getenv("SUDO_UID") }
)

// Resolve returns the current user, or when running as root the user named by flagUser
// (a name or uid), falling back to SUDO_UID: the daemon always serves one regular user.
func Resolve(flagUser string) (Identity, error) {
	if geteuid() != 0 {
		u, err := user.Current()
		if err != nil {
			return Identity{}, err
		}
		return from(u, false), nil
	}
	spec := flagUser
	if spec == "" {
		spec = sudoUID()
	}
	if spec == "" {
		return Identity{}, errors.New("running as root: pass --user <name|uid> for the user the daemon serves")
	}
	u, err := lookup(spec)
	if err != nil {
		return Identity{}, err
	}
	if u.Uid == "0" {
		return Identity{}, errors.New("the daemon serves a regular user, not root")
	}
	return from(u, true), nil
}

// Process describes how this process was started, for the daemon's log. A root daemon's
// served user comes from SUDO_UID, which is invisible afterwards: that is what made a
// daemon started by a root CLI bind a socket the CLI never looked at.
func Process() string {
	return fmt.Sprintf("euid=%d sudo_uid=%q", geteuid(), sudoUID())
}
