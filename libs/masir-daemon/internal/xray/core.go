package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/geodata"
	corenet "github.com/xtls/xray-core/common/net"
	xserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"
	confserial "github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all" // registers all protocols/transports
	"github.com/xtls/xray-core/transport/internet"
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
	err := c.applyLocked(ctx, p)
	if err != nil && c.inst == nil && prev != nil {
		if rerr := c.restartLocked(context.WithoutCancel(ctx), prev); rerr != nil {
			return errors.Join(err, rerr)
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
	c.stopLocked()
	return nil
}

func (c *Core) restartLocked(ctx context.Context, p *Profile) error {
	c.stopLocked()

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
	setMainInstance(inst)
	c.generation++
	if err := c.setFallback(ctx, p.fallback); err != nil {
		c.stopLocked()
		return err
	}
	return nil
}

func (c *Core) stopLocked() {
	setMainInstance(nil)
	c.generation++
	if c.inst != nil {
		_ = c.inst.Close()
	}
	c.inst, c.cur = nil, nil
	c.inbounds, c.outbounds, c.routing, c.balancer, c.stats = nil, nil, nil, nil, nil
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

var (
	mainCoreMu   sync.RWMutex
	mainInstance *core.Instance
)

func setMainInstance(inst *core.Instance) {
	mainCoreMu.Lock()
	mainInstance = inst
	mainCoreMu.Unlock()
}

// restoreMainDialer points Xray's global system dialer back to the main running instance.
// A temporary instance started for latency testing overwrites the global dialer in Xray,
// so when the test ends the main instance's dialerProxy handlers (e.g. loopback-dns) must be restored.
func restoreMainDialer() {
	mainCoreMu.RLock()
	inst := mainInstance
	mainCoreMu.RUnlock()
	if inst == nil {
		internet.InitSystemDialer(nil, nil)
		return
	}
	var dc dns.Client
	if f := inst.GetFeature(dns.ClientType()); f != nil {
		dc, _ = f.(dns.Client)
	}
	var obm outbound.Manager
	if f := inst.GetFeature(outbound.ManagerType()); f != nil {
		obm, _ = f.(outbound.Manager)
	}
	internet.InitSystemDialer(dc, obm)
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

const DefaultProbeURL = "https://www.google.com/generate_204"

type LatencyOptions struct {
	URL      string        // probe URL; empty means DefaultProbeURL
	Attempts int           // requests per config; the fastest wins (default 2)
	Timeout  time.Duration // limit for one request (default 12s)
	Full     bool          // keep dns/routing/policy instead of stripping them
}

func (o LatencyOptions) withDefaults() LatencyOptions {
	if o.URL == "" {
		o.URL = DefaultProbeURL
	}
	if o.Attempts < 1 {
		o.Attempts = 2
	}
	if o.Timeout <= 0 {
		o.Timeout = 12 * time.Second
	}
	return o
}

// Only these app configs survive when Full is false.
var essentialApps = map[string]bool{
	"xray.app.proxyman.OutboundConfig": true,
	"xray.app.dispatcher.Config":       true,
	"xray.app.log.Config":              true,
}

// MeasureDelay starts a temporary Xray instance from cfgJSON, sends requests
// through its first outbound, and returns the fastest round trip.
// Cancelling ctx aborts the test; the instance is always closed.
func MeasureDelay(ctx context.Context, cfgJSON []byte, opt LatencyOptions) (time.Duration, error) {
	opt = opt.withDefaults()

	cfg, err := confserial.LoadJSONConfig(bytes.NewReader(cfgJSON))
	if err != nil {
		return 0, fmt.Errorf("load config: %w", err)
	}
	if len(cfg.Outbound) == 0 {
		return 0, errors.New("config has no outbounds")
	}

	cfg.Inbound = nil // nothing listens on any port
	if !opt.Full {
		keep := cfg.App[:0] // filter in place, reusing the same backing array
		for _, app := range cfg.App {
			if essentialApps[app.Type] {
				keep = append(keep, app)
			}
		}
		cfg.App = keep
	}

	// core.New and Start can't be cancelled, so check first.
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	inst, err := core.New(cfg)
	if err != nil {
		return 0, fmt.Errorf("create instance: %w", err)
	}
	// core.New overwrote Xray's global system dialer with this temporary instance.
	// Restore the main instance immediately and ensure inst.Close runs on exit.
	restoreMainDialer()
	defer func() {
		_ = inst.Close()
		restoreMainDialer()
		debug.FreeOSMemory()
	}()
	if err := inst.Start(); err != nil {
		return 0, fmt.Errorf("start instance: %w", err)
	}

	return measureThrough(ctx, inst, "", opt)
}

// Probe is one outbound to latency-test; Tag only identifies it in results.
type Probe struct {
	Tag      string
	Outbound json.RawMessage
}

const maxProbeBatch = 16

// MeasureEach tests many outbounds on temporary Xray instances in bounded batches,
// up to workers at a time.
// report gets exactly one call per probe (never concurrently), even when ctx is cancelled.
func MeasureEach(ctx context.Context, probes []Probe, opt LatencyOptions, workers int, report func(tag string, d time.Duration, err error)) {
	opt = opt.withDefaults()
	if workers < 1 {
		workers = 1
	}
	var mu sync.Mutex
	send := func(tag string, d time.Duration, err error) {
		mu.Lock()
		defer mu.Unlock()
		report(tag, d, err)
	}
	defer func() {
		restoreMainDialer()
		debug.FreeOSMemory()
	}()

	// An outbound that doesn't build would stop the whole instance from starting.
	var valid []Probe
	for _, p := range probes {
		it, err := newItem(p.Outbound, "tag", p.Tag)
		if err == nil {
			_, err = buildOutbound(it.JSON)
		}
		if err != nil {
			send(p.Tag, 0, fmt.Errorf("invalid outbound: %w", err))
			continue
		}
		valid = append(valid, p)
	}
	if len(valid) == 0 {
		return
	}

	for i := 0; i < len(valid); i += maxProbeBatch {
		if ctx.Err() != nil {
			for _, p := range valid[i:] {
				send(p.Tag, 0, ctx.Err())
			}
			break
		}
		end := min(i+maxProbeBatch, len(valid))
		measureBatch(ctx, valid[i:end], opt, workers, send)
	}
}

func measureBatch(ctx context.Context, batch []Probe, opt LatencyOptions, workers int, send func(string, time.Duration, error)) {
	outs := make([]json.RawMessage, 0, len(batch))
	for _, p := range batch {
		it, _ := newItem(p.Outbound, "tag", p.Tag)
		outs = append(outs, it.JSON)
	}

	failBatch := func(err error) {
		for _, p := range batch {
			send(p.Tag, 0, err)
		}
	}

	cfg, err := confserial.LoadJSONConfig(bytes.NewReader(marshal(map[string]any{"outbounds": outs})))
	if err != nil {
		failBatch(fmt.Errorf("load config: %w", err))
		return
	}
	cfg.Inbound = nil
	keep := cfg.App[:0]
	for _, app := range cfg.App {
		if essentialApps[app.Type] {
			keep = append(keep, app)
		}
	}
	cfg.App = keep
	inst, err := core.New(cfg)
	if err != nil {
		failBatch(fmt.Errorf("load instance: %w", err))
		return
	}
	// core.New overwrote Xray's global system dialer with this temporary instance.
	// Restore the main instance immediately and ensure inst.Close runs on exit.
	restoreMainDialer()
	defer func() {
		_ = inst.Close()
		restoreMainDialer()
	}()
	if err := inst.Start(); err != nil {
		failBatch(fmt.Errorf("start instance: %w", err))
		return
	}

	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(workers, len(batch)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tag := range jobs {
				if err := ctx.Err(); err != nil {
					send(tag, 0, err)
					continue
				}
				d, err := measureThrough(ctx, inst, tag, opt)
				send(tag, d, err)
			}
		}()
	}
	for _, p := range batch {
		jobs <- p.Tag
	}
	close(jobs)
	wg.Wait()
}

// measureThrough probes via the outbound tagged tag, or Xray's default outbound when tag is empty.
func measureThrough(ctx context.Context, inst *core.Instance, tag string, opt LatencyOptions) (time.Duration, error) {
	tr := &http.Transport{
		TLSHandshakeTimeout: 6 * time.Second,
		// Instead of opening a real socket, hand the connection to Xray.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dest, err := corenet.ParseDestination(network + ":" + addr)
			if err != nil {
				return nil, err
			}
			if tag != "" {
				ctx = session.SetForcedOutboundTagToContext(ctx, tag)
			}
			return core.Dial(ctx, inst, dest)
		},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}

	best := time.Duration(-1)
	var lastErr error
	for i := 0; i < opt.Attempts; i++ {
		if err := ctx.Err(); err != nil {
			if best >= 0 {
				return best, nil // cancelled, but we already have a result
			}
			return 0, err
		}
		d, err := probeOnce(ctx, client, opt.URL, opt.Timeout)
		if err != nil {
			lastErr = err
			continue
		}
		if best < 0 || d < best {
			best = d
		}
	}
	if best < 0 {
		return 0, lastErr
	}
	return best, nil
}

// probeOnce is its own function so defer cancel() runs after every request,
// not once at the end of the loop.
func probeOnce(ctx context.Context, client *http.Client, url string, timeout time.Duration) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("unexpected status %s", resp.Status)
	}
	return time.Since(start), nil
}
