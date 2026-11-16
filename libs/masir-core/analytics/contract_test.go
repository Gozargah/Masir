package analytics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPropertyContractRejectsPrivateAndReservedPayloads(t *testing.T) {
	id := EventID()
	got := Properties(map[string]any{"$set": map[string]any{"email": "private@example.com"}, "raw_url": "https://secret.example.com", "error_message": "read /Users/private/settings.json: failed", "operation_id": id, "source": "gui", "state": map[string]any{"secret": "value"}})
	if len(got) != 3 || got["operation_id"] != id || got["source"] != "gui" || got["error_message"] != "read <path>: failed" {
		t.Fatalf("properties=%v", got)
	}
}
func TestOfflineConsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MASIR_ANALYTICS", "")
	if Allowed(dir, "0.1.3") {
		t.Fatal("missing notice allowed telemetry")
	}
	_ = MarkNoticeShown(dir)
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"analytics":false}`), 0600)
	t.Setenv("MASIR_ANALYTICS", "on")
	if Allowed(dir, "0.1.3") {
		t.Fatal("environment bypassed opt-out")
	}
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"analytics":true}`), 0600)
	if !Allowed(dir, "0.1.3") {
		t.Fatal("consent not honored")
	}
	t.Setenv("DO_NOT_TRACK", "1")
	if Allowed(dir, "0.1.3") {
		t.Fatal("DNT ignored")
	}
}

func TestContractKeepsExistingBoundedRuleMetadataAfterJSONReplay(t *testing.T) {
	p := Properties(map[string]any{"matchers": []any{"domain", "process"}, "prepend": true, "renamed": false, "url_changed": true})
	matchers, ok := p["matchers"].([]string)
	if !ok || len(matchers) != 2 || p["prepend"] != true || p["url_changed"] != true {
		t.Fatalf("lost existing metadata: %v", p)
	}
}
