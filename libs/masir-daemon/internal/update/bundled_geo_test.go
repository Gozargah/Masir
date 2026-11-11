package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func TestGeoUpdateDoesNotOverwriteBundledCache(t *testing.T) {
	assets := t.TempDir()
	os.WriteFile(filepath.Join(assets, geopack.CacheMarker), []byte("bundle"), 0o600)
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_GEO, t.TempDir())
	if _, err := r.Start(Geo{AssetDir: assets, WorkDir: r.workDir}.Run); err != nil {
		t.Fatal(err)
	}
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, "update the app") {
		t.Fatalf("job: %v", job)
	}
}
