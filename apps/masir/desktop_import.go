package main

import (
	"context"
	"errors"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/service"
	masird "github.com/Gozargah/Masir/libs/masir-daemon"
)

// daemonImportDesktop runs from the new bundled CLI, so old services need no new
// RPC support. It never downloads components and the installer verifies its own
// embedded release keys; the GUI cannot supply a key or a privileged target path.
func daemonImportDesktop(a args) error {
	user, source := a.str("user"), a.str("from")
	if user == "" {
		user = currentUser()
	}
	if source == "" {
		return errors.New("missing desktop component directory")
	}
	if !elevated() {
		return elevate("daemon", "import-desktop", "--user", user, "--from", source)
	}
	ctx, cancel := context.WithTimeout(appCtx, 4*time.Minute)
	defer cancel()
	return masird.InstallDesktopOffline(ctx, source, user, version, stopOfflineService, func() error {
		return errors.Join(labelServiceBinary(service.BinaryPath()), startOfflineService())
	})
}
