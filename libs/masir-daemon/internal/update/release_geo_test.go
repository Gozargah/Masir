package update

import (
	"context"
	"errors"
	"fmt"
	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func TestCombinedReleaseUpdate(t *testing.T) {
	for _, scenario := range []string{"success", "missing geo", "preflight refuses", "binary install fails"} {
		t.Run(scenario, func(t *testing.T) {
			gh := newFakeGitHub(t)
			name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
			files := map[string]string{exe("masir"): "new binary", "geoip.dat": "new ip", "geosite.dat": "new site"}
			if scenario == "missing geo" {
				delete(files, "geoip.dat")
			}
			arc := archive(t, name, files)
			gh.publish(ReleaseRepo, "v0.2.0", map[string][]byte{name: arc, "checksums.txt": []byte(fmt.Sprintf("%s  %s\n", sum(arc), name))})
			assets := t.TempDir()
			for name, content := range map[string]string{"geoip.dat": "old ip", "geosite.dat": "old site"} {
				if err := os.WriteFile(filepath.Join(assets, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			bin := filepath.Join(t.TempDir(), exe("masir"))
			if err := geopack.Record(assets); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(bin, []byte("old binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			if scenario == "binary install fails" {
				// Force backup creation to fail after the geo pair has been installed.
				if err := os.Mkdir(bin+".previous", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bin+".previous", "occupied"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			restarted, checked := false, false
			r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
			task := Masir{Clients: clients(), Current: "0.1.0", Binary: bin, AssetDir: assets, WorkDir: r.workDir,
				Preflight: func(_ context.Context, staged string) error {
					checked = true
					for name, want := range files {
						got, err := os.ReadFile(filepath.Join(filepath.Dir(staged), name))
						if err != nil || string(got) != want {
							return fmt.Errorf("staged %s = %q: %v", name, got, err)
						}
					}
					got, _ := os.ReadFile(filepath.Join(assets, "geoip.dat"))
					if string(got) != "old ip" {
						return errors.New("changed active data before preflight")
					}
					if scenario == "preflight refuses" {
						return errors.New("rules refused")
					}
					return nil
				}, Restart: func() { restarted = true }}
			if _, err := r.Start(task.Run); err != nil {
				t.Fatal(err)
			}
			job := wait(t, r)
			if managed, err := geopack.Managed(assets); err != nil || !managed {
				t.Fatalf("managed baseline/rollback: %v %v", managed, err)
			}
			success := scenario == "success"
			if success && job.State != pb.UpdateState_UPDATE_STATE_RESTARTING {
				t.Fatalf("%v", job)
			}
			if !success && job.State != pb.UpdateState_UPDATE_STATE_FAILED {
				t.Fatalf("%v", job)
			}
			if restarted != success {
				t.Fatalf("restarted=%v", restarted)
			}
			if checked != (scenario != "missing geo") {
				t.Fatalf("checked=%v", checked)
			}
			for name, old := range map[string]string{"geoip.dat": "old ip", "geosite.dat": "old site"} {
				want := old
				if success {
					want = files[name]
				}
				got, err := os.ReadFile(filepath.Join(assets, name))
				if err != nil || string(got) != want {
					t.Fatalf("%s=%q want %q: %v", name, got, want, err)
				}
				if success {
					got, err = os.ReadFile(filepath.Join(assets, name+".previous"))
					if err != nil || string(got) != old {
						t.Fatalf("backup %s=%q: %v", name, got, err)
					}
				}
			}
			got, _ := os.ReadFile(bin)
			want := "old binary"
			if success {
				want = "new binary"
			}
			if string(got) != want {
				t.Fatalf("binary=%q", got)
			}
		})
	}
}

func TestUnpackRejectsDuplicateReleaseFilesAndClearsStaleData(t *testing.T) {
	for _, ext := range []string{".tar.gz", ".zip"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			archivePath := filepath.Join(t.TempDir(), "release"+ext)
			data := archive(t, archivePath, map[string]string{exe("masir"): "a", "nested/" + exe("masir"): "b"})
			if err := os.WriteFile(archivePath, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := unpack(archivePath, dir); err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("%v", err)
			}
			data = archive(t, archivePath, map[string]string{exe("masir"): "a"})
			os.WriteFile(archivePath, data, 0o644)
			os.WriteFile(filepath.Join(dir, "geoip.dat"), []byte("stale"), 0o644)
			if err := unpack(archivePath, dir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "geoip.dat")); !os.IsNotExist(err) {
				t.Fatalf("stale data survived: %v", err)
			}
		})
	}
}

func TestFailedReleaseRestoresEarlierRollbackPair(t *testing.T) {
	assets, staged := t.TempDir(), t.TempDir()
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		for path, content := range map[string]string{filepath.Join(assets, name): "current", filepath.Join(assets, name+".previous"): "older", filepath.Join(staged, name): "new"} {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	rollback, err := installReleaseGeo(staged, assets)
	if err != nil {
		t.Fatal(err)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		for suffix, want := range map[string]string{"": "current", ".previous": "older"} {
			got, err := os.ReadFile(filepath.Join(assets, name+suffix))
			if err != nil || string(got) != want {
				t.Fatalf("%s%s=%q: %v", name, suffix, got, err)
			}
		}
	}
}
