//go:build linux

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/service"

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

	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
	// Install publishes RESTARTING before it calls Restart, and wait returns on
	// that state — so the assertion waits for the callback itself.
	restarted := make(chan struct{})
	var once sync.Once
	restart := func() { once.Do(func() { close(restarted) }) }

	// The service shape: Binary is the service's path, ServiceBinary empty.
	serviceBinaryPath = func() string { return svcBin }
	task := Masir{Clients: clients(), Current: "0.1.0", Binary: svcBin, Privileged: true, WorkDir: r.workDir, Restart: restart}
	if _, err := r.Start(task.Run); err != nil {
		t.Fatal(err)
	}
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_RESTARTING || job.Error != "" {
		t.Fatalf("job = %v (%s)", job.State, job.Error)
	}
	if got := labelOf(t, svcBin); !strings.Contains(got, ":bin_t:") {
		t.Errorf("service binary label = %q, want bin_t — the next start would be confined", got)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Error("the update published RESTARTING but never ran the restart callback")
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

// The desktop retry uses this check after matching the signed payload. Repair
// the live inode without rewriting it or replacing the original backup.
func TestEnsureServiceReadyRepairsMatchingBinary(t *testing.T) {
	if !service.SelinuxEnforcing() {
		t.Skip("SELinux is not enforcing")
	}
	bin := filepath.Join(t.TempDir(), "masir")
	if err := os.WriteFile(bin, []byte("already installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".previous", []byte("previous release"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if service.LabelOK(bin) {
		t.Fatal("test binary must start with the default SELinux label")
	}
	oldPath := serviceBinaryPath
	t.Cleanup(func() { serviceBinaryPath = oldPath })
	serviceBinaryPath = func() string { return bin }
	if err := (Masir{Binary: bin}).EnsureServiceReady(); err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat(bin)
	if err != nil || !os.SameFile(info, current) || !service.LabelOK(bin) {
		t.Fatalf("retry did not repair the live inode: label %q, stat %v", labelOf(t, bin), err)
	}
	if got, err := os.ReadFile(bin + ".previous"); err != nil || string(got) != "previous release" {
		t.Fatalf("retry changed the rollback backup: %q (%v)", got, err)
	}
}
