package daemon

import (
	"fmt"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/identity"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/store"
	"github.com/Gozargah/Masir/libs/masir-daemon/ports"
)

// WaitPortsFree waits until the ports the user's saved settings would have Xray bind are
// free, or timeout passes.
//
// A service stop answers before the old daemon has closed Xray's listeners: launchd's
// bootout returns once the job is gone and systemd's stop once the unit is dead, but the
// process's sockets outlive the job record by a moment either way. Whoever removes the
// service has to wait for the ports themselves, or the daemon that replaces it probes a
// port the first one still holds, reports "address already in use", and comes up without
// the proxy the person was using.
func WaitPortsFree(user string, timeout time.Duration) error {
	id, err := identity.Resolve(user)
	if err != nil {
		return err
	}
	dir, err := configDir(id)
	if err != nil {
		return err
	}
	st, err := store.Load(dir)
	if err != nil {
		return err
	}
	if !st.Settings.XrayRuns() {
		return nil // nothing was bound: the daemon had masir off
	}
	if err := ports.WaitFree(st, timeout); err != nil {
		return fmt.Errorf("wait for xray's ports: %w", err)
	}
	return nil
}
