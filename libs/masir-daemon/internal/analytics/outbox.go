package analytics

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coreanalytics "github.com/Gozargah/Masir/libs/masir-core/analytics"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/userfs"
	"github.com/posthog/posthog-go"
)

const outboxDir = "analytics/outbox"
const maxOutboxBytes = 256 << 10
const maxOutboxEvents = 100

type queuedEvent struct {
	Capture   *posthog.Capture   `json:"capture,omitempty"`
	Exception *posthog.Exception `json:"exception,omitempty"`
}

func messageID(m posthog.Message) string {
	switch e := m.(type) {
	case posthog.Capture:
		return e.Uuid
	case posthog.Exception:
		return e.Uuid
	}
	return ""
}
func apiMessageID(m posthog.APIMessage) string {
	switch e := m.(type) {
	case posthog.CaptureInApi:
		return e.Uuid
	case posthog.ExceptionInApi:
		return e.Uuid
	}
	return ""
}
func (c *Client) Success(m posthog.APIMessage) {
	c.deliveryMu.Lock()
	if c.deliveries == nil {
		c.deliveries = map[string]bool{}
	}
	if id := apiMessageID(m); id != "" {
		if _, exists := c.deliveries[id]; exists {
			c.deliveries[id] = true
		}
	}
	c.deliveryMu.Unlock()
}
func (c *Client) Failure(m posthog.APIMessage, _ error) {
	c.deliveryMu.Lock()
	delete(c.deliveries, apiMessageID(m))
	c.deliveryFailures++
	c.deliveryMu.Unlock()
}

// enqueue saves before handing a message to the SDK. The outbox is bounded and
// best effort; analytics failures never fail the person's operation.
func (c *Client) enqueue(m posthog.Message) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled || c.api == nil {
		return false
	}
	persisted := false
	id := messageID(m)
	if c.dir != "" && id != "" {
		q := queuedEvent{}
		switch e := m.(type) {
		case posthog.Capture:
			q.Capture = &e
		case posthog.Exception:
			q.Exception = &e
		}
		raw, err := json.Marshal(q)
		if err == nil && len(raw) <= maxSpoolEventBytes {
			dir := filepath.Join(c.dir, outboxDir)
			if userfs.MkdirAll(dir) == nil {
				c.pruneOutbox(dir, len(raw))
				persisted = userfs.Write(filepath.Join(dir, id+".json"), raw) == nil
			}
		}
	}
	c.deliveryMu.Lock()
	if c.dir != "" && !persisted {
		c.queueDrops++
	}
	if c.deliveries == nil {
		c.deliveries = map[string]bool{}
	}
	if len(c.deliveries) >= maxOutboxEvents {
		for k, ack := range c.deliveries {
			if ack {
				delete(c.deliveries, k)
			}
		}
	}
	if len(c.deliveries) < maxOutboxEvents {
		c.deliveries[id] = false
	}
	c.deliveryMu.Unlock()
	if err := c.api.Enqueue(m); err != nil {
		c.Failure(m.APIfy(), err)
	}
	return persisted
}

// DrainOutbox replays at the existing analytics cadence. SDK retries keep the
// same UUID, so an ambiguous acknowledgement cannot create a new logical event.
func (c *Client) DrainOutbox(ctx context.Context) {
	if !c.Enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled || c.api == nil || c.dir == "" {
		return
	}
	dir := filepath.Join(c.dir, outboxDir)
	entries, _ := userfs.ReadDir(dir)
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		path := filepath.Join(dir, e.Name())
		c.deliveryMu.Lock()
		ack, active := c.deliveries[id]
		c.deliveryMu.Unlock()
		if ack {
			_ = userfs.Remove(path)
			c.deliveryMu.Lock()
			delete(c.deliveries, id)
			c.deliveryMu.Unlock()
			continue
		}
		if active {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxSpoolEventBytes || time.Since(info.ModTime()) > maxSpoolAge {
			_ = userfs.Remove(path)
			continue
		}
		raw, err := userfs.ReadFile(path)
		if err != nil {
			continue
		}
		var q queuedEvent
		if json.Unmarshal(raw, &q) != nil {
			_ = userfs.Remove(path)
			continue
		}
		var m posthog.Message
		if q.Capture != nil {
			e := *q.Capture
			if !knownEvent(e.Event) || e.Timestamp.IsZero() || time.Since(e.Timestamp) > maxSpoolAge || time.Until(e.Timestamp) > time.Minute {
				_ = userfs.Remove(path)
				continue
			}
			e.Properties = posthog.Properties(coreanalytics.Properties(map[string]any(e.Properties)))
			e.Properties["$process_person_profile"] = false
			e = posthog.Capture{Uuid: e.Uuid, DistinctId: e.DistinctId, Timestamp: e.Timestamp, Event: e.Event, Properties: e.Properties}
			m = e
		} else if q.Exception != nil {
			e := *q.Exception
			if len(e.ExceptionList) != 1 || !knownEvent(e.ExceptionList[0].Type) || e.Timestamp.IsZero() || time.Since(e.Timestamp) > maxSpoolAge || time.Until(e.Timestamp) > time.Minute {
				_ = userfs.Remove(path)
				continue
			}
			e.Properties = posthog.Properties(coreanalytics.Properties(map[string]any(e.Properties)))
			e.Properties["$process_person_profile"] = false
			e.DebugImages = nil
			e.ExceptionList[0].Value = coreanalytics.SanitizeMessage(e.ExceptionList[0].Value)
			e.ExceptionList[0].Stacktrace = cleanStack(e.ExceptionList[0].Stacktrace)
			fingerprint := exceptionFingerprint(e.ExceptionList[0].Type, e.Properties, e.ExceptionList[0].Stacktrace)
			e = posthog.Exception{Uuid: e.Uuid, DistinctId: e.DistinctId, Timestamp: e.Timestamp, Properties: e.Properties, ExceptionList: e.ExceptionList, ExceptionFingerprint: &fingerprint}
			m = e
		}
		if m == nil || messageID(m) != id || !validEventID(id) {
			_ = userfs.Remove(path)
			continue
		}
		// Only replay this install's events. Opt-out must never resurrect an old ID.
		sameID := q.Capture != nil && q.Capture.DistinctId == c.installID || q.Exception != nil && q.Exception.DistinctId == c.installID
		if !sameID {
			_ = userfs.Remove(path)
			continue
		}
		c.deliveryMu.Lock()
		if c.deliveries == nil {
			c.deliveries = map[string]bool{}
		}
		c.deliveries[id] = false
		c.deliveryMu.Unlock()
		if err := c.api.Enqueue(m); err != nil {
			c.Failure(m.APIfy(), err)
		}
	}
}
func (c *Client) pruneOutbox(dir string, adding int) {
	entries, _ := userfs.ReadDir(dir)
	type item struct {
		name string
		size int64
		at   time.Time
	}
	var files []item
	var total int64
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		c.deliveryMu.Lock()
		ack := c.deliveries[id]
		c.deliveryMu.Unlock()
		if ack {
			_ = userfs.Remove(filepath.Join(dir, e.Name()))
			c.deliveryMu.Lock()
			delete(c.deliveries, id)
			c.deliveryMu.Unlock()
			continue
		}
		if e.IsDir() {
			continue
		}
		i, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, item{e.Name(), i.Size(), i.ModTime()})
		total += i.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, f := range files {
		if len(files) < maxOutboxEvents && total+int64(adding) <= maxOutboxBytes && time.Since(f.at) <= maxSpoolAge {
			break
		}
		_ = userfs.Remove(filepath.Join(dir, f.name))
		c.deliveryMu.Lock()
		c.queueDrops++
		c.deliveryMu.Unlock()
		total -= f.size
		files = files[1:]
		c.deliveryMu.Lock()
		delete(c.deliveries, strings.TrimSuffix(f.name, ".json"))
		c.deliveryMu.Unlock()
	}
}

func validEventID(id string) bool {
	return coreanalytics.ValidEventID(id)
}

func knownEvent(event string) bool {
	switch event {
	case "release_check_started", "release_check_finished", "analytics_disabled", "analytics_enabled", "cli_panic", "connectivity_summary", "daemon_error", "daemon_panic", "daemon_start_failed", "daemon_started", "daemon_stopped", "gui_panic", "rpc_failed", "operation_finished", "operation_started", "power_changed", "power_changed_failed", "proxy_selected", "routing_mode_changed_failed", "rule_added", "rule_altered", "rule_moved", "rule_removed", "self_update_finished", "subscription_added", "subscription_altered", "subscription_removed", "tun_changed", "tun_failed", "update_applied", "update_attempt_finished", "update_checked", "update_finished", "update_ignored", "update_paused", "update_resumed", "update_started", "update_stopped", "update_verified", "xray_apply_failed", "xray_started", "xray_stopped":
		return true
	}
	return false
}

// DeliveryStats exposes bounded local diagnostics, not another telemetry event.
func (c *Client) DeliveryStats() (failures, drops uint64) {
	c.deliveryMu.Lock()
	defer c.deliveryMu.Unlock()
	return c.deliveryFailures, c.queueDrops
}
