package xray

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/xtls/xray-core/proxy/tun"
)

func TestPreparedProbeBindingAvailable(t *testing.T) {
	if os.Getenv("MASIR_XRAY_SOURCE") == "" {
		t.Skip("stock dependency build")
	}
	if _, ok := any(new(tun.InterfaceUpdater)).(probeSocketBinder); !ok {
		t.Fatal("prepared dependency is missing the socket binding API")
	}
}

func TestProbeRetriesWhenTunDisappears(t *testing.T) {
	if _, ok := any(new(tun.InterfaceUpdater)).(probeSocketBinder); !ok {
		t.Skip("stock dependency build")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	snapshots := 0
	_, err := MeasureDelay(context.Background(), []byte(`{"outbounds":[{"protocol":"freedom"}]}`), LatencyOptions{
		URL: target.URL, Attempts: 1, CurrentTunName: func() string {
			snapshots++
			if snapshots == 1 {
				return "masir-test-interface-that-does-not-exist"
			}
			return ""
		},
	})
	if err != nil || snapshots != 2 {
		t.Fatalf("expected successful retry without TUN, snapshots=%d err=%v", snapshots, err)
	}
}
