package main

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type refreshDeadlineDaemon struct {
	pb.DaemonServiceClient
	contexts []context.Context
}

func (d *refreshDeadlineDaemon) ListSubscriptions(ctx context.Context, _ *pb.ListSubscriptionsRequest, _ ...grpc.CallOption) (*pb.ListSubscriptionsResponse, error) {
	d.contexts = append(d.contexts, ctx)
	return &pb.ListSubscriptionsResponse{Subscriptions: []*pb.Subscription{{Id: "one"}, {Id: "two"}}}, nil
}

func (d *refreshDeadlineDaemon) GetSubscription(ctx context.Context, _ *pb.GetSubscriptionRequest, _ ...grpc.CallOption) (*pb.GetSubscriptionResponse, error) {
	d.contexts = append(d.contexts, ctx)
	return &pb.GetSubscriptionResponse{}, nil
}

func (d *refreshDeadlineDaemon) ListRoutingRules(ctx context.Context, _ *pb.ListRoutingRulesRequest, _ ...grpc.CallOption) (*pb.ListRoutingRulesResponse, error) {
	d.contexts = append(d.contexts, ctx)
	return &pb.ListRoutingRulesResponse{}, nil
}

func TestRefreshListsSharesDeadline(t *testing.T) {
	d := &refreshDeadlineDaemon{}
	a := newApp(d, "")
	a.async = func(fn func()) { fn() }
	start := time.Now()
	a.refreshLists()
	if len(d.contexts) != 2 {
		t.Fatalf("got %d calls, want 2", len(d.contexts))
	}
	deadline, ok := d.contexts[0].Deadline()
	if !ok || deadline.Before(start.Add(rpcTimeout)) || deadline.After(time.Now().Add(rpcTimeout)) {
		t.Fatalf("unexpected refresh deadline %v", deadline)
	}
	for _, ctx := range d.contexts[1:] {
		if ctx != d.contexts[0] {
			t.Fatal("refresh calls did not share a context")
		}
	}
}

func TestCallRPCRetryAndDeadline(t *testing.T) {
	calls := 0
	var contexts []context.Context
	result, err := callRPC(func(ctx context.Context) (int, error) {
		calls++
		contexts = append(contexts, ctx)
		if _, ok := ctx.Deadline(); !ok {
			t.Error("attempt has no deadline")
		}
		if calls < rpcAttempts {
			return 0, status.Error(codes.Unavailable, "connection error")
		}
		return 42, nil
	})
	if err != nil || result != 42 || calls != rpcAttempts {
		t.Fatalf("result=%d err=%v calls=%d", result, err, calls)
	}
	if contexts[0] == contexts[1] || contexts[1] == contexts[2] {
		t.Fatal("attempts did not receive fresh contexts")
	}

	calls = 0
	_, err = callRPC(func(context.Context) (int, error) {
		calls++
		return 0, status.Error(codes.Unknown, "release lookup failed")
	})
	if status.Code(err) != codes.Unknown || calls != 1 {
		t.Fatalf("release error: err=%v calls=%d", err, calls)
	}
}

func TestCallRPCContextStopsRetryOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := callRPCContext(ctx, func(context.Context) (int, error) {
		calls++
		cancel()
		return 0, status.Error(codes.Unavailable, "connection error")
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
