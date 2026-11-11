package geopack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledGeoProvenance(t *testing.T) {
	dir := t.TempDir()
	if ok, err := Managed(dir); !ok || err != nil {
		t.Fatalf("first install: %v %v", ok, err)
	}
	for _, name := range names {
		os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644)
	}
	if ok, err := Managed(dir); ok || err != nil {
		t.Fatalf("legacy data: %v %v", ok, err)
	}
	if err := Record(dir); err != nil {
		t.Fatal(err)
	}
	if ok, err := Managed(dir); !ok || err != nil {
		t.Fatalf("installed data: %v %v", ok, err)
	}
	os.WriteFile(filepath.Join(dir, "geosite.dat"), []byte("custom"), 0o644)
	if ok, err := Managed(dir); ok || err != nil {
		t.Fatalf("custom data: %v %v", ok, err)
	}
	for _, name := range names {
		os.Remove(filepath.Join(dir, name))
	}
	if ok, err := Managed(dir); ok || err != nil {
		t.Fatalf("deleted managed pair mistaken for first install: %v %v", ok, err)
	}
}
