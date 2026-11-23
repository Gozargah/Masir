package subs

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/xray"
	"github.com/xtls/xray-core/infra/conf"
)

// parseOutboundProfile preserves a full provider config. Masir owns listeners;
// provider inbounds contribute metadata for translating routing predicates.
func parseOutboundProfile(obj map[string]json.RawMessage, outbounds json.RawMessage) (*OutboundProfile, error) {
	routing := obj["routing"]
	if len(routing) == 0 || string(routing) == "null" {
		routing = json.RawMessage(`{}`)
	}
	var layout conf.RouterConfig
	if err := json.Unmarshal(routing, &layout); err != nil {
		return nil, fmt.Errorf("subscription routing: %w", err)
	}

	profile := &OutboundProfile{
		Routing:          routing,
		Observatory:      obj["observatory"],
		BurstObservatory: obj["burstObservatory"],
	}
	profile.Base = make(map[string]json.RawMessage)
	for key, raw := range obj {
		switch key {
		case "remarks", "inbounds", "outbounds", "routing", "observatory", "burstObservatory":
			continue
		}
		profile.Base[key] = raw
	}
	if err := json.Unmarshal(outbounds, &profile.Outbounds); err != nil {
		return nil, fmt.Errorf("subscription profile outbounds: %w", err)
	}
	if raw := obj["inbounds"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &profile.Ingress); err != nil {
			return nil, fmt.Errorf("subscription profile ingress: %w", err)
		}
	}
	if err := validateProfileRouting(profile, &layout); err != nil {
		return nil, err
	}
	profile.DefaultBalancer = defaultBalancer(layout.RuleList, profile.Ingress)
	return profile, nil
}

func validateProfileRouting(profile *OutboundProfile, layout *conf.RouterConfig) error {
	if layout.Script != "" {
		return fmt.Errorf("subscription profile uses a routing script")
	}
	tags := make(map[string]bool, len(profile.Outbounds))
	outbounds := make([]map[string]json.RawMessage, len(profile.Outbounds))
	for i, raw := range profile.Outbounds {
		if err := json.Unmarshal(raw, &outbounds[i]); err != nil {
			return fmt.Errorf("invalid subscription profile outbound %d", i+1)
		}
	}
	for i, raw := range profile.Outbounds {
		if _, err := dialerProfile(outbounds[i], outbounds); err != nil {
			return err
		}
		var outbound struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(raw, &outbound); err != nil {
			return fmt.Errorf("invalid subscription profile outbound %d", i+1)
		}
		if outbound.Tag != "" {
			if tags[outbound.Tag] {
				return fmt.Errorf("subscription profile outbound %d repeats a tag", i+1)
			}
			tags[outbound.Tag] = true
		}
		if err := xray.ValidateOutbound(raw); err != nil {
			return fmt.Errorf("invalid subscription profile outbound %d", i+1)
		}
	}
	balancers := make(map[string]bool, len(layout.Balancers))
	for i, balancer := range layout.Balancers {
		if balancer == nil || balancers[balancer.Tag] {
			return fmt.Errorf("subscription profile balancer %d has a missing or repeated definition", i+1)
		}
		if _, err := balancer.Build(); err != nil {
			return fmt.Errorf("invalid subscription profile balancer %d", i+1)
		}
		if balancer.FallbackTag != "" && !tags[balancer.FallbackTag] {
			return fmt.Errorf("subscription profile balancer %d has an unknown fallback outbound", i+1)
		}
		matches := false
		for tag := range tags {
			for _, prefix := range balancer.Selectors {
				matches = matches || strings.HasPrefix(tag, prefix)
			}
		}
		if !matches {
			return fmt.Errorf("subscription profile balancer %d selects no outbound", i+1)
		}
		balancers[balancer.Tag] = true
	}
	for i, raw := range layout.RuleList {
		if err := xray.ValidateRule(raw); err != nil {
			return fmt.Errorf("invalid subscription profile rule %d", i+1)
		}
		var target struct {
			Outbound string `json:"outboundTag"`
			Balancer string `json:"balancerTag"`
		}
		if err := json.Unmarshal(raw, &target); err != nil {
			return fmt.Errorf("invalid subscription profile rule %d", i+1)
		}
		switch {
		case target.Balancer != "" && !balancers[target.Balancer]:
			return fmt.Errorf("subscription profile rule %d targets an unknown balancer", i+1)
		case target.Balancer == "" && !tags[target.Outbound]:
			return fmt.Errorf("subscription profile rule %d targets an unknown outbound", i+1)
		}
	}
	return nil
}

// defaultBalancer finds the provider's default for ordinary client ingress.
// Destination-specific rules remain in the profile and are evaluated before
// that default. Different client defaults are left for scoped routing.
func defaultBalancer(rules []json.RawMessage, ingress []ProfileIngress) string {
	var aliases []string
	for _, in := range ingress {
		switch strings.ToLower(in.Protocol) {
		case "socks", "http", "mixed", "tun":
			if in.Tag != "" {
				aliases = append(aliases, in.Tag)
			}
		}
	}
	if len(aliases) == 0 {
		aliases = []string{""}
	}
	var selected string
	for _, alias := range aliases {
		for _, network := range []string{"tcp", "udp"} {
			target := defaultRuleBalancer(rules, alias, network)
			if target == "" || (selected != "" && selected != target) {
				return ""
			}
			selected = target
		}
	}
	return selected
}

func defaultRuleBalancer(rules []json.RawMessage, alias, network string) string {
	for _, raw := range rules {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		for field := range fields {
			switch strings.ToLower(field) {
			case "type", "ruletag", "outboundtag", "balancertag", "inboundtag", "network":
				delete(fields, field)
			}
		}
		if len(fields) != 0 {
			continue
		}
		var rule struct {
			Balancer string          `json:"balancerTag"`
			Inbound  conf.StringList `json:"inboundTag"`
			Network  conf.StringList `json:"network"`
		}
		if json.Unmarshal(raw, &rule) != nil ||
			(len(rule.Inbound) != 0 && !slices.Contains(rule.Inbound, alias)) ||
			(len(rule.Network) != 0 && !slices.Contains(rule.Network, network)) {
			continue
		}
		return rule.Balancer
	}
	return ""
}

// BalancerSummary describes the default pool, or all declared pools when
// the profile has no shared default. Count overlapping selectors only once.
func (p *OutboundProfile) BalancerSummary() (string, uint32) {
	if p == nil {
		return "", 0
	}
	var routing conf.RouterConfig
	if json.Unmarshal(p.Routing, &routing) != nil {
		return "", 0
	}
	var selectors []string
	var strategy string
	for _, pool := range routing.Balancers {
		if pool == nil || (p.DefaultBalancer != "" && pool.Tag != p.DefaultBalancer) {
			continue
		}
		selectors = append(selectors, pool.Selectors...)
		kind := pool.Strategy.Type
		if kind == "" {
			kind = "random"
		}
		if strategy == "" {
			strategy = kind
		} else if !strings.EqualFold(strategy, kind) {
			strategy = "mixed"
		}
	}

	var members uint32
	for _, raw := range p.Outbounds {
		var outbound struct {
			Tag string `json:"tag"`
		}
		if json.Unmarshal(raw, &outbound) != nil || outbound.Tag == "" {
			continue
		}
		for _, selector := range selectors {
			if strings.HasPrefix(outbound.Tag, selector) {
				members++
				break
			}
		}
	}
	return strategy, members
}
