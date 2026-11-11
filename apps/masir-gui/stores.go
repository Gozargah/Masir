package main

import (
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

type subscriptionStore struct {
	subscriptions []*pb.Subscription
	servers       []server
}

type ruleStore struct {
	rules []*pb.RoutingRule
}

type updateStore struct {
	state updateState
}
