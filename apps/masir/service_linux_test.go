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
		"Environment=GOMEMLIMIT=100MiB",
		"MemoryHigh=150M",
		"MemoryMax=200M",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("unit is missing %q:\n%s", want, u)
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
