package main

import (
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// jsonSubs mirrors what loadNodes returns: the subscription message carries
// its configs inline, including the credential blob that must never be printed.
func jsonSubs() []subNodes {
	return []subNodes{
		{sub: &pb.Subscription{
			Id:          "sub1",
			Name:        "alpha",
			Url:         "https://example.com/sub",
			ConfigCount: 2,
			Configs: []*pb.SubscriptionConfig{
				{
					Id:           "cfg1",
					Name:         "Germany",
					Protocol:     "vless",
					OutboundJson: `{"password":"LEAKME"}`,
					Latency:      &pb.LatencyResult{LatencyMs: 136, TestedAtMs: 1794800000000},
				},
				{Id: "cfg2", Name: "Norway", Protocol: "vless"}, // never tested
			},
		}},
		{sub: &pb.Subscription{Id: "sub2", Name: "beta"}},
	}
}

func TestMarshalSubList(t *testing.T) {
	b, err := marshalSubList(jsonSubs(), "sub1/cfg2")
	if err != nil {
		t.Fatalf("marshalSubList: %v", err)
	}
	out := string(b)

	if strings.Contains(out, "\x1b") {
		t.Error("JSON output contains ANSI escapes")
	}
	if strings.Contains(out, "LEAKME") {
		t.Error("JSON output leaks the outbound credential blob")
	}

	var doc struct {
		Subscriptions []struct {
			Id          string `json:"id"`
			Name        string `json:"name"`
			ConfigCount int    `json:"configCount"`
			Configs     []struct {
				Id       string `json:"id"`
				Name     string `json:"name"`
				Protocol string `json:"protocol"`
				Latency  *struct {
					LatencyMs string `json:"latencyMs"`
				} `json:"latency"`
				OutboundJson string `json:"outboundJson"`
			} `json:"configs"`
		} `json:"subscriptions"`
		Active struct {
			SubscriptionId string `json:"subscriptionId"`
			ConfigId       string `json:"configId"`
		} `json:"active"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if len(doc.Subscriptions) != 2 {
		t.Fatalf("got %d subscriptions, want 2", len(doc.Subscriptions))
	}
	sub := doc.Subscriptions[0]
	if sub.Id != "sub1" || sub.Name != "alpha" || sub.ConfigCount != 2 {
		t.Errorf("subscription fields = %+v", sub)
	}
	if len(sub.Configs) != 2 {
		t.Fatalf("got %d configs, want 2", len(sub.Configs))
	}
	cfg := sub.Configs[0]
	if cfg.Id != "cfg1" || cfg.Protocol != "vless" {
		t.Errorf("config fields = %+v", cfg)
	}
	if cfg.Latency == nil || cfg.Latency.LatencyMs != "136" {
		t.Errorf("latency = %+v, want latencyMs \"136\" (protojson renders int64 as a string)", cfg.Latency)
	}
	if cfg.OutboundJson != "" {
		t.Error("outboundJson survived into the output")
	}
	// A config never tested simply has no latency key.
	if doc.Subscriptions[0].Configs[1].Latency != nil {
		t.Errorf("untested config has latency %+v, want absent", doc.Subscriptions[0].Configs[1].Latency)
	}
	if doc.Active.SubscriptionId != "sub1" || doc.Active.ConfigId != "cfg2" {
		t.Errorf("active = %+v, want sub1/cfg2", doc.Active)
	}
}

func TestMarshalSubListEmpty(t *testing.T) {
	b, err := marshalSubList(nil, "")
	if err != nil {
		t.Fatalf("marshalSubList: %v", err)
	}
	// An empty list must marshal as [] (a usable array), never null.
	if !strings.Contains(string(b), `"subscriptions": []`) {
		t.Errorf("empty output = %s", b)
	}
	var doc struct {
		Subscriptions []any `json:"subscriptions"`
		Active        struct {
			SubscriptionId string `json:"subscriptionId"`
		} `json:"active"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(doc.Subscriptions) != 0 || doc.Active.SubscriptionId != "" {
		t.Errorf("empty doc = %+v", doc)
	}
}

func TestMarshalUse(t *testing.T) {
	b, err := marshalUse("sub1/cfg1", "Germany", "id")
	if err != nil {
		t.Fatalf("marshalUse: %v", err)
	}
	var doc struct {
		Active struct {
			SubscriptionId string `json:"subscriptionId"`
			ConfigId       string `json:"configId"`
		} `json:"active"`
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc.Active.SubscriptionId != "sub1" || doc.Active.ConfigId != "cfg1" {
		t.Errorf("active = %+v, want sub1/cfg1", doc.Active)
	}
	if doc.Name != "Germany" || doc.Reason != "id" {
		t.Errorf("name/reason = %q/%q", doc.Name, doc.Reason)
	}
}
