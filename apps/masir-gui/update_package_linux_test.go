//go:build linux

package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fakeInfo struct {
	mode fs.FileMode
	uid  uint32
}

func (f fakeInfo) Name() string       { return "" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return &syscall.Stat_t{Uid: f.uid} }

func TestRootOwnedPath(t *testing.T) {
	const bin = "/opt/masir-desktop/bin/masir"
	tree := func(change string, info fakeInfo) func(string) (os.FileInfo, error) {
		return func(p string) (os.FileInfo, error) {
			if p == change {
				return info, nil
			}
			if p == bin {
				return fakeInfo{mode: 0o755}, nil
			}
			return fakeInfo{mode: fs.ModeDir | 0o755}, nil
		}
	}
	for _, c := range []struct {
		name   string
		change string
		info   fakeInfo
		ok     bool
	}{
		{"all root 0755", "", fakeInfo{}, true},
		{"group-writable parent", "/opt/masir-desktop", fakeInfo{mode: fs.ModeDir | 0o775}, false},
		{"parent owned by a person", "/opt", fakeInfo{mode: fs.ModeDir | 0o755, uid: 1000}, false},
		{"symlink component", "/opt/masir-desktop/bin", fakeInfo{mode: fs.ModeSymlink | 0o755}, false},
		{"binary 0777", bin, fakeInfo{mode: 0o777}, false},
		{"root itself writable", "/", fakeInfo{mode: fs.ModeDir | 0o777}, false},
	} {
		err := rootOwnedPath(bin, tree(c.change, c.info))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
	if rootOwnedPath("opt/masir", tree("", fakeInfo{})) == nil {
		t.Error("a relative path passed")
	}
}

// The real lstat: a file in the person's temp dir is never trusted, and an installed
// package's CLI is, when this machine has one.
func TestRootOwnedPathOnDisk(t *testing.T) {
	f := filepath.Join(t.TempDir(), "masir")
	if err := os.WriteFile(f, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 && rootOwnedPath(f, os.Lstat) == nil {
		t.Errorf("%s passed", f)
	}
	const installed = "/opt/masir-desktop/bin/masir"
	if _, err := os.Lstat(installed); err != nil {
		t.Skip("no installed package")
	}
	if err := rootOwnedPath(installed, os.Lstat); err != nil {
		t.Errorf("installed CLI: %v", err)
	}
}

func TestParseFetchOutput(t *testing.T) {
	out := "progress 10 100\ngarbage\nprogress x y\nprogress 5\n" +
		"progress 100 100\n  Run: masir something\npackage /c/masir desktop.deb\nchecksums /c/sums.txt\nsignature /c/sums.txt.minisig"
	var got [][2]int64
	f, err := parseFetchOutput(strings.NewReader(out), func(done, total int64) { got = append(got, [2]int64{done, total}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != [2]int64{10, 100} || got[1] != [2]int64{100, 100} {
		t.Errorf("progress %v", got)
	}
	want := packageFiles{"/c/masir desktop.deb", "/c/sums.txt", "/c/sums.txt.minisig"}
	if f != want {
		t.Errorf("files %+v, want %+v", f, want)
	}
	for _, partial := range []string{"", "progress 1 2\n", "checksums /a\nsignature /b\n", "package /p\nchecksums /a\n"} {
		if _, err := parseFetchOutput(strings.NewReader(partial), func(int64, int64) {}); err == nil {
			t.Errorf("%q: no error", partial)
		}
	}
}

// fakeCLI is a masir that runs script for `desktop fetch-package`.
func fakeCLI(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "masir")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunFetchExitCodes(t *testing.T) {
	for _, c := range []struct {
		exit   int
		stderr string
		text   string
		remove bool
	}{
		{20, "error: Can't reach github.com", "GitHub isn't reachable. Connect Masir and try again.", false},
		{10, "error: sha256 mismatch", "The download didn't match the signed release; nothing was installed.", true},
		{1, "error: Boom", "Boom", false},
	} {
		bin := fakeCLI(t, fmt.Sprintf("echo 'progress 1 2'\necho '  masir update'\necho %q >&2\nexit %d", c.stderr, c.exit))
		_, err := runFetch(context.Background(), bin, packageFetch{"1.2.3", "deb", t.TempDir(), ""}, func(int64, int64) {})
		kind, code, ok := elevationKindOf(err)
		if !ok || kind != elevationFailed || code != c.exit {
			t.Errorf("exit %d: kind=%d code=%d ok=%v (%v)", c.exit, kind, code, ok, err)
		}
		text, remove := packageFailure(err, packageFiles{pkg: "/p.deb"}, "deb")
		if text != c.text || remove != c.remove {
			t.Errorf("exit %d: %q remove=%v, want %q %v", c.exit, text, remove, c.text, c.remove)
		}
	}
}

func TestRunFetchArgsAndFiles(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	bin := fakeCLI(t, `printf '%s\n' "$@" > `+log+`
echo "progress 2 2"
echo "package /d/p.deb"; echo "checksums /d/s"; echo "signature /d/s.minisig"`)
	var progress int
	f, err := runFetch(context.Background(), bin, packageFetch{"1.2.3", "rpm", "/d", "http://127.0.0.1:7890"}, func(int64, int64) { progress++ })
	if err != nil || f != (packageFiles{"/d/p.deb", "/d/s", "/d/s.minisig"}) || progress != 1 {
		t.Fatalf("%+v %v progress=%d", f, err, progress)
	}
	args, _ := os.ReadFile(log)
	want := "desktop\nfetch-package\n--version\n1.2.3\n--format\nrpm\n--dir\n/d\n--proxy\nhttp://127.0.0.1:7890\n"
	if string(args) != want {
		t.Errorf("args %q", args)
	}
	if _, err := runFetch(context.Background(), bin, packageFetch{"1.2.3", "deb", "/d", ""}, func(int64, int64) {}); err != nil {
		t.Fatal(err)
	}
	if args, _ := os.ReadFile(log); strings.Contains(string(args), "--proxy") {
		t.Errorf("empty proxy passed: %q", args)
	}
}

// installBlockOf a file nobody packaged, in a folder the person writes, is blockNone;
// the installed .deb on this machine, if any, is blockPackageDeb.
func TestInstallBlockOf(t *testing.T) {
	f := filepath.Join(t.TempDir(), "masir-desktop")
	if err := os.WriteFile(f, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if b := installBlockOf(f); b != blockNone {
		t.Errorf("temp file: %d", b)
	}
	const installed = "/opt/masir-desktop/masir-desktop"
	if _, err := os.Stat(installed); err != nil {
		t.Skip("no installed package")
	}
	if b := installBlockOf(installed); b != blockPackageDeb && b != blockPackageRpm {
		t.Errorf("installed app: %d", b)
	}
}
