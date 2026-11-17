package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	masird "github.com/Gozargah/Masir/libs/masir-daemon"
)

func TestReportUsesACodedExit(t *testing.T) {
	if got := report(coded(11, errors.New("x"))); got != 11 {
		t.Fatalf("coded exit = %d, want 11", got)
	}
	if got := report(errors.New("x")); got != 1 {
		t.Fatalf("plain exit = %d, want 1", got)
	}
}

func TestReleaseBaseOverrideIsLoopbackOnly(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8799", "http://localhost:1", "http://[::1]:2"} {
		t.Setenv("MASIR_DESKTOP_RELEASE_BASE", ok)
		if got, err := releaseBase(); err != nil || got != ok {
			t.Fatalf("%s: got %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"https://evil.example", "http://10.0.0.1:1", "::"} {
		t.Setenv("MASIR_DESKTOP_RELEASE_BASE", bad)
		if got, err := releaseBase(); err == nil {
			t.Fatalf("%s accepted as %q", bad, got)
		}
	}
	t.Setenv("MASIR_DESKTOP_RELEASE_BASE", "")
	if got, err := releaseBase(); err != nil || got != masird.DesktopReleaseBase {
		t.Fatalf("unset: got %q, %v", got, err)
	}
}

// The signing key is baked in at build time, so a real signed release can't be served
// from a test: the download is stubbed and only the command's protocol is checked.
func TestFetchPackagePrintsMachineReadableResult(t *testing.T) {
	old := fetchPackage
	defer func() { fetchPackage = old }()
	var gotProxy string
	fetchPackage = func(_ context.Context, base, version, format, arch, dir, proxy string, progress func(done, total int64)) (string, string, string, error) {
		gotProxy = proxy
		for i := int64(1); i <= 100; i++ { // faster than the throttle
			progress(i, 100)
		}
		return dir + "/p.deb", dir + "/checksums.txt", dir + "/checksums.txt.minisig", nil
	}
	a := args{flags: map[string][]string{"version": {"0.1.7"}, "format": {"deb"}, "dir": {"/d"}, "proxy": {"http://127.0.0.1:1"}}}
	var out bytes.Buffer
	if err := fetchDesktopPackage(&out, a); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	n := len(lines)
	if n < 4 || n > 10 {
		t.Fatalf("got %d lines: %q", n, lines)
	}
	if lines[n-4] != "progress 100 100" || lines[n-3] != "package /d/p.deb" ||
		lines[n-2] != "checksums /d/checksums.txt" || lines[n-1] != "signature /d/checksums.txt.minisig" {
		t.Fatalf("bad tail: %q", lines)
	}
	if gotProxy != "http://127.0.0.1:1" {
		t.Fatalf("proxy = %q", gotProxy)
	}

	fetchPackage = func(context.Context, string, string, string, string, string, string, func(int64, int64)) (string, string, string, error) {
		return "", "", "", masird.ErrPackageMismatch
	}
	err := fetchDesktopPackage(&out, a)
	if code := report(err); code != exitMismatch {
		t.Fatalf("mismatch exit = %d", code)
	}
	a.flags["format"] = []string{"zip"}
	if err := fetchDesktopPackage(&out, a); err == nil {
		t.Fatal("format zip accepted")
	}
}
