package xray

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	// Occupy the other helper slots, then check cancellation while queued.
	for range cap(probeSlot) - 1 {
		probeSlot <- struct{}{}
	}
	defer func() {
		for range cap(probeSlot) - 1 {
			<-probeSlot
		}
	}()
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
	if len(probeSlot) != cap(probeSlot)-1 {
		t.Fatal("worker slot leaked")
	}
}

func TestProbeStreamsAndPreservesPartialResults(t *testing.T) {
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

func TestProbeConcurrentCallers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	var callers sync.WaitGroup
	for range 2 {
		callers.Go(func() {
			probes := make([]Probe, 17)
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

func TestNullProbeDoesNotFailValidNeighbor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	seen := map[string]bool{}
	MeasureEach(context.Background(), []Probe{
		{Tag: "invalid", Outbound: json.RawMessage("null")},
		{Tag: "valid", Outbound: json.RawMessage(`{"protocol":"freedom"}`)},
	}, LatencyOptions{URL: srv.URL, Attempts: 1}, 1, func(tag string, _ time.Duration, err error) {
		seen[tag] = true
		if (tag == "invalid") != (err != nil) {
			t.Errorf("%s: %v", tag, err)
		}
	})
	if len(seen) != 2 {
		t.Fatalf("missing results: %v", seen)
	}
}

func TestProbeIgnoresXrayNoiseOnStdout(t *testing.T) {
	// Xray logs to stdout on its own: a deprecation warning while the config parses
	// (before the probe's quiet log config exists), REALITY's `show` trace, congestion
	// debug. One such line ahead of the first result used to fail the decode for the
	// whole batch: "json: cannot unmarshal number into Go value of type probeResult".
	stream := "" +
		"2026/11/16 17:39:16.880425 [Warning] common/errors: The feature Trojan (with no Flow, etc.) is deprecated, not recommended for using and might be removed.\n" +
		"REALITY remoteAddr: 203.0.113.7:443\tis using X25519MLKEM768 for TLS' communication: true\n" +
		"\n" +
		"{\"Index\":0,\"Duration\":1000920,\"Error\":\"\"}\n" +
		"{\"Index\":1,\"Duration\":0,\"Error\":\"dial tcp: connection refused\"}\n"
	var got []probeResult
	received, err := readProbeResults(bufio.NewReader(strings.NewReader(stream)), 2, func(r probeResult) {
		got = append(got, r)
	})
	if err != nil {
		t.Fatalf("noise failed the decode: %v", err)
	}
	if received != 2 || len(got) != 2 {
		t.Fatalf("got %d results, want 2", received)
	}
	if got[0].Duration != 1000920*time.Nanosecond || got[0].Error != "" {
		t.Errorf("first result: %+v", got[0])
	}
	if got[1].Error != "dial tcp: connection refused" {
		t.Errorf("second result: %+v", got[1])
	}
}

func TestProbeRejectsMalformedResult(t *testing.T) {
	_, err := readProbeResults(bufio.NewReader(strings.NewReader("{\"Index\":0,\"Duration\":\"slow\"}\n")), 1, func(probeResult) {})
	if err == nil {
		t.Fatal("malformed result line accepted")
	}
	_, err = readProbeResults(bufio.NewReader(strings.NewReader("{\"Index\":2,\"Duration\":1}\n")), 1, func(probeResult) {})
	if err == nil {
		t.Fatal("out-of-range index accepted")
	}
	_, err = readProbeResults(bufio.NewReader(strings.NewReader("{\"Index\":0}\n{\"Index\":0}\n")), 2, func(probeResult) {})
	if err == nil {
		t.Fatal("duplicate index accepted")
	}
}

func TestProbeRejectsExcessiveBudget(t *testing.T) {
	if _, err := MeasureDelay(context.Background(), nil, LatencyOptions{Timeout: probeLifetime, Attempts: 2}); err == nil {
		t.Fatal("unserviceable request budget accepted")
	}
}

func TestIsolatedBalancedProfileProbe(t *testing.T) {
	healthReady := make(chan struct{})
	var markHealthy sync.Once
	earlyRequest := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			// The first probe reaches the fallback before health is known.
			time.Sleep(350 * time.Millisecond)
			markHealthy.Do(func() { close(healthReady) })
		} else {
			select {
			case <-healthReady:
			default:
				select {
				case earlyRequest <- struct{}{}:
				default:
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	cfg := []byte(strings.ReplaceAll(`{
		"outbounds":[{"tag":"node","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}],
		"observatory":{"subjectSelector":["node"],"probeURL":"HEALTH_URL"},
		"routing":{
			"balancers":[
				{"tag":"masir-fallback","selector":["block"]},
				{"tag":"pool","selector":["node"],"fallbackTag":"block","strategy":{"type":"leastPing"}}
			],
			"rules":[{"protocol":["http"],"balancerTag":"pool"},{"network":"tcp,udp","outboundTag":"block"}]
		}
	}`, "HEALTH_URL", target.URL+"/health"))
	// Colliding provider tags must remain isolated while both profiles run.
	results := 0
	MeasureEach(context.Background(), []Probe{{Tag: "same", Profile: cfg}, {Tag: "same", Profile: cfg}}, LatencyOptions{
		URL: target.URL, Attempts: 1, Timeout: 4 * time.Second,
	}, 2, func(_ string, _ time.Duration, err error) {
		results++
		if err != nil {
			t.Error(err)
		}
	})
	if results != 2 {
		t.Fatalf("got %d profile results", results)
	}
	select {
	case <-earlyRequest:
		t.Fatal("probe reached the provider member before the observer reported health")
	default:
	}
}

func TestProbeSlowConfigDoesNotHoldLaterConfigs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var first sync.Once
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slow := false
		first.Do(func() { slow = true })
		if slow {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	defer cancel()
	probes := make([]Probe, 17)
	for i := range probes {
		probes[i] = Probe{Tag: "same", Outbound: json.RawMessage(`{"protocol":"freedom"}`)}
	}
	results := make(chan error, len(probes))
	done := make(chan struct{})
	go func() {
		defer close(done)
		MeasureEach(ctx, probes, LatencyOptions{URL: target.URL, Attempts: 1}, 8, func(_ string, _ time.Duration, err error) { results <- err })
	}()
	for range len(probes) - 1 {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("fast config: %v", err)
			}
		case <-ctx.Done():
			<-done
			t.Fatal("a slow config blocked later configs")
		}
	}
	cancel()
	<-done
	if err := <-results; !errors.Is(err, context.Canceled) {
		t.Fatalf("slow config: %v", err)
	}
}

func TestProbeGlobalLimitAcrossCallers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	arrived := make(chan struct{}, 32)
	permits := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-permits:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer target.Close()
	defer cancel()
	var callers sync.WaitGroup
	for range 2 {
		callers.Go(func() {
			probes := make([]Probe, maxProbeWorkers)
			for i := range probes {
				probes[i] = Probe{Outbound: json.RawMessage(`{"protocol":"freedom"}`)}
			}
			MeasureEach(ctx, probes, LatencyOptions{URL: target.URL, Attempts: 1}, 32, func(_ string, _ time.Duration, err error) {
				if err != nil && ctx.Err() == nil {
					t.Errorf("probe: %v", err)
				}
			})
		})
	}
	defer callers.Wait()
	defer cancel()
	for range maxProbeWorkers {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("parallel helper slots not filled")
		}
	}
	select {
	case <-arrived:
		t.Fatal("global helper limit exceeded")
	case <-time.After(100 * time.Millisecond):
	}
	close(permits)
	callers.Wait()
	if len(probeSlot) != 0 {
		t.Fatal("helper slots leaked")
	}
}
