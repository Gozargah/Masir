//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCheckServiceGeoPairRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "geoip.dat"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "geosite.dat"), []byte("site"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkServiceGeoPair(dir); err == nil {
		t.Fatal("expected error for FIFO geoip.dat")
	}
}
