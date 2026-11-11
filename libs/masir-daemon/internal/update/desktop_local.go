package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
)

// StageDesktopDirectory authenticates metadata using this binary's embedded keys,
// then verifies every expanded byte in private staging. No network or unsigned
// development override is consulted. Nothing in source is executed.
// The caller must supply an empty private staged directory beneath a trusted
// parent (newDesktopStaging in the service path). CreateStagedFile creates regular
// exclusive files on Unix and additionally protects their owner/DACL on Windows.
func StageDesktopDirectory(ctx context.Context, source, staged, current, expected string) (updatebundle.Descriptor, error) {
	read := func(name string, limit int64) ([]byte, error) {
		f, e := os.Open(filepath.Join(source, name))
		if e != nil {
			return nil, e
		}
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, limit+1))
		if int64(len(b)) > limit {
			return nil, errors.New("oversized component metadata")
		}
		return b, e
	}
	data, err := read(updatebundle.DescriptorName, 16384)
	if err != nil {
		return updatebundle.Descriptor{}, err
	}
	sig, err := read(updatebundle.SignatureName, 2048)
	if err != nil {
		return updatebundle.Descriptor{}, err
	}
	d, err := VerifyDesktopDescriptor(data, sig, current)
	if err != nil {
		return d, err
	}
	if strings.TrimPrefix(d.Version, "v") != strings.TrimPrefix(expected, "v") {
		return d, errors.New("component version differs from the desktop helper")
	}
	for _, name := range updatebundle.Names(runtime.GOOS) {
		if err := ctx.Err(); err != nil {
			return d, err
		}
		in, e := updatebundle.Open(source, name)
		if e != nil {
			return d, e
		}
		mode := os.FileMode(0644)
		if name == updatebundle.Names(runtime.GOOS)[0] {
			mode = 0755
		}
		out, e := geopack.CreateStagedFile(filepath.Join(staged, name), mode)
		if e != nil {
			in.Close()
			return d, e
		}
		h := sha256.New()
		want := d.Files[name]
		n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, want.Size+1))
		in.Close()
		closeErr := out.Close()
		if e != nil {
			return d, e
		}
		if closeErr != nil {
			return d, closeErr
		}
		if n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
			return d, fmt.Errorf("component hash or size mismatch: %s", name)
		}
	}
	return d, ctx.Err()
}
