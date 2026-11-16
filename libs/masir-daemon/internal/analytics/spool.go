package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	coreanalytics "github.com/Gozargah/Masir/libs/masir-core/analytics"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/userfs"
)

const (
	maxSpoolEventBytes = 16 << 10
	maxSpoolAge        = 7 * 24 * time.Hour
)

// clientEvents are the events a client may report, over RPC or through the spool. The
// spool is a directory the user can write to, so it gets no more trust than the RPC.
var clientEvents = map[string]bool{
	"self_update_finished": true,
	"cli_panic":            true,
	"gui_panic":            true,
	"daemon_start_failed":  true,
}

// ClientEventAllowed reports whether a client may report an event of this name.
func ClientEventAllowed(name string) bool { return clientEvents[name] }

// CleanClientProps removes what a client may not set: PostHog's reserved "$" properties.
func CleanClientProps(props map[string]any) {
	clean := coreanalytics.Properties(props)
	for k := range props {
		delete(props, k)
	}
	for k, v := range clean {
		props[k] = v
	}
}

// DrainSpool sends events the CLI left while the daemon was unreachable. If analytics
// is off, the spool is deleted instead. Every event is checked as strictly as one
// arriving over RPC, and anything odd (too big, too old, not a regular file) is dropped.
func (c *Client) DrainSpool(ctx context.Context) {
	if !c.Enabled() {
		c.clearSpool()
		return
	}
	dir := coreanalytics.SpoolDir(c.dir)
	entries, err := userfs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ctx.Err() != nil {
			return
		}
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		if info, err := ent.Info(); err == nil && info.Mode().IsRegular() && info.Size() <= maxSpoolEventBytes {
			if !c.sendSpooled(path) {
				continue
			}
		}
		_ = userfs.Remove(path)
	}
}

func (c *Client) sendSpooled(path string) bool {
	raw, err := userfs.ReadFile(path)
	if err != nil || len(raw) > maxSpoolEventBytes {
		return true
	}
	var e coreanalytics.Event
	if json.Unmarshal(raw, &e) != nil || !ClientEventAllowed(e.Event) || e.CreatedAt.IsZero() || time.Since(e.CreatedAt) > maxSpoolAge || time.Until(e.CreatedAt) > time.Minute {
		return true
	}
	props := e.Properties
	if props == nil {
		props = map[string]any{}
	}
	CleanClientProps(props)
	if validEventID(e.ID) {
		props["event_id"] = e.ID
	}
	source := "cli_spool"
	if e.Event == "gui_panic" || props["origin_component"] == "gui" || props["component"] == "gui" {
		source = "gui_spool"
	}
	if props["origin_component"] == "daemon" {
		source = "daemon_spool"
	}
	if props["component"] == "gui" {
		props["origin_component"] = "gui"
		props["origin_app_version"] = props["app_version"]
	}
	props["source"] = source
	durable := c.trackAt(e.Event, props, e.CreatedAt)
	if e.Event == "cli_panic" || e.Event == "gui_panic" {
		message, _ := props["message"].(string)
		c.trackExceptionAt(e.Event, errors.New(message), props, e.CreatedAt)
	}
	return durable
}

func (c *Client) clearSpool() {
	_ = userfs.RemoveAll(coreanalytics.SpoolDir(c.dir))
}
