package systemdns

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"time"
)

// Verify the local route without querying an external server. Upstream reachability
// is not a prerequisite for installing system DNS (or for connecting while offline).
func checkRoutes(ctx context.Context, name string, servers []string) error {
	return checkRoutesWith(ctx, name, servers, func(ctx context.Context, path string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, path, args...).CombinedOutput()
	})
}

func checkRoutesWith(ctx context.Context, name string, servers []string, run func(context.Context, string, ...string) ([]byte, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, server := range servers {
		ip, err := netip.ParseAddr(server)
		if err != nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
			return fmt.Errorf("DNS server %q is not a routable TUN destination", server)
		}
		family := "-inet"
		if ip.Is6() {
			family = "-inet6"
		}
		out, err := run(ctx, "/sbin/route", "-n", "get", family, server)
		if err != nil {
			return fmt.Errorf("route to DNS server %s: %w: %s", server, err, out)
		}
		if routeInterface(string(out)) != name {
			return fmt.Errorf("DNS server %s is not routed through %s", server, name)
		}
	}
	return nil
}
func routeInterface(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "interface:" {
			return fields[1]
		}
	}
	return ""
}
