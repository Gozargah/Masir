package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func TestUpdatePreservesCustomPairAndValidatesIt(t *testing.T) {
	for _, kind := range []string{"modified", "legacy", "validation fails", "changed during validation", "binary fails"} {
		t.Run(kind, func(t *testing.T) {
			assets, staged := t.TempDir(), t.TempDir()
			for _, name := range []string{"geoip.dat", "geosite.dat"} {
				os.WriteFile(filepath.Join(assets, name), []byte("original "+name), 0o644)
				os.WriteFile(filepath.Join(staged, name), []byte("release "+name), 0o644)
			}
			if kind != "legacy" {
				if err := geopack.Record(assets); err != nil {
					t.Fatal(err)
				}
			}
			os.WriteFile(filepath.Join(assets, "geoip.dat"), []byte("custom ip"), 0o644)
			bin := filepath.Join(t.TempDir(), exe("masir"))
			os.WriteFile(bin, []byte("old binary"), 0o755)
			os.WriteFile(filepath.Join(staged, exe("masir")), []byte("new binary"), 0o755)
			if kind == "binary fails" {
				os.Mkdir(bin+".previous", 0o755)
				os.WriteFile(filepath.Join(bin+".previous", "occupied"), nil, 0o644)
			}
			checked := false
			m := Masir{Binary: bin, AssetDir: assets, PreflightGeo: func(_ context.Context, _ string, dir string) error {
				checked = true
				for name, want := range map[string]string{"geoip.dat": "custom ip", "geosite.dat": "original geosite.dat"} {
					got, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil || string(got) != want {
						return fmt.Errorf("preflight %s=%s (%v)", name, got, err)
					}
				}
				if kind == "validation fails" {
					return errors.New("custom rules refused")
				}
				if kind == "changed during validation" {
					os.WriteFile(filepath.Join(assets, "geoip.dat"), []byte("later edit"), 0o644)
				}
				return nil
			}}
			r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
			r.Start(func(ctx context.Context, r *Reporter) error { return m.Install(ctx, r, staged) })
			job := wait(t, r)
			ok := kind == "modified" || kind == "legacy"
			if !checked || (job.State == pb.UpdateState_UPDATE_STATE_DONE) != ok {
				t.Fatalf("job=%v checked=%v", job, checked)
			}
			for name, want := range map[string]string{"geoip.dat": "custom ip", "geosite.dat": "original geosite.dat"} {
				if kind == "changed during validation" && name == "geoip.dat" {
					want = "later edit"
				}
				got, _ := os.ReadFile(filepath.Join(assets, name))
				if string(got) != want {
					t.Fatalf("overwrote %s: %s", name, got)
				}
			}
			if ok {
				b, _ := os.ReadFile(filepath.Join(assets, "geoip.dat.previous"))
				if string(b) != "custom ip" {
					t.Fatal("rollback lost custom pair")
				}
				retry := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
				retry.Start(func(ctx context.Context, r *Reporter) error { return m.Install(ctx, r, staged) })
				if job := wait(t, retry); job.State != pb.UpdateState_UPDATE_STATE_DONE {
					t.Fatalf("retry: %v", job)
				}
				previous, _ := os.ReadFile(bin + ".previous")
				if string(previous) != "old binary" {
					t.Fatalf("retry replaced rollback binary: %s", previous)
				}
			}
			b, _ := os.ReadFile(bin)
			want := "old binary"
			if ok {
				want = "new binary"
			}
			if string(b) != want {
				t.Fatalf("binary=%s", b)
			}
		})
	}
}
