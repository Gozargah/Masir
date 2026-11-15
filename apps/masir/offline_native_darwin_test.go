package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/service"
	masird "github.com/Gozargah/Masir/libs/masir-daemon"
)

func nativeAssertProtectedInstallation(t *testing.T) {
	t.Helper()
	if service.LegacyLocation() {
		t.Fatal("launchd does not use the protected service directory")
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
		t.Fatal("service installation changed the Homebrew parent's owner")
	}
}

// The public release is authenticated for normal installation, but the legacy
// migration allowlist deliberately recognizes no pre-reset executable. Exercise
// rejection without replacing the active launchd job or changing its binary.
func nativeRejectUnsupportedLegacy(t *testing.T, payload, who string) {
	t.Helper()
	uid, err := strconv.Atoi(who)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(service.BinaryPath())
	if err != nil {
		t.Fatal(err)
	}
	definition, err := os.ReadFile(service.DefinitionPath())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Stat(filepath.Dir(service.LegacyDarwinDir))
	if err != nil {
		t.Fatal(err)
	}
	if parent.Sys().(*syscall.Stat_t).Uid != uint32(uid) {
		t.Fatal("legacy parent must belong to the served user")
	}
	if err := os.Mkdir(service.LegacyDarwinDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(service.LegacyDarwinDir)
	for _, name := range []string{"Masir", "geoip.dat", "geosite.dat"} {
		raw, err := os.ReadFile(filepath.Join(service.InstallDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0644)
		if name == "Masir" {
			mode = 0755
		}
		if err := os.WriteFile(filepath.Join(service.LegacyDarwinDir, name), raw, mode); err != nil {
			t.Fatal(err)
		}
	}
	// LegacyLocation reads the definition on disk. Keep the running supervisor
	// untouched; an unsupported snapshot must be refused before its stop callback.
	defer func() {
		if err := os.WriteFile(service.DefinitionPath(), definition, 0644); err != nil {
			t.Errorf("restore service definition: %v", err)
		}
	}()
	if err := os.WriteFile(service.DefinitionPath(), []byte(servicePlist(service.LegacyDarwinBinary, uid)), 0644); err != nil {
		t.Fatal(err)
	}
	for _, altered := range []bool{false, true} {
		if altered {
			if err := os.WriteFile(service.LegacyDarwinBinary, []byte("#!/bin/sh\nexit 91\n"), 0755); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		stopped := false
		err := masird.InstallDesktopOffline(ctx, payload, who, nativeNewVersion, func() error { stopped = true; return nil }, func() error { t.Error("unsupported executable triggered recovery"); return nil })
		cancel()
		if err == nil || !strings.Contains(err.Error(), "not an authenticated") || stopped {
			t.Fatalf("unsupported legacy executable was not rejected before stop: %v stopped=%v", err, stopped)
		}
		current, err := os.ReadFile(service.BinaryPath())
		if err != nil || !bytes.Equal(current, before) {
			t.Fatalf("unsupported legacy import changed the protected binary: %v", err)
		}
	}
}
