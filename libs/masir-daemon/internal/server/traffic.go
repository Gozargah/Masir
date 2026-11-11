package server

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/store"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/xray"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

const (
	defaultTrafficInterval = time.Second
	minTrafficInterval     = 250 * time.Millisecond
)

func (s *Server) WatchTraffic(req *pb.WatchTrafficRequest, stream pb.DaemonService_WatchTrafficServer) error {
	every := time.Duration(req.IntervalMs) * time.Millisecond
	if every == 0 {
		every = defaultTrafficInterval
	}
	every = max(every, minTrafficInterval)

	tick := time.NewTicker(every)
	defer tick.Stop()
	var (
		prev     TrafficReading
		prevTime time.Time
	)
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-s.stopped:
			return nil
		case now := <-tick.C:
			s.collectTraffic(stream.Context())
			cur, ok, err := s.xray.Traffic(stream.Context())
			if err != nil || !ok { // Xray is down or restarting: start counting again once it is back
				prev, prevTime = TrafficReading{}, time.Time{}
				continue
			}
			r := TrafficReading{Up: cur.Up, Down: cur.Down, Valid: true}
			if res := rate(prev, prevTime, r, now); res != nil {
				if err := stream.Send(res); err != nil {
					return err
				}
			}
			prev, prevTime = r, now
		}
	}
}

// collectTraffic queries outbound stats from Xray and accumulates them into
// per-outbound and per-server counters that survive Xray restarts.
func (s *Server) collectTraffic(ctx context.Context) {
	// Do not hold mu while waiting for another query: status also needs mu.
	for {
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if s.collectMu.TryLock() {
			break
		}
		s.mu.Unlock()
		s.collectMu.Lock()
		s.collectMu.Unlock()
	}
	activeSub, activeCfg, tagToServer := s.trafficAttributionLocked()
	s.mu.Unlock()
	defer s.collectMu.Unlock()

	s.collectTrafficReading(ctx, activeSub, activeCfg, tagToServer)
}

// collectTrafficLocked is called when s.mu is already held (e.g. during applyLocked).
func (s *Server) collectTrafficLocked(ctx context.Context) {
	s.collectMu.Lock()
	defer s.collectMu.Unlock()
	activeSub, activeCfg, tagToServer := s.trafficAttributionLocked()
	s.collectTrafficReading(ctx, activeSub, activeCfg, tagToServer)
}

// trafficAttributionLocked snapshots the selection and rules under s.mu.
func (s *Server) trafficAttributionLocked() (activeSub, activeCfg string, tagToServer map[string]string) {
	if s.subs != nil {
		if a, err := s.subs.Active(); err == nil {
			activeSub, activeCfg = a.SubscriptionID, a.ConfigID
		}
	}
	tagToServer = make(map[string]string)
	for _, rr := range s.resolved {
		if rr.Rule.Target.Kind == store.TargetConfig && rr.OutboundTag != "" {
			tagToServer[rr.OutboundTag] = rr.Rule.Target.SubscriptionID + "/" + rr.Rule.Target.ConfigID
		}
	}

	return activeSub, activeCfg, tagToServer
}

// collectTrafficReading is called with collectMu held, so readings are applied in order.
func (s *Server) collectTrafficReading(ctx context.Context, activeSub, activeCfg string, tagToServer map[string]string) {
	if ctx.Err() != nil {
		return
	}
	raw, gen, ok, err := s.xray.OutboundTraffic(ctx)
	if err != nil {
		return
	}
	s.updateTraffic(raw, gen, ok, activeSub, activeCfg, tagToServer)
}

func (s *Server) updateTraffic(raw map[string]xray.Traffic, gen uint64, ok bool, activeSub, activeCfg string, tagToServer map[string]string) {
	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()

	if !ok {
		// Xray is not running; reset last reading map so new start counts from zero.
		s.lastXrayOutbound = map[string]xray.Traffic{}
		return
	}

	isNewInstance := gen != s.lastXrayGen
	s.lastXrayGen = gen
	if isNewInstance {
		s.lastXrayOutbound = map[string]xray.Traffic{}
	}

	for tag, cur := range raw {
		prev := s.lastXrayOutbound[tag]
		var deltaUp, deltaDown uint64
		if cur.Up >= prev.Up {
			deltaUp = cur.Up - prev.Up
		} else {
			deltaUp = cur.Up // Xray restarted and reset its internal counters
		}
		if cur.Down >= prev.Down {
			deltaDown = cur.Down - prev.Down
		} else {
			deltaDown = cur.Down
		}

		out := s.cumulativeOutbound[tag]
		out.Up += deltaUp
		out.Down += deltaDown
		s.cumulativeOutbound[tag] = out

		// Attribute to server if this is the active proxy or a pinned server rule
		var serverKey string
		if tag == "proxy" {
			if activeSub != "" && activeCfg != "" {
				serverKey = activeSub + "/" + activeCfg
			}
		} else if srv, exists := tagToServer[tag]; exists {
			serverKey = srv
		}

		if serverKey != "" {
			srv := s.cumulativeServer[serverKey]
			srv.Up += deltaUp
			srv.Down += deltaDown
			s.cumulativeServer[serverKey] = srv
		}

		s.lastXrayOutbound[tag] = cur
	}
}

func (s *Server) serverTraffic(subID, configID string) xray.Traffic {
	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()
	return s.cumulativeServer[subID+"/"+configID]
}

// GetOutboundTraffic returns cumulative traffic bytes per outbound tag and per server.
func (s *Server) GetOutboundTraffic(ctx context.Context, _ *pb.GetOutboundTrafficRequest) (*pb.GetOutboundTrafficResponse, error) {
	s.collectTraffic(ctx)

	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()

	outbounds := make([]*pb.OutboundTraffic, 0, len(s.cumulativeOutbound))
	for tag, tr := range s.cumulativeOutbound {
		outbounds = append(outbounds, &pb.OutboundTraffic{
			Tag:           tag,
			UplinkBytes:   tr.Up,
			DownlinkBytes: tr.Down,
		})
	}
	sort.Slice(outbounds, func(i, j int) bool { return outbounds[i].Tag < outbounds[j].Tag })

	servers := make([]*pb.ServerTraffic, 0, len(s.cumulativeServer))
	for key, tr := range s.cumulativeServer {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) == 2 {
			servers = append(servers, &pb.ServerTraffic{
				SubscriptionId: parts[0],
				ConfigId:       parts[1],
				UploadBytes:    tr.Up,
				DownloadBytes:  tr.Down,
			})
		}
	}
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].SubscriptionId == servers[j].SubscriptionId {
			return servers[i].ConfigId < servers[j].ConfigId
		}
		return servers[i].SubscriptionId < servers[j].SubscriptionId
	})

	return &pb.GetOutboundTrafficResponse{
		Outbounds: outbounds,
		Servers:   servers,
	}, nil
}

// TrafficReading is one read of the counters.
type TrafficReading struct {
	Up, Down uint64
	Valid    bool
}

// rate is the speed between two readings, or nil without an earlier one. A counter that
// went down means Xray restarted in between, so that interval counts from zero.
func rate(prev TrafficReading, prevTime time.Time, cur TrafficReading, now time.Time) *pb.WatchTrafficResponse {
	if !prev.Valid {
		return nil
	}
	secs := now.Sub(prevTime).Seconds()
	if secs <= 0 {
		return nil
	}
	delta := func(before, after uint64) uint64 {
		if after < before {
			before = 0
		}
		return uint64(float64(after-before) / secs)
	}
	return &pb.WatchTrafficResponse{
		UplinkBps:     delta(prev.Up, cur.Up),
		DownlinkBps:   delta(prev.Down, cur.Down),
		UplinkTotal:   cur.Up,
		DownlinkTotal: cur.Down,
	}
}
