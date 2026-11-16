package xray

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clog "github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/platform"
)

func TestLogSinkLevels(t *testing.T) {
	var gotLevel, gotMsg string
	h := &teeHandler{sink: func(level, msg string) {
		gotLevel, gotMsg = level, msg
	}}
	h.Handle(&clog.GeneralMessage{Severity: clog.Severity_Warning, Content: "there is a problem"})
	if gotLevel != "warn" || gotMsg != "there is a problem" {
		t.Fatalf("warning = %q %q", gotLevel, gotMsg)
	}
	h.Handle(&clog.GeneralMessage{Severity: clog.Severity_Error, Content: "it failed"})
	if gotLevel != "error" || gotMsg != "it failed" {
		t.Fatalf("error = %q %q", gotLevel, gotMsg)
	}
}

// Latency tests create Xray instances in helper processes. The main
// instance must continue feeding the log stream without a log-level toggle.
func TestLogSinkSurvivesLatencyTests(t *testing.T) {
	for _, mode := range []string{"each", "delay", "failed-delay"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			seen := map[string]bool{}
			SetLogSink(func(_ string, msg string) {
				mu.Lock()
				seen[msg] = true
				mu.Unlock()
			})
			t.Cleanup(func() { SetLogSink(nil) })
			raw, err := json.Marshal(map[string]any{
				"log":       map[string]any{"loglevel": "info", "error": filepath.Join(t.TempDir(), "xray-error.log")},
				"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			p, err := ParseProfile(raw)
			if err != nil {
				t.Fatal(err)
			}
			startCore(t, p)
			assertLog := func(text string) {
				t.Helper()
				clog.Record(&clog.GeneralMessage{Severity: clog.Severity_Info, Content: text})
				mu.Lock()
				got := seen[text]
				mu.Unlock()
				if !got {
					t.Errorf("main info log missing from stream: %s", text)
				}
			}
			assertLog("before probe")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertLog("during probe")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			opt := LatencyOptions{URL: srv.URL, Attempts: 1}
			switch mode {
			case "each":
				MeasureEach(context.Background(), []Probe{{Tag: "probe", Outbound: json.RawMessage(`{"protocol":"freedom"}`)}}, opt, 1, func(_ string, _ time.Duration, err error) {
					if err != nil {
						t.Error(err)
					}
				})
			case "delay":
				if _, err := MeasureDelay(context.Background(), []byte(`{"outbounds":[{"protocol":"freedom"}]}`), opt); err != nil {
					t.Fatal(err)
				}
			case "failed-delay":
				bad := []byte(`{"outbounds":[]}`)
				if _, err := MeasureDelay(context.Background(), bad, LatencyOptions{Full: true}); err == nil {
					t.Fatal("wanted instance creation failure")
				}
			}
			assertLog("after probe")
		})
	}
}

// Restoring globals after core.New is too late: initialization itself mutates
// process-wide state. Probes must never initialize Xray in the daemon process.
func TestLatencyDoesNotInitializeXrayInParent(t *testing.T) {
	var reloads atomic.Int32
	platform.RegisterEnvReload(func() error { reloads.Add(1); return nil })
	before := reloads.Load()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	_, err := MeasureDelay(context.Background(), []byte(`{"outbounds":[{"protocol":"freedom"}]}`), LatencyOptions{URL: srv.URL, Attempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := reloads.Load() - before; got != 0 {
		t.Fatalf("probe initialized Xray in the parent process %d times", got)
	}
}
