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
	"time"

	applog "github.com/xtls/xray-core/app/log"
	corenet "github.com/xtls/xray-core/common/net"
	xserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	confserial "github.com/xtls/xray-core/infra/conf/serial"
)

const DefaultProbeURL = "https://www.google.com/generate_204"

type LatencyOptions struct {
	CurrentTunName  func() string `json:"-"` // snapshot after acquiring the worker slot
	TunName         string        // active Masir TUN to exclude from the probe route
	URL             string        // probe URL; empty means DefaultProbeURL
	Attempts        int           // requests per config; the fastest wins (default 2)
	Timeout         time.Duration // limit for one request (default 12s)
	BalancedProfile bool          // use Masir’s managed routing and observatory
	Full            bool          // keep dns/routing/policy instead of stripping them
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

// measureDelayLocal starts a temporary Xray instance from cfgJSON, sends requests
// through its first outbound, and returns the fastest round trip.
// Cancelling ctx aborts the test; the instance is always closed.
func measureDelayLocal(ctx context.Context, cfgJSON []byte, opt LatencyOptions) (time.Duration, error) {
	opt = opt.withDefaults()

	if opt.BalancedProfile {
		p, err := ParseProfile(cfgJSON)
		if err != nil {
			return 0, err
		}
		p.inbounds = nil
		p.base["log"] = json.RawMessage(`{"loglevel":"none"}`)
		engine := New("")
		defer engine.Close()
		if err := engine.Apply(ctx, p); err != nil {
			return 0, err
		}

		return measureThrough(ctx, engine.inst, "", opt)
	}

	cfg, err := confserial.LoadJSONConfig(bytes.NewReader(cfgJSON))
	if err != nil {
		return 0, fmt.Errorf("load config: %w", err)
	}
	if len(cfg.Outbound) == 0 {
		return 0, errors.New("config has no outbounds")
	}

	quietProbeLogs(cfg)
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
	// The helper process owns all of this instance's resources.
	defer inst.Close()
	if err := inst.Start(); err != nil {
		return 0, fmt.Errorf("start instance: %w", err)
	}

	return measureThrough(ctx, inst, "", opt)
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
			if opt.BalancedProfile {
				ctx = session.ContextWithContent(ctx, &session.Content{SniffingRequest: session.SniffingRequest{
					Enabled: true, RouteOnly: true, OverrideDestinationForProtocol: []string{"http", "tls", "quic"},
				}})
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
		d, err := probeAttempt(ctx, client, opt)
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

// Give observers time to publish their first result after a fast failed
// fallback request. The retries share one attempt's existing timeout.
func probeAttempt(ctx context.Context, client *http.Client, opt LatencyOptions) (time.Duration, error) {
	if !opt.BalancedProfile {
		return probeOnce(ctx, client, opt.URL, opt.Timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		delay, err := probeOnce(ctx, client, opt.URL, opt.Timeout)
		if err == nil {
			return delay, nil
		}
		client.CloseIdleConnections()
		select {
		case <-ctx.Done():
			return 0, err
		case <-tick.C:
		}
	}
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

// Probe logs never open caller-supplied files or share the result stream.
func quietProbeLogs(cfg *core.Config) {
	for i, app := range cfg.App {
		if app.Type == "xray.app.log.Config" {
			cfg.App[i] = xserial.ToTypedMessage(&applog.Config{
				AccessLogType: applog.LogType_None,
				ErrorLogType:  applog.LogType_None,
			})
		}
	}
}
