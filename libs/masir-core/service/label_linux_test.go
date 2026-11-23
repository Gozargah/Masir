//go:build linux

package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A machine without SELinux must be a no-op: no commands run, no error, so the
// service install never depends on labeling tools it doesn't have.
func TestEnsureBinaryLabelSkipsWithoutSELinux(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	selinuxEnforce = filepath.Join(t.TempDir(), "not-there")
	var calls []string
	runLabelCommand = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	if err := EnsureBinaryLabel("/opt/masir/masir"); err != nil {
		t.Fatalf("label without SELinux: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("ran %v with no SELinux", calls)
	}
}

// The label itself (chcon) and the persistent rule (semanage fcontext) both go out,
// in that order — chcon is what takes effect now, the rule survives a relabel.
func TestEnsureBinaryLabelLabelsAndPersists(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "1")
	var calls []string
	runLabelCommand = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	if err := EnsureBinaryLabel("/usr/local/lib/masir/masir"); err != nil {
		t.Fatalf("label: %v", err)
	}
	want := []string{
		"chcon -t bin_t /usr/local/lib/masir/masir",
		"semanage fcontext -a -t bin_t /usr/local/lib/masir/masir",
	}
	if len(calls) != len(want) {
		t.Fatalf("ran %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
}

// An existing rule for the path — possibly with another type, which the next
// restorecon would apply over the chcon — must be repaired with -m after -a
// refuses to add a duplicate.
func TestEnsureBinaryLabelRepairsAnExistingRule(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "1")
	var calls []string
	runLabelCommand = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "semanage" && args[1] == "-a" {
			return errors.New("Value already defined")
		}
		return nil
	}
	if err := EnsureBinaryLabel("/usr/local/lib/masir/masir"); err != nil {
		t.Fatalf("label: %v", err)
	}
	want := []string{
		"chcon -t bin_t /usr/local/lib/masir/masir",
		"semanage fcontext -a -t bin_t /usr/local/lib/masir/masir",
		"semanage fcontext -m -t bin_t /usr/local/lib/masir/masir",
	}
	if len(calls) != len(want) {
		t.Fatalf("ran %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
}

// semanage not being installed must not fail the install: the label is already
// applied by chcon, and the persistent rule is only the belt to those braces.
func TestEnsureBinaryLabelPersistsBestEffort(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "1")
	runLabelCommand = func(name string, _ ...string) error {
		if name == "semanage" {
			return errors.New("executable file not found in $PATH")
		}
		return nil
	}
	if err := EnsureBinaryLabel("/usr/local/lib/masir/masir"); err != nil {
		t.Fatalf("label with no semanage: %v", err)
	}
}

// Enforcing with chcon itself failing means the daemon would start confined and
// every connection would be denied: that install must fail loudly, not ship broken.
func TestEnsureBinaryLabelFailsWhenEnforcedLabelFails(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "1")
	runLabelCommand = func(name string, _ ...string) error {
		if name == "chcon" {
			return errors.New("permission denied")
		}
		return nil
	}
	if err := EnsureBinaryLabel("/usr/local/lib/masir/masir"); err == nil {
		t.Fatal("label failed while enforcing; want an error")
	}
}

// Permissive never blocks a connection, so a failed label there is a shrug:
// the install proceeds.
func TestEnsureBinaryLabelPermissiveStaysQuiet(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "0")
	runLabelCommand = func(string, ...string) error {
		return errors.New("permission denied")
	}
	if err := EnsureBinaryLabel("/usr/local/lib/masir/masir"); err != nil {
		t.Fatalf("permissive label error: %v", err)
	}
}

// LabelOK is what `masir update` consults to decide whether content that already
// matches still needs a repair pass: a wrong label means not up to date. The
// chcon here is real, unprivileged, on a file the test owns — the same call an
// update makes, with the same result an update would see.
func TestLabelOK(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })

	// Without SELinux there is never anything to fix.
	selinuxEnforce = filepath.Join(t.TempDir(), "not-there")
	if !LabelOK("/any/path") {
		t.Error("LabelOK without SELinux = false; want true")
	}
	if _, err := os.Stat("/sys/fs/selinux/enforce"); err != nil {
		t.Skip("no mounted selinuxfs here; the label read cannot be exercised")
	}
	writeEnforce(t, "1")
	f := filepath.Join(t.TempDir(), "masir")
	if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A fresh file carries its directory's default label, not bin_t.
	if LabelOK(f) {
		t.Fatalf("LabelOK on a default-labeled file = true; label is %q", labelOf(f))
	}
	if err := EnsureBinaryLabel(f); err != nil {
		t.Fatalf("real label: %v", err)
	}
	if !LabelOK(f) {
		t.Errorf("LabelOK after bin_t = false; label is %q", labelOf(f))
	}
}

// labelOf reads the label the daemon's label check reads, for failure messages.
func labelOf(path string) string {
	buf := make([]byte, 256)
	n, err := syscall.Getxattr(path, "security.selinux", buf)
	if err != nil {
		return err.Error()
	}
	return string(buf[:n])
}

func writeEnforce(t *testing.T, content string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "enforce")
	if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selinuxEnforce = p
}

func TestEnsureBinaryReadyRepairsExecutableMode(t *testing.T) {
	oldEnforce := selinuxEnforce
	t.Cleanup(func() { selinuxEnforce = oldEnforce })
	selinuxEnforce = filepath.Join(t.TempDir(), "no-selinux")
	bin := filepath.Join(t.TempDir(), "masir")
	if err := os.WriteFile(bin, []byte("matching binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if BinaryReady(bin) {
		t.Fatal("a nonexecutable binary was accepted")
	}
	if err := EnsureBinaryReady(bin); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !BinaryReady(bin) || after.Mode().Perm() != 0o755 || !os.SameFile(before, after) {
		t.Fatalf("repair must make the same inode executable: got mode %v", after.Mode())
	}
}
