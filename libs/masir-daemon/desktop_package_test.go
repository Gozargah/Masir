package daemon

import (
	"context"
	"strings"
	"testing"
)

func TestFetchDesktopPackageRefusesAForeignProxy(t *testing.T) {
	for _, proxy := range []string{"https://evil.example:1", "http://10.0.0.1:7890"} {
		_, _, _, err := FetchDesktopPackage(context.Background(), "https://example.com", "0.1.7", "deb", "amd64", t.TempDir(), proxy, nil)
		if err == nil || !strings.Contains(err.Error(), "refusing proxy") {
			t.Fatalf("%s: err = %v, want a refusal", proxy, err)
		}
	}
}
