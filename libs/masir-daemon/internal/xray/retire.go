package xray

import (
	"context"
	"slices"
	"strings"
	"time"
)

const (
	retiredOutboundGrace = 2 * time.Minute
	retiredOutboundCheck = 2 * time.Second
	maxRetiredOutbounds  = 256
)

type retiredOutbound struct {
	item    item
	expires time.Time
}

// Keep superseded subscription handlers addressable during the grace period.
// An existing connection can still dispatch through its provider's dial chain.
func (c *Core) retireOutbound(o item) {
	if c.retired == nil {
		c.retired = make(map[string]retiredOutbound)
	}
	c.retired[o.Tag] = retiredOutbound{item: o, expires: time.Now().Add(retiredOutboundGrace)}
	c.reapRetired()
}

func (c *Core) reapRetired() {
	if c.retireTimer != nil {
		c.retireTimer.Stop()
		c.retireTimer = nil
	}
	tags := make([]string, 0, len(c.retired))
	for tag := range c.retired {
		tags = append(tags, tag)
	}
	slices.SortFunc(tags, func(a, b string) int {
		return c.retired[a].expires.Compare(c.retired[b].expires)
	})
	var nextCheck time.Duration
	for _, tag := range tags {
		delay := time.Until(c.retired[tag].expires)
		group := strings.SplitN(tag, "/", 5)
		canRelease := false
		if len(group) == 5 && delay < retiredOutboundGrace-retiredOutboundCheck {
			counter := c.stats.GetCounter("masir-active-flows>>>" + strings.Join(group[:4], "/"))
			canRelease = counter != nil && counter.Value() == 0
		}
		if delay > 0 && len(c.retired) <= maxRetiredOutbounds && !canRelease {
			wait := min(delay, retiredOutboundCheck)
			if nextCheck == 0 || wait < nextCheck {
				nextCheck = wait
			}
			continue
		}
		handler := c.outbounds.GetHandler(tag)
		if err := c.outbounds.RemoveHandler(context.Background(), tag); err == nil && handler != nil {
			_ = handler.Close()
		}
		delete(c.retired, tag)
	}
	if nextCheck > 0 {
		generation := c.generation
		c.retireTimer = time.AfterFunc(nextCheck, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.inst != nil && c.generation == generation {
				c.reapRetired()
				_ = c.refreshBootstrapDNS(c.cur, nil)
			}
		})
	}
}

func isProfileOutbound(tag string) bool {
	return strings.HasPrefix(tag, "masir-profile/") || strings.HasPrefix(tag, "sub-")
}
