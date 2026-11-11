package main

import (
	"aead.dev/minisign"
	"bytes"
	"crypto/rand"
	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSignDesktopComponentsAndRejectWrongPublicKey(t *testing.T) {
	pub, private, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := private.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINISIGN_SECRET_KEY", string(secret))
	t.Setenv("MASIR_UPDATE_PUBKEY", pub.String())
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range updatebundle.Names(runtime.GOOS) {
		os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644)
	}
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"sign-desktop", root, "1.2.0", runtime.GOOS, runtime.GOARCH}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, updatebundle.DescriptorName))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(dir, updatebundle.SignatureName))
	if err != nil {
		t.Fatal(err)
	}
	if !minisign.Verify(pub, data, signature) {
		t.Fatal("invalid component signature")
	}
	packed := t.TempDir()
	if err := geopack.Pack(dir, packed); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"geoip.dat.gz", "geosite.dat.gz", geopack.ManifestName} {
		b, err := os.ReadFile(filepath.Join(packed, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		os.Remove(filepath.Join(dir, name))
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	compressedData, _ := os.ReadFile(filepath.Join(dir, updatebundle.DescriptorName))
	compressedSignature, _ := os.ReadFile(filepath.Join(dir, updatebundle.SignatureName))
	if !bytes.Equal(data, compressedData) || !minisign.Verify(pub, compressedData, compressedSignature) {
		t.Fatal("packed resources changed the signed service components")
	}
	other, _, _ := minisign.GenerateKey(rand.Reader)
	t.Setenv("MASIR_UPDATE_PUBKEY", other.String())
	if err := run(); err == nil {
		t.Fatal("signed components with a mismatching release key")
	}
}
