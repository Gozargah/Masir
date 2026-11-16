// Package ports answers whether the TCP ports Xray would bind are still taken.
//
// A daemon must know this before it starts Xray: a bind failure after Xray's API feature
// has started makes xray-core's commander race its own Close (it reads its gRPC server
// from a goroutine while Close nils it), which is a nil-pointer crash in a goroutine the
// daemon can't recover. The app asks the same question from the other side, while a
// service hands over to another daemon, so the addresses are worked out in one place:
// a port the new daemon will bind is a port the old one has to have let go of.
package ports

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/store"
)

// Busy binds every TCP port the state asks for and closes them again, failing on the
// first one that is taken. The error names the port the way Xray would ("proxy: listen
// tcp 0.0.0.0:7890: bind: address already in use"), so a person reading it can act.
//
// Every probe listener stays open until the end, so conflicting TCP inbounds (or an
// inbound on the proxy's port) are caught too.
func Busy(st store.State) error {
	var listeners []net.Listener
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	probe := func(what, host string, port int) error {
		l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		listeners = append(listeners, l)
		return nil
	}

	if port := st.Settings.ProxyPort; port != 0 {
		host := "127.0.0.1"
		if st.Settings.AllowLAN {
			host = "0.0.0.0"
		}
		if err := probe("proxy", host, port); err != nil {
			return err
		}
	}

	for _, inbound := range st.Inbounds {
		var config struct {
			Protocol string          `json:"protocol"`
			Listen   string          `json:"listen"`
			Port     json.RawMessage `json:"port"`
		}
		if err := json.Unmarshal(inbound.Config, &config); err != nil {
			continue // Xray accepted the config; a shape we don't parse must not block it
		}
		if config.Protocol == "wireguard" {
			continue // WireGuard opens UDP itself; it has no TCP listener to probe.
		}
		port, ok := singlePort(config.Port)
		if !ok {
			continue // no single TCP port: a range, a list, a unix socket or port 0
		}
		if strings.HasPrefix(config.Listen, "/") || strings.HasPrefix(config.Listen, "@") {
			continue // a unix socket, not a TCP address
		}
		host := config.Listen
		if host == "" {
			host = "0.0.0.0"
		}
		if err := probe("inbound "+inbound.Tag, host, port); err != nil {
			return err
		}
	}
	return nil
}

// singlePort reads Xray's port field, which may be a number or a quoted number. Ranges
// and lists are left to Xray: probing every port in them is not what a startup check is
// for.
func singlePort(raw json.RawMessage) (int, bool) {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" || strings.ContainsAny(text, "-,") {
		return 0, false
	}
	port, err := strconv.Atoi(text)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

// WaitFree waits until Busy reports nothing, or timeout passes. A stop that answers a
// supervisor is not the same as a process that has closed its listeners: launchd's
// bootout and systemd's stop return while the old daemon is still tearing Xray down, so
// whoever starts the replacement has to wait for the ports, not for the job to vanish.
func WaitFree(st store.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := Busy(st)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}
