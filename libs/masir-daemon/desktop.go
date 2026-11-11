package daemon

import (
	"context"
	"errors"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/identity"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/server"
)

// InstallDesktopOffline upgrades a legacy service from independently verified local
// components. The caller supplies supervisor stop/recovery operations, never paths
// to privileged targets or signing keys.
func InstallDesktopOffline(ctx context.Context, source, user, version string, stop, resume func() error) error {
	id, err := identity.Resolve(user)
	if err != nil {
		return err
	}
	if !id.Privileged {
		return errors.New("offline desktop import requires administrator permission")
	}
	dir, err := configDir(id)
	if err != nil {
		return err
	}
	return server.OfflineDesktopUpdate(ctx, source, version, dir, id, stop, resume)
}
