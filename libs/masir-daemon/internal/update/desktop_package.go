package update

import (
	"errors"
	"fmt"
	"strings"
)

// DesktopChecksums lists the SHA-256 of every desktop release file; DesktopSignature
// is its minisign signature.
const (
	DesktopChecksums = "checksums-desktop.txt"
	DesktopSignature = "checksums-desktop.txt.minisig"
)

// ErrPackageMismatch wraps every reason a desktop package isn't the signed release file.
var ErrPackageMismatch = errors.New("the download didn't match the signed release")

// DesktopPackageName is the Linux .deb or .rpm of the desktop app; format is "deb" or
// "rpm" and arch is a GOARCH.
func DesktopPackageName(version, format, arch string) string {
	return fmt.Sprintf("masir-desktop-linux-%s-%s.%s", strings.TrimPrefix(version, "v"), arch, format)
}

// VerifyDesktopPackage decides whether the package at path is a signed desktop release
// for arch, before anything parses it, and returns the version the signed list binds it
// to. The version comes from the signed line, never from path's name. There is no
// unsigned escape hatch: this runs as root on a file a person handed it.
func VerifyDesktopPackage(list, signature []byte, path, arch string) (version, format string, err error) {
	keys, err := trustedKeys()
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrPackageMismatch, err)
	}
	if len(keys) == 0 {
		return "", "", fmt.Errorf("%w: this build has no update signing key", ErrPackageMismatch)
	}
	if err := verifySignature(keys, list, signature); err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrPackageMismatch, err)
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
