package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aead.dev/minisign"
)

func TestVerifyDesktopPackage(t *testing.T) {
	pub, priv := newKey(t)
	_, other := newKey(t)
	pkg := []byte("a masir package")

	tests := []struct {
		name    string
		listed  string // the file name the signed list gives the package's hash
		arch    string
		signer  minisign.PrivateKey
		noKeys  bool
		alter   bool
		version string
		format  string
	}{
		{name: "deb", listed: "masir-desktop-linux-0.1.7-amd64.deb", arch: "amd64", signer: priv, version: "0.1.7", format: "deb"},
		{name: "rpm", listed: "masir-desktop-linux-0.1.7-amd64.rpm", arch: "amd64", signer: priv, version: "0.1.7", format: "rpm"},
		{name: "another key", listed: "masir-desktop-linux-0.1.7-amd64.deb", arch: "amd64", signer: other},
		{name: "altered package", listed: "masir-desktop-linux-0.1.7-amd64.deb", arch: "amd64", signer: priv, alter: true},
		{name: "another arch", listed: "masir-desktop-linux-0.1.7-arm64.deb", arch: "amd64", signer: priv},
		{name: "not a package", listed: "masir-desktop-portable-linux-0.1.7-amd64.tar.gz", arch: "amd64", signer: priv},
		{name: "bad version", listed: "masir-desktop-linux-../../x-amd64.deb", arch: "amd64", signer: priv},
		{name: "no keys", listed: "masir-desktop-linux-0.1.7-amd64.deb", arch: "amd64", signer: priv, noKeys: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.noKeys {
				trust(t)
				t.Setenv("MASIR_UPDATE_ALLOW_UNSIGNED", "1")
			} else {
				trust(t, pub)
			}
			list := []byte(fmt.Sprintf("%s  %s\n%s  update-linux-amd64.json\n", sum(pkg), tc.listed, sum([]byte("other"))))
			sig := minisign.Sign(tc.signer, list)
			// The caller's file name says nothing: only the signed line may name the version.
			path := filepath.Join(t.TempDir(), "masir-desktop-linux-9.9.9-amd64.deb")
			body := pkg
			if tc.alter {
				body = append([]byte{pkg[0] ^ 1}, pkg[1:]...)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}

			version, format, err := VerifyDesktopPackage(list, sig, path, tc.arch)
			if tc.version == "" {
				if err == nil {
					t.Fatalf("got %q %q, want an error", version, format)
				}
				if !errors.Is(err, ErrPackageMismatch) {
					t.Fatalf("err = %v, want ErrPackageMismatch", err)
				}
				if tc.name == "another key" && !errors.Is(err, errBadSignature) {
					t.Fatalf("err = %v, want errBadSignature too", err)
				}
				return
			}
			if err != nil || version != tc.version || format != tc.format {
				t.Fatalf("got %q %q %v, want %q %q", version, format, err, tc.version, tc.format)
			}
		})
	}

	if got := DesktopPackageName("v0.1.7", "deb", "amd64"); got != "masir-desktop-linux-0.1.7-amd64.deb" {
		t.Fatalf("DesktopPackageName = %q", got)
	}
}

const fetchName = "masir-desktop-linux-0.1.7-amd64.deb"

// releaseServer serves a signed list for pkg plus body as the package; ranges records
// the Range header of each package request.
func releaseServer(t *testing.T, pkg, body []byte) (base string, ranges *[]string) {
	t.Helper()
	return releaseServerListing(t, pkg, body, fetchName)
}

// releaseServerListing serves body at fetchName's URL, with a signed list that gives
// pkg's hash the name listed.
func releaseServerListing(t *testing.T, pkg, body []byte, listed string) (base string, ranges *[]string) {
	t.Helper()
	pub, priv := newKey(t)
	trust(t, pub)
	list := []byte(fmt.Sprintf("%s  %s\n", sum(pkg), listed))
	sig := minisign.Sign(priv, list)
	var mu sync.Mutex
	seen := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/v0.1.7/") {
		case DesktopChecksums:
			w.Write(list)
		case DesktopSignature:
			w.Write(sig)
		case fetchName:
			mu.Lock()
			*seen = append(*seen, r.Header.Get("Range"))
			mu.Unlock()
			http.ServeContent(w, r, fetchName, time.Time{}, bytes.NewReader(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, seen
}

func fetchReq(base, dir string) PackageRequest {
	return PackageRequest{Base: base, Version: "0.1.7", Format: "deb", Arch: "amd64", Dir: dir,
		Clients: Clients{Direct: http.DefaultClient}}
}

func TestFetchDesktopPackageDownloadsAndVerifies(t *testing.T) {
	pkg := []byte(strings.Repeat("masir package ", 100))
	base, _ := releaseServer(t, pkg, pkg)
	dir := filepath.Join(t.TempDir(), "dl")
	var done, total int64
	files, err := FetchDesktopPackage(context.Background(), fetchReq(base, dir), func(d, n int64) { done, total = d, n })
	if err != nil {
		t.Fatal(err)
	}
	if total != int64(len(pkg)) || done != total {
		t.Fatalf("last progress = %d/%d, want %d/%d", done, total, len(pkg), len(pkg))
	}
	for _, f := range []string{files.Package, files.Checksums, files.Signature} {
		if filepath.Dir(f) != dir {
			t.Fatalf("%s isn't in %s", f, dir)
		}
	}
	if got, _ := os.ReadFile(files.Package); !bytes.Equal(got, pkg) {
		t.Fatal("package bytes differ")
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", fi.Mode().Perm())
	}
}

func TestFetchDesktopPackageResumesAPartialFile(t *testing.T) {
	pkg := []byte(strings.Repeat("masir package ", 100))
	base, ranges := releaseServer(t, pkg, pkg)
	dir := t.TempDir()
	half := len(pkg) / 2
	if err := os.WriteFile(filepath.Join(dir, fetchName), pkg[:half], 0o600); err != nil {
		t.Fatal(err)
	}
	var done, total int64
	if _, err := FetchDesktopPackage(context.Background(), fetchReq(base, dir), func(d, n int64) { done, total = d, n }); err != nil {
		t.Fatal(err)
	}
	if total != int64(len(pkg)) || done != total {
		t.Fatalf("last progress = %d/%d, want %d/%d", done, total, len(pkg), len(pkg))
	}
	if want := fmt.Sprintf("bytes=%d-", half); len(*ranges) != 1 || (*ranges)[0] != want {
		t.Fatalf("ranges = %v, want [%s]", *ranges, want)
	}
}

func TestFetchDesktopPackageFallsBackToTheProxy(t *testing.T) {
	pkg := []byte("a masir package")
	base, _ := releaseServer(t, pkg, pkg)
	r := fetchReq(base, t.TempDir())
	r.Clients.Direct = &http.Client{Transport: failing{}}
	r.Clients.Proxy = http.DefaultClient
	if _, err := FetchDesktopPackage(context.Background(), r, func(int64, int64) {}); err != nil {
		t.Fatal(err)
	}
}

type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("no route") }

func TestFetchDesktopPackageUnreachable(t *testing.T) {
	r := fetchReq("http://127.0.0.1:1", t.TempDir())
	r.Clients.Direct = &http.Client{Transport: failing{}}
	r.Clients.Proxy = &http.Client{Transport: failing{}}
	_, err := FetchDesktopPackage(context.Background(), r, func(int64, int64) {})
	if !errors.Is(err, ErrReleaseUnreachable) {
		t.Fatalf("err = %v, want ErrReleaseUnreachable", err)
	}
}

func TestFetchDesktopPackageMissingAsset(t *testing.T) {
	pkg := []byte("a masir package")
	base, _ := releaseServer(t, pkg, pkg)
	r := fetchReq(base, t.TempDir())
	r.Version = "0.1.7"
	r.Arch = "arm64" // listed for amd64 only, so the server has no such asset
	_, err := FetchDesktopPackage(context.Background(), r, func(int64, int64) {})
	if err == nil || !strings.Contains(err.Error(), "masir-desktop-linux-0.1.7-arm64.deb") || errors.Is(err, ErrReleaseUnreachable) {
		t.Fatalf("err = %v, want a 404 naming the asset", err)
	}
}

func TestFetchDesktopPackageMismatchDeletesTheDownload(t *testing.T) {
	pkg := []byte("a masir package")
	base, _ := releaseServer(t, pkg, []byte("another package"))
	dir := t.TempDir()
	_, err := FetchDesktopPackage(context.Background(), fetchReq(base, dir), func(int64, int64) {})
	if !errors.Is(err, ErrPackageMismatch) {
		t.Fatalf("err = %v, want ErrPackageMismatch", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fetchName)); !os.IsNotExist(err) {
		t.Fatalf("the bad package is still there: %v", err)
	}
}

func TestFetchDesktopPackageTruncatedPartialIsNotAccepted(t *testing.T) {
	pkg := []byte(strings.Repeat("masir package ", 100))
	base, _ := releaseServer(t, pkg, pkg)
	dir := t.TempDir()
	stale := append(append([]byte{}, pkg...), "junk"...)
	if err := os.WriteFile(filepath.Join(dir, fetchName), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	// The stale partial fails the hash, so the fetch starts clean once and succeeds.
	files, err := FetchDesktopPackage(context.Background(), fetchReq(base, dir), func(int64, int64) {})
	if err != nil {
		t.Fatalf("err = %v, want a clean retry to succeed", err)
	}
	if got, _ := os.ReadFile(files.Package); !bytes.Equal(got, pkg) {
		t.Fatal("accepted wrong bytes")
	}
}

// Another signed release served under this version's name is not this version.
func TestFetchDesktopPackageRefusesAnotherSignedRelease(t *testing.T) {
	pkg := []byte("a masir package")
	for _, listed := range []string{"masir-desktop-linux-0.1.6-amd64.deb", "masir-desktop-linux-0.1.7-amd64.rpm"} {
		base, _ := releaseServerListing(t, pkg, pkg, listed)
		dir := t.TempDir()
		_, err := FetchDesktopPackage(context.Background(), fetchReq(base, dir), func(int64, int64) {})
		if !errors.Is(err, ErrPackageMismatch) {
			t.Fatalf("%s: err = %v, want ErrPackageMismatch", listed, err)
		}
		if _, err := os.Stat(filepath.Join(dir, fetchName)); !os.IsNotExist(err) {
			t.Fatalf("%s: the package is still there: %v", listed, err)
		}
	}
}

func TestFetchDesktopPackageRefusesPlainHTTP(t *testing.T) {
	_, err := FetchDesktopPackage(context.Background(), fetchReq("http://example.com", t.TempDir()), func(int64, int64) {})
	if err == nil || !strings.Contains(err.Error(), "need HTTPS") {
		t.Fatalf("err = %v, want an HTTPS refusal", err)
	}
}

func TestFetchDesktopPackageStaysInItsDirectory(t *testing.T) {
	for _, bad := range []func(*PackageRequest){
		func(r *PackageRequest) { r.Version = "../../x" },
		func(r *PackageRequest) { r.Format = "deb/../../x" },
		func(r *PackageRequest) { r.Arch = "amd64/../../x" },
	} {
		r := fetchReq("https://example.com", t.TempDir())
		bad(&r)
		if _, err := FetchDesktopPackage(context.Background(), r, func(int64, int64) {}); err == nil || !strings.Contains(err.Error(), "refusing desktop package") {
			t.Fatalf("%+v: err = %v, want a refusal", r, err)
		}
	}
}

func TestFetchDesktopPackageProxy404IsNotUnreachable(t *testing.T) {
	pkg := []byte("a masir package")
	base, _ := releaseServer(t, pkg, pkg)
	r := fetchReq(base, t.TempDir())
	r.Arch = "arm64"
	r.Clients.Direct = &http.Client{Transport: failing{}}
	r.Clients.Proxy = http.DefaultClient
	_, err := FetchDesktopPackage(context.Background(), r, func(int64, int64) {})
	if err == nil || !strings.Contains(err.Error(), "masir-desktop-linux-0.1.7-arm64.deb") || errors.Is(err, ErrReleaseUnreachable) {
		t.Fatalf("err = %v, want a 404 naming the asset", err)
	}
}
