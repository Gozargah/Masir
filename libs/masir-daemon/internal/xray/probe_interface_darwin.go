package xray

import (
	"net"
	"syscall"

	"github.com/xtls/xray-core/proxy/tun"
	"github.com/xtls/xray-core/transport/internet"
)

// probeSocketBinder is supplied by the patched dependency. Detecting this API
// keeps stock builds working without coupling runtime binding to Go build tags.
type probeSocketBinder interface {
	BindSocket(string, string, syscall.RawConn) error
	SetExcludedTunIndex(int)
}

// Helpers have no TUN inbound and therefore install their own binding controller.
func configureProbeInterface(tunName string) error {
	if tunName == "" {
		return nil
	}
	binder, ok := any(new(tun.InterfaceUpdater)).(probeSocketBinder)
	if !ok {
		return nil // stock Xray
	}
	iface, err := net.InterfaceByName(tunName)
	if err != nil {
		return err
	}
	binder.SetExcludedTunIndex(iface.Index)
	return internet.RegisterDialerController(binder.BindSocket)
}
