// test-sign makes a throwaway minisign key and signs files with it, so a local build can
// stand in for a signed release in update tests (scripts/build-test-packages.sh). It
// never touches the release key.
//
//	test-sign keygen       prints MASIR_UPDATE_PUBKEY=… and MINISIGN_SECRET_KEY=…
//	test-sign sign <file>  writes <file>.minisig with MINISIGN_SECRET_KEY
package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"

	"aead.dev/minisign"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "test-sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 1 && args[0] == "keygen":
		pub, priv, err := minisign.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		text, err := priv.MarshalText()
		if err != nil {
			return err
		}
		// Only the base64 line: one shell word, which sign-desktop and UnmarshalText
		// both accept without its comment line.
		lines := strings.Split(strings.TrimSpace(string(text)), "\n")
		fmt.Printf("MASIR_UPDATE_PUBKEY=%s\nMINISIGN_SECRET_KEY=%s\n", pub.String(), lines[len(lines)-1])
		return nil
	case len(args) == 2 && args[0] == "sign":
		// The same forms sign-desktop reads: quoted or not, newlines as \n escapes.
		secret := strings.ReplaceAll(strings.Trim(strings.TrimSpace(os.Getenv("MINISIGN_SECRET_KEY")), "\"'"), `\n`, "\n")
		var key minisign.PrivateKey
		if err := key.UnmarshalText([]byte(secret)); err != nil {
			return fmt.Errorf("MINISIGN_SECRET_KEY: %w", err)
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		return os.WriteFile(args[1]+".minisig", minisign.Sign(key, data), 0o644)
	}
	return fmt.Errorf("usage: test-sign keygen | test-sign sign <file>")
}
