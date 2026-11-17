package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"google.golang.org/protobuf/proto"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// pkgFake stands in for the bundled CLI's download, the helper check and pkexec.
type pkgFake struct {
	cache      string
	fetches    []packageFetch
	installs   int
	helperErr  error
	fetchErr   error
	installErr error
	relaunches int
	phases     []updatePhase
	files      packageFiles
}

// packageApp is a packaged 0.1.6 app whose daemon reports 0.1.7, not marked available:
// the service is already current, the drift a packaged install shows today.
func packageApp(t *testing.T, block installBlock) (*app, *fakeDaemon, *pkgFake) {
	t.Helper()
	f := ukServers()
	f.status.Version = "0.1.7"
	a := newTestApp(f)
	a.version = "0.1.6"
	a.reachable = true
	p := &pkgFake{cache: t.TempDir()}
	saved := []any{detectInstallBlock, fetchPackage, helperTrusted, installPackage, packageCacheDir, relaunchApp, reconcileUpdatedService, appUpdatesEnabled, daemonWaitTimeout}
	t.Cleanup(func() {
		detectInstallBlock = saved[0].(func() installBlock)
		fetchPackage = saved[1].(func(context.Context, packageFetch, func(int64, int64)) (packageFiles, error))
		helperTrusted = saved[2].(func() (string, error))
		installPackage = saved[3].(func(string, packageFiles, int, func()) error)
		packageCacheDir = saved[4].(func() (string, error))
		relaunchApp = saved[5].(func())
		reconcileUpdatedService = saved[6].(func() error)
		appUpdatesEnabled = saved[7].(func() bool)
		daemonWaitTimeout = saved[8].(time.Duration)
	})
	daemonWaitTimeout = time.Second
	appUpdatesEnabled = func() bool { return false }
	detectInstallBlock = func() installBlock { return block }
	packageCacheDir = func() (string, error) { return p.cache, nil }
	relaunchApp = func() { p.relaunches++ }
	reconcileUpdatedService = func() error {
		p.phases = append(p.phases, a.updates.state.phase) // after the restart wait
		return nil
	}
	fetchPackage = func(_ context.Context, r packageFetch, progress func(int64, int64)) (packageFiles, error) {
		p.fetches = append(p.fetches, r)
		p.phases = append(p.phases, a.updates.state.phase)
		if err := os.MkdirAll(r.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p.files = packageFiles{filepath.Join(r.dir, "masir-desktop-linux-0.1.7-amd64.deb"), filepath.Join(r.dir, "sums"), filepath.Join(r.dir, "sig")}
		if err := os.WriteFile(p.files.pkg, []byte("deb"), 0o600); err != nil {
			t.Fatal(err)
		}
		progress(100, 100)
		p.phases = append(p.phases, a.updates.state.phase)
		if p.fetchErr != nil {
			return packageFiles{}, p.fetchErr // the CLI deletes a mismatch itself
		}
		return p.files, nil
	}
	helperTrusted = func() (string, error) { return "/opt/masir-desktop/bin/masir", p.helperErr }
	installPackage = func(bin string, files packageFiles, uid int, authorized func()) error {
		p.installs++
		if bin != "/opt/masir-desktop/bin/masir" || files != p.files {
			t.Errorf("install %s %+v", bin, files)
		}
		p.phases = append(p.phases, a.updates.state.phase)
		if a.updateLabel() != "Enter Password…" || a.daemonStep() != daemonWait {
			t.Errorf("before the password: label %q", a.updateLabel())
		}
		authorized()
		p.phases = append(p.phases, a.updates.state.phase)
		if p.installErr == nil {
			// The restarted daemon runs the new release; the app hasn't seen it yet.
			next := proto.Clone(f.status).(*pb.Status)
			next.Version = "0.1.7"
			f.status = next
			f.restartVersion = "0.1.7"
		}
		return p.installErr
	}
	a.detectPackageInstall()
	a.applyRelease(releaseInfo("0.1.7", "0.1.7"))
	f.calls = nil
	return a, f, p
}

func elevationExit(code int) error {
	return &elevationError{kind: elevationFailed, code: code, msg: "root permission wasn't granted: helper said no"}
}

func TestMyGoEnabledInstallsAreUnchanged(t *testing.T) {
	a, f, p := packageApp(t, blockPackageDeb)
	appUpdatesEnabled = func() bool { return true }
	a.updates.state.block = blockNone
	a.detectPackageInstall()
	if a.updates.state.block != blockNone || a.packageUpdateAvailable() {
		t.Fatal("an app MyGo can update entered the package flow")
	}
	a.updates.state.app = &mygo.Update{Version: "0.1.7"}
	installed := 0
	oldInstall := installAppUpdate
	t.Cleanup(func() { installAppUpdate = oldInstall })
	installAppUpdate = func(context.Context, *mygo.Update, func(int64, int64)) error { installed++; return nil }
	a.updates.state.phase = updateAvailable
	a.startUpdate()
	if len(p.fetches) != 0 || p.installs != 0 || installed != 1 {
		t.Fatalf("package flow ran, or MyGo's didn't (%d): %v", installed, f.calls)
	}
}

func TestAPackagedAppBehindItsServiceOffersAnUpdate(t *testing.T) {
	a, _, _ := packageApp(t, blockPackageDeb)
	a.version = "0.1.2"
	a.applyRelease(releaseInfo("0.1.5", "0.1.5")) // the service is current: Available is false
	if a.updateLabel() != "New Update" || !a.showUpdateControl() {
		t.Fatalf("label %q, control %v", a.updateLabel(), a.showUpdateControl())
	}
	b, _, _ := packageApp(t, blockNone)
	b.version = "0.1.2"
	b.applyRelease(releaseInfo("0.1.5", "0.1.5"))
	if b.showUpdateControl() {
		t.Fatal("an app MyGo can update showed a package update")
	}
}

func TestPackageUpdateHappyPathWithAService(t *testing.T) {
	a, f, p := packageApp(t, blockPackageDeb)
	f.status.ServiceInstalled = true
	f.status.Version = "0.1.6"
	a.applyStatus(f.status)
	a.startUpdate()
	want := []updatePhase{updateAppDownloading, updateVerifying, updateAuthorizing, updateAppInstalling, updateRestarting}
	if !slices.Equal(p.phases, want) {
		t.Fatalf("phases %v, want %v", p.phases, want)
	}
	if p.relaunches != 1 || !a.updates.state.relaunching || f.called("restart") {
		t.Fatalf("relaunches %d, calls %v", p.relaunches, f.calls)
	}
	if left, _ := os.ReadDir(p.cache); len(left) != 0 {
		t.Fatalf("download kept: %v", left)
	}
}

// With no service, the daemon runs from the package's own copy; an older daemon that
// doesn't report Bundled is known by its path.
func TestPackageUpdateWithAPackagedDaemonRestartsIt(t *testing.T) {
	a, f, p := packageApp(t, blockPackageDeb)
	a.binary = "/opt/masir-desktop/bin/masir"
	f.status.Version = "0.1.6"
	a.applyStatus(f.status)
	a.startUpdate()
	if f.count("restart") != 1 || p.relaunches != 1 || a.updates.state.phase == updateFailed {
		t.Fatalf("restarts %d, relaunches %d, tooltip %q", f.count("restart"), p.relaunches, a.updateTooltip())
	}
}

// The package is installed even when the daemon doesn't come back in time: the old app
// must not stay with a retry that can only find the new version installed.
func TestAnInstalledPackageRelaunchesWhenTheRestartFails(t *testing.T) {
	a, f, p := packageApp(t, blockPackageDeb)
	a.binary = "/opt/masir-desktop/bin/masir"
	f.status.Version = "0.1.6"
	a.applyStatus(f.status)
	installPackage = func(_ string, _ packageFiles, _ int, authorized func()) error { authorized(); return nil }
	a.startUpdate()
	if p.relaunches != 1 || !a.updates.state.relaunching || len(a.notices) != 1 {
		t.Fatalf("relaunches %d, notices %v", p.relaunches, a.notices)
	}
}

func TestPackageUpdateWithoutADaemonRelaunches(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	a.status = nil
	a.reachable = false
	a.startUpdate()
	if len(p.fetches) != 1 || p.fetches[0].proxy != "" || p.relaunches != 1 {
		t.Fatalf("fetches %+v, relaunches %d", p.fetches, p.relaunches)
	}
}

func TestPackageUpdateUsesTheProxyOnlyWhenItRuns(t *testing.T) {
	for _, running := range []bool{true, false} {
		a, f, p := packageApp(t, blockPackageRpm)
		f.status.ProxyRunning = running
		a.applyStatus(f.status)
		a.startUpdate()
		want := ""
		if running {
			want = "http://127.0.0.1:7890"
		}
		if p.fetches[0].proxy != want || p.fetches[0].format != "rpm" || p.fetches[0].version != "0.1.7" {
			t.Fatalf("running %v: fetch %+v", running, p.fetches[0])
		}
		for _, c := range f.calls {
			if strings.Contains(c, "power") || strings.Contains(c, "settings") {
				t.Fatalf("the update touched the proxy: %v", f.calls)
			}
		}
	}
}

func TestPackageUpdatePausesAndResumes(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	var ctxs []context.Context
	fetchPackage = func(ctx context.Context, r packageFetch, progress func(int64, int64)) (packageFiles, error) {
		ctxs = append(ctxs, ctx)
		p.fetches = append(p.fetches, r)
		if len(ctxs) == 1 {
			progress(30, 100)
			a.pauseUpdate()
			return packageFiles{}, ctx.Err()
		}
		return packageFiles{}, errors.New("stop here")
	}
	a.startUpdate()
	if ctxs[0].Err() == nil || a.updates.state.phase != updateAppPaused || !strings.HasPrefix(a.updateLabel(), "Paused 1/1 · ") {
		t.Fatalf("paused: label %q", a.updateLabel())
	}
	a.resumeUpdate()
	if len(p.fetches) != 2 {
		t.Fatalf("fetches %d", len(p.fetches))
	}
}

func TestPackageUpdateCancelledDialogReturnsQuietly(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	p.installErr = &elevationError{kind: elevationCancelled, msg: "permission wasn't granted"}
	a.startUpdate()
	if a.updateLabel() != "New Update" || a.updates.state.err != "" || len(a.notices) != 0 {
		t.Fatalf("label %q, err %q, notices %v", a.updateLabel(), a.updates.state.err, a.notices)
	}
	if _, err := os.Stat(p.files.pkg); err != nil {
		t.Fatalf("download dropped: %v", err)
	}
}

func TestPackageUpdateFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		fetch   error
		install error
		tooltip string
		kept    bool
	}{
		{"not authorized", nil, &elevationError{kind: elevationDenied, msg: "x"}, "Your account isn't allowed to install software.", true},
		{"installer busy", nil, elevationExit(pkgExitBusy), "Another installer is running. Try again in a minute.", true},
		{"mismatch at install", nil, elevationExit(pkgExitMismatch), "The download didn't match the signed release; nothing was installed.", false},
		{"mismatch at download", elevationExit(pkgExitMismatch), nil, "The download didn't match the signed release; nothing was installed.", false},
		{"unreachable", elevationExit(pkgExitUnreachable), nil, "GitHub isn't reachable. Connect Masir and try again.", true},
		{"not newer", nil, elevationExit(pkgExitNotNewer), "This version is already installed. Restart Masir.", true},
		{"package manager", nil, elevationExit(12), "helper said no", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, _, p := packageApp(t, blockPackageDeb)
			p.fetchErr, p.installErr = c.fetch, c.install
			a.startUpdate()
			if a.updateLabel() != "Update failed" || a.updateTooltip() != c.tooltip {
				t.Fatalf("label %q, tooltip %q", a.updateLabel(), a.updateTooltip())
			}
			if c.fetch != nil {
				if p.installs != 0 {
					t.Fatal("installed after a failed download")
				}
				return // a failed fetch hands over no files
			}
			if _, err := os.Stat(p.files.pkg); (err == nil) != c.kept {
				t.Fatalf("download kept = %v, want %v", err == nil, c.kept)
			}
		})
	}
}

func TestPackageUpdateInstallerBusyThenRetry(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	p.installErr = elevationExit(pkgExitBusy)
	a.startUpdate()
	p.installErr = nil
	a.retryUpdate()
	if p.installs != 2 || len(p.fetches) != 2 || p.fetches[1].dir != p.fetches[0].dir {
		t.Fatalf("installs %d, fetches %+v", p.installs, p.fetches)
	}
}

func TestPackageUpdateWithoutPkexecShowsTheCommand(t *testing.T) {
	for _, block := range []installBlock{blockPackageDeb, blockPackageRpm} {
		a, _, p := packageApp(t, block)
		p.installErr = &elevationError{kind: elevationUnavailable, msg: "no agent"}
		a.startUpdate()
		tool := map[installBlock]string{blockPackageDeb: "sudo apt install ", blockPackageRpm: "sudo dnf install "}[block]
		if tip := a.updateTooltip(); !strings.Contains(tip, tool+p.files.pkg) || !filepath.IsAbs(p.files.pkg) {
			t.Fatalf("tooltip %q", tip)
		}
	}
}

func TestPackageUpdateUntrustedHelperFailsBeforeThePrompt(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	p.helperErr = errors.New("/opt/masir-desktop/bin is writable by group")
	a.startUpdate()
	if p.installs != 0 || a.updates.state.phase != updateFailed || !strings.Contains(a.updateTooltip(), "writable") {
		t.Fatalf("installs %d, tooltip %q", p.installs, a.updateTooltip())
	}
}

func TestPackageInstalledButServicePendingRelaunches(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	p.installErr = elevationExit(pkgExitServicePending)
	a.startUpdate()
	if p.relaunches != 1 || !a.updates.state.relaunching || a.updates.state.phase == updateFailed {
		t.Fatalf("relaunches %d, phase %v", p.relaunches, a.updates.state.phase)
	}
}

func TestPackageUpdateRemovesStaleDownloads(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	stale := filepath.Join(p.cache, "0.1.6")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	p.installErr = elevationExit(pkgExitBusy) // keep the new download to look at
	a.startUpdate()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale download kept: %v", err)
	}
}

func TestCancellingAPackageDownloadDeletesIt(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	fetchPackage = func(ctx context.Context, r packageFetch, progress func(int64, int64)) (packageFiles, error) {
		os.MkdirAll(r.dir, 0o700)
		progress(30, 100)
		a.pauseUpdate()
		return packageFiles{}, ctx.Err()
	}
	a.startUpdate()
	a.cancelUpdate()
	if a.updateLabel() != "New Update" || a.updates.state.pkgSync {
		t.Fatalf("label %q", a.updateLabel())
	}
	if left, _ := os.ReadDir(p.cache); len(left) != 0 {
		t.Fatalf("download kept: %v", left)
	}
}

func TestIgnoringAPackageUpdateHidesIt(t *testing.T) {
	a, f, _ := packageApp(t, blockPackageDeb)
	f.release = releaseInfo("0.1.7", "0.1.7")
	a.ignoreUpdate()
	if !f.called("ignore 0.1.7") || a.showUpdateControl() {
		t.Fatalf("calls %v, control %v", f.calls, a.showUpdateControl())
	}
}

func TestReadOnlyInstallExplainsItself(t *testing.T) {
	a, _, p := packageApp(t, blockReadOnly)
	if a.updateLabel() != "New Update" || a.updateTooltip() != "Masir can't update itself here: its folder isn't writable. Download the new version from the release page." {
		t.Fatalf("label %q, tooltip %q", a.updateLabel(), a.updateTooltip())
	}
	a.startUpdate()
	if len(p.fetches) != 0 || a.updates.state.phase != updateAvailable {
		t.Fatalf("a click started something: phase %v", a.updates.state.phase)
	}
}

func TestFinishUpdateStillWinsOverAPackageUpdate(t *testing.T) {
	a, f, p := packageApp(t, blockPackageDeb)
	f.status.ServiceInstalled, f.status.Privileged, f.status.Version = true, true, "0.1.5"
	f.release = releaseInfo("0.1.5", "v0.1.7")
	a.observeUpdates(f.status)
	if a.updateLabel() != "Finish Update" {
		t.Fatalf("label %q", a.updateLabel())
	}
	installBundledDaemon = func(string, func()) error { return errors.New("stop") }
	t.Cleanup(func() { installBundledDaemon = importLegacyDesktop })
	installedComponents = func() (string, error) { return "/opt/masir-desktop/bin", nil }
	t.Cleanup(func() { installedComponents = installedComponentDirectory })
	a.startUpdate()
	if len(p.fetches) != 0 {
		t.Fatal("the package flow ran before Finish Update")
	}
}

func TestTheVersionOverrideNeedsALoopbackBase(t *testing.T) {
	rel := releaseInfo("0.1.5", "v0.1.5")
	t.Setenv("MASIR_DESKTOP_RELEASE_VERSION", "0.1.91")
	for base, want := range map[string]string{"": "v0.1.5", "https://example.com": "v0.1.5", "http://127.0.0.1:8799": "0.1.91"} {
		t.Setenv("MASIR_DESKTOP_RELEASE_BASE", base)
		if got := packageTargetVersion(rel); got != want {
			t.Fatalf("base %q: %q, want %q", base, got, want)
		}
	}
}

// A paused download's last post can arrive after the resume started a newer one.
func TestAStalePackageRunCannotMoveTheNewOne(t *testing.T) {
	a, _, p := packageApp(t, blockPackageDeb)
	var work []func()
	a.async = func(fn func()) { work = append(work, fn) }
	fetchPackage = func(ctx context.Context, r packageFetch, progress func(int64, int64)) (packageFiles, error) {
		p.fetches = append(p.fetches, r)
		<-ctx.Done()
		return packageFiles{}, ctx.Err()
	}
	a.startUpdate()
	a.pauseUpdate()
	a.resumeUpdate()
	if len(work) != 2 || a.updates.state.phase != updateAppDownloading {
		t.Fatalf("work %d, phase %v", len(work), a.updates.state.phase)
	}
	work[0]() // the paused run ends now and posts
	if a.updates.state.phase != updateAppDownloading || a.updates.state.appCancel == nil {
		t.Fatalf("the stale run moved the new one: phase %v", a.updates.state.phase)
	}
	a.cancelUpdate()
	work[1]()
	if a.updateLabel() != "New Update" {
		t.Fatalf("label %q", a.updateLabel())
	}
}

func TestAPackageUpdateNeedsARealNewerVersion(t *testing.T) {
	t.Setenv("MASIR_DESKTOP_RELEASE_BASE", "http://127.0.0.1:8799")
	for _, v := range []string{"../../x", "0.1.6", "0.1.5"} {
		a, _, p := packageApp(t, blockPackageDeb)
		t.Setenv("MASIR_DESKTOP_RELEASE_VERSION", v)
		a.updates.state.phase = updateAvailable
		a.startPackageUpdate()
		if len(p.fetches) != 0 || a.updates.state.pkgSync {
			t.Fatalf("%s: started", v)
		}
	}
}
