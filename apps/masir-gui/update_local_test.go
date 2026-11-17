package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
)

func localUpdateApp(t *testing.T) (*app, *fakeDaemon) {
	t.Helper()
	f := ukServers()
	a := newTestApp(f)
	a.version = "1.2.0"
	f.status.Version = "1.1.0"
	f.status.ServiceInstalled = true
	f.status.Privileged = true
	f.releaseErr = errors.New("offline")
	oldDir, oldHelper, oldReconcile := installedComponents, installBundledDaemon, reconcileUpdatedService
	t.Cleanup(func() {
		installedComponents, installBundledDaemon, reconcileUpdatedService = oldDir, oldHelper, oldReconcile
	})
	installedComponents = func() (string, error) { return "/installed/bin", nil }
	installBundledDaemon = func(string, func()) error { t.Fatal("unsolicited administrator helper"); return nil }
	reconcileUpdatedService = func() error { return nil }
	f.calls = nil
	return a, f
}
func TestLocalDaemonUpdateWaitsForClick(t *testing.T) {
	a, f := localUpdateApp(t)
	for range 3 {
		a.observeUpdates(f.status)
	}
	if a.updateLabel() != "Update daemon" || !a.showUpdateControl() || a.busyUpdate() {
		t.Fatalf("missing idle offer: %+v", a.updates.state)
	}
	if len(f.calls) != 0 {
		t.Fatalf("discovery used daemon APIs: %v", f.calls)
	}
	calls := 0
	installBundledDaemon = func(dir string, _ func()) error {
		calls++
		if dir != "/installed/bin" || a.updateLabel() != "Updating daemon..." || a.daemonStep() != daemonWait {
			t.Fatal("wrong import or missing busy guard")
		}
		f.status.Version = a.version
		return nil
	}
	a.startUpdate()
	if calls != 1 || a.updates.state.bundleSync || a.updates.state.phase != updateIdle || f.called("restart") {
		t.Fatalf("upgrade did not finish: calls=%d state=%+v", calls, a.updates.state)
	}
}
func TestServiceOfferIgnoresBundledCommandFallback(t *testing.T) {
	a, f := localUpdateApp(t)
	a.binary = "/Applications/Masir.app/Contents/Resources/bin/masir"
	a.observeUpdates(f.status)
	if !a.updates.state.bundleOffer {
		t.Fatal("bundled command fallback hid an installed service upgrade")
	}
}
func TestLocalDaemonOfferEligibility(t *testing.T) {
	for _, kind := range []string{"equal", "newer", "dev", "bundled", "no service", "unprivileged", "busy"} {
		t.Run(kind, func(t *testing.T) {
			a, f := localUpdateApp(t)
			switch kind {
			case "equal":
				f.status.Version = a.version
			case "newer":
				f.status.Version = "2.0.0"
			case "dev":
				a.version = "dev"
			case "bundled":
				f.status.Bundled = true
			case "no service":
				f.status.ServiceInstalled = false
			case "unprivileged":
				f.status.Privileged = false
			case "busy":
				a.updates.state.phase = updateDownloading
			}
			installedComponents = func() (string, error) { t.Fatal("read ineligible bundle"); return "", nil }
			if a.offerBundledDaemonUpdate(f.status) {
				t.Fatal("offered ineligible update")
			}
		})
	}
}
func TestLocalDaemonFailureOnlyRetriesOnClick(t *testing.T) {
	for _, reason := range []string{"permission declined", "bad signature", "preflight failed"} {
		t.Run(reason, func(t *testing.T) {
			a, f := localUpdateApp(t)
			calls := 0
			installBundledDaemon = func(string, func()) error { calls++; return errors.New(reason) }
			a.observeUpdates(f.status)
			a.startUpdate()
			a.observeUpdates(f.status)
			if calls != 1 || a.updateLabel() != "Retry" || !strings.Contains(a.updateTooltip(), reason) {
				t.Fatalf("failure hidden or retried: %d %+v", calls, a.updates.state)
			}
			a.retryUpdate()
			if calls != 2 {
				t.Fatal("retry did not invoke helper")
			}
			if len(f.calls) != 0 {
				t.Fatalf("failure used daemon update API: %v", f.calls)
			}
		})
	}
}
func TestLocalDaemonIgnoresLateJobsAndDuplicateClicks(t *testing.T) {
	a, f := localUpdateApp(t)
	a.observeUpdates(f.status)
	var work []func()
	a.async = func(fn func()) { work = append(work, fn) }
	a.startUpdate()
	a.startUpdate()
	a.observeUpdates(f.status)
	a.applyJob(&pb.UpdateJob{State: pb.UpdateState_UPDATE_STATE_FAILED, Error: "late"})
	a.ignoreUpdate()
	if len(work) != 1 || a.updates.state.phase != updateInstalling {
		t.Fatalf("duplicated or interrupted: %d %+v", len(work), a.updates.state)
	}
}
func TestLocalDaemonDoesNotTrustHelperExitAlone(t *testing.T) {
	a, f := localUpdateApp(t)
	old := daemonWaitTimeout
	t.Cleanup(func() { daemonWaitTimeout = old })
	daemonWaitTimeout = 0
	calls := 0
	installBundledDaemon = func(string, func()) error { calls++; return nil }
	a.observeUpdates(f.status)
	a.startUpdate()
	if a.updates.state.phase != updateFailed || !a.updates.state.bundleSync {
		t.Fatal("reported success without running new version")
	}
	for range 2 {
		a.observeUpdates(f.status)
	}
	if calls != 1 {
		t.Fatal("automatically retried failed restart")
	}
	f.status.Version = a.version
	daemonWaitTimeout = time.Second
	a.retryUpdate()
	if a.updates.state.phase != updateIdle {
		t.Fatal("could not confirm installed daemon on retry")
	}
}
func TestMissingComponentsFailBeforeElevation(t *testing.T) {
	a, f := localUpdateApp(t)
	installedComponents = func() (string, error) { return "", os.ErrNotExist }
	a.observeUpdates(f.status)
	if a.updateLabel() != "Update daemon" {
		t.Fatal("missing version-gap action")
	}
	a.startUpdate()
	if a.updates.state.phase != updateFailed {
		t.Fatal("missing components were not reported")
	}
	if f.called("start update") {
		t.Fatal("missing components triggered a download")
	}
}

func TestDismissLocalDaemonOfferStaysDismissed(t *testing.T) {
	a, f := localUpdateApp(t)
	a.observeUpdates(f.status)
	a.ignoreUpdate()
	a.observeUpdates(f.status)
	if a.updates.state.bundleOffer || a.updates.state.phase != updateIdle {
		t.Fatal("dismissed offer came back on status")
	}
}

func TestDaemonUpdateControlInvokesHelperOnlyOnClick(t *testing.T) {
	a, f := localUpdateApp(t)
	a.observeUpdates(f.status)
	calls := 0
	installBundledDaemon = func(string, func()) error { calls++; return errors.New("permission declined") }
	tt := tester(a)
	if calls != 0 {
		t.Fatal("rendering prompted for administrator access")
	}
	if err := tt.Click("Update daemon"); err != nil {
		t.Fatal("daemon update button was not clickable")
	}
	if calls != 1 || a.updates.state.phase != updateFailed {
		t.Fatalf("button did not invoke local helper: %d", calls)
	}
}

func TestLocalDaemonMismatchReportsBothVersions(t *testing.T) {
	a, f := localUpdateApp(t)
	old := daemonWaitTimeout
	t.Cleanup(func() { daemonWaitTimeout = old })
	daemonWaitTimeout = time.Millisecond
	installBundledDaemon = func(string, func()) error { return nil }
	a.observeUpdates(f.status)
	a.startUpdate()
	if a.updates.state.phase != updateFailed || !strings.Contains(a.updateTooltip(), "1.1.0") || !strings.Contains(a.updateTooltip(), "1.2.0") {
		t.Fatalf("missing running/expected versions: %s", a.updateTooltip())
	}
}

func TestRunningCLIUpdateTakesPriorityOverLocalOffer(t *testing.T) {
	a, f := localUpdateApp(t)
	a.observeUpdates(f.status)
	a.applyJob(&pb.UpdateJob{State: pb.UpdateState_UPDATE_STATE_DOWNLOADING, Version: "1.3.0", BytesDone: 1, BytesTotal: 10})
	if a.updates.state.bundleOffer || a.updates.state.phase != updateDownloading {
		t.Fatal("local offer hid a running update")
	}
}

func TestDaemonOfferReturnsAfterServiceBecomesEligibleAgain(t *testing.T) {
	a, f := localUpdateApp(t)
	a.observeUpdates(f.status)
	f.status.ServiceInstalled = false
	a.observeUpdates(f.status)
	if a.updates.state.bundleOffer {
		t.Fatal("offered update for a removed service")
	}
	f.status.ServiceInstalled = true
	a.observeUpdates(f.status)
	if !a.updates.state.bundleOffer || a.updateLabel() != "Update daemon" {
		t.Fatal("eligible service did not regain its update action")
	}
}

func TestDaemonClassificationBeforeFirstStatus(t *testing.T) {
	a, _ := localUpdateApp(t)
	a.status = nil
	a.binary = "/Applications/Masir.app/Contents/Resources/bin/masir"
	if !a.daemonIsBundled() {
		t.Fatal("nil status lost bundled path fallback")
	}
	a.binary = "/usr/local/bin/masir"
	if a.daemonIsBundled() {
		t.Fatal("nil status classified standalone path as bundled")
	}
}

type unavailableUpdateDaemon struct{ pb.DaemonServiceClient }

func (unavailableUpdateDaemon) GetStatus(context.Context, *pb.GetStatusRequest, ...grpc.CallOption) (*pb.GetStatusResponse, error) {
	return nil, errors.New("unreachable")
}
func TestUnavailableDaemonDoesNotReportCachedVersionAsRunning(t *testing.T) {
	a, _ := localUpdateApp(t)
	a.d = unavailableUpdateDaemon{}
	err := a.waitForDaemon(a.version, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "didn't come back") || strings.Contains(err.Error(), "still running") {
		t.Fatalf("misleading unreachable diagnostic: %v", err)
	}
}
func TestLocalOfferTakesPriorityOverNewerRemoteRelease(t *testing.T) {
	a, f := localUpdateApp(t)
	f.releaseErr = nil
	f.release = releaseInfo(f.status.Version, "v1.3.0")
	a.applyRelease(f.release)
	a.observeUpdates(f.status)
	if a.updateLabel() != "Update daemon" {
		t.Fatal("remote release replaced the local action")
	}
	calls := 0
	installBundledDaemon = func(string, func()) error { calls++; f.status.Version = a.version; return nil }
	a.startUpdate()
	if calls != 1 || f.called("start update") {
		t.Fatalf("local click downloaded a remote release: %v", f.calls)
	}
}
