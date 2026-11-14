package systemdns

import (
	"context"
	"errors"
	"testing"
)

func TestDNSStartupRequiresOnlyLocalRoutes(t *testing.T) {
	err := checkRoutesWith(context.Background(), "utun10", []string{"1.1.1.1", "2606:4700:4700::1111"}, func(_ context.Context, path string, args ...string) ([]byte, error) {
		if path != "/sbin/route" {
			return nil, errors.New("connection timed out; no servers could be reached")
		}
		return []byte("interface: utun10\n"), nil
	})
	if err != nil {
		t.Fatalf("DNS startup depends on an external lookup: %v", err)
	}
}
