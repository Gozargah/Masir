//go:build linux

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// The shape a real service update has: the daemon runs from the service's own
// copy (Binary == the service path, ServiceBinary empty because there is nothing
// different to sync — see Server.serviceBinary), and that is the path line one of
// Install replaces. It must be labeled too, through the seam that names the
// service's path.
func TestMasirUpdateLabelsTheRunningServiceBinary(t *testing.T) {
	if _, err := os.Stat("/sys/fs/selinux/enforce"); err != nil {
		t.Skip("no mounted selinuxfs; there is no label to check")
	}
	gh := newFakeGitHub(t)
	name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	arc := archive(t, name, map[string]string{exe("masir"): "new masir", "geosite.dat": "x"})
	gh.publish(ReleaseRepo, "v0.2.0", map[string][]byte{
		name:            arc,
		"checksums.txt": []byte(fmt.Sprintf("%s  %s\n%s  other.tar.gz\n", sum(arc), name, strings.Repeat("0", 64))),
	})

	dir := t.TempDir()
	svcBin := filepath.Join(dir, exe("masir"))
	os.WriteFile(svcBin, []byte("old service masir"), 0o755)
	// A binary that is NOT the service's copy — a session daemon's own file, say —
	// must keep its label: bin_t is not a generic stamp for anything an update
	// rewrites.
	other := filepath.Join(dir, "elsewhere", exe("masir"))
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, []byte("old session masir"), 0o755)

	oldPath := serviceBinaryPath
	t.Cleanup(func() { serviceBinaryPath = oldPath })

	var restarted atomic.Bool
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())

	// The service shape: Binary is the service's path, ServiceBinary empty.
	serviceBinaryPath = func() string { return svcBin }
	task := Masir{Clients: clients(), Current: "0.1.0", Binary: svcBin, Privileged: true, WorkDir: r.workDir, Restart: func() { restarted.Store(true) }}
	if _, err := r.Start(task.Run); err != nil {
		t.Fatal(err)
	}
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_RESTARTING || job.Error != "" {
		t.Fatalf("job = %v (%s)", job.State, job.Error)
	}
	if got := labelOf(t, svcBin); !strings.Contains(got, ":bin_t:") {
		t.Errorf("service binary label = %q, want bin_t — the next start would be confined", got)
	}

	// Not the service's path: updated, but not labeled.
	serviceBinaryPath = func() string { return filepath.Join(dir, "not-this-one") }
	task = Masir{Clients: clients(), Current: "0.1.0", Binary: other, Privileged: true, WorkDir: r.workDir, Restart: func() {}}
	if _, err := r.Start(task.Run); err != nil {
		t.Fatal(err)
	}
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_RESTARTING || job.Error != "" {
		t.Fatalf("second job = %v (%s)", job.State, job.Error)
	}
	if got := labelOf(t, other); strings.Contains(got, ":bin_t:") {
		t.Errorf("non-service binary label = %q; only the service path should be labeled", got)
	}
}
