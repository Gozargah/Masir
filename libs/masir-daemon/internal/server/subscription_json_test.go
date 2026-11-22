package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func TestLargeSourceJSONIsFetchedOnlyOnDemand(t *testing.T) {
	s := newTestServer(t)
	source, err := json.Marshal(map[string]any{
		"remarks": "Large config", "notes": strings.Repeat("x", 5<<20),
		"outbounds": []any{map[string]any{"protocol": "trojan", "streamSettings": map[string]any{"security": "tls"}, "settings": map[string]any{"servers": []any{map[string]any{"address": "example.com", "port": 443, "password": "test"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(source) }))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub, err := s.subs.Add(ctx, provider.URL, "Large config", 0)
	if err != nil {
		t.Fatal(err)
	}
	client := dialInProcess(t, s)
	listed, err := client.ListSubscriptions(ctx, &pb.ListSubscriptionsRequest{IncludeConfigs: true})
	if err != nil {
		t.Fatalf("ordinary list failed: %v", err)
	}
	if proto.Size(listed) > 4096 {
		t.Fatalf("source leaked into list: %d bytes", proto.Size(listed))
	}
	got, err := client.GetSubscriptionConfigJSON(ctx, &pb.GetSubscriptionConfigJSONRequest{SubscriptionId: sub.ID, ConfigId: sub.Configs[0].ID}, grpc.MaxCallRecvMsgSize(16<<20))
	if err != nil {
		t.Fatal(err)
	}
	var parsed any
	if err := json.Unmarshal([]byte(got.Json), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(got.Json) < 5<<20 || !strings.Contains(got.Json, strings.Repeat("x", 5<<20)) {
		t.Fatal("large source truncated")
	}
}
