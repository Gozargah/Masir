package xray

import (
	"context"
	"strings"

	"github.com/xtls/xray-core/features/stats"
)

// Inbounds the daemon adds for itself; their bytes aren't an app's traffic.
var internalInbounds = map[string]bool{
	apiInboundTag:   true,
	"dns-bootstrap": true,
}

// Traffic is the bytes the apps on this machine have sent (Up) and received (Down)
// through Xray's inbounds since Xray started.
type Traffic struct{ Up, Down uint64 }

// Traffic reads the counters. ok is false while Xray isn't running.
func (c *Core) Traffic(ctx context.Context) (t Traffic, ok bool, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stats := c.stats
	if stats == nil {
		return Traffic{}, false, nil
	}
	names, err := readCounters(ctx, stats, "inbound>>>")
	if err != nil {
		return Traffic{}, false, err
	}
	return sumTraffic(names), true, nil
}

// sumTraffic adds up the per-inbound counters, named like "inbound>>>tun-in>>>traffic>>>uplink".
// For an inbound, uplink is what came in from the app and downlink what went back to it.
func sumTraffic(counters map[string]int64) Traffic {
	var t Traffic
	for name, v := range counters {
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "inbound" || parts[2] != "traffic" || internalInbounds[parts[1]] || v < 0 {
			continue
		}
		switch parts[3] {
		case "uplink":
			t.Up += uint64(v)
		case "downlink":
			t.Down += uint64(v)
		}
	}
	return t
}

// OutboundTraffic returns traffic per outbound tag since Xray started,
// the generation of the running instance, and whether Xray is currently running.
func (c *Core) OutboundTraffic(ctx context.Context) (map[string]Traffic, uint64, bool, error) {
	c.mu.RLock()
	stats := c.stats
	gen := c.generation
	defer c.mu.RUnlock()
	if stats == nil {
		return nil, 0, false, nil
	}
	names, err := readCounters(ctx, stats, "outbound>>>")
	if err != nil {
		return nil, 0, false, err
	}
	return parseOutboundTraffic(names), gen, true, nil
}

// parseOutboundTraffic groups the per-outbound counters by tag.
// Names look like "outbound>>>proxy>>>traffic>>>uplink".
func parseOutboundTraffic(counters map[string]int64) map[string]Traffic {
	out := make(map[string]Traffic)
	for name, v := range counters {
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "outbound" || parts[2] != "traffic" || v < 0 {
			continue
		}
		tag := parts[1]
		t := out[tag]
		switch parts[3] {
		case "uplink":
			t.Up += uint64(v)
		case "downlink":
			t.Down += uint64(v)
		}
		out[tag] = t
	}
	return out
}

// readCounters mirrors QueryStats filtering and never resets cumulative counters.
// The caller holds Core.mu for the manager's lifetime.
func readCounters(ctx context.Context, manager stats.Manager, pattern string) (map[string]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := make(map[string]int64)
	manager.VisitCounters(func(name string, counter stats.Counter) bool {
		if ctx.Err() != nil {
			return false
		}
		if strings.Contains(name, pattern) {
			names[name] = counter.Value()
		}
		return true
	})
	return names, ctx.Err()
}
