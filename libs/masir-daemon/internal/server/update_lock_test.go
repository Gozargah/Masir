package server

import (
	"context"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/update"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"testing"
	"time"
)

func TestOfflineCompatibilityRejectsEitherActiveJob(t *testing.T) {
	for _, target := range []pb.UpdateTarget{pb.UpdateTarget_UPDATE_TARGET_MASIR, pb.UpdateTarget_UPDATE_TARGET_GEO} {
		t.Run(target.String(), func(t *testing.T) {
			s := newTestServer(t)
			s.initUpdates(t.TempDir())
			daemon := dialInProcess(t, s)
			if err := requireIdleUpdateJobs(context.Background(), daemon); err != nil {
				t.Fatal(err)
			}
			r := s.upd.masir
			if target == pb.UpdateTarget_UPDATE_TARGET_GEO {
				r = s.upd.geo
			}
			done := make(chan struct{})
			_, err := r.Start(func(ctx context.Context, _ *update.Reporter) error { defer close(done); <-ctx.Done(); return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			if err := requireIdleUpdateJobs(context.Background(), daemon); err == nil {
				t.Fatal("offline helper accepted active job")
			}
			r.Stop()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("job did not stop")
			}
		})
	}
}
