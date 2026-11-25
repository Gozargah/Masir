package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceGeoPreflight(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		source, dest                 []string
		managed, wantCopy, wantError bool
	}{
		{"fresh install without geo", nil, nil, true, false, true},
		{"fresh install with pair", []string{"geoip.dat", "geosite.dat"}, nil, true, true, false},
		{"partial source", []string{"geoip.dat"}, []string{"geoip.dat", "geosite.dat"}, true, false, true},
		{"binary only with existing pair", nil, []string{"geoip.dat", "geosite.dat"}, true, false, false},
		{"custom pair", []string{"geoip.dat", "geosite.dat"}, []string{"geoip.dat", "geosite.dat"}, false, false, false},
		{"partial custom pair", []string{"geoip.dat", "geosite.dat"}, []string{"geoip.dat"}, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, dest := t.TempDir(), t.TempDir()
			for _, n := range tc.source {
				if err := os.WriteFile(filepath.Join(source, n), []byte("source"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range tc.dest {
				if err := os.WriteFile(filepath.Join(dest, n), []byte("custom"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			copy, err := preflightServiceGeo(source, dest, tc.managed)
			if copy != tc.wantCopy || (err != nil) != tc.wantError {
				t.Fatalf("copy=%v err=%v", copy, err)
			}
			for _, n := range tc.dest {
				b, err := os.ReadFile(filepath.Join(dest, n))
				if err != nil || string(b) != "custom" {
					t.Fatal("preflight modified installed data")
				}
			}
		})
	}
}
