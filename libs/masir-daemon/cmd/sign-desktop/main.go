// sign-desktop binds the service executable and geo pair to a release/platform using
// the existing minisign key. The secret is read only from the environment.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aead.dev/minisign"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 5 {
		return fmt.Errorf("usage: sign-desktop directory version os arch")
	}
	dir := filepath.Join(os.Args[1], "bin")
	secret := []byte(strings.ReplaceAll(strings.Trim(strings.TrimSpace(os.Getenv("MINISIGN_SECRET_KEY")), "\"'"), `\n`, "\n"))
	if len(secret) == 0 {
		// Source builds still work; they use the legacy update path until signed metadata exists.
		for _, name := range []string{updatebundle.DescriptorName, updatebundle.SignatureName} {
			os.Remove(filepath.Join(dir, name))
		}
		return nil
	}
	var key minisign.PrivateKey
	if err := key.UnmarshalText(secret); err != nil {
		// CI's existing minisign invocation also supports an empty-password key.
		key, err = minisign.DecryptKey("", secret)
		if err != nil {
			return fmt.Errorf("invalid desktop component signing key (must be unencrypted or use an empty password)")
		}
	}
	data, err := updatebundle.Create(dir, os.Args[2], os.Args[3], os.Args[4])
	if err != nil {
		return err
	}
	signature := minisign.Sign(key, data)
	valid := false
	for _, text := range strings.Split(os.Getenv("MASIR_UPDATE_PUBKEY"), ",") {
		var pub minisign.PublicKey
		if pub.UnmarshalText([]byte(strings.Trim(strings.TrimSpace(text), "\"'"))) == nil && minisign.Verify(pub, data, signature) {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("desktop component signature does not match MASIR_UPDATE_PUBKEY")
	}
	if err := os.WriteFile(filepath.Join(dir, updatebundle.DescriptorName), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, updatebundle.SignatureName), signature, 0o644)
}
