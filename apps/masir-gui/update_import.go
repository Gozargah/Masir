package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func desktopComponents(dir string) (string, error) {
	if runtime.GOOS == "darwin" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".app" {
			return "", fmt.Errorf("prepared update has no unique app bundle")
		}
		return filepath.Join(dir, entries[0].Name(), "Contents", "Resources", "bin"), nil
	}
	return filepath.Join(dir, "bin"), nil
}

// importDesktopComponents treats paths and metadata here as untrusted: the service
// verifies the signature and complete bytes independently in its own staging area.
func importDesktopComponents(ctx context.Context, client pb.DaemonServiceClient, dir, version string) error {
	dir, err := desktopComponents(dir)
	if err != nil {
		return err
	}
	return importComponentDirectory(ctx, client, dir, version)
}

// importComponentDirectory also accepts the installed app's bin directory. A manual
// app replacement can use the same authenticated service transaction as an app update.
func importComponentDirectory(ctx context.Context, client pb.DaemonServiceClient, dir, version string) error {
	d, descriptor, err := readComponentDescriptor(dir, version)
	if err != nil {
		return err
	}
	signature, err := readSmallComponent(dir, updatebundle.SignatureName, 2048)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	stream, err := client.ImportDesktopUpdate(ctx)
	if err != nil {
		return err
	}
	send := func(m *pb.ImportDesktopUpdateRequest) error {
		if err := stream.Send(m); err != nil {
			// Send may report only EOF when an older service rejects the method.
			// Read its final status so startup can recognize Unimplemented.
			if _, final := stream.CloseAndRecv(); final != nil {
				return final
			}
			return err
		}
		return nil
	}
	if err := send(&pb.ImportDesktopUpdateRequest{ManifestBytes: descriptor, Signature: signature}); err != nil {
		return err
	}
	for _, name := range updatebundle.Names(runtime.GOOS) {
		f, err := updatebundle.Open(dir, name)
		if err != nil {
			return err
		}
		expected, ok := d.Files[name]
		if !ok || expected.Size <= 0 || expected.Size > updatebundle.MaxFileSize {
			f.Close()
			return fmt.Errorf("invalid component %s", name)
		}
		var sent int64
		buf := make([]byte, 128<<10)
		for {
			n, e := f.Read(buf)
			if n > 0 {
				sent += int64(n)
				if sent > expected.Size {
					f.Close()
					return fmt.Errorf("component %s exceeds signed size", name)
				}
				if err := send(&pb.ImportDesktopUpdateRequest{File: name, Data: append([]byte(nil), buf[:n]...)}); err != nil {
					f.Close()
					return err
				}
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				f.Close()
				return e
			}
		}
		f.Close()
		if sent != expected.Size {
			return fmt.Errorf("component %s is truncated", name)
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return err
	}
	if !sameVersion(response.Version, version) {
		return fmt.Errorf("service installed an unexpected version")
	}
	return nil
}

func readSmallComponent(dir, name string, limit int64) ([]byte, error) {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("oversized %s", name)
	}
	return b, err
}

func readComponentDescriptor(dir, version string) (updatebundle.Descriptor, []byte, error) {
	var d updatebundle.Descriptor
	data, err := readSmallComponent(dir, updatebundle.DescriptorName, 16384)
	if err != nil {
		return d, nil, err
	}
	if err = json.Unmarshal(data, &d); err != nil {
		return d, nil, err
	}
	if !sameVersion(d.Version, version) {
		return d, nil, fmt.Errorf("desktop component version differs from the app update")
	}
	return d, data, nil
}

func installLegacyDesktopComponents(dir, version string, authorized func()) error {
	if _, _, err := readComponentDescriptor(dir, version); err != nil {
		return err
	}
	// MyGo authenticated the archive; the elevated helper independently verifies
	// the signed descriptor and all component bytes in private staging.
	return installBundledDaemon(dir, authorized)
}
