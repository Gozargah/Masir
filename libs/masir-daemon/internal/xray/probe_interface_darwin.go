package xray

import (
	"syscall"

	"github.com/xtls/xray-core/proxy/tun"
	"github.com/xtls/xray-core/transport/internet"
)

// probeSocketBinder is supplied by the patched dependency. Detecting this API
// keeps stock builds working without coupling runtime binding to Go build tags.
type probeSocketBinder interface {
	BindSocket(string, string, syscall.RawConn) error
}

// Helpers have no TUN inbound and therefore install their own binding controller.
func configureProbeInterface() error {
	binder, ok := any(new(tun.InterfaceUpdater)).(probeSocketBinder)
	if !ok {
		return nil
	} // stock Xray; patched builds are checked by the tests
	return internet.RegisterDialerController(binder.BindSocket)
}
