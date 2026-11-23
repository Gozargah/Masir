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

	"golang.org/x/sys/unix"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// The regression this exists for: an update rewrote the service binary through
// replaceFile — a new inode, born with the directory's default label — and nothing
// labeled it, so the next systemd start ran the daemon in confined init_t where
// every connection is denied (docs/selinux.md, seen live twice on Fedora 43).
// The update must hand the service a bin_t file, and must leave every other
// binary's label alone.
//
// The chcon here is real and unprivileged — a file this test owns in a temp dir —
// so it fails on master (no label call at all) and passes with the fix.
func TestMasirUpdateLabelsTheServiceBinary(t *testing.T) {
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
	bin := filepath.Join(dir, exe("masir"))
	os.WriteFile(bin, []byte("old masir"), 0o755)
	svcBin := filepath.Join(dir, "service", exe("masir"))
	os.MkdirAll(filepath.Dir(svcBin), 0o755)
	os.WriteFile(svcBin, []byte("old service masir"), 0o755)
	clientBin := filepath.Join(dir, "client", exe("masir"))
	os.MkdirAll(filepath.Dir(clientBin), 0o755)
	os.WriteFile(clientBin, []byte("old client masir"), 0o755)

	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
	// Install publishes RESTARTING before it calls Restart, and wait returns on
	// that state — so the assertion waits for the callback itself, not just the
	// published state.
	restarted := make(chan struct{})
	var once sync.Once
	restart := func() { once.Do(func() { close(restarted) }) }
	task := Masir{Clients: clients(), Current: "0.1.0", Binary: bin, ServiceBinary: svcBin, ClientBinary: clientBin, Privileged: true, WorkDir: r.workDir, Restart: restart}
	if _, err := r.Start(task.Run); err != nil {
		t.Fatal(err)
	}
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_RESTARTING || job.Error != "" {
		t.Fatalf("job = %v (%s)", job.State, job.Error)
	}

	// The service's copy is the file systemd will exec: it must be bin_t.
	if got := labelOf(t, svcBin); !strings.Contains(got, ":bin_t:") {
		t.Errorf("service binary label = %q, want bin_t — the next start would be confined", got)
	}
	// The running binary and the CLI's own copy keep whatever label suits their
	// location: bin_t is the service's type, not a generic "new file" stamp.
	if got := labelOf(t, bin); strings.Contains(got, ":bin_t:") {
		t.Errorf("running binary label = %q; only the service copy should be labeled", got)
	}
	if got := labelOf(t, clientBin); strings.Contains(got, ":bin_t:") {
		t.Errorf("CLI copy label = %q; only the service copy should be labeled", got)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Error("the update published RESTARTING but never ran the restart callback")
	}
}

// labelOf reads security.selinux the way the update's own label check does.
func labelOf(t *testing.T, path string) string {
	t.Helper()
	buf := make([]byte, 256)
	n, err := unix.Getxattr(path, "security.selinux", buf)
	if err != nil {
		return err.Error()
	}
	return string(buf[:n])
}
