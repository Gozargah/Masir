package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/update"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) ImportDesktopUpdate(stream grpc.ClientStreamingServer[pb.ImportDesktopUpdateRequest, pb.ImportDesktopUpdateResponse]) error {
	ctx := stream.Context()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Minute || time.Until(deadline) <= 0 {
		return status.Error(codes.InvalidArgument, "desktop import requires a deadline within five minutes")
	}
	if s.upd == nil {
		return status.Error(codes.FailedPrecondition, "updates are unavailable")
	}
	release, err := s.lockUpdate()
	if err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	defer release()
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.File != "" || len(first.Data) != 0 {
		return status.Error(codes.InvalidArgument, "first message must contain only signed metadata")
	}
	d, err := update.VerifyDesktopDescriptor(first.ManifestBytes, first.Signature, Version)
	if err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if s.xray.AssetDir() == "" {
		return status.Error(codes.FailedPrecondition, "no active geo directory")
	}
	dir, err := s.newDesktopStaging()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := receiveDesktopFiles(dir, d, stream.Recv); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m := update.Masir{Binary: executable(), ServiceBinary: s.serviceBinary(), Privileged: s.id.Privileged, AssetDir: s.xray.AssetDir(),
		PreflightGeo: s.checkWithNewVersionAndGeo,
	}
	// A desktop commit can fail after service staging succeeded. Retrying must not
	// overwrite the previous release backups with the already staged new release.
	if desktopInstalledMatches(d, m.Binary, m.AssetDir) {
		if err := m.PreflightGeo(ctx, filepath.Join(dir, updatebundle.Names(runtime.GOOS)[0]), dir); err != nil {
			return err
		}
		return stream.SendAndClose(&pb.ImportDesktopUpdateResponse{Version: d.Version})
	}
	// A private runner supplies progress without exposing a partial import as a normal
	// downloadable job. Installation completes synchronously before staging is removed.
	runner := update.NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, dir)
	done := make(chan error, 1)
	if _, err := runner.Start(func(_ context.Context, r *update.Reporter) error {
		err := m.Install(ctx, r, dir)
		done <- err
		return err
	}); err != nil {
		return err
	}
	if err := <-done; err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return stream.SendAndClose(&pb.ImportDesktopUpdateResponse{Version: d.Version})
}

// receiveDesktopFiles only writes the fixed component names, within signed size
// limits, into the daemon's private directory. It never executes incoming bytes.
func receiveDesktopFiles(dir string, d updatebundle.Descriptor, recv func() (*pb.ImportDesktopUpdateRequest, error)) error {
	for _, name := range updatebundle.Names(runtime.GOOS) {
		expected := d.Files[name]
		path := filepath.Join(dir, name)
		mode := os.FileMode(0o644)
		if name == updatebundle.Names(runtime.GOOS)[0] {
			mode = 0o755
		}
		f, err := openDesktopComponent(path, mode)
		if err != nil {
			return err
		}
		h := sha256.New()
		var received int64
		for received < expected.Size {
			chunk, e := recv()
			if e != nil {
				f.Close()
				return fmt.Errorf("receive %s: %w", name, e)
			}
			if chunk.File != name || len(chunk.Data) == 0 || len(chunk.Data) > 256<<10 || len(chunk.ManifestBytes) != 0 || len(chunk.Signature) != 0 || int64(len(chunk.Data)) > expected.Size-received {
				f.Close()
				return status.Error(codes.InvalidArgument, "invalid component chunk")
			}
			if _, e := io.MultiWriter(f, h).Write(chunk.Data); e != nil {
				f.Close()
				return e
			}
			received += int64(len(chunk.Data))
		}
		if err := f.Close(); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != expected.SHA256 {
			return status.Error(codes.PermissionDenied, "component hash mismatch")
		}
	}
	if _, err := recv(); !errors.Is(err, io.EOF) {
		return status.Error(codes.InvalidArgument, "unexpected trailing component data")
	}
	return nil
}

// Shared by streaming and elevated imports: retrying identical verified bytes
// must never replace the original rollback backups with the new release.
func desktopInstalledMatches(d updatebundle.Descriptor, binary, assets string) bool {
	for _, name := range updatebundle.Names(runtime.GOOS) {
		path := filepath.Join(assets, name)
		if name == updatebundle.Names(runtime.GOOS)[0] {
			path = binary
		}
		f, e := os.Open(path)
		if e != nil {
			return false
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(f, d.Files[name].Size+1))
		f.Close()
		if e != nil || n != d.Files[name].Size || hex.EncodeToString(h.Sum(nil)) != d.Files[name].SHA256 {
			return false
		}
	}
	return true
}
