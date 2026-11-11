package update

import (
	"aead.dev/minisign"
	"context"
	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOfflineDesktopVerificationFailsClosed(t *testing.T) {
	for _, kind := range []string{"valid raw", "valid packed", "packed corrupt", "packed truncated", "bad signature", "changed manifest", "wrong version", "downgrade", "modified executable", "modified geo", "truncated geo", "oversized component", "unsigned override", "missing file"} {
		t.Run(kind, func(t *testing.T) {
			pub, key := newKey(t)
			trust(t, pub)
			source := t.TempDir()
			for _, name := range updatebundle.Names(runtime.GOOS) {
				if e := os.WriteFile(filepath.Join(source, name), []byte("signed-"+name), 0755); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "valid packed" || strings.HasPrefix(kind, "packed ") {
				packed := t.TempDir()
				if e := geopack.Pack(source, packed); e != nil {
					t.Fatal(e)
				}
				b, _ := os.ReadFile(filepath.Join(source, updatebundle.Names(runtime.GOOS)[0]))
				os.WriteFile(filepath.Join(packed, updatebundle.Names(runtime.GOOS)[0]), b, 0755)
				source = packed
			}
			data, e := updatebundle.Create(source, "0.2.0", runtime.GOOS, runtime.GOARCH)
			if e != nil {
				t.Fatal(e)
			}
			sig := minisign.Sign(key, data)
			current, expected := "0.1.0", "0.2.0"
			switch kind {
			case "packed corrupt", "packed truncated":
				name := filepath.Join(source, "geoip.dat.gz")
				b, _ := os.ReadFile(name)
				if kind == "packed corrupt" {
					b[len(b)-1] ^= 0xff
				} else {
					b = b[:len(b)-4]
				}
				os.WriteFile(name, b, 0644)
			case "bad signature":
				_, other := newKey(t)
				sig = minisign.Sign(other, data)
			case "changed manifest":
				data = append(data, ' ')
			case "wrong version":
				expected = "0.2.1"
			case "downgrade":
				current = "0.2.1"
			case "modified executable":
				os.WriteFile(filepath.Join(source, updatebundle.Names(runtime.GOOS)[0]), []byte("attacker executable"), 0755)
			case "modified geo":
				os.WriteFile(filepath.Join(source, "geoip.dat"), []byte("attacker geo data"), 0644)
			case "truncated geo":
				os.WriteFile(filepath.Join(source, "geosite.dat"), []byte("x"), 0644)
			case "oversized component":
				f, e := os.OpenFile(filepath.Join(source, "geoip.dat"), os.O_APPEND|os.O_WRONLY, 0)
				if e != nil {
					t.Fatal(e)
				}
				f.Write([]byte("extra"))
				f.Close()
			case "unsigned override":
				trust(t)
				t.Setenv("MASIR_UPDATE_ALLOW_UNSIGNED", "1")
			case "missing file":
				os.Remove(filepath.Join(source, "geoip.dat"))
			}
			os.WriteFile(filepath.Join(source, updatebundle.DescriptorName), data, 0644)
			os.WriteFile(filepath.Join(source, updatebundle.SignatureName), sig, 0644)
			staged, e := geopack.NewStaging(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			_, e = StageDesktopDirectory(context.Background(), source, staged, current, expected)
			good := kind == "valid raw" || kind == "valid packed"
			if (e == nil) != good {
				t.Fatalf("%s accepted=%v: %v", kind, e == nil, e)
			}
			if good {
				// Installation must depend only on its verified private snapshot,
				// even if the app/source changes immediately after staging.
				os.WriteFile(filepath.Join(source, updatebundle.Names(runtime.GOOS)[0]), []byte("changed after verification"), 0755)
				for _, name := range updatebundle.Names(runtime.GOOS) {
					b, e := os.ReadFile(filepath.Join(staged, name))
					if e != nil || string(b) != "signed-"+name {
						t.Fatalf("wrong staged bytes: %s %v", name, e)
					}
				}
			}
		})
	}
}
