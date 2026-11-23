package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Gozargah/Masir/libs/masir-core/service"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// A persistent label failure must leave the installed inode and rollback
// backup intact. On enforcing hosts, also check the actual label survives.
func TestLabelFailurePreservesInstalledBinary(t *testing.T) {
	oldEnsure := labelBinary
	oldSvc := serviceBinaryPath
	t.Cleanup(func() {
		labelBinary = oldEnsure
		serviceBinaryPath = oldSvc
	})

	gh := newFakeGitHub(t)
	name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	arc := archive(t, name, map[string]string{exe("masir"): "new masir"})
	gh.publish(ReleaseRepo, "v0.2.0", map[string][]byte{
		name:            arc,
		"checksums.txt": []byte(fmt.Sprintf("%s  %s\n%s  other.tar.gz\n", sum(arc), name, strings.Repeat("0", 64))),
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, exe("masir"))
	if err := os.WriteFile(bin, []byte("old masir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if service.SelinuxEnforcing() {
		if err := service.LabelBinary(bin); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".previous", []byte("earlier masir"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This binary is the service's copy, so Install must label it — and that is
	// exactly the step forced to fail here.
	serviceBinaryPath = func() string { return bin }
	var labeled []string
	labelBinary = func(p string) error {
		labeled = append(labeled, p)
		return errors.New("chcon: permission denied")
	}

	var lastErr string
	install := func() pb.UpdateState {
		t.Helper()
		r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
		task := Masir{Clients: clients(), Current: "0.1.0", Binary: bin, WorkDir: r.workDir}
		if _, err := r.Start(task.Run); err != nil {
			t.Fatal(err)
		}
		job := wait(t, r)
		lastErr = job.Error
		return job.State
	}
	install()
	if !strings.Contains(lastErr, "label") {
		t.Fatalf("first install error = %q, want the label failure", lastErr)
	}
	if got, _ := os.ReadFile(bin); string(got) != "old masir" {
		t.Errorf("binary after the failed label = %q; the install must be undone", got)
	}
	current, err := os.Stat(bin)
	if err != nil || !os.SameFile(info, current) || (service.SelinuxEnforcing() && !service.LabelOK(bin)) {
		t.Fatalf("failed install changed the live inode or its label: %v", err)
	}
	if got, _ := os.ReadFile(bin + ".previous"); string(got) != "earlier masir" {
		t.Errorf("rollback copy after the failed label = %q; want the earlier binary", got)
	}
	if len(labeled) != 1 || labeled[0] != bin+".new" {
		t.Errorf("label calls = %v; want only the staged replacement", labeled)
	}

	// A retry must not turn the failed copy into the rollback target.
	labelBinary = service.LabelBinary
	if state := install(); state != pb.UpdateState_UPDATE_STATE_DONE {
		t.Fatalf("retry state = %v (%s)", state, lastErr)
	}
	if got, _ := os.ReadFile(bin); string(got) != "new masir" {
		t.Errorf("binary after the retry = %q; want the new one", got)
	}
	if got, _ := os.ReadFile(bin + ".previous"); string(got) != "old masir" {
		t.Errorf("retry saved %q over the rollback copy; want the pre-update binary", got)
	}
}

// A separate service copy must be labeled before the primary binary changes.
func TestServiceLabelFailurePreservesBothBinaries(t *testing.T) {
	oldEnsure := labelBinary
	oldSvc := serviceBinaryPath
	t.Cleanup(func() {
		labelBinary = oldEnsure
		serviceBinaryPath = oldSvc
	})

	gh := newFakeGitHub(t)
	name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	arc := archive(t, name, map[string]string{exe("masir"): "new masir"})
	gh.publish(ReleaseRepo, "v0.2.0", map[string][]byte{
		name:            arc,
		"checksums.txt": []byte(fmt.Sprintf("%s  %s\n%s  other.tar.gz\n", sum(arc), name, strings.Repeat("0", 64))),
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, exe("masir"))
	svc := filepath.Join(dir, "service", exe("masir"))
	if err := os.MkdirAll(filepath.Dir(svc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("old running"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc, []byte("old service"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only the service copy fails its label check, as a real one would on a host
	// where chcon refuses the type change.
	serviceBinaryPath = func() string { return svc }
	labelBinary = func(p string) error {
		if p == svc+".new" {
			return errors.New("chcon: permission denied")
		}
		return nil
	}

	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
	task := Masir{Clients: clients(), Current: "0.1.0", Binary: bin, ServiceBinary: svc, WorkDir: r.workDir}
	if _, err := r.Start(task.Run); err != nil {
		t.Fatal(err)
	}
	job := wait(t, r)
	if !strings.Contains(job.Error, "label") {
		t.Fatalf("install error = %q, want the service label failure", job.Error)
	}
	for _, c := range []struct{ path, want string }{
		{bin, "old running"},
		{svc, "old service"},
	} {
		if got, _ := os.ReadFile(c.path); string(got) != c.want {
			t.Errorf("%s = %q; want %q", c.path, got, c.want)
		}
	}
}
