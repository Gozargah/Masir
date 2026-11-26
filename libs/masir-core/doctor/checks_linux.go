//go:build linux

package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Gozargah/Masir/libs/masir-core/service"
)

// Seams: the tests replace these instead of touching the real machine.
var (
	runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		// Combined output: `ping` puts "message too long" on stderr, and the MTU
		// check needs that text to tell a local rejection from a blackhole.
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	selinuxEnforcing = service.SelinuxEnforcing
	binaryLabelOK    = service.LabelOK
	serviceBinary    = service.BinaryPath
	ipv6ProbeTarget  = "[2606:4700:4700::1111]:443"
	mtuProbeTarget   = "1.1.1.1"
	hasIPv6          = hasGlobalIPv6
)

func platformChecks() []Check {
	return []Check{
		{ID: "selinux-label", Title: "SELinux label", Group: "System", Run: checkSELinux},
		{ID: "selinux-denials", Title: "SELinux denials", Group: "System", Run: checkAVC},
		{ID: "xray-restarts", Title: "Xray restarts", Group: "System", Run: checkChurn},
		{ID: "default-route", Title: "Default route", Group: "Network", Run: checkDefaultRoute},
		{ID: "ipv6-path", Title: "IPv6 path", Group: "Network", Run: checkIPv6},
		{ID: "packet-size", Title: "Packet size", Group: "Network", Run: checkMTU},
	}
}

// checkSELinux catches the update-aftermath that confines the daemon: a
// replaced binary keeps the directory's label instead of bin_t.
func checkSELinux(ctx context.Context, env Env) Result {
	if !selinuxEnforcing() {
		return Result{State: Skip, Detail: "SELinux is not enforcing on this system."}
	}
	bin := serviceBinary()
	if bin == "" {
		return Result{State: Skip, Detail: "No service binary is known on this system."}
	}
	if _, err := os.Stat(bin); err != nil {
		return Result{State: Skip, Detail: fmt.Sprintf("There is no service binary at %s.", bin)}
	}
	if binaryLabelOK(bin) {
		return Result{State: OK, Detail: fmt.Sprintf("The service binary %s carries the label it needs.", bin)}
	}
	detail := fmt.Sprintf("The service binary %s does not carry the bin_t label", bin)
	if label := labelText(bin); label != "" {
		detail += fmt.Sprintf(" (it is %s)", label)
	}
	return Result{
		State:  Fail,
		Detail: detail + ", so the next service start runs in a confined domain with no network.",
		Fix:    fmt.Sprintf("Relabel it and restart: `sudo restorecon -F %s` then `masir daemon restart` (`masir daemon label` does both).", bin),
	}
}

// labelText reads security.selinux for the message; an empty string when the
// filesystem does not carry labels.
func labelText(bin string) string {
	buf := make([]byte, 256)
	n, err := syscall.Getxattr(bin, "security.selinux", buf)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(buf[:n]), "\x00")
}

func checkAVC(ctx context.Context, env Env) Result {
	if !selinuxEnforcing() {
		return Result{State: Skip, Detail: "SELinux is not enforcing on this system."}
	}
	out, err := runCommand(ctx, "journalctl", "-q", "--no-pager", "_TRANSPORT=audit", "--since", "-10 min")
	if err != nil {
		return Result{State: Skip, Detail: "The audit log is not readable here."}
	}
	denials := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "denied") && strings.Contains(line, "masir") {
			denials++
		}
	}
	if denials > 0 {
		return Result{
			State:  Warn,
			Detail: fmt.Sprintf("%d SELinux denials mention masir in the last 10 minutes.", denials),
			Fix:    "If masir cannot connect or open its tunnel, relabel its service binary (`masir daemon label`) and restart it.",
		}
	}
	return Result{State: OK, Detail: "No SELinux denials mention masir in the last 10 minutes."}
}

// checkDefaultRoute catches another VPN or a manual route out-ranking the
// tunnel: masir's TUN must own the lowest-metric default route.
func checkDefaultRoute(ctx context.Context, env Env) Result {
	if !env.TunRunning {
		return Result{State: Skip, Detail: "The tunnel is off."}
	}
	out, err := runCommand(ctx, "ip", "-4", "route", "show", "default")
	if err != nil {
		return Result{State: Skip, Detail: "The route table could not be read with `ip`."}
	}
	best, bestMetric := "", 1<<31-1
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		dev, metric := "", 0
		for i := 0; i+1 < len(fields); i++ {
			switch fields[i] {
			case "dev":
				dev = fields[i+1]
			case "metric":
				fmt.Sscanf(fields[i+1], "%d", &metric)
			}
		}
		if dev != "" && metric < bestMetric {
			best, bestMetric = dev, metric
		}
	}
	if best == "" {
		return Result{State: Skip, Detail: "There is no IPv4 default route to inspect."}
	}
	gw := strings.TrimSpace(env.TunGateway)
	if i := strings.IndexByte(gw, '/'); i >= 0 {
		gw = gw[:i]
	}
	tun := interfaceFor(gw)
	if tun == "" {
		return Result{State: Skip, Detail: "The tunnel interface could not be identified."}
	}
	if best != tun {
		return Result{
			State:  Warn,
			Detail: fmt.Sprintf("The IPv4 default route goes through %s, not through the tunnel (%s): traffic can bypass masir.", best, tun),
			Fix:    "Another VPN or a manual route is winning. Disconnect the other one, or reconnect masir so its route has the lowest metric.",
		}
	}
	return Result{State: OK, Detail: fmt.Sprintf("The IPv4 default route goes through the tunnel (%s, metric %d).", tun, bestMetric)}
}

// interfaceFor finds the interface that carries an address.
func interfaceFor(ip string) string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			got, _, err := net.ParseCIDR(a.String())
			if err == nil && got.String() == ip {
				return iface.Name
			}
		}
	}
	return ""
}

// checkIPv6 tests the path the tunnel takes for IPv6 traffic: a route that
// swallows packets (instead of refusing them) makes IPv6-preferring apps stall.
func checkIPv6(ctx context.Context, env Env) Result {
	if !env.TunRunning {
		return Result{State: Skip, Detail: "The tunnel is off."}
	}
	if !hasIPv6() {
		return Result{State: Skip, Detail: "This host has no global IPv6 address, so there is no IPv6 path to test."}
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(cctx, "tcp6", ipv6ProbeTarget)
	if err != nil {
		return Result{
			State:  Warn,
			Detail: "IPv6 traffic does not get through the tunnel: a well-known address did not answer.",
			Fix:    "Apps that prefer IPv6 can stall or disconnect. Route ::/0 outside the tunnel or disable IPv6 while it is on.",
		}
	}
	conn.Close()
	return Result{State: OK, Detail: "IPv6 traffic gets through the tunnel."}
}

// hasGlobalIPv6 reports whether the host has a routable IPv6 address at all;
// a link-local address alone means IPv6 is not configured and the probe would
// fail for reasons unrelated to masir.
func hasGlobalIPv6() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return true // cannot tell: let the probe speak for itself
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil || ip.To4() != nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			return true
		}
	}
	return false
}

// checkMTU separates the two ways a full-size probe fails: the kernel refusing
// a packet it knows is too large (the path MTU is simply below 1500, nothing is
// lost) versus a silent blackhole, which really does stall large transfers.
func checkMTU(ctx context.Context, env Env) Result {
	if !env.TunRunning {
		return Result{State: Skip, Detail: "The tunnel is off."}
	}
	if _, err := exec.LookPath("ping"); err != nil {
		return Result{State: Skip, Detail: "`ping` is not installed, so packet size cannot be probed."}
	}
	if out, err := runCommand(ctx, "ping", "-q", "-c", "1", "-W", "1", mtuProbeTarget); err != nil {
		return Result{State: Skip, Detail: fmt.Sprintf("The network did not answer a plain ping (%s), so the size probe proves nothing.", firstLine(out))}
	}
	out, err := runCommand(ctx, "ping", "-q", "-M", "do", "-s", "1472", "-c", "1", "-W", "1", mtuProbeTarget)
	if err == nil {
		return Result{State: OK, Detail: "Full-size packets pass the tunnel path."}
	}
	if pmtu := localMTURejection(string(out)); pmtu != "" {
		return Result{
			State:  OK,
			Detail: fmt.Sprintf("This path's MTU is below 1500 (%s): oversized packets are rejected locally, not dropped, so large transfers still work.", pmtu),
		}
	}
	return Result{
		State:  Warn,
		Detail: fmt.Sprintf("Small packets pass, but a full-size (1500-byte) packet gets no reply: %s.", firstLine(out)),
		Fix:    "This is the classic MTU blackhole — downloads stall halfway. Lower the tunnel MTU (1400 usually works); the setting is not exposed yet, so please report this.",
	}
}

// localMTURejection reports the "message too long"-style local error (with the
// MTU the kernel names, when it does) and "" for a timeout or lost packet.
func localMTURejection(output string) string {
	text := strings.ToLower(output)
	for _, marker := range []string{"message too long", "frag needed", "emsgsize", "too long"} {
		if !strings.Contains(text, marker) {
			continue
		}
		if m := regexp.MustCompile(`mtu\s*[= ]\s*(\d+)`).FindStringSubmatch(text); m != nil {
			return "mtu " + m[1]
		}
		return "rejected locally"
	}
	return ""
}

// firstLine keeps multi-line command output from flooding a one-line result.
func firstLine(out []byte) string {
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "no output"
	}
	return line
}

// checkChurn counts Xray builds: a UI that polls `masir status` with checks
// rebuilds Xray twice per poll, which looks like instability from outside.
func checkChurn(ctx context.Context, env Env) Result {
	out, err := runCommand(ctx, "journalctl", "-q", "--no-pager", "-u", "masird", "--since", "-10 min")
	if err != nil {
		return Result{State: Skip, Detail: "The service journal is not readable here."}
	}
	starts := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Xray") && strings.Contains(line, "started") {
			starts++
		}
	}
	if starts > 6 {
		return Result{
			State:  Warn,
			Detail: fmt.Sprintf("Xray was started %d times in the last 10 minutes.", starts),
			Fix:    "Something polls `masir status` in a loop, and every check rebuilds Xray. Poll with `masir status --no-check` and run the full check less often.",
		}
	}
	return Result{State: OK, Detail: fmt.Sprintf("Xray was started %d times in the last 10 minutes.", starts)}
}
