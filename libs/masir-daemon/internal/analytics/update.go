package analytics

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/userfs"
)

const pendingUpdateFile = "analytics/update-pending.json"

// PendingUpdate is telemetry only: it never decides whether a binary may run.
// Written before requesting restart; reconciled by the next boot, never a timer.
type PendingUpdate struct {
	OperationID string    `json:"operation_id"`
	From        string    `json:"from"`
	Version     string    `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
}

func (c *Client) AwaitUpdateRestart(opID, from, version string) {
	if !c.Enabled() || version == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	p := PendingUpdate{opID, from, version, time.Now().UTC()}
	raw, err := json.Marshal(p)
	if err != nil {
		return
	}
	path := filepath.Join(c.dir, pendingUpdateFile)
	if userfs.MkdirAll(filepath.Dir(path)) == nil {
		_ = userfs.Write(path, raw)
	}
}

// VerifyUpdateRestart confirms only the running daemon's version. It does not
// claim that a desktop relaunch, client binary copy, or connectivity was verified.
func (c *Client) VerifyUpdateRestart(current string) {
	path := filepath.Join(c.dir, pendingUpdateFile)
	raw, err := userfs.ReadFile(path)
	if err != nil {
		return
	}
	defer userfs.Remove(path)
	if !c.Enabled() || len(raw) > 4096 {
		return
	}
	var p PendingUpdate
	if json.Unmarshal(raw, &p) != nil || p.CreatedAt.IsZero() || time.Since(p.CreatedAt) > maxSpoolAge || time.Until(p.CreatedAt) > time.Minute {
		return
	}
	if len(p.Version) > 100 || len(p.From) > 100 || !validEventID(p.OperationID) {
		return
	}
	outcome := "version_mismatch"
	if strings.TrimPrefix(current, "v") == strings.TrimPrefix(p.Version, "v") {
		outcome = "success"
	}
	c.Track("update_verified", map[string]any{
		"operation_id": p.OperationID, "operation": "update", "source": "daemon_boot",
		"from_version": p.From, "target_version": p.Version,
		"outcome": outcome, "success": outcome == "success", "verification": "daemon_version",
		"duration_ms": time.Since(p.CreatedAt).Milliseconds(),
	})
}
