package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// The regression this exists for: when the label step failed after replaceFile,
// the failed binary used to stay in place — and a retry then saved it over the
// .previous copy, destroying the version `masir daemon rollback` needs. A failed
// install must undo itself and leave the pre-update binary as the rollback target.
func TestLabelFailureRestoresPreviousBinary(t *testing.T) {
	oldEnsure := ensureBinaryLabel
	oldSvc := serviceBinaryPath
	t.Cleanup(func() {
		ensureBinaryLabel = oldEnsure
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
	// This binary is the service's copy, so Install must label it — and that is
	// exactly the step forced to fail here.
	serviceBinaryPath = func() string { return bin }
	var labeled []string
	ensureBinaryLabel = func(p string) error {
		labeled = append(labeled, p)
		return errors.New("semanage: permission denied")
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
	if got, _ := os.ReadFile(bin + ".previous"); string(got) != "old masir" {
		t.Errorf("rollback copy after the failed label = %q; want the pre-update binary", got)
	}
	// The restore recreated the file — a new inode — so it must have relabeled it.
	if len(labeled) != 2 || labeled[1] != bin {
		t.Errorf("label calls = %v; want the install attempt and one relabel of the restore", labeled)
	}

	// A retry must not turn the failed copy into the rollback target.
	ensureBinaryLabel = func(string) error { return nil }
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

// When the running binary and the service copy are different files, a failure on
// the service copy must undo BOTH replacements: a retry would otherwise save the
// failed copies over the .previous backups of both files.
func TestServiceLabelFailureRestoresBothBinaries(t *testing.T) {
	oldEnsure := ensureBinaryLabel
	oldSvc := serviceBinaryPath
	t.Cleanup(func() {
		ensureBinaryLabel = oldEnsure
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
	// where semanage refuses the type change.
	serviceBinaryPath = func() string { return svc }
	ensureBinaryLabel = func(p string) error {
		if p == svc {
			return errors.New("semanage: permission denied")
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
		{bin + ".previous", "old running"},
		{svc, "old service"},
		{svc + ".previous", "old service"},
	} {
		if got, _ := os.ReadFile(c.path); string(got) != c.want {
			t.Errorf("%s = %q; want %q", c.path, got, c.want)
		}
	}
}
