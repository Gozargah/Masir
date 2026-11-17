package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"aead.dev/minisign"
)

func TestVerifyDesktopPackage(t *testing.T) {
	pub, priv := newKey(t)
	_, other := newKey(t)
	pkg := []byte("a masir package")

	tests := []struct {
		name    string
		listed  string // the file name the signed list gives the package's hash
		arch    string
		signer  minisign.PrivateKey
		noKeys  bool
		alter   bool
		version string
		format  string
	}{
		{name: "deb", listed: "masir-desktop-linux-0.1.7-amd64.deb", arch: "amd64", signer: priv, version: "0.1.7", format: "deb"},
		{name: "rpm", listed: "masir-desktop-linux-0.1.7-amd64.rpm", arch: "amd64", signer: priv, version: "0.1.7", format: "rpm"},
		{name: "another key", listed: "masir-desktop-linux-0.1.7-amd64.deb", arch: "amd64", signer: other},
		{name: "altered package", listed: "masir-desktop-linux-0.1.7-amd64.deb", arch: "amd64", signer: priv, alter: true},
		{name: "another arch", listed: "masir-desktop-linux-0.1.7-arm64.deb", arch: "amd64", signer: priv},
		{name: "not a package", listed: "masir-desktop-portable-linux-0.1.7-amd64.tar.gz", arch: "amd64", signer: priv},
		{name: "bad version", listed: "masir-desktop-linux-../../x-amd64.deb", arch: "amd64", signer: priv},
		{name: "no keys", listed: "masir-desktop-linux-0.1.7-amd64.deb", arch: "amd64", signer: priv, noKeys: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.noKeys {
				trust(t)
				t.Setenv("MASIR_UPDATE_ALLOW_UNSIGNED", "1")
			} else {
				trust(t, pub)
			}
			list := []byte(fmt.Sprintf("%s  %s\n%s  update-linux-amd64.json\n", sum(pkg), tc.listed, sum([]byte("other"))))
			sig := minisign.Sign(tc.signer, list)
			// The caller's file name says nothing: only the signed line may name the version.
			path := filepath.Join(t.TempDir(), "masir-desktop-linux-9.9.9-amd64.deb")
			body := pkg
			if tc.alter {
				body = append([]byte{pkg[0] ^ 1}, pkg[1:]...)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}

			version, format, err := VerifyDesktopPackage(list, sig, path, tc.arch)
			if tc.version == "" {
				if err == nil {
					t.Fatalf("got %q %q, want an error", version, format)
				}
				if !errors.Is(err, ErrPackageMismatch) {
					t.Fatalf("err = %v, want ErrPackageMismatch", err)
				}
				if tc.name == "another key" && !errors.Is(err, errBadSignature) {
					t.Fatalf("err = %v, want errBadSignature too", err)
				}
				return
			}
			if err != nil || version != tc.version || format != tc.format {
				t.Fatalf("got %q %q %v, want %q %q", version, format, err, tc.version, tc.format)
			}
		})
	}

	if got := DesktopPackageName("v0.1.7", "deb", "amd64"); got != "masir-desktop-linux-0.1.7-amd64.deb" {
		t.Fatalf("DesktopPackageName = %q", got)
	}
}
