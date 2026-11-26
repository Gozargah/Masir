package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/paths"
)

// Seams: the tests replace these instead of touching the real machine.
var (
	listen        = net.Listen
	dial          = net.DialTimeout
	lookupHost    = net.DefaultResolver.LookupHost
	readFile      = os.ReadFile
	configDir     = paths.ConfigDirNoCreate
	proxyProbeURL = "https://www.gstatic.com/generate_204"
	dnsProbeHost  = "example.com"
	probeProxy    = httpProbe
)

// statusKnown reports whether the daemon's own state could be read: alive, no
// status error. Checks that reason about the daemon's internals say "unknown"
// instead of guessing when it is false.
func statusKnown(env Env) bool { return env.DaemonRunning && env.StatusError == "" }

// httpProbe fetches proxyProbeURL through the proxy and reports what happened.
func httpProbe(addr string) (int, time.Duration, error) {
	u, err := url.Parse("http://" + addr)
	if err != nil {
		return 0, 0, err
	}
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true},
	}
	start := time.Now()
	resp, err := client.Get(proxyProbeURL)
	took := time.Since(start)
	if err != nil {
		return 0, took, err
	}
	resp.Body.Close()
	return resp.StatusCode, took, nil
}

func baseChecks() []Check {
	return []Check{
		{ID: "daemon", Title: "Daemon", Group: "Connection", Run: checkDaemon},
		{ID: "versions", Title: "Versions", Group: "Connection", Run: checkVersions},
		{ID: "daemon-error", Title: "Daemon error", Group: "Connection", Run: checkDaemonError},
		{ID: "xray", Title: "Xray core", Group: "Connection", Run: checkXray},
		{ID: "proxy-port", Title: "Proxy port", Group: "Connection", Run: checkProxyPort},
		{ID: "proxy-reach", Title: "Proxy path", Group: "Connection", Run: checkProxyReach},
		{ID: "tunnel", Title: "Tunnel", Group: "Network", Run: checkTunnel},
		{ID: "dns", Title: "DNS", Group: "Network", Run: checkDNS},
		{ID: "routing", Title: "Routing rules", Group: "System", Run: checkRouting},
	}
}

func checkDaemon(ctx context.Context, env Env) Result {
	if !env.DaemonRunning {
		return Result{
			State:  Fail,
			Detail: "The masir daemon is not running, so nothing is being routed.",
			Fix:    "Start it with `masir connect` (or `masir daemon start`).",
		}
	}
	if env.StatusError != "" {
		return Result{
			State:  Fail,
			Detail: fmt.Sprintf("The daemon is alive but does not answer its status request: %s.", strings.TrimSuffix(env.StatusError, ".")),
			Fix:    "Restart it (`masir daemon restart`). If it keeps failing, `masir logs --level error` shows why — a half-updated install is a common cause.",
		}
	}
	return Result{State: OK, Detail: "The daemon is running and answers."}
}

func checkVersions(ctx context.Context, env Env) Result {
	if !env.DaemonRunning {
		return Result{State: Skip, Detail: "The daemon is not running, so its version is unknown."}
	}
	if env.StatusError != "" {
		return Result{State: Skip, Detail: "The daemon's status is unknown, so its version cannot be read."}
	}
	if env.CLIVersion == "" || env.DaemonVersion == "" {
		return Result{State: Skip, Detail: "One of the two versions was not reported."}
	}
	if isDevVersion(env.CLIVersion) {
		return Result{State: Skip, Detail: "This is a development build, so there is no release version to compare with."}
	}
	if env.CLIVersion == env.DaemonVersion {
		return Result{State: OK, Detail: fmt.Sprintf("The command line and the daemon both run %s.", env.CLIVersion)}
	}
	return Result{
		State:  Warn,
		Detail: fmt.Sprintf("The command line is %s, the daemon is %s: the install is half updated.", env.CLIVersion, env.DaemonVersion),
		Fix:    "Restart the service so both run the same version (`masir daemon restart`); if they still differ, reinstall so both come from one release.",
	}
}

// isDevVersion reports versions that carry no release number: a build from
// source never matches a release, so comparing them proves nothing.
func isDevVersion(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "dev", "development", "unknown", "0.0.0", "snapshot":
		return true
	}
	return false
}

// checkDaemonError quotes the daemon's own last failure: it is usually the most
// precise sentence available about why traffic is not flowing.
func checkDaemonError(ctx context.Context, env Env) Result {
	if !env.DaemonRunning {
		return Result{State: Skip, Detail: "The daemon is not running, so it has nothing to report."}
	}
	if env.StatusError != "" {
		return Result{State: Skip, Detail: "The daemon's status is unknown, so its last error cannot be read."}
	}
	msg := strings.TrimSpace(env.LastError)
	if msg == "" {
		return Result{State: OK, Detail: "The daemon reports no error."}
	}
	detail := fmt.Sprintf("The daemon reports: %s.", strings.TrimSuffix(msg, "."))
	if !env.Power {
		return Result{
			State:  Warn,
			Detail: detail + " (from its last attempt; it is idle now)",
			Fix:    "Fix what the message names, then connect again with `masir connect`.",
		}
	}
	return Result{
		State:  Fail,
		Detail: detail + " Traffic is not flowing through masir.",
		Fix:    "If it names a port, another proxy or VPN client is holding it: stop that client, then reconnect. Otherwise `masir logs --level error` has the detail.",
	}
}

func checkXray(ctx context.Context, env Env) Result {
	if !env.DaemonRunning {
		return Result{State: Skip, Detail: "The daemon is not running, so Xray is not either."}
	}
	if env.StatusError != "" {
		return Result{State: Skip, Detail: "The daemon's status is unknown, so Xray cannot be checked."}
	}
	if !env.Power {
		return Result{State: Skip, Detail: "masir is idle (started but not connected), so Xray is off by design.", Fix: ""}
	}
	if !env.XrayRunning {
		return Result{
			State:  Fail,
			Detail: "The daemon runs, but Xray does not: no traffic is proxied.",
			Fix:    "`masir status` and `masir logs --level error` show why; usually the active server's config failed to load or its port is taken.",
		}
	}
	detail := "Xray is running."
	if env.XrayVersion != "" {
		detail = fmt.Sprintf("Xray is running (core %s).", env.XrayVersion)
	}
	return Result{State: OK, Detail: detail}
}

func checkProxyPort(ctx context.Context, env Env) Result {
	addr := env.ProxyAddress
	if addr == "" {
		return Result{State: Skip, Detail: "The daemon did not report a proxy address."}
	}
	ln, err := listen("tcp", addr)
	free := err == nil
	if ln != nil {
		ln.Close()
	}
	if err != nil && !portInUse(err) {
		// Not a conflict: permission, a bad address, a firewall. Say what happened
		// instead of blaming another program.
		return Result{
			State:  Skip,
			Detail: fmt.Sprintf("The proxy port %s could not be tested: %v.", addr, err),
			Fix:    "Run the command as the user who runs masir, not as root.",
		}
	}
	if !statusKnown(env) {
		if free {
			return Result{State: Skip, Detail: fmt.Sprintf("The proxy port %s is free; masir's own state is unknown.", addr)}
		}
		return Result{
			State:  Warn,
			Detail: fmt.Sprintf("%s is in use, and masir's own state is unknown, so the owner cannot be told.", addr),
			Fix:    "Restart the daemon (`masir daemon restart`) and run this again.",
		}
	}
	switch {
	case env.XrayRunning && free:
		return Result{
			State:  Warn,
			Detail: fmt.Sprintf("Xray reports running, but nothing is listening on %s yet.", addr),
			Fix:    "Give it a moment; if it persists, restart the daemon (`masir daemon restart`).",
		}
	case env.XrayRunning:
		return Result{State: OK, Detail: fmt.Sprintf("The proxy port %s is in use by masir's Xray.", addr)}
	case free:
		return Result{State: OK, Detail: fmt.Sprintf("The proxy port %s is free.", addr)}
	default:
		return Result{
			State:  Fail,
			Detail: fmt.Sprintf("Another process holds %s, so masir cannot bind its port.", addr),
			Fix:    "Stop that process — another proxy client can hold the same port — then `masir connect`.",
		}
	}
}

// portInUse reports whether a bind error is a conflict rather than a different
// failure (permission, bad address). The messages are the ones the kernels
// produce on Linux, macOS and Windows.
func portInUse(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"address already in use", "only one usage of each socket address", "eaddrinuse"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func checkProxyReach(ctx context.Context, env Env) Result {
	addr := env.ProxyAddress
	if addr == "" || !env.XrayRunning {
		return Result{State: Skip, Detail: "The proxy is not running, so there is nothing to reach."}
	}
	if !statusKnown(env) {
		return Result{State: Skip, Detail: "masir's own state is unknown, so a successful request could belong to another client on the port."}
	}
	status, took, err := probeProxy(addr)
	if err != nil {
		return Result{
			State:  Warn,
			Detail: fmt.Sprintf("The proxy port answers, but a request through it failed: %v.", err),
			Fix:    "Check the active server: `masir status` shows its latency, `masir logs --level error` shows the reason.",
		}
	}
	if status >= 400 {
		return Result{
			State:  Warn,
			Detail: fmt.Sprintf("A request through the proxy returned HTTP %d.", status),
			Fix:    "Check the active server and the routing rules for the probe address.",
		}
	}
	return Result{State: OK, Detail: fmt.Sprintf("A request through the proxy succeeded in %d ms.", took.Milliseconds())}
}

func checkDNS(ctx context.Context, env Env) Result {
	start := time.Now()
	_, err := lookupHost(ctx, dnsProbeHost)
	took := time.Since(start)
	if err != nil {
		return Result{
			State:  Fail,
			Detail: fmt.Sprintf("DNS resolution failed for %s.", dnsProbeHost),
			Fix:    "If the tunnel is on, DNS goes through masir: disconnect once and resolve again. Otherwise check the system resolver settings (`masir settings` has the DNS options).",
		}
	}
	return Result{State: OK, Detail: fmt.Sprintf("%s resolved in %d ms.", dnsProbeHost, took.Milliseconds())}
}

func checkRouting(ctx context.Context, env Env) Result {
	dir, err := configDir()
	if err != nil {
		return Result{State: Skip, Detail: "The configuration directory could not be found."}
	}
	data, err := readFile(filepath.Join(dir, "routing.json"))
	if err != nil {
		return Result{State: Skip, Detail: "No saved routing rules were found."}
	}
	// Decode the way the daemon's loader does ({"rules":[{tag,match,target}]}),
	// so a file that parses as JSON but would be rejected at load is caught here.
	var routing struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(data, &routing); err != nil {
		return Result{
			State:  Fail,
			Detail: fmt.Sprintf("The saved routing rules (routing.json) cannot be read the way masir reads them: %v.", err),
			Fix:    "`masir rules list` reports the same error; restore the file from a backup or re-add the rules.",
		}
	}
	for i, raw := range routing.Rules {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rule); err != nil {
			return Result{
				State:  Fail,
				Detail: fmt.Sprintf("Routing rule %d is not an object, so the rule list cannot load.", i+1),
				Fix:    "`masir rules list` reports the same error; re-add the rules or restore a backup.",
			}
		}
	}
	return Result{State: OK, Detail: fmt.Sprintf("The saved routing rules load (%d rules).", len(routing.Rules))}
}

func checkTunnel(ctx context.Context, env Env) Result {
	if !env.TunRunning {
		return Result{State: Skip, Detail: "The tunnel is off; only proxy-mode checks apply."}
	}
	gw := strings.TrimSpace(env.TunGateway)
	if i := strings.IndexByte(gw, '/'); i >= 0 {
		gw = gw[:i]
	}
	if gw == "" {
		return Result{State: Skip, Detail: "The daemon did not report the tunnel's gateway."}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return Result{State: Skip, Detail: "Network interfaces could not be listed."}
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err == nil && ip.String() == gw {
				return Result{State: OK, Detail: fmt.Sprintf("The tunnel interface %s carries %s.", iface.Name, gw)}
			}
		}
	}
	return Result{
		State:  Fail,
		Detail: fmt.Sprintf("The daemon reports the tunnel on, but no interface carries its gateway address %s.", gw),
		Fix:    "Disconnect and reconnect: `masir disconnect` then `masir connect`. If that does not stick, look at `masir logs --level error`.",
	}
}
