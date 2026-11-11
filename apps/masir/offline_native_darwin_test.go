package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/service"
	masird "github.com/Gozargah/Masir/libs/masir-daemon"
)

func nativeAssertProtectedMigration(t *testing.T, original []byte) {
	t.Helper()
	if service.LegacyLocation() {
		t.Fatal("launchd still uses the Homebrew service directory")
	}
	old, e := os.ReadFile(service.LegacyDarwinBinary)
	if e != nil || !bytes.Equal(old, original) {
		t.Fatalf("migration modified legacy executable: %v", e)
	}
	for p := service.BinaryPath(); ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil {
			t.Fatal(e)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			t.Fatalf("migrated target is not root-controlled: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	parent, e := os.Stat(filepath.Dir(service.LegacyDarwinDir))
	if e != nil {
		t.Fatal(e)
	}
	if parent.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		t.Fatal("migration changed the Homebrew parent's owner")
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		a, e := os.ReadFile(filepath.Join(service.LegacyDarwinDir, name))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(service.InstallDir(), name))
		if e != nil || !bytes.Equal(a, b) {
			t.Fatalf("legacy custom data was not preserved: %s %v", name, e)
		}
	}
}
func nativeRejectAlteredLegacy(t *testing.T, payload, who string) {
	original := service.LegacyDarwinBinary
	backup := original + ".fixture-original"
	if e := os.Rename(original, backup); e != nil {
		t.Fatal(e)
	}
	defer func() {
		os.Remove(original)
		if e := os.Rename(backup, original); e != nil {
			t.Fatal(e)
		}
	}()
	if e := os.WriteFile(original, []byte("#!/bin/sh\nexit 91\n"), 0755); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	stopped := false
	e := masird.InstallDesktopOffline(ctx, payload, who, nativeNewVersion, func() error { stopped = true; return nil }, func() error { t.Error("untrusted executable triggered recovery"); return nil })
	if e == nil || !strings.Contains(e.Error(), "not an authenticated") || stopped {
		t.Fatalf("altered legacy executable was not rejected before stop: %v stopped=%v", e, stopped)
	}
	if _, e := os.Stat(service.BinaryPath()); !os.IsNotExist(e) {
		t.Fatalf("untrusted import installed a target: %v", e)
	}
}

func nativePrepareLegacyCustomData(t *testing.T) {
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		f, e := os.OpenFile(filepath.Join(service.LegacyDarwinDir, name), os.O_WRONLY|os.O_APPEND, 0)
		if e != nil {
			t.Fatal(e)
		}
		// A valid unknown protobuf field changes provenance without changing entries.
		_, e = f.Write([]byte{0xa0, 0x06, 0x01})
		closeErr := f.Close()
		if e != nil {
			t.Fatal(e)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}

func nativeReproduceReleasedGuard(t *testing.T, helper, payload, who string) {
	t.Helper()
	for _, path := range []string{service.LegacyDarwinDir, service.LegacyDarwinBinary} {
		info, e := os.Lstat(path)
		if e != nil {
			t.Fatal(e)
		}
		st := info.Sys().(*syscall.Stat_t)
		if st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			t.Fatalf("legacy fixture is not root-owned: %s", path)
		}
	}
	parent, e := os.Stat(filepath.Dir(service.LegacyDarwinDir))
	if e != nil {
		t.Fatal(e)
	}
	if parent.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		t.Fatal("legacy fixture parent must belong to the user")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, e := exec.CommandContext(ctx, "sudo", "-n", helper, "daemon", "import-desktop", "--from", payload, "--user", who).CombinedOutput()
	if e == nil || !strings.Contains(string(out), "installed service is not root-controlled") {
		t.Fatalf("old service did not reproduce the reported rejection: %v %s", e, out)
	}
	t.Log("Installed service rejection reproduced: installed service is not root-controlled")
	if !service.LegacyLocation() {
		t.Fatal("failed old helper changed the launchd target")
	}
}
