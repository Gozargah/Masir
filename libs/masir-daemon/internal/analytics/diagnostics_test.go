package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreanalytics "github.com/Gozargah/Masir/libs/masir-core/analytics"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/update"
	"github.com/posthog/posthog-go"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReleaseDiagnosticsSurviveRPC(t *testing.T) {
	for _, tc := range []struct {
		status                  int
		remaining, kind, reason string
	}{
		{429, "", "rate_limited", "release_rate_limited"},
		{403, "0", "rate_limited", "release_rate_limited"},
		{403, "10", "http", "release_http_status"},
		{404, "", "not_found", "release_not_found"},
		{502, "", "http", "release_http_status"},
	} {
		hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Status: http.StatusText(tc.status), Header: http.Header{"X-Ratelimit-Remaining": []string{tc.remaining}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		_, err := update.Latest(context.Background(), update.Clients{Direct: hc}, "example/repo")
		if err == nil {
			t.Fatal("expected release error")
		}
		err = RPCError(err)
		if got := ErrorKind(err); got != tc.kind {
			t.Errorf("status %d: kind=%s", tc.status, got)
		}
		if got := ErrorReason(err); got != tc.reason {
			t.Errorf("status %d: reason=%s", tc.status, got)
		}
		if HTTPStatus(err) != tc.status {
			t.Errorf("lost HTTP status %d", tc.status)
		}
	}
}
func TestTransportDiagnosticsSurviveRPC(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind string
	}{
		{context.DeadlineExceeded, "timeout"},
		{&net.DNSError{Err: "private DNS failure", Name: "secret.example.com"}, "dns"},
	} {
		if got := ErrorKind(RPCError(tc.err)); got != tc.kind {
			t.Errorf("kind=%s want %s", got, tc.kind)
		}
	}
}

func TestUpdateVerificationAcrossProcesses(t *testing.T) {
	for _, tc := range []struct{ running, outcome string }{{"0.1.3", "success"}, {"v0.1.3", "success"}, {"0.1.2", "version_mismatch"}} {
		t.Run(tc.running, func(t *testing.T) {
			dir := t.TempDir()
			id := coreanalytics.EventID()
			old := &Client{dir: dir, enabled: true, api: &fakeSink{}}
			old.AwaitUpdateRestart(id, "0.1.2", "v0.1.3")
			sink := &fakeSink{}
			next := &Client{dir: dir, enabled: true, api: sink, version: tc.running}
			next.VerifyUpdateRestart(tc.running)
			next.VerifyUpdateRestart(tc.running)
			if len(sink.events) != 1 {
				t.Fatalf("verification events=%d", len(sink.events))
			}
			p := sink.events[0].Properties
			if p["outcome"] != tc.outcome || p["operation_id"] != id || p["success"] != (tc.outcome == "success") {
				t.Fatalf("verification=%v", p)
			}
		})
	}
}
func TestOutboxSurvivesProcessAndAcknowledgesDelivery(t *testing.T) {
	dir := t.TempDir()
	first := &fakeSink{}
	c := &Client{dir: dir, installID: "install", enabled: true, api: first}
	c.Track("daemon_started", nil)
	if len(first.events) != 1 {
		t.Fatal("event not queued")
	}
	event := first.events[0]
	second := &fakeSink{}
	restarted := &Client{dir: dir, installID: "install", enabled: true, api: second}
	restarted.DrainOutbox(t.Context())
	if len(second.events) != 1 || second.events[0].Uuid != event.Uuid || !second.events[0].Timestamp.Equal(event.Timestamp) {
		t.Fatalf("unstable replay: %+v", second.events)
	}
	restarted.Failure(second.events[0].APIfy(), errors.New("offline"))
	restarted.DrainOutbox(t.Context())
	if len(second.events) != 2 || second.events[1].Uuid != event.Uuid {
		t.Fatal("retry changed identity")
	}
	restarted.Success(second.events[1].APIfy())
	restarted.DrainOutbox(t.Context())
	entries, _ := os.ReadDir(filepath.Join(dir, outboxDir))
	if len(entries) != 0 {
		t.Fatal("acknowledged event still pending")
	}
}
func TestOptOutPurgesDurableTelemetry(t *testing.T) {
	dir := t.TempDir()
	c := &Client{dir: dir, enabled: true, api: &fakeSink{}}
	c.Track("daemon_started", nil)
	c.AwaitUpdateRestart(coreanalytics.EventID(), "0.1.2", "0.1.3")
	c.SetEnabled(false)
	for _, p := range []string{outboxDir, pendingUpdateFile} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("opt-out retained %s: %v", p, err)
		}
	}
}
func TestExceptionPrivacyAndExpectedFailures(t *testing.T) {
	sink := &exceptionSink{}
	c := &Client{dir: t.TempDir(), installID: "install", enabled: true, api: sink}
	c.TrackError("xray_apply_failed", errors.New("listen tcp 127.0.0.1:7890: address already in use"), nil, true)
	if len(sink.exceptions) != 0 {
		t.Fatal("expected failure became an exception")
	}
	c.TrackError("daemon_panic", errors.New(`panic in /Users/private/config.json for "personal server" https://secret.example.com/token`), nil, true)
	if len(sink.exceptions) != 1 {
		t.Fatal("panic not captured as an exception")
	}
	e := sink.exceptions[0]
	if strings.Contains(e.ExceptionList[0].Value, "private") || strings.Contains(e.ExceptionList[0].Value, "secret.example.com") {
		t.Fatal("exception leaked private context")
	}
	if e.Properties["$process_person_profile"] != false {
		t.Fatal("person profiles enabled")
	}
	for _, f := range e.ExceptionList[0].Stacktrace.Frames {
		if filepath.IsAbs(f.Filename) || strings.Contains(f.Function, "github.com/") || f.InstructionAddr != "" {
			t.Fatalf("unsafe frame: %+v", f)
		}
	}
}

type exceptionSink struct{ exceptions []posthog.Exception }

func (s *exceptionSink) Enqueue(m posthog.Message) error {
	if e, ok := m.(posthog.Exception); ok {
		s.exceptions = append(s.exceptions, e)
	}
	return nil
}
func (*exceptionSink) Close() error { return nil }

func TestOutboxBounded(t *testing.T) {
	c := &Client{dir: t.TempDir(), enabled: true, api: &fakeSink{}}
	for i := 0; i < maxOutboxEvents+10; i++ {
		c.Track("daemon_started", nil)
	}
	entries, _ := os.ReadDir(filepath.Join(c.dir, outboxDir))
	if len(entries) > maxOutboxEvents {
		t.Fatalf("outbox grew to %d", len(entries))
	}
	if len(c.deliveries) > maxOutboxEvents {
		t.Fatal("in-flight bookkeeping grew unbounded")
	}
}

func TestExpiredUpdateIsNotVerified(t *testing.T) {
	c := &Client{dir: t.TempDir(), enabled: true, api: &fakeSink{}}
	_ = os.MkdirAll(filepath.Join(c.dir, "analytics"), 0700)
	_ = os.WriteFile(filepath.Join(c.dir, pendingUpdateFile), []byte(`{"operation_id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","from":"0.1.2","version":"0.1.3","created_at":"2000-01-01T00:00:00Z"}`), 0600)
	c.VerifyUpdateRestart("0.1.3")
	if len(c.api.(*fakeSink).events) != 0 {
		t.Fatal("expired restart verified")
	}
}

func TestOutboxReplayRevalidatesPropertiesAndFrames(t *testing.T) {
	dir := t.TempDir()
	sink := &exceptionSink{}
	c := &Client{dir: dir, installID: "install", enabled: true, api: sink}
	c.TrackError("daemon_panic", errors.New("test panic"), nil, true)
	entries, err := os.ReadDir(filepath.Join(dir, outboxDir))
	if err != nil {
		t.Fatal(err)
	}
	// Alter a persisted exception as if the user-owned file had been replaced.
	for _, entry := range entries {
		p := filepath.Join(dir, outboxDir, entry.Name())
		raw, _ := os.ReadFile(p)
		var q queuedEvent
		if json.Unmarshal(raw, &q) != nil || q.Exception == nil {
			continue
		}
		q.Exception.Properties["$set"] = map[string]any{"email": "private@example.com"}
		q.Exception.ExceptionList[0].Stacktrace = &posthog.ExceptionStacktrace{Type: "native", Frames: []posthog.StackFrame{{Filename: "/Users/private/file.go", Function: "private", LineNo: 1}, {Filename: "libs/masir-daemon/internal/server/analytics.go", Function: "libs/masir-daemon/internal/server.(*Server).TrackPanic", LineNo: 100}}}
		raw, _ = json.Marshal(q)
		_ = os.WriteFile(p, raw, 0600)
	}
	next := &Client{dir: dir, installID: "install", enabled: true, api: sink}
	next.DrainOutbox(t.Context())
	if len(sink.exceptions) != 2 {
		t.Fatal("exception not replayed")
	}
	e := sink.exceptions[1]
	if _, ok := e.Properties["$set"]; ok {
		t.Fatal("replay trusted reserved property")
	}
	if len(e.ExceptionList[0].Stacktrace.Frames) != 1 || e.ExceptionList[0].Stacktrace.Frames[0].Filename != "libs/masir-daemon/internal/server/analytics.go" {
		t.Fatal("replay lost safe frames or trusted private frames")
	}
}

func TestClientPanicKeepsTimestampAndUUIDWithoutInventingStack(t *testing.T) {
	sink := &exceptionSink{}
	c := &Client{dir: t.TempDir(), installID: "install", enabled: true, api: sink}
	id := coreanalytics.EventID()
	ts := time.Now().Add(-time.Minute).UTC()
	props := map[string]any{"event_id": id, "message": "unexpected panic", "stack_fingerprint": "012345abcdef"}
	c.TrackClientException("gui_panic", props, ts)
	c.TrackClientException("gui_panic", props, ts)
	if len(sink.exceptions) != 2 {
		t.Fatal("client exceptions missing")
	}
	first, second := sink.exceptions[0], sink.exceptions[1]
	if first.Uuid == id || first.Uuid != second.Uuid || !first.Timestamp.Equal(ts) {
		t.Fatal("client exception identity or time changed")
	}
	if len(first.ExceptionList[0].Stacktrace.Frames) != 0 {
		t.Fatal("daemon stack misrepresented as client stack")
	}
}
