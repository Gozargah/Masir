package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gozargah/Masir/libs/masir-core/paths"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/compose"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/store"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/subs"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/userfs"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/xray"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// change edits a copy of the state, applies it, and saves it only once it runs.
func (s *Server) change(ctx context.Context, msg string, fn func(*store.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.state.Clone()
	if err := fn(&next); err != nil {
		return toStatus(err)
	}
	if err := s.applyLocked(ctx, next, true); err != nil {
		return err
	}
	if err := store.Save(s.dir, next); err != nil {
		_ = s.applyLocked(context.WithoutCancel(ctx), s.state, false) // keep what runs and what's saved in agreement
		return status.Error(codes.Internal, "save state: "+err.Error())
	}
	s.state = next
	if msg != "" {
		s.Log("info", msg)
	}
	return nil
}

// applyLocked composes st with the current selection and brings Xray to it:
// running when power or always-on proxy is on (and the daemon isn't idle), stopped
// otherwise. Expects s.mu held.
//
// attended says a caller is waiting on the result and can be asked what to do about rules
// that can't be built (see gate); background applies pass false.
func (s *Server) applyLocked(ctx context.Context, st store.State, attended bool) error {
	return s.applyLockedWithCollection(ctx, st, attended, true)
}

// Selection changes collect before switching the active config.
func (s *Server) applyLockedWithCollection(ctx context.Context, st store.State, attended, collect bool) error {
	active, err := s.subs.Active()
	if err != nil {
		return status.Error(codes.Internal, "read selection: "+err.Error())
	}
	runs := st.Settings.XrayRuns() && !s.idle.Load()
	tun := runs && st.Settings.Power && st.Settings.Tun && s.id.Privileged
	skip, err := s.gate(st, len(active.Outbound) > 0, runs, attended)
	if err != nil {
		return err
	}
	in := compose.Input{
		Skip:    skip,
		State:   st,
		Proxy:   runs,
		Tun:     tun,
		LogDir:  s.logDir(),
		Active:  active.Outbound,
		Resolve: s.resolveTarget,
	}
	if tun {
		in.TunName = s.tunName()
	}
	out, err := compose.Build(in)
	if err != nil {
		return status.Error(codes.Internal, "compose: "+err.Error())
	}
	if s.id.Privileged {
		// Root runs configs it didn't write (a subscription's servers, custom inbounds):
		// none may point Xray at a file.
		if err := xray.CheckNoLocalFiles(out.Config); err != nil {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
	}
	p, err := xray.ParseProfile(out.Config)
	if err != nil {
		return status.Error(codes.Internal, "compose: "+err.Error())
	}

	if collect {
		s.collectTrafficLocked(ctx)
	}

	wasRunning := s.xray.Running()
	if runs && !wasRunning {
		// Xray reports a taken port only after its API feature has started, and a Close
		// right then races xray-core's commander. Checking ports first turns that crash
		// into the error the person would have seen.
		if err := probePorts(st); err != nil {
			mode := "proxy"
			if tun {
				mode = "tun"
			}
			s.trackErrorLocked(st, "xray_apply_failed", err, map[string]any{"mode": mode}, true)
			return status.Error(codes.Internal, err.Error())
		}
	}
	if runs {
		if err := s.xray.Apply(ctx, p); err != nil {
			mode := "proxy"
			if tun {
				mode = "tun"
			}
			s.trackErrorLocked(st, "xray_apply_failed", err, map[string]any{"mode": mode}, true)
			return status.Error(codes.Internal, err.Error())
		}
	} else if err := s.xray.Close(); err != nil {
		s.trackErrorLocked(st, "xray_apply_failed", err, map[string]any{"mode": "off"}, true)
		return status.Error(codes.Internal, err.Error())
	}
	s.resolved = out.Rules
	s.running = running{proxy: runs, tun: tun, tunName: in.TunName}
	if !wasRunning && runs {
		mode := "proxy"
		if tun {
			mode = "tun"
		}
		s.trackLocked(st, "xray_started", map[string]any{"mode": mode})
	} else if wasRunning && !runs {
		s.trackLocked(st, "xray_stopped", map[string]any{"reason": "state"})
	}
	if runs {
		host := "127.0.0.1"
		if st.Settings.AllowLAN {
			host = "0.0.0.0"
		}
		s.running.proxyAddr = net.JoinHostPort(host, strconv.Itoa(st.Settings.ProxyPort))
	}
	defer s.changes.notify()

	// xray.json is output: what runs (or would), for people to read. Never read back.
	if raw, err := p.Marshal(); err == nil {
		s.config = raw
		if err := userfs.Write(filepath.Join(s.dir, "xray.json"), raw); err != nil {
			s.Log("warn", "write xray.json: "+err.Error())
		}
	}
	return nil
}

// tunName keeps the interface going while it runs. Xray can't rename a TUN through its
// API, so picking a new number would mean tearing the interface and its routes down and
// back up on every apply, dropping every connection that goes through it.
func (s *Server) tunName() string {
	if s.running.tun && s.running.tunName != "" {
		return s.running.tunName
	}
	return compose.TunName()
}

// reconcileLocked applies the saved state when no caller is waiting on the result (boot,
// subscription refreshes, retries). A failure is kept in lastErr for the status and the
// retry loop, since nothing else would report it. Expects s.mu held.
func (s *Server) reconcileLocked(ctx context.Context) error {
	err := s.applyLocked(ctx, s.state, false)
	prev := s.lastErr
	s.lastErr = ""
	if err != nil {
		s.lastErr = status.Convert(err).Message()
		if s.lastErr != prev {
			s.Log("error", "apply config: "+s.lastErr)
		}
	} else if prev != "" {
		s.Log("info", "config applied after earlier failure")
	}
	if s.lastErr != prev {
		s.changes.notify()
	}
	return err
}

// subscriptionsChanged rebuilds after a refresh or removal: pinned configs may have
// moved, fallen back to proxy, or the selection may have followed a rotated config.
func (s *Server) subscriptionsChanged(ctx context.Context) {
	s.mu.Lock()
	_ = s.reconcileLocked(ctx)
	s.mu.Unlock()
	s.refreshSubscriptionCounts()
}

func (s *Server) resolveTarget(t store.Target) (subs.Config, bool) {
	return s.subs.Find(t.SubscriptionID, t.ConfigID, t.Name)
}

func toStatus(err error) error {
	if _, ok := status.FromError(err); ok {
		return err // already a gRPC status
	}
	switch {
	case errors.Is(err, store.ErrInvalid), errors.Is(err, subs.ErrInvalid), errors.Is(err, xray.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, subs.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrExists), errors.Is(err, subs.ErrExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, store.ErrInUse), errors.Is(err, xray.ErrNotRunning):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, subs.ErrFetch):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// logDir is where Xray writes its error log. A root daemon keeps it where only root can
// write: Xray opens the file by path, and in the user's directory a link could point it
// at any file. Without a safe place the log stays on stdout.
func (s *Server) logDir() string {
	if !s.id.Privileged {
		return s.dir
	}
	dir := paths.PrivilegedLogDir(s.id.UID)
	if dir == "" { // Windows keeps it with the daemon's other files
		return s.dir
	}
	// The run directory is shared with the socket, which the user must reach: 0755.
	if paths.EnsureRootDir(paths.PrivilegedRunDir, 0, 0o755) != nil || paths.EnsureRootDir(dir, 0, 0o700) != nil {
		log.Printf("xray's error log is off: %s isn't a directory only root controls", dir)
		return ""
	}
	return dir
}

// probePorts fails when a port Xray will bind is already taken, before it starts. A bind
// failure after Xray's API feature has started makes xray-core's commander race its own
// Close (it reads its gRPC server from a goroutine while Close nils it), which is a
// nil-pointer crash in a goroutine the daemon can't recover. See the call in
// applyLockedWithCollection.
//
// Every TCP probe listener stays open until the end, so conflicting TCP inbounds (or an
// inbound on the proxy's port) are caught here too.
func probePorts(st store.State) error {
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
