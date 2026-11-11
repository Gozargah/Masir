package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func makeTestPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for x := 0; x < 16; x++ {
		for y := 0; y < 16; y++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}
	return buf.Bytes()
}

func TestCleanRuleDomain(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"google.com", "google.com"},
		{"domain:google.com", "google.com"},
		{"full:google.com", "google.com"},
		{"geosite:google", "google.com"},
		{"geosite:github", "github.com"},
		{"geosite:youtube", "youtube.com"},
		{"geosite:netflix", "netflix.com"},
		{"geosite:google@cn", "google.com"},
		{"*.example.com", "example.com"},
		{".example.com", "example.com"},
		{"https://sub.domain.org/path", "sub.domain.org"},
		{"domain:sub.domain.org:8080", "sub.domain.org"},
		{"geosite:category-ads", ""},
		{"geosite:category-ads-all", ""},
		{"geosite:geolocation-!cn", ""},
		{"geosite:cn", ""},
		{"geosite:ir", ""},
		{"regexp:^.*\\.google\\.com$", ""},
		{"", ""},
		{"   ", ""},
	}

	for _, c := range cases {
		got := cleanRuleDomain(c.input)
		if got != c.want {
			t.Errorf("cleanRuleDomain(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFaviconStoreSWRAndFallback(t *testing.T) {
	dir := t.TempDir()
	png1 := makeTestPNG(t, color.RGBA{R: 255, A: 255})
	png2 := makeTestPNG(t, color.RGBA{B: 255, A: 255})

	var fetchCalls atomic.Int32
	var shouldFail atomic.Bool
	var currentPNG atomic.Value
	currentPNG.Store(png1)

	store := newFaviconStore()
	store.cacheDir = dir
	store.ttl = 7 * 24 * time.Hour
	store.retryInterval = 1 * time.Minute
	store.fetchFn = func(domain string, sz int) ([]byte, error) {
		fetchCalls.Add(1)
		if shouldFail.Load() {
			return nil, fmt.Errorf("network error: domain unreachable")
		}
		return currentPNG.Load().([]byte), nil
	}

	inlineAsync := func(fn func()) { fn() }
	var doneCalls atomic.Int32
	done := func() { doneCalls.Add(1) }

	domain := "example.com"

	// 1. Initial lookup: not cached yet
	bmp := store.lookup(domain, inlineAsync, done)
	if bmp == nil {
		// Because inlineAsync ran synchronously, the worker ran and updated the entry
		bmp = store.lookup(domain, inlineAsync, done)
	}
	if bmp == nil {
		t.Fatalf("expected bitmap to be loaded after synchronous fetch")
	}
	if fetchCalls.Load() != 1 {
		t.Fatalf("expected 1 fetch call, got %d", fetchCalls.Load())
	}

	// Verify it was saved to disk
	diskPath := store.diskPath(domain)
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatalf("disk file not found at %s: %v", diskPath, err)
	}

	// 2. Second lookup within TTL (~7 days): should return cached bitmap immediately without fetching
	bmp2 := store.lookup(domain, inlineAsync, done)
	if bmp2 == nil {
		t.Fatalf("expected cached bitmap, got nil")
	}
	if fetchCalls.Load() != 1 {
		t.Fatalf("expected fetchCalls to remain 1 for fresh cache, got %d", fetchCalls.Load())
	}

	// 3. Simulate cache expiration (stale after > 7 days)
	store.mu.Lock()
	entry := store.known[domain]
	staleTime := time.Now().Add(-8 * 24 * time.Hour)
	entry.cachedAt = staleTime
	// Also adjust disk modTime
	_ = os.Chtimes(diskPath, staleTime, staleTime)
	store.mu.Unlock()

	// Update current image for revalidation
	currentPNG.Store(png2)

	// SWR lookup: must return previous cached bitmap immediately, and trigger background revalidation
	bmpStale := store.lookup(domain, inlineAsync, done)
	if bmpStale == nil {
		t.Fatalf("SWR lookup must return existing cached bitmap, got nil")
	}
	if fetchCalls.Load() != 2 {
		t.Fatalf("expected 2 fetch calls after revalidation, got %d", fetchCalls.Load())
	}

	// 4. Stale again, but this time revalidation FAILS: must fallback to prev stored favicon
	store.mu.Lock()
	entry = store.known[domain]
	entry.cachedAt = staleTime
	_ = os.Chtimes(diskPath, staleTime, staleTime)
	store.mu.Unlock()

	shouldFail.Store(true)

	bmpBeforeFail := store.lookup(domain, inlineAsync, done)
	if bmpBeforeFail == nil {
		t.Fatalf("lookup must return prev stored bitmap, got nil")
	}
	if fetchCalls.Load() != 3 {
		t.Fatalf("expected 3 fetch calls, got %d", fetchCalls.Load())
	}

	// After failed refetch, subsequent lookups must continue returning prev stored favicon!
	bmpAfterFail := store.lookup(domain, inlineAsync, done)
	if bmpAfterFail == nil {
		t.Fatalf("after failed refetch, store must fallback to prev stored favicon, got nil")
	}
	// And verify disk file still exists and was not deleted/overwritten
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatalf("disk file should still exist as fallback: %v", err)
	}
}

func TestFaviconStoreDiskPersistenceAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	pngData := makeTestPNG(t, color.RGBA{G: 255, A: 255})
	domain := "github.com"

	// Instance 1: fetches and caches to disk
	store1 := newFaviconStore()
	store1.cacheDir = dir
	store1.fetchFn = func(dom string, sz int) ([]byte, error) {
		return pngData, nil
	}
	inlineAsync := func(fn func()) { fn() }
	_ = store1.lookup(domain, inlineAsync, nil)

	// Instance 2: new store (simulating app restart) with no network (fetch fails)
	store2 := newFaviconStore()
	store2.cacheDir = dir
	store2.fetchFn = func(dom string, sz int) ([]byte, error) {
		return nil, fmt.Errorf("offline")
	}

	// Must load from disk cache immediately
	bmp := store2.lookup(domain, inlineAsync, nil)
	if bmp == nil {
		t.Fatalf("expected store2 to load cached favicon from disk on startup, got nil")
	}
}

func TestFaviconAPIURLFormat(t *testing.T) {
	var requestedURL string
	var requestedDomain string
	var requestedSz string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURL = r.URL.String()
		requestedDomain = r.URL.Query().Get("domain")
		requestedSz = r.URL.Query().Get("sz")
		pngData := makeTestPNG(t, color.RGBA{R: 128, G: 128, B: 128, A: 255})
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngData)
	}))
	defer ts.Close()

	store := newFaviconStore()
	store.cacheDir = t.TempDir()
	store.fetchFn = func(dom string, sz int) ([]byte, error) {
		// Verify URL formatting matches Google favicon API specification
		apiURL := fmt.Sprintf("%s/s2/favicons?domain=%s&sz=%d", ts.URL, url.QueryEscape(dom), sz)
		resp, err := http.Get(apiURL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return buf.Bytes(), nil
	}

	_ = store.lookup("google.com", func(fn func()) { fn() }, nil)

	if requestedDomain != "google.com" {
		t.Errorf("expected domain=google.com, got %q (url: %s)", requestedDomain, requestedURL)
	}
	if requestedSz != "32" {
		t.Errorf("expected sz=32, got %q (url: %s)", requestedSz, requestedURL)
	}
}

func TestRoutingRuleDomainInfo(t *testing.T) {
	r1 := &pb.RoutingRule{
		RuleTag:  "google-rule",
		RuleJson: `{"domain":["geosite:google","full:google.com"]}`,
	}
	inf1 := ruleInfo(r1)
	if inf1.domain != "google.com" {
		t.Errorf("expected domain 'google.com', got %q", inf1.domain)
	}
	if inf1.section != "Website Categories" {
		t.Errorf("expected section 'Website Categories', got %q", inf1.section)
	}

	r2 := &pb.RoutingRule{
		RuleTag:  "bank-rule",
		RuleJson: `{"domain":["full:bank.test"]}`,
	}
	inf2 := ruleInfo(r2)
	if inf2.domain != "bank.test" {
		t.Errorf("expected domain 'bank.test', got %q", inf2.domain)
	}
	if inf2.section != "Domains" {
		t.Errorf("expected section 'Domains', got %q", inf2.section)
	}

	r3 := &pb.RoutingRule{
		RuleTag:  "app-rule",
		RuleJson: `{"process":["/Applications/Safari.app/"]}`,
	}
	inf3 := ruleInfo(r3)
	if inf3.domain != "" {
		t.Errorf("expected empty domain for app rule, got %q", inf3.domain)
	}
}
