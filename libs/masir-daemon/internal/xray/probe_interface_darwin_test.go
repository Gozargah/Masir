package xray

import (
	"github.com/xtls/xray-core/proxy/tun"
	"os"
	"testing"
)

func TestPreparedProbeBindingAvailable(t *testing.T) {
	if os.Getenv("MASIR_XRAY_SOURCE") == "" {
		t.Skip("stock dependency build")
	}
	if _, ok := any(new(tun.InterfaceUpdater)).(probeSocketBinder); !ok {
		t.Fatal("prepared dependency is missing the socket binding API")
	}
}
