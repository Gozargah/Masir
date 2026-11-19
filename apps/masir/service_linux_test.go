//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gozargah/Masir/libs/masir-core/service"
)

// The daemon ends its streams and exits on SIGTERM; the unit only needs a bound for a stop
// that can't finish, so `systemctl stop`/`restart` never waits systemd's 90s default.
func TestServiceUnitBoundsTheStop(t *testing.T) {
	u := serviceUnit("/usr/local/lib/masir/masir", 1000)
	for _, want := range []string{
		"ExecStart=/usr/local/lib/masir/masir daemon run --user 1000",
		"TimeoutStopSec=10",
		"Restart=on-failure",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("unit is missing %q:\n%s", want, u)
		}
	}
}

func TestServiceUnitLeavesMemoryLimitsToSystemPolicy(t *testing.T) {
	u := serviceUnit("/usr/local/lib/masir/masir", 1000)
	for _, setting := range []string{"GOMEMLIMIT=", "MemoryHigh=", "MemoryMax="} {
		if strings.Contains(u, setting) {
			t.Errorf("service unit forces %s", setting)
		}
	}
}

// An update replaces the service's own binary and the geo data beside it, which
// ProtectSystem=full would otherwise make read-only.
func TestServiceUnitKeepsTheInstallDirectoryWritable(t *testing.T) {
	u := serviceUnit("/usr/local/lib/masir/masir", 1000)
	for _, want := range []string{
		"NoNewPrivileges=yes",
		"PrivateTmp=yes",
		"ProtectSystem=full",
		"CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_NET_ADMIN",
		"ReadWritePaths=-" + service.InstallDir(),
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("unit is missing %q:\n%s", want, u)
		}
	}
}

// An update reconciles the unit: only a definition that actually changed is written, and
// the caller knows whether it has to reload and restart.
func TestWriteServiceDefinitionOnlyWritesChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masird.service")
	changed, err := writeServiceDefinition(path, "one")
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	changed, err = writeServiceDefinition(path, "one")
	if err != nil || changed {
		t.Fatalf("same text: changed=%v err=%v", changed, err)
	}
	changed, err = writeServiceDefinition(path, "two")
	if err != nil || !changed {
		t.Fatalf("changed text: changed=%v err=%v", changed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "two" {
		t.Fatalf("file = %q", got)
	}
}

func TestRetryServiceDefinitionReloadsAnUnchangedUnit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "masird.service")
	if err := os.WriteFile(path, []byte("current unit"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "commands")
	t.Setenv("PATH", dir)
	t.Setenv("SYSTEMCTL_LOG", logPath)
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SYSTEMCTL_LOG\"\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if restarted, err := applyLinuxServiceDefinition(path, "current unit", false); err != nil || restarted {
		t.Fatalf("ordinary sync: restarted=%v err=%v", restarted, err)
	}
	if restarted, err := applyLinuxServiceDefinition(path, "current unit", true); err != nil || !restarted {
		t.Fatalf("retry: restarted=%v err=%v", restarted, err)
	}
	want := "daemon-reload\nis-active --quiet masird.service\nrestart masird.service\n"
	if got, err := os.ReadFile(logPath); err != nil || string(got) != want {
		t.Fatalf("systemctl commands = %q, err=%v", got, err)
	}
}
