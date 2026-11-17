package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aead.dev/minisign"
)

// The printed key pair signs a file whose signature the printed public key accepts, in
// the forms the update code parses (a bare "RW…" key, as publicKeys holds it).
func TestKeygenSignRoundTrip(t *testing.T) {
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	err := run([]string{"keygen"})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		env[k] = v
	}
	t.Setenv("MINISIGN_SECRET_KEY", env["MINISIGN_SECRET_KEY"])
	file := filepath.Join(t.TempDir(), "checksums-desktop.txt")
	os.WriteFile(file, []byte("abc  masir-desktop-linux-0.1.90-amd64.deb\n"), 0o600)
	if err := run([]string{"sign", file}); err != nil {
		t.Fatal(err)
	}
	sig, _ := os.ReadFile(file + ".minisig")
	data, _ := os.ReadFile(file)
	var pub minisign.PublicKey
	if err := pub.UnmarshalText([]byte(env["MASIR_UPDATE_PUBKEY"])); err != nil || !minisign.Verify(pub, data, sig) {
		t.Fatalf("signature does not verify (key err %v)\n%s", err, out)
	}
}
