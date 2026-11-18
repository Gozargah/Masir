//go:build xray_patched

package xray

import (
	"syscall"

	"github.com/xtls/xray-core/proxy/tun"
	"github.com/xtls/xray-core/transport/internet"
)

// Probe helpers have no TUN inbound, so they do not install Xray's interface
// controller. The host may still route through the daemon's TUN. Select the
// physical default route for each new socket, including XICMP's ICMP sockets.
func configureProbeInterface() error {
	updater := new(tun.InterfaceUpdater)
	return internet.RegisterDialerController(func(network, address string, c syscall.RawConn) error {
		return updater.BindSocket(network, address, c)
	})
}
