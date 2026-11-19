package update

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DesktopReleaseBase is where a release's desktop files live: <base>/v<version>/<name>.
const DesktopReleaseBase = "https://github.com/" + ReleaseRepo + "/releases/download"

// DesktopChecksums lists the SHA-256 of every desktop release file; DesktopSignature
// is its minisign signature.
const (
	DesktopChecksums = "checksums-desktop.txt"
	DesktopSignature = "checksums-desktop.txt.minisig"
)

// ErrPackageMismatch wraps every reason a desktop package isn't the signed release file.
var ErrPackageMismatch = errors.New("the download didn't match the signed release")

// ErrReleaseUnreachable means neither the direct client nor the proxy got a response.
var ErrReleaseUnreachable = errors.New("the release isn't reachable")

// DesktopPackageName is the Linux .deb or .rpm of the desktop app; format is "deb" or
// "rpm" and arch is a GOARCH.
func DesktopPackageName(version, format, arch string) string {
	return fmt.Sprintf("masir-desktop-linux-%s-%s.%s", strings.TrimPrefix(version, "v"), arch, format)
}

// verifyDesktopList checks the list's signature against the trusted keys.
func verifyDesktopList(list, signature []byte) error {
	keys, err := trustedKeys()
	if err == nil && len(keys) == 0 {
		err = errors.New("this build has no update signing key")
	}
	if err == nil {
		err = verifySignature(keys, list, signature)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPackageMismatch, err)
	}
	return nil
}

// VerifyDesktopPackage decides whether the package at path is a signed desktop release
// for arch, before anything parses it, and returns the version the signed list binds it
// to. The version comes from the signed line, never from path's name. There is no
// unsigned escape hatch: this runs as root on a file a person handed it. The caller must
// pass a private copy it controls (the root command copies into a root-only 0700
// directory first, opened O_NOFOLLOW): the file is hashed by path, and a path the user
// can still write could change after the check.
func VerifyDesktopPackage(list, signature []byte, path, arch string) (version, format string, err error) {
	if err := verifyDesktopList(list, signature); err != nil {
		return "", "", err
	}
	got, err := fileSHA256(path)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrPackageMismatch, err)
	}
	for _, line := range strings.Split(string(list), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.EqualFold(fields[0], got) {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		for _, f := range []string{"deb", "rpm"} {
			v, ok := strings.CutPrefix(name, "masir-desktop-linux-")
			if !ok {
				break
			}
			v, ok = strings.CutSuffix(v, "-"+arch+"."+f)
			if !ok {
				continue
			}
			// The name must round-trip, so a "v" prefix or an odd arch can't slip through.
			if v, err := VersionTag(v); err == nil && DesktopPackageName(v, f, arch) == name {
				return v, f, nil
			}
		}
	}
	return "", "", fmt.Errorf("%w: it isn't the signed masir-desktop-linux package for %s", ErrPackageMismatch, arch)
}

// PackageRequest names one desktop package: Base is DesktopReleaseBase (or a test server).
type PackageRequest struct {
	Base, Version, Format, Arch, Dir string
	Clients                          Clients
}

// PackageFiles are the downloaded package and the signed list that vouches for it.
type PackageFiles struct{ Package, Checksums, Signature string }

// FetchDesktopPackage downloads the package, the checksum list and its signature into
// Dir, resuming a partial package, and returns only a package VerifyDesktopPackage
// accepts. progress gets (done, total); total is 0 until a response gives the size.
func FetchDesktopPackage(ctx context.Context, r PackageRequest, progress func(done, total int64)) (PackageFiles, error) {
	base := strings.TrimSuffix(r.Base, "/") + "/v" + strings.TrimPrefix(r.Version, "v") + "/"
	name := DesktopPackageName(r.Version, r.Format, r.Arch)
	// The version comes from a release tag: never let it, or anything else, leave Dir.
	if _, err := VersionTag(r.Version); err != nil || (r.Format != "deb" && r.Format != "rpm") || filepath.Base(name) != name {
		return PackageFiles{}, fmt.Errorf("refusing desktop package %q", name)
	}
	files := PackageFiles{
		Package:   filepath.Join(r.Dir, name),
		Checksums: filepath.Join(r.Dir, DesktopChecksums),
		Signature: filepath.Join(r.Dir, DesktopSignature),
	}
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return files, err
	}
	list, err := fetchSmall(ctx, r.Clients, base+DesktopChecksums)
	if err != nil {
		return files, unreachable(ctx, err)
	}
	sig, err := fetchSmall(ctx, r.Clients, base+DesktopSignature)
	if err != nil {
		return files, unreachable(ctx, err)
	}
	// Fail before a large download when the list isn't signed by a trusted key.
	if err := verifyDesktopList(list, sig); err != nil {
		return files, err
	}
	if err := os.WriteFile(files.Checksums, list, 0o600); err != nil {
		return files, err
	}
	if err := os.WriteFile(files.Signature, sig, 0o600); err != nil {
		return files, err
	}

	// A leftover partial may be stale or longer than the real file: if it fails the
	// hash, start clean once. A fresh download that fails is a real mismatch.
	for attempt := 0; ; attempt++ {
		resumed := fileSize(files.Package) > 0
		done, total := fileSize(files.Package), int64(0)
		if err := downloadSized(ctx, r.Clients, base+name, files.Package, func(n int64) {
			done += n
			progress(done, total)
		}, func(n int64) { total = n }); err != nil {
			return files, unreachable(ctx, err)
		}
		v, f, err := VerifyDesktopPackage(list, sig, files.Package, r.Arch)
		if err == nil && (v != strings.TrimPrefix(r.Version, "v") || f != r.Format) {
			// Signed, but another release: the caller would install it as this one.
			err = fmt.Errorf("%w: the server sent masir-desktop %s (%s), not %s (%s)", ErrPackageMismatch, v, f, r.Version, r.Format)
		}
		if err == nil {
			return files, nil
		}
		os.Remove(files.Package)
		if !resumed || attempt > 0 {
			return files, err
		}
	}
}

// unreachable marks a failure to get any response; an HTTP status such as 404 isn't one.
func unreachable(ctx context.Context, err error) error {
	var ue *url.Error
	var se *statusError
	if ctx.Err() == nil && errors.As(err, &ue) && !errors.As(err, &se) {
		return fmt.Errorf("%w: %w", ErrReleaseUnreachable, err)
	}
	return err
}
