package analytics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StartupFailure only writes a consent-gated local event. It never starts a
// daemon or sends a network request; a healthy daemon may deliver it later.
func StartupFailure(dir, version, component, stage string, err error) {
	if err == nil || !Allowed(dir, version) {
		return
	}
	_ = WriteSpool(dir, Event{Event: "daemon_start_failed", Properties: map[string]any{
		"origin_component": component, "origin_app_version": version, "source": component,
		"stage": stage, "outcome": "failure", "success": false, "error_message": SanitizeMessage(err.Error()),
		"error_fingerprint": Fingerprint(err.Error()),
	}, CreatedAt: time.Now().UTC()})
}

// Allowed is shared by offline clients; explicit opt-out and notice always win.
func Allowed(dir, version string) bool {
	if !NoticeShown(dir) {
		return false
	}
	if v := os.Getenv("DO_NOT_TRACK"); v != "" && v != "0" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MASIR_ANALYTICS"))) {
	case "off", "0", "false", "no":
		return false
	case "on", "1", "true", "yes":
	default:
		if version == "dev" {
			return false
		}
	}
	var settings struct {
		Analytics *bool `json:"analytics"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return false
	}
	if json.Unmarshal(raw, &settings) != nil {
		return false
	}
	return settings.Analytics == nil || *settings.Analytics
}
