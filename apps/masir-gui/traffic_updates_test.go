package main

import (
	"context"
	"io"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
)

type trafficDaemon struct {
	*fakeDaemon
	stop context.CancelFunc
}

func (f *trafficDaemon) WatchTraffic(ctx context.Context, _ *pb.WatchTrafficRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchTrafficResponse], error) {
	return &trafficStream{stop: f.stop}, nil
}

type trafficStream struct {
	grpc.ClientStream
	at   int
	stop context.CancelFunc
}

func (s *trafficStream) Recv() (*pb.WatchTrafficResponse, error) {
	samples := []uint64{0, 0, 100, 100, 0, 0}
	if s.at == len(samples) {
		s.stop()
		return nil, io.EOF
	}
	v := samples[s.at]
	s.at++
	return &pb.WatchTrafficResponse{DownlinkBps: v}, nil
}
func TestTrafficStreamDoesNotRedrawIdenticalSamples(t *testing.T) {
	f := &trafficDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	// Run the stream synchronously; its final receive cancels this watch.
	a.async = func(fn func()) { f.stop = a.trafficCancel; fn() }
	posts := 0
	a.post = func(fn func()) { posts++; fn() }
	a.watchTraffic()
	if posts != 3 {
		t.Fatalf("posted %d redraws; want initial sample, change and return to zero", posts)
	}
	if a.down != 0 || a.up != 0 {
		t.Fatal("traffic did not return to zero")
	}
}
