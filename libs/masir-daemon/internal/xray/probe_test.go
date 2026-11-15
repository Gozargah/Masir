package xray

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestProbeCancellationReleasesWorker(t *testing.T) {
	started := make(chan struct{}, 1)
	disconnected := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		disconnected <- struct{}{}
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := MeasureDelay(ctx, []byte(`{"outbounds":[{"protocol":"freedom"}]}`), LatencyOptions{URL: srv.URL, Attempts: 1})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("probe never reached server")
	}
	// Another caller must be able to cancel while waiting for the occupied slot.
	queued, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	_, err := MeasureDelay(queued, nil, LatencyOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued cancellation: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled helper was not reaped")
	}
	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled helper kept its connection")
	}
	// This runs only after runProbe has waited for the previous child to exit.
	if _, err := MeasureDelay(context.Background(), []byte(`{"outbounds":[]}`), LatencyOptions{}); err == nil {
		t.Fatal("invalid config succeeded")
	}
	if len(probeSlot) != 0 {
		t.Fatal("worker slot leaked")
	}
}

func TestProbeBatchStreamsAndPreservesPartialResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arrived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer func() { cancel(); srv.Close() }()
	// Same caller tag is intentional: internal result identity must use indexes.
	probes := []Probe{
		{Tag: "same", Outbound: json.RawMessage(`not json`)},
		{Tag: "same", Outbound: json.RawMessage(`{"protocol":"freedom"}`)},
	}
	results := make(chan error, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		MeasureEach(ctx, probes, LatencyOptions{URL: srv.URL, Attempts: 1}, 2, func(_ string, _ time.Duration, err error) {
			results <- err
		})
	}()
	select {
	case err := <-results:
		if err == nil {
			t.Fatal("malformed outbound succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("early failure was not streamed")
	}
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("valid outbound was blocked by invalid neighbor")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("batch cancellation hung")
	}
	if err := <-results; !errors.Is(err, context.Canceled) {
		t.Fatalf("unfinished result: %v", err)
	}
	select {
	case <-results:
		t.Fatal("duplicate result")
	default:
	}
}

func TestProbeBatchesAndConcurrentCallers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	var callers sync.WaitGroup
	for range 2 {
		callers.Go(func() {
			probes := make([]Probe, maxProbeBatch+1)
			for i := range probes {
				probes[i] = Probe{Tag: "same", Outbound: json.RawMessage(`{"protocol":"freedom"}`)}
			}
			count := 0
			MeasureEach(context.Background(), probes, LatencyOptions{URL: srv.URL, Attempts: 1}, 2, func(_ string, _ time.Duration, err error) {
				count++
				if err != nil {
					t.Error(err)
				}
			})
			if count != len(probes) {
				t.Errorf("got %d results, want %d", count, len(probes))
			}
		})
	}
	callers.Wait()
}

func TestProbeDoesNotOpenConfiguredLogFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "must-not-exist.log")
	cfg, err := json.Marshal(map[string]any{
		"log":       map[string]any{"loglevel": "debug", "error": path, "access": path},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
		// Probes never bind caller-supplied listeners.
		"inbounds": []any{map[string]any{"protocol": "socks", "listen": "192.0.2.1", "port": 7890}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MeasureDelay(context.Background(), cfg, LatencyOptions{URL: srv.URL, Attempts: 1, Full: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("probe touched log file: %v", err)
	}
}
