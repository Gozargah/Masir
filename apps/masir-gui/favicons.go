package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	faviconSize          = 32
	faviconTTL           = 7 * 24 * time.Hour
	faviconRetryInterval = 10 * time.Minute
)

// cleanRuleDomain extracts a domain name suitable for fetching a favicon.
// It handles domain rules, full domains, and geosite categories.
func cleanRuleDomain(raw string) string {
	d := strings.TrimSpace(raw)
	if i := strings.IndexByte(d, '@'); i != -1 {
		d = d[:i]
	}
	for _, prefix := range []string{"geosite:", "full:", "domain:", "regexp:", "keyword:"} {
		d = strings.TrimPrefix(d, prefix)
	}
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimPrefix(d, ".")
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	if i := strings.IndexByte(d, '/'); i != -1 {
		d = d[:i]
	}
	if i := strings.IndexByte(d, ':'); i != -1 {
		d = d[:i]
	}
	d = strings.TrimSpace(strings.ToLower(d))
	if d == "" || strings.HasPrefix(d, "category-") || strings.HasPrefix(d, "geolocation-") {
		return ""
	}
	if strings.ContainsAny(d, "^$[]*+?\\!#%&'\"") {
		return ""
	}
	if strings.Contains(d, ".") {
		return d
	}
	// For geosite website codes without a dot (e.g. "google", "github", "youtube", "netflix")
	if len(d) > 2 {
		return d + ".com"
	}
	return ""
}

type faviconEntry struct {
	bitmap   *ui.Bitmap
	cachedAt time.Time
	failedAt time.Time
}

func (e *faviconEntry) shouldRevalidate(ttl, retryInterval time.Duration, now time.Time) bool {
	if e.bitmap == nil {
		if !e.failedAt.IsZero() && now.Sub(e.failedAt) < retryInterval {
			return false
		}
		return true
	}
	if !e.failedAt.IsZero() && now.Sub(e.failedAt) < retryInterval {
		return false
	}
	return now.Sub(e.cachedAt) > ttl
}

// faviconStore manages cached website favicons using an SWR strategy with disk persistence.
type faviconStore struct {
	mu            sync.Mutex
	known         map[string]*faviconEntry
	queue         []string
	queued        map[string]bool
	loading       bool
	cacheDir      string
	ttl           time.Duration
	retryInterval time.Duration
	size          int
	fetchFn       func(domain string, sz int) ([]byte, error)
}

func newFaviconStore() *faviconStore {
	return &faviconStore{
		known:         map[string]*faviconEntry{},
		queued:        map[string]bool{},
		ttl:           faviconTTL,
		retryInterval: faviconRetryInterval,
		size:          faviconSize,
	}
}

func (s *faviconStore) dir() string {
	if s.cacheDir != "" {
		return s.cacheDir
	}
	if mygo.App.IsReady() {
		if dir, err := mygo.App.Path(mygo.PathCache); err == nil && dir != "" {
			return filepath.Join(dir, "favicons")
		}
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "masir", "favicons")
	}
	return filepath.Join(os.TempDir(), "masir-favicons")
}

func (s *faviconStore) diskPath(domain string) string {
	sum := sha1.Sum([]byte(strings.ToLower(domain)))
	return filepath.Join(s.dir(), hex.EncodeToString(sum[:])+".png")
}

func (s *faviconStore) readDisk(domain string) (*ui.Bitmap, time.Time, bool) {
	path := s.diskPath(domain)
	fi, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	bmp, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil, time.Time{}, false
	}
	return bmp, fi.ModTime(), true
}

func (s *faviconStore) saveDisk(domain string, data []byte) {
	dir := s.dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := s.diskPath(domain)
	_ = os.WriteFile(path, data, 0o644)
}

func defaultFaviconFetch(domain string, sz int) ([]byte, error) {
	apiURL := fmt.Sprintf("https://www.google.com/s2/favicons?domain=%s&sz=%d", url.QueryEscape(domain), sz)
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("favicon fetch returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *faviconStore) fetch(domain string) ([]byte, error) {
	if s.fetchFn != nil {
		return s.fetchFn(domain, s.size)
	}
	if !mygo.App.IsReady() {
		return nil, fmt.Errorf("app not ready for network fetch")
	}
	return defaultFaviconFetch(domain, s.size)
}

// lookup returns what is known of the domain favicon (or loads it from disk cache),
// and queues background fetch/revalidation if missing or stale (SWR).
func (s *faviconStore) lookup(domain string, async func(func()), done func()) *ui.Bitmap {
	if domain == "" {
		return nil
	}
	s.mu.Lock()
	now := time.Now()
	entry, ok := s.known[domain]
	if !ok {
		entry = &faviconEntry{}
		if bmp, modTime, ok := s.readDisk(domain); ok {
			entry.bitmap = bmp
			entry.cachedAt = modTime
		}
		s.known[domain] = entry
	}

	needsReval := entry.shouldRevalidate(s.ttl, s.retryInterval, now)
	if needsReval && !s.queued[domain] {
		s.queued[domain] = true
		s.queue = append(s.queue, domain)
	}

	start := !s.loading && len(s.queue) > 0
	if start {
		s.loading = true
	}
	bmp := entry.bitmap
	s.mu.Unlock()

	if start {
		s.work(async, done)
	}
	return bmp
}

// work processes queued favicon lookups in the background.
func (s *faviconStore) work(async func(func()), done func()) {
	async(func() {
		for {
			s.mu.Lock()
			if len(s.queue) == 0 {
				s.loading = false
				s.mu.Unlock()
				return
			}
			domain := s.queue[0]
			s.queue = s.queue[1:]
			delete(s.queued, domain)
			s.mu.Unlock()

			data, err := s.fetch(domain)
			var bmp *ui.Bitmap
			if err == nil {
				bmp, err = ui.DecodeBitmap(data)
			}

			s.mu.Lock()
			entry := s.known[domain]
			if entry == nil {
				entry = &faviconEntry{}
				s.known[domain] = entry
			}

			updated := false
			if err == nil && bmp != nil {
				// Succeeded: update cache in memory and on disk
				entry.bitmap = bmp
				entry.cachedAt = time.Now()
				entry.failedAt = time.Time{}
				s.saveDisk(domain, data)
				updated = true
			} else {
				// Failed: fallback to prev stored favicon for the website
				if entry.bitmap == nil {
					if diskBmp, modTime, ok := s.readDisk(domain); ok {
						entry.bitmap = diskBmp
						entry.cachedAt = modTime
						updated = true
					}
				}
				entry.failedAt = time.Now()
			}
			s.mu.Unlock()

			if updated && done != nil {
				done()
			}
		}
	})
}
