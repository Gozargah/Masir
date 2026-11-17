package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/server"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/update"
)

var ErrPackageMismatch, ErrReleaseUnreachable = update.ErrPackageMismatch, update.ErrReleaseUnreachable

// DesktopPackageName is the release file name of the Linux desktop package.
func DesktopPackageName(version, format, arch string) string {
	return update.DesktopPackageName(version, format, arch)
}

// VerifyDesktopPackage checks the package at path against the signed checksum list.
func VerifyDesktopPackage(list, signature []byte, path, arch string) (version, format string, err error) {
	return update.VerifyDesktopPackage(list, signature, path, arch)
}

// FetchDesktopPackage downloads a desktop package and its signed list into dir, directly
// and then through proxyURL when direct fails. proxyURL is "" or a loopback URL such as
// "http://127.0.0.1:<port>"; it is passed only when the proxy already runs.
func FetchDesktopPackage(ctx context.Context, base, version, format, arch, dir, proxyURL string, progress func(done, total int64)) (pkg, sums, sig string, err error) {
	// No overall timeout: a download may rightly take long.
	transport := func(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
		return &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			Proxy:                 proxy,
		}
	}
	c := update.Clients{
		Direct:    &http.Client{Transport: transport(nil)},
		UserAgent: "masir/" + server.Version,
	}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return "", "", "", err
		}
		if ip := net.ParseIP(u.Hostname()); u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return "", "", "", fmt.Errorf("refusing proxy %s: it must be http on the loopback address", proxyURL)
		}
		c.Proxy = &http.Client{Transport: transport(http.ProxyURL(u))}
	}
	f, err := update.FetchDesktopPackage(ctx, update.PackageRequest{
		Base: base, Version: version, Format: format, Arch: arch, Dir: dir, Clients: c,
	}, progress)
	return f.Package, f.Checksums, f.Signature, err
}
