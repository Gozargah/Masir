package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	coreanalytics "github.com/Gozargah/Masir/libs/masir-core/analytics"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/analytics"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/update"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func analyticsServer(t *testing.T) *Server {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"Ok"}`))
	}))
	t.Cleanup(endpoint.Close)
	t.Setenv("MASIR_POSTHOG_HOST", endpoint.URL)
	t.Setenv("MASIR_POSTHOG_KEY", "phc_test")
	t.Setenv("MASIR_ANALYTICS", "on")
	t.Setenv("DO_NOT_TRACK", "")
	s := newTestServer(t)
	if err := coreanalytics.MarkNoticeShown(s.dir); err != nil {
		t.Fatal(err)
	}
	s.analytics = analytics.New(analytics.Options{Dir: s.dir, Version: "0.1.3", Enabled: true})
	t.Cleanup(s.CloseAnalytics)
	return s
}
func recordedEvents(t *testing.T, s *Server) []struct {
	Event      string
	Properties map[string]any
} {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.dir, "analytics/outbox"))
	if err != nil {
		t.Fatal(err)
	}
	var events []struct {
		Event      string
		Properties map[string]any
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(s.dir, "analytics/outbox", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			Capture *struct {
				Event      string
				Properties map[string]any
			}
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.Capture != nil {
			events = append(events, *record.Capture)
		}
	}
	return events
}
func TestOperationCorrelatesOutcomeAndSubsystemFailure(t *testing.T) {
	s := analyticsServer(t)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("masir-client", "gui"))
	_, err := s.UnaryInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: rpcMethodPrefix + "SetPower"}, func(ctx context.Context, _ any) (any, error) {
		s.trackError("xray_apply_failed", errors.New("address already in use"), operationProps(ctx, nil), false)
		return nil, status.Error(codes.Internal, "address already in use")
	})
	if err == nil {
		t.Fatal("operation unexpectedly succeeded")
	}
	counts := map[string]int{}
	id := ""
	for _, e := range recordedEvents(t, s) {
		counts[e.Event]++
		got, _ := e.Properties["operation_id"].(string)
		if got == "" || id != "" && got != id {
			t.Fatalf("correlation lost: %v", e)
		}
		id = got
		if e.Properties["source"] != "gui" {
			t.Fatal("lost client source")
		}
		if e.Event == "operation_finished" && e.Properties["outcome"] != "failure" {
			t.Fatal("incorrect outcome")
		}
	}
	for _, event := range []string{"operation_started", "operation_finished", "rpc_failed", "xray_apply_failed"} {
		if counts[event] != 1 {
			t.Fatalf("%s count=%d", event, counts[event])
		}
	}
}
func TestSuccessfulPollingDoesNotGenerateOperations(t *testing.T) {
	s := analyticsServer(t)
	for _, method := range []string{"GetStatus", "GetSettings", "ListSubscriptions", "TestLatency"} {
		_, _ = s.UnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: rpcMethodPrefix + method}, func(context.Context, any) (any, error) { return nil, nil })
	}
	if _, err := os.Stat(filepath.Join(s.dir, "analytics/outbox")); !os.IsNotExist(err) {
		t.Fatal("successful reads generated telemetry")
	}
}
func TestRestartingUpdateIsNotFinishedSuccessfully(t *testing.T) {
	s := analyticsServer(t)
	s.initUpdates(s.dir)
	task := s.wrapUpdate("masir", false, "test", func(ctx context.Context, r *update.Reporter) error {
		r.State(pb.UpdateState_UPDATE_STATE_RESTARTING)
		return nil
	})
	if _, err := s.upd.masir.Start(task); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for s.upd.masir.Active() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	found := false
	for _, e := range recordedEvents(t, s) {
		if e.Event == "update_finished" {
			t.Fatal("restart incorrectly recorded as finished")
		}
		if e.Event == "update_applied" {
			found = true
			if _, ok := e.Properties["success"]; ok {
				t.Fatal("pending restart claimed success")
			}
		}
	}
	if !found {
		t.Fatal("missing pending-restart event")
	}
}

func TestProbeSummaryIsBoundedAndContainsNoDestinations(t *testing.T) {
	s := analyticsServer(t)
	for range 100 {
		s.recordConnectivity(2*time.Millisecond, nil)
	}
	for range 20 {
		s.recordConnectivity(time.Second, errors.New("lookup private.example.com: no such host"))
	}
	s.flushConnectivity()
	events := recordedEvents(t, s)
	if len(events) != 2 {
		t.Fatalf("expected two summaries, got %d", len(events))
	}
	counts := map[string]float64{}
	for _, e := range events {
		if e.Event != "connectivity_summary" {
			t.Fatalf("unexpected event: %s", e.Event)
		}
		counts[e.Properties["outcome"].(string)] = e.Properties["count"].(float64)
		if _, ok := e.Properties["error_message"]; ok {
			t.Fatal("summary included private error text")
		}
	}
	if counts["success"] != 100 || counts["dns"] != 20 {
		t.Fatalf("summary counts=%v", counts)
	}
}
