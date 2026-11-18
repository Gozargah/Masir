//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyPkexec(t *testing.T) {
	for _, c := range []struct {
		exit   int
		output string
		kind   elevationKind
	}{
		{126, "", elevationCancelled},
		{127, "Error executing command as another user: Not authorized", elevationDenied},
		{127, "Error executing command as another user: No authentication agent found.", elevationUnavailable},
		{127, "Error creating textual authentication agent: Error opening current controlling terminal for the process (`/dev/tty'): No such device or address", elevationUnavailable},
		{1, "boom", elevationFailed},
		{11, "installer busy", elevationFailed},
		{13, "service pending", elevationFailed},
	} {
		e := classifyPkexec(c.exit, c.output)
		kind, code, ok := elevationKindOf(e)
		if !ok || kind != c.kind || code != c.exit {
			t.Errorf("%d %q: kind=%d code=%d ok=%v, want kind=%d", c.exit, c.output, kind, code, ok, c.kind)
		}
		if c.kind != elevationCancelled && !strings.HasPrefix(e.Error(), "root permission wasn't granted: ") {
			t.Errorf("%d: wording %q", c.exit, e.Error())
		}
	}
}

// fakePkexec puts a pkexec on PATH that runs script instead of asking for root.
func fakePkexec(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pkexec"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPkexecAnnouncedSignalsOnce(t *testing.T) {
	fakePkexec(t, "echo authorized; echo working >&2; echo authorized; exit 0")
	calls := 0
	out, err := pkexecAnnounced(func() { calls++ }, "masir")
	if err != nil || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
	if !strings.Contains(string(out), "working") {
		t.Fatalf("output = %q", out)
	}
}

func TestPkexecAnnouncedReportsTheHelper(t *testing.T) {
	fakePkexec(t, "echo authorized; echo 'installer busy' >&2; exit 11")
	_, err := pkexecAnnounced(nil, "masir")
	kind, code, ok := elevationKindOf(err)
	if !ok || kind != elevationFailed || code != 11 || !strings.Contains(err.Error(), "installer busy") || strings.Contains(err.Error(), "authorized") {
		t.Fatalf("err = %v, kind = %d, code = %d", err, kind, code)
	}
	fakePkexec(t, "exit 126")
	if kind, _, _ := elevationKindOf(func() error { _, err := pkexecAnnounced(nil, "masir"); return err }()); kind != elevationCancelled {
		t.Fatalf("dismissed dialog: kind = %d", kind)
	}
	t.Setenv("PATH", t.TempDir())
	if kind, _, _ := elevationKindOf(func() error { _, err := pkexecAnnounced(nil, "masir"); return err }()); kind != elevationUnavailable {
		t.Fatalf("missing pkexec: kind = %d", kind)
	}
}
