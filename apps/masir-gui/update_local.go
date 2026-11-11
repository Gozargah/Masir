package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"golang.org/x/mod/semver"
)

var (
	installedComponents  = installedComponentDirectory
	installBundledDaemon = importLegacyDesktop
)

// installedComponentDirectory runs off the UI thread. Only release bundles carry
// signed components; a source build or an explicitly selected daemon is left alone.
func installedComponentDirectory() (string, error) {
	if os.Getenv("MASIR_BIN") != "" || (os.Getenv("MASIR_APP_NAME") != "" && os.Getenv("MASIR_APP_NAME") != "Masir") {
		return "", os.ErrNotExist
	}
	bin := bundledBinary()
	if bin == "" {
		return "", os.ErrNotExist
	}
	dir := filepath.Dir(bin)
	for _, name := range []string{updatebundle.DescriptorName, updatebundle.SignatureName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func newerRelease(next, current string) bool {
	canonical := func(v string) string { return "v" + strings.TrimPrefix(strings.TrimSpace(v), "v") }
	n, c := canonical(next), canonical(current)
	return semver.IsValid(n) && semver.IsValid(c) && semver.Compare(n, c) > 0
}

// offerBundledDaemonUpdate only compares status and versions. File validation and all
// privileged work belong to the explicit action, never an app-open/status event.
func (a *app) offerBundledDaemonUpdate(st *pb.Status) bool {
	if a.updates.state.bundleSync {
		return true
	}
	eligible := st.GetServiceInstalled() && st.GetPrivileged() && !a.daemonIsBundled() && newerRelease(a.version, st.GetVersion())
	if !eligible {
		if a.updates.state.bundleOffer {
			a.updates.state.bundleOffer = false
			a.updates.state.bundleGap = "" // eligibility changes are not dismissals
			if a.updates.state.phase == updateAvailable {
				a.updates.state.phase = updateIdle
			}
		}
		return false
	}
	if a.busyUpdate() || a.updates.state.checking || a.updates.state.attaching || a.serviceBusy {
		return false
	}
	gap := a.version + "|" + st.GetVersion()
	if !a.updates.state.bundleOffer && a.updates.state.bundleGap == gap {
		return false
	}
	a.updates.state.bundleGap = gap
	a.updates.state.bundleOffer = true
	a.updates.state.phase = updateAvailable
	return true
}

func (a *app) startBundledDaemonUpdate() {
	if !a.updates.state.bundleOffer || a.busyUpdate() || a.serviceBusy {
		return
	}
	a.updates.state.bundleOffer = false
	a.updates.state.bundleSync = true
	a.updates.state.phase = updateInstalling
	a.updates.state.err = ""
	a.updates.state.tunWasOn = a.status.GetTunRunning()
	version := a.version
	a.async(func() {
		dir, err := installedComponents()
		if err == nil {
			err = installBundledDaemon(dir)
		}
		a.post(func() {
			if err != nil {
				a.updates.state.phase = updateFailed
				a.updates.state.err = describe(err)
				a.notifyError("Couldn't update the daemon: " + a.updates.state.err)
				return
			}
			a.followDaemonRestart(version, false)
		})
	})
}
