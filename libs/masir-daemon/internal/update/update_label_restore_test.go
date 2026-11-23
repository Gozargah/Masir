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
	ensureBinaryLabel = func(string) error { return errors.New("semanage: permission denied") }

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
