package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Gozargah/Masir/apps/masir/internal/ui"
	"github.com/Gozargah/Masir/libs/masir-core/analytics"
	"github.com/Gozargah/Masir/libs/masir-core/paths"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// maybeAnalyticsNotice shows the anonymous analytics notice once per install on the
// first interactive command. It is skipped when analytics is off. The daemon sends
// nothing until this has been shown, so this is what switches analytics on.
func maybeAnalyticsNotice() {
	if !ui.Interactive() || !analyticsEnabledCLI() {
		return
	}
	dir, err := paths.ConfigDir()
	if err != nil || analytics.NoticeShown(dir) {
		return
	}
	if analytics.MarkNoticeShown(dir) != nil {
		return // better silent than tracked without having told the user
	}
	ui.Note("masir sends anonymous usage and error data to improve it.\n"+
		"Turn it off with `masir settings set analytics off`.",
		ui.Yellow("▲ Anonymous analytics"))
}

// captureEvent sends an event through a running daemon, or spools it for later.
func captureEvent(name string, props map[string]any) {
	if !analyticsAllowedCLI() {
		return
	}
	if props == nil {
		props = map[string]any{}
	}
	props["event_id"] = analytics.EventID()
	createdAt := time.Now().UTC().Truncate(time.Millisecond)
	raw, err := json.Marshal(props)
	if err != nil {
		return
	}
	if c, ok := runningDaemon(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, callErr := c.CaptureEvent(ctx, &pb.CaptureEventRequest{
			Event:          name,
			PropertiesJson: string(raw),
			TimestampMs:    createdAt.UnixMilli(),
		})
		cancel()
		if callErr == nil {
			return
		}
	}
	dir, err := paths.ConfigDir()
	if err != nil {
		return
	}
	_ = analytics.WriteSpool(dir, analytics.Event{
		ID:         props["event_id"].(string),
		Event:      name,
		Properties: props,
		CreatedAt:  createdAt,
	})
}

func errorProps(err error) map[string]any {
	props := map[string]any{"error_kind": errorKind(err)}
	if code := status.Code(err); code != codes.OK && code != codes.Unknown {
		props["grpc_code"] = code.String()
	}
	if reason := errorReason(err); reason != "" {
		props["error_reason"] = reason
	}
	props["error_fingerprint"] = analytics.Fingerprint(message(err))
	return props
}

func errorKind(err error) string {
	if err == errReported {
		return "reported"
	}
	return analytics.ErrorKind(err)
}

// analyticsAllowedCLI is a local gate for spooling: analytics is on and the user has been
// told. The daemon checks both again.
func analyticsAllowedCLI() bool {
	if !analyticsEnabledCLI() {
		return false
	}
	dir, err := paths.ConfigDir()
	return err == nil && analytics.NoticeShown(dir)
}

// analyticsEnabledCLI reports whether the setting and environment allow analytics.
func analyticsEnabledCLI() bool {
	if v := os.Getenv("DO_NOT_TRACK"); v != "" && v != "0" {
		return false
	}
	// Read the user's setting first: an explicit opt-out always wins.
	setting := true
	if dir, err := paths.ConfigDir(); err == nil {
		if raw, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
			var s struct {
				Analytics *bool `json:"analytics"`
			}
			if json.Unmarshal(raw, &s) == nil && s.Analytics != nil {
				setting = *s.Analytics
			}
		}
	}
	if !setting {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MASIR_ANALYTICS"))) {
	case "off", "0", "false", "no":
		return false
	case "on", "1", "true", "yes":
		return true
	}
	return version != "dev"
}
