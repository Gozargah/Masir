package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/client"
	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/paths"
	"github.com/Gozargah/Masir/libs/masir-core/service"
	"github.com/Gozargah/Masir/libs/masir-core/transport"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/identity"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/store"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/update"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// OfflineDesktopUpdate is the elevated compatibility path for pre-import services.
// stop quiesces the supervisor; resume runs on success or failure while the
// process lock is still held, so no other helper races restart recovery.
func OfflineDesktopUpdate(ctx context.Context, source, expected, config string, id identity.Identity, stop, resume func() error) (err error) {
	if !id.Privileged || !service.Installed() {
		return errors.New("offline import requires an installed service and administrator permission")
	}
	control, err := lockOfflineService()
	if err != nil {
		return err
	}
	defer control.close()
	stopped := false
	defer func() {
		if stopped {
			// Never restart an unsafe legacy directory after a failed stop.
			if e := control.publish(); e != nil {
				err = errors.Join(err, e)
				return
			}
			err = errors.Join(err, resume())
		}
	}()
	installedVersion := func() (string, error) { return control.version(ctx) }
	current, err := installedVersion()
	if err != nil {
		return err
	}
	s := &Server{dir: config, id: id}
	staged, err := s.newDesktopStaging()
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	descriptor, err := update.StageDesktopDirectory(ctx, source, staged, current, expected)
	if err != nil {
		return err
	}
	// Refuse unreadable saved state rather than validate an empty fallback rule list.
	state, err := store.Load(config)
	if err != nil {
		return err
	}
	s.state = state
	preflight := func(ctx context.Context, bin, assets string) error {
		state, err := store.Load(config)
		if err != nil {
			return err
		}
		s.state = state
		if err := s.checkWithNewVersionAndGeo(ctx, bin, assets); err != nil {
			return err
		}
		if !stopped {
			if err := legacyServiceUpdatesIdle(ctx, id); err != nil {
				return err
			}
			stopped = true
			if err := stop(); err != nil {
				return err
			}
			if err := control.publish(); err != nil {
				return err
			}
			now, err := installedVersion()
			if err != nil {
				return err
			}
			if now != current {
				return errors.New("service changed during offline update; retry")
			}
			// The service can no longer change its files. Re-read saved rules after exit.
			state, err = store.Load(config)
			if err != nil {
				return err
			}
			s.state = state
			if err := s.checkWithNewVersionAndGeo(ctx, bin, assets); err != nil {
				return err
			}
		}
		return nil
	}
	if control.snapshot != "" {
		managed, e := geopack.Managed(control.snapshot)
		if e != nil {
			return e
		}
		assets := control.snapshot
		if managed {
			assets = staged
		}
		if e := preflight(ctx, filepath.Join(staged, updatebundle.Names(runtime.GOOS)[0]), assets); e != nil {
			return e
		}
	}
	if desktopInstalledMatches(descriptor, service.BinaryPath(), service.InstallDir()) {
		return preflight(ctx, filepath.Join(staged, updatebundle.Names(runtime.GOOS)[0]), service.InstallDir())
	}
	m := update.Masir{Binary: service.BinaryPath(), AssetDir: service.InstallDir(), Privileged: true, PreflightGeo: preflight}
	runner := update.NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, filepath.Dir(staged))
	done := make(chan error, 1)
	if _, err := runner.Start(func(_ context.Context, r *update.Reporter) error { e := m.Install(ctx, r, staged); done <- e; return e }); err != nil {
		return err
	}
	if err := <-done; err != nil {
		return fmt.Errorf("offline desktop update: %w", err)
	}
	return nil
}

// Windows legacy directories may be writable through old UAC-owned objects. A
// control snapshots authenticated code and publishes a fresh protected directory;
// Unix controls only hold the existing root-controlled service lock.
type offlineServiceControl struct {
	close    func()
	version  func(context.Context) (string, error)
	snapshot string
	publish  func() error
}

func readOfflineVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, service.BinaryPath(), "--version").Output()
	if e != nil {
		return "", e
	}
	fields := strings.Fields(string(b))
	if len(fields) != 3 || fields[0] != "masir" || fields[1] != "version" || !update.IsRelease(fields[2]) {
		return "", errors.New("installed service has no valid release version")
	}
	return fields[2], nil
}

// Legacy services predate the shared writer lock. Refuse their active jobs before
// asking the supervisor to stop them; never cancel an install in order to upgrade.
func legacyServiceUpdatesIdle(ctx context.Context, id identity.Identity) error {
	sock := paths.PrivilegedSocketPath(id.UID)
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	probe, err := transport.DialContext(probeCtx, sock)
	cancel()
	if err != nil {
		if legacyListenerAbsent(err) {
			return nil
		}
		return fmt.Errorf("cannot determine legacy daemon update state: %w", err)
	}
	probe.Close()
	conn, daemon, err := client.DialSocket(sock)
	if err != nil {
		return err
	}
	defer conn.Close()
	return requireIdleUpdateJobs(ctx, daemon)
}

func requireIdleUpdateJobs(ctx context.Context, daemon pb.DaemonServiceClient) error {
	for _, target := range []pb.UpdateTarget{pb.UpdateTarget_UPDATE_TARGET_MASIR, pb.UpdateTarget_UPDATE_TARGET_GEO} {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		stream, err := daemon.WatchUpdate(ctx, &pb.WatchUpdateRequest{Target: target})
		if err != nil {
			cancel()
			return err
		}
		reply, err := stream.Recv()
		cancel()
		if err != nil {
			return err
		}
		switch reply.GetJob().GetState() {
		case pb.UpdateState_UPDATE_STATE_UNSPECIFIED, pb.UpdateState_UPDATE_STATE_DONE, pb.UpdateState_UPDATE_STATE_UP_TO_DATE, pb.UpdateState_UPDATE_STATE_FAILED, pb.UpdateState_UPDATE_STATE_STOPPED, pb.UpdateState_UPDATE_STATE_BLOCKED:
		default:
			return errors.New("a daemon or geo update is active; finish or cancel it before installing the desktop update")
		}
	}
	return nil
}

// A timeout, permission failure or busy pipe says nothing about running jobs.
func legacyListenerAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}
