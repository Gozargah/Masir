package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	masird "github.com/Gozargah/Masir/libs/masir-daemon"
)

var fetchPackage = masird.FetchDesktopPackage

// desktopCommands is the hidden `masir desktop` group the packaged Linux app runs.
func desktopCommands() []*cobra.Command {
	fetch := leaf("fetch-package", "Download and verify a desktop package", func(a args) error {
		return fetchDesktopPackage(os.Stdout, a)
	}, cobra.NoArgs,
		flagSpec{names: []string{"version"}, help: "The release version"},
		flagSpec{names: []string{"format"}, help: "deb or rpm"},
		flagSpec{names: []string{"dir"}, help: "Where to put the files"},
		flagSpec{names: []string{"proxy"}, help: "A running local proxy to try when direct fails"})
	fetch.Hidden = true
	desktop := group("desktop", "Desktop app helpers", fetch)
	desktop.Hidden = true
	return []*cobra.Command{desktop}
}

// releaseBase is the release host, or the loopback override a test or development
// build may set. It never lets MASIR_DESKTOP_RELEASE_BASE point at a remote host.
func releaseBase() (string, error) {
	v := os.Getenv("MASIR_DESKTOP_RELEASE_BASE")
	if v == "" {
		return masird.DesktopReleaseBase, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", fmt.Errorf("MASIR_DESKTOP_RELEASE_BASE: %w", err)
	}
	if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("MASIR_DESKTOP_RELEASE_BASE must be on the loopback address, not %q", u.Hostname())
	}
	return v, nil
}

// fetchDesktopPackage writes only the protocol the app parses to out: progress lines,
// then the package, checksum and signature paths. Errors go to stderr through report.
func fetchDesktopPackage(out io.Writer, a args) error {
	version, format, dir := a.str("version"), a.str("format"), a.str("dir")
	if version == "" || dir == "" || (format != "deb" && format != "rpm") {
		return errors.New("fetch-package needs --version, --dir and --format deb|rpm")
	}
	base, err := releaseBase()
	if err != nil {
		return err
	}
	var last time.Time
	pkg, sums, sig, err := fetchPackage(appCtx, base, version, format, runtime.GOARCH, dir, a.str("proxy"), func(done, total int64) {
		if now := time.Now(); now.Sub(last) >= 100*time.Millisecond || (total > 0 && done >= total) {
			last = now
			fmt.Fprintf(out, "progress %d %d\n", done, total)
		}
	})
	switch {
	case errors.Is(err, masird.ErrReleaseUnreachable):
		return coded(exitUnreachable, err)
	case errors.Is(err, masird.ErrPackageMismatch):
		return coded(exitMismatch, err)
	case err != nil:
		return err
	}
	fmt.Fprintf(out, "package %s\nchecksums %s\nsignature %s\n", pkg, sums, sig)
	return nil
}
