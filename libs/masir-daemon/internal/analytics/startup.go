package analytics

import (
	"encoding/json"
	"path/filepath"
	"time"

	coreanalytics "github.com/Gozargah/Masir/libs/masir-core/analytics"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/userfs"
)

// SpoolStartup uses the pinned filesystem when privileged. No network client is
// opened, and an early failure cannot opt the person into analytics.
func SpoolStartup(dir, version, stage string, err error) {
	if err == nil || !coreanalytics.Allowed(dir, version) {
		return
	}
	e := coreanalytics.Event{ID: coreanalytics.EventID(), Event: "daemon_start_failed", CreatedAt: time.Now().UTC(), Properties: map[string]any{
		"origin_component": "daemon", "origin_app_version": version, "source": "daemon_spool", "stage": stage,
		"outcome": "failure", "success": false, "error_kind": ErrorKind(err), "error_message": coreanalytics.SanitizeMessage(err.Error()), "error_fingerprint": coreanalytics.Fingerprint(err.Error()),
	}}
	e.Properties["event_id"] = e.ID
	raw, eerr := json.Marshal(e)
	if eerr != nil || len(raw) > maxSpoolEventBytes {
		return
	}
	path := coreanalytics.SpoolDir(dir)
	if userfs.MkdirAll(path) != nil {
		return
	}
	entries, _ := userfs.ReadDir(path)
	var size int64
	for _, entry := range entries {
		if i, e := entry.Info(); e == nil {
			size += i.Size()
		}
	}
	if len(entries) >= 100 || size+int64(len(raw)) > 256<<10 {
		return
	}
	_ = userfs.Write(filepath.Join(path, coreanalytics.EventID()+".json"), raw)
}
