package server

import (
	"sync"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/analytics"
)

type connectivityBucket struct{ count, total, max int64 }
type connectivityMetrics struct {
	mu      sync.Mutex
	since   time.Time
	buckets map[string]connectivityBucket
}

// These are summaries of existing probes, never new traffic or per-destination
// logs. The bounded error vocabulary caps both memory and telemetry cardinality.
func (s *Server) recordConnectivity(duration time.Duration, err error) {
	if s.analytics == nil || !s.analytics.Enabled() {
		return
	}
	kind := "success"
	if err != nil {
		kind = analytics.ErrorKind(err)
	}
	s.connectivity.mu.Lock()
	defer s.connectivity.mu.Unlock()
	if s.connectivity.buckets == nil {
		s.connectivity.buckets = map[string]connectivityBucket{}
		s.connectivity.since = time.Now()
	}
	b := s.connectivity.buckets[kind]
	b.count++
	b.total += duration.Milliseconds()
	if duration.Milliseconds() > b.max {
		b.max = duration.Milliseconds()
	}
	s.connectivity.buckets[kind] = b
}
func (s *Server) flushConnectivity() {
	s.connectivity.mu.Lock()
	buckets, since := s.connectivity.buckets, s.connectivity.since
	s.connectivity.buckets = nil
	s.connectivity.mu.Unlock()
	for kind, b := range buckets {
		// Kind describes the observed error; a timeout is not proof of a TCP or TLS stage.
		s.track("connectivity_summary", map[string]any{"source": "existing_probe", "outcome": kind, "count": b.count, "duration_total_ms": b.total, "duration_max_ms": b.max, "window_seconds": time.Since(since).Seconds(), "dns_strategy": "UseIPv4"})
	}
}
