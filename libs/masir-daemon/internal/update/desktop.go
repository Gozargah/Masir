package update

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"

	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
)

// VerifyDesktopDescriptor never uses the development unsigned escape hatch. Neither
// the expected hashes nor the trusted key may be supplied by the RPC caller.
func VerifyDesktopDescriptor(data, signature []byte, current string) (updatebundle.Descriptor, error) {
	var d updatebundle.Descriptor
	if len(data) == 0 || len(data) > 16384 || len(signature) == 0 || len(signature) > 2048 {
		return d, errors.New("invalid desktop descriptor size")
	}
	keys, err := trustedKeys()
	if err != nil {
		return d, err
	}
	if len(keys) == 0 {
		return d, errors.New("no trusted component signing key")
	}
	if err := verifySignature(keys, data, signature); err != nil {
		return d, err
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, err
	}
	if d.Role != updatebundle.Role || d.OS != runtime.GOOS || d.Arch != runtime.GOARCH {
		return d, errors.New("desktop component role or platform mismatch")
	}
	if _, err := VersionTag(d.Version); err != nil {
		return d, err
	}
	if !IsRelease(current) || Newer(current, d.Version) {
		return d, errors.New("desktop import would downgrade or replace a development daemon")
	}
	if len(d.Files) != 3 {
		return d, errors.New("desktop import requires exactly three components")
	}
	for _, name := range updatebundle.Names(runtime.GOOS) {
		f, ok := d.Files[name]
		hash, err := hex.DecodeString(f.SHA256)
		if !ok || f.Size <= 0 || f.Size > updatebundle.MaxFileSize || err != nil || len(hash) != 32 {
			return d, fmt.Errorf("invalid component: %s", name)
		}
	}
	return d, nil
}
