package xray

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/xtls/xray-core/common/geodata"
	corenet "github.com/xtls/xray-core/common/net"
	xserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"
	confserial "github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all" // registers all protocols/transports
)

// Core owns the single Xray instance. Everything else in masird talks to this, never to *core.Instance.
type Core struct {
	assetDir string // where geoip.dat and geosite.dat live

	mu         sync.RWMutex
	inst       *core.Instance
	inbounds   inbound.Manager
	outbounds  outbound.Manager
	routing    routing.Router
	balancer   routing.BalancerOverrider
	stats      stats.Manager
	cur        *Profile
	generation uint64
	systemDNS  systemDNS
}

type systemDNS interface {
	before(*Profile) error
	after(context.Context, *Profile) error
	close() error
}

// New takes the directory holding geoip.dat/geosite.dat (e.g. the bundle's Resources/).
func New(assetDir string) *Core {
	if assetDir != "" {
		_ = os.Setenv("XRAY_LOCATION_ASSET", assetDir)
	}
	return &Core{assetDir: assetDir}
}

// AssetDir is where Xray reads geoip.dat and geosite.dat.
func (c *Core) AssetDir() string { return c.assetDir }

// ReloadGeodata makes the running Xray use new geo files. Xray caches what it loaded,
// so rebuilding the config alone would keep the old data.
func ReloadGeodata() error {
	return errors.Join(geodata.IPReg.Reload(), geodata.DomainReg.Reload())
}

// Apply is the single entry point for changes. It restarts Xray or patches it live.
// If p can't run at all, the previous profile is brought back and the error returned.
func (c *Core) Apply(ctx context.Context, p *Profile) error {
	c.mu.Lock() // held for the whole apply so changes cannot interleave
	defer c.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	prev := c.cur
	if c.systemDNS != nil {
		if err := c.systemDNS.before(p); err != nil {
			return err
		}
	}
	err := c.applyLocked(ctx, p)
	if err != nil && c.inst != nil && c.systemDNS != nil {
		// A live apply can fail before removing the old inbound, after before()
		// suspended DNS. Reinstate DNS for the profile that still runs.
		err = errors.Join(err, c.systemDNS.after(context.WithoutCancel(ctx), c.cur))
	}
	if err == nil && c.systemDNS != nil {
		if dnsErr := c.systemDNS.after(ctx, p); dnsErr != nil {
			err = errors.Join(dnsErr, c.stopLocked())
		}
	}
	if err != nil && c.inst == nil && prev != nil {
		if rerr := c.restartLocked(context.WithoutCancel(ctx), prev); rerr != nil {
			return errors.Join(err, rerr)
		}
		if c.systemDNS != nil {
			if rerr := c.systemDNS.after(context.WithoutCancel(ctx), prev); rerr != nil {
				return errors.Join(err, rerr, c.stopLocked())
			}
		}
	}
	return err
}

// applyLocked is the only place that changes c.cur, so c.cur always describes what Xray runs.
func (c *Core) applyLocked(ctx context.Context, p *Profile) error {
	if c.inst == nil || p.needsRestart(c.cur) {
		return c.restartLocked(ctx, p)
	}
	if err := c.applyLiveLocked(ctx, c.cur, p); err != nil {
		// Live state is now partially applied. Rebuild from scratch rather than guess.
		return c.restartLocked(context.WithoutCancel(ctx), p)
	}
	c.cur = p
	return nil
}

func (c *Core) Running() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inst != nil
}

func (c *Core) GetVersion() string {
	return core.Version()
}

// Dial opens a connection through the running Xray, following its routing rules.
func (c *Core) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	inst := c.inst
	if inst == nil {
		return nil, ErrNotRunning
	}
	dest, err := corenet.ParseDestination(network + ":" + addr)
	if err != nil {
		return nil, err
	}
	return core.Dial(ctx, inst, dest)
}

func (c *Core) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopLocked()
}

func (c *Core) restartLocked(ctx context.Context, p *Profile) error {
	if err := c.stopLocked(); err != nil {
		return err
	}

	raw, err := p.bootstrap()
	if err != nil {
		return err
	}
	cfg, err := confserial.LoadJSONConfig(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("xray config: %w", err)
	}
	inst, err := core.New(cfg)
	if err != nil {
		return err
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		return err
	}
	if err := c.bindFeatures(inst); err != nil {
		_ = inst.Close()
		return err
	}
	c.inst, c.cur = inst, p
	c.generation++
	if err := c.setFallback(ctx, p.fallback); err != nil {
		c.stopLocked()
		return err
	}
	return nil
}

func (c *Core) stopLocked() error {
	var err error
	if c.systemDNS != nil {
		err = c.systemDNS.close()
	}
	c.generation++
	if c.inst != nil {
		_ = c.inst.Close()
	}
	c.inst, c.cur = nil, nil
	c.inbounds, c.outbounds, c.routing, c.balancer, c.stats = nil, nil, nil, nil, nil
	return err
}

// bindFeatures validates all dependencies before publishing any of them. Called under mu.
func (c *Core) bindFeatures(inst *core.Instance) error {
	im, ok := inst.GetFeature(inbound.ManagerType()).(inbound.Manager)
	if !ok {
		return errors.New("xray: required inbound manager unavailable")
	}
	om, ok := inst.GetFeature(outbound.ManagerType()).(outbound.Manager)
	if !ok {
		return errors.New("xray: required outbound manager unavailable")
	}
	rt, ok := inst.GetFeature(routing.RouterType()).(routing.Router)
	if !ok {
		return errors.New("xray: required router unavailable")
	}
	bo, ok := rt.(routing.BalancerOverrider)
	if !ok {
		return fmt.Errorf("xray: router %T does not support balancer overrides", rt)
	}
	sm, ok := inst.GetFeature(stats.ManagerType()).(stats.Manager)
	if !ok {
		return errors.New("xray: required statistics manager unavailable")
	}
	c.inbounds, c.outbounds, c.routing, c.balancer, c.stats = im, om, rt, bo, sm
	return nil
}

// applyLiveLocked order: sync inbounds, add/replace outbounds, swap rules, then drop outbounds nothing needs.
// The fallback goes last: retargeting its balancer is atomic, so the mode changes all at once.
func (c *Core) applyLiveLocked(ctx context.Context, old, next *Profile) error {
	if err := c.syncInbounds(ctx, old.inbounds, next.inbounds); err != nil {
		return err
	}

	oldOut := byTag(old.outbounds)
	nextOut := byTag(next.outbounds)

	for _, o := range next.outbounds {
		prev, exists := oldOut[o.Tag]
		if exists && bytes.Equal(prev.JSON, o.JSON) {
			continue
		}
		if exists { // no "update" in the API: replace = remove + add (brief gap for that tag)
			if err := c.removeOutbound(ctx, o.Tag); err != nil {
				return err
			}
		}
		if err := c.addOutbound(ctx, o); err != nil {
			return err
		}
	}

	// Only the tail that differs is rewritten: Xray appends rules in order, so leaving
	// the unchanged head alone keeps the order and leaves its rules untouched.
	if n := commonPrefix(old.rules, next.rules); n < len(old.rules) || n < len(next.rules) {
		for i := len(old.rules) - 1; i >= n; i-- {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := c.routing.RemoveRule(old.rules[i].Tag); err != nil {
				return err
			}
		}
		for _, r := range next.rules[n:] {
			if err := c.addRule(ctx, r); err != nil {
				return err
			}
		}
	}

	for _, o := range old.outbounds {
		if _, keep := nextOut[o.Tag]; !keep {
			if err := c.removeOutbound(ctx, o.Tag); err != nil {
				return err
			}
		}
	}

	if old.fallback != next.fallback {
		if err := c.setFallback(ctx, next.fallback); err != nil {
			return err
		}
	}
	return nil
}

// setFallback points the catch-all's balancer at an outbound. One call, so no rule is
// removed and no connection notices; the balancer's selector is only the start-up value.
func (c *Core) setFallback(ctx context.Context, f fallback) error {
	if f.balancer == "" {
		return nil
	}
	// The mode is daemon state: finish the atomic override even if the caller cancelled.
	return c.balancer.SetOverrideTarget(f.balancer, f.target)
}

// Removals go first so a re-added inbound can take over the port of the one it replaces.
func (c *Core) syncInbounds(ctx context.Context, old, next []item) error {
	oldIn, nextIn := byTag(old), byTag(next)
	for _, in := range old {
		if n, keep := nextIn[in.Tag]; !keep || !bytes.Equal(n.JSON, in.JSON) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := c.inbounds.RemoveHandler(ctx, in.Tag); err != nil {
				return err
			}
		}
	}
	for _, in := range next {
		if prev, exists := oldIn[in.Tag]; exists && bytes.Equal(prev.JSON, in.JSON) {
			continue
		}
		built, err := buildInbound(in.JSON)
		if err != nil {
			return fmt.Errorf("inbound %q: %w", in.Tag, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := core.AddInboundHandler(c.inst, built); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) addOutbound(ctx context.Context, o item) error {
	built, err := buildOutbound(o.JSON)
	if err != nil {
		return fmt.Errorf("outbound %q: %w", o.Tag, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return core.AddOutboundHandler(c.inst, built)
}

func (c *Core) removeOutbound(ctx context.Context, tag string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.outbounds.RemoveHandler(ctx, tag)
}

func (c *Core) addRule(ctx context.Context, r item) error {
	built, err := buildRule(r.JSON)
	if err != nil {
		return fmt.Errorf("rule %q: %w", r.Tag, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.routing.AddRule(xserial.ToTypedMessage(built), true) // append preserves rule order
}

func byTag(items []item) map[string]item {
	m := make(map[string]item, len(items))
	for _, it := range items {
		m[it.Tag] = it
	}
	return m
}

func sameItems(a, b []item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Tag != b[i].Tag || !bytes.Equal(a[i].JSON, b[i].JSON) {
			return false
		}
	}
	return true
}

// commonPrefix is how many leading items are identical, tag and JSON. A change only
// rewrites the rules after it, so an unchanged rule is never removed and re-added.
func commonPrefix(a, b []item) int {
	n := 0
	for n < len(a) && n < len(b) && a[n].Tag == b[n].Tag && bytes.Equal(a[n].JSON, b[n].JSON) {
		n++
	}
	return n
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
