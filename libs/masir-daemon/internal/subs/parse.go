package subs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/xray"
	"github.com/xtls/libxray/share"
)

// Config is one server a subscription provides.
type Config struct {
	// SourceRef identifies the shared document in Subscription.Sources on disk.
	SourceRef string `json:"sourceRef,omitempty"`
	// SourceJSON retains the complete imported entry before runtime normalization.
	// Share links retain their converted outbound JSON. Older saved configs omit it.
	SourceJSON json.RawMessage  `json:"sourceJson,omitempty"`
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Protocol   string           `json:"protocol"`
	Address    string           `json:"address,omitempty"`
	Port       int              `json:"port,omitempty"`
	Network    string           `json:"network,omitempty"`
	Security   string           `json:"security,omitempty"`
	Outbound   json.RawMessage  `json:"outbound"` // untagged; whoever uses it picks the tag
	Profile    *OutboundProfile `json:"profile,omitempty"`
	Latency    *Latency         `json:"latency,omitempty"`
}

// OutboundProfile preserves a server's dial dependencies or the provider's routing graph.
type OutboundProfile struct {
	Base             map[string]json.RawMessage `json:"base,omitempty"`
	Outbounds        []json.RawMessage          `json:"outbounds"`
	Routing          json.RawMessage            `json:"routing"`
	Observatory      json.RawMessage            `json:"observatory,omitempty"`
	BurstObservatory json.RawMessage            `json:"burstObservatory,omitempty"`
	Ingress          []ProfileIngress           `json:"ingress,omitempty"`
	DefaultBalancer  string                     `json:"defaultBalancer,omitempty"`
}

// ProfileIngress describes a provider rule's inbound context without listener settings.
type ProfileIngress struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

var errNoConfigs = errors.New("subscription has no usable configs")

// utility outbounds that ride along in full configs but aren't servers.
var utilityProtocols = map[string]bool{"freedom": true, "blackhole": true, "dns": true, "loopback": true}

// Parse accepts xray JSON (an outbound, a list of outbounds, a full config, or a list of
// full configs) or share links (vless://, vmess://, trojan://, ss://, hysteria2://...,
// plain or base64). Configs Xray can't build are dropped.
func Parse(body []byte) ([]Config, error) {
	text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	var named []namedOutbound
	var err error
	switch {
	case strings.HasPrefix(text, "["):
		named, err = parseJSONArray(text)
	case strings.HasPrefix(text, "{"):
		named, err = parseJSONObject(json.RawMessage(text))
	default:
		named, err = parseShareLinks(text)
	}
	if err != nil {
		return nil, err
	}

	out := make([]Config, 0, len(named))
	seen := map[string]bool{}
	var validationErr error
	for _, n := range named {
		c, err := newConfig(n)
		if err != nil {
			if validationErr == nil {
				validationErr = err
			}
			continue
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		if validationErr != nil {
			return nil, fmt.Errorf("%w: %v", errNoConfigs, validationErr)
		}
		return nil, errNoConfigs
	}
	return out, nil
}

type namedOutbound struct {
	source    json.RawMessage
	sourceRef string
	name      string
	raw       map[string]json.RawMessage
	profile   *OutboundProfile
}

func parseJSONArray(text string) ([]namedOutbound, error) {
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, fmt.Errorf("subscription JSON: %w", err)
	}
	var out []namedOutbound
	for _, it := range items {
		n, err := parseJSONObject(it)
		if err != nil {
			continue // one odd entry shouldn't sink the rest
		}
		out = append(out, n...)
	}
	return out, nil
}

// parseJSONObject handles a single outbound, a full config (one server, named by
// "remarks"), or a bare {"outbounds": [...]} list where every server counts.
func parseJSONObject(raw json.RawMessage) ([]namedOutbound, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("subscription JSON: %w", err)
	}
	if err := normalizeRuntimeKeys(obj); err != nil {
		return nil, err
	}
	if _, ok := obj["protocol"]; ok {
		profile, err := dialerProfile(obj, []map[string]json.RawMessage{obj})
		return []namedOutbound{{raw: obj, profile: profile, source: append(json.RawMessage(nil), raw...)}}, err
	}
	outsRaw, ok := obj["outbounds"]
	if !ok {
		return nil, errors.New("subscription JSON has no outbounds")
	}
	var outs []map[string]json.RawMessage
	if err := json.Unmarshal(outsRaw, &outs); err != nil {
		return nil, fmt.Errorf("subscription JSON outbounds: %w", err)
	}

	// Runtime settings make this a full config. Provider-only metadata still
	// accompanies an outbound list without changing how servers are selected.
	fullConfig := false
	for key := range obj {
		switch strings.ToLower(key) {
		case "remarks", "inbounds", "routing", "log", "dns", "policy", "api", "metrics", "stats", "reverse", "fakedns", "observatory", "burstobservatory", "transport", "env", "version", "geodata":
			fullConfig = true
		}
	}
	if !fullConfig {
		var entries []json.RawMessage
		if err := json.Unmarshal(outsRaw, &entries); err != nil {
			return nil, err
		}
		var list []namedOutbound
		for i, o := range outs {
			if !utilityProtocols[str(o["protocol"])] {
				profile, err := dialerProfile(o, outs)
				if err != nil {
					continue
				}
				source := entries[i]
				if profile != nil || len(obj) > 1 {
					source = raw
				}
				list = append(list, namedOutbound{raw: o, profile: profile, source: source})
			}
		}
		return list, nil
	}

	main := pickMainOutbound(outs)
	if main == nil {
		return nil, errors.New("full config has no outbound")
	}
	profile, err := parseOutboundProfile(obj, outsRaw)
	if err != nil {
		return nil, err
	}
	return []namedOutbound{{name: str(obj["remarks"]), raw: main, profile: profile, source: append(json.RawMessage(nil), raw...)}}, nil
}

// pickMainOutbound prefers the outbound tagged "proxy", else the first real server.
func pickMainOutbound(outs []map[string]json.RawMessage) map[string]json.RawMessage {
	for _, o := range outs {
		if str(o["tag"]) == "proxy" {
			return o
		}
	}
	for _, o := range outs {
		if !utilityProtocols[str(o["protocol"])] {
			return o
		}
	}
	if len(outs) > 0 {
		return outs[0]
	}
	return nil
}

// parseShareLinks handles v2rayN-style vmess here and hands every other link to libXray.
func parseShareLinks(text string) ([]namedOutbound, error) {
	if !strings.Contains(text, "://") {
		decoded, err := decodeBase64(text)
		if err != nil || !strings.Contains(decoded, "://") {
			return nil, errors.New("unrecognized subscription format")
		}
		text = decoded
	}

	var out []namedOutbound
	var rest []string
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, ok, err := parseVMessQR(line)
		switch {
		case !ok:
			rest = append(rest, line)
		case err == nil:
			out = append(out, n)
		}
	}
	if len(rest) == 0 {
		return out, nil
	}

	raw, err := share.ConvertShareLinksToXrayJson(strings.Join(rest, "\n"), "")
	if err != nil {
		if len(out) > 0 {
			return out, nil // libXray errors only when none of its lines were usable
		}
		return nil, fmt.Errorf("unrecognized subscription format: %w", err)
	}
	var doc struct {
		Outbounds []map[string]json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	for _, o := range doc.Outbounds {
		out = append(out, namedOutbound{raw: o}) // libXray puts the link's #remark in tag
	}
	return out, nil
}

func newConfig(n namedOutbound) (Config, error) {
	o := n.raw
	source := n.source
	if len(source) == 0 {
		source, _ = json.Marshal(o)
	}
	name := n.name
	if name == "" {
		name = str(o["tag"])
	}
	delete(o, "tag")
	stripDialerProxy(o)

	outbound, err := json.Marshal(o)
	if err != nil {
		return Config{}, err
	}
	if err := xray.ValidateOutbound(outbound); err != nil {
		return Config{}, err
	}

	c := Config{
		SourceJSON: source,
		SourceRef:  n.sourceRef,
		ID:         configID(outbound),
		Name:       strings.TrimSpace(name),
		Protocol:   str(o["protocol"]),
		Outbound:   outbound,
		Profile:    n.profile,
	}
	if c.Profile != nil {
		encoded, err := json.Marshal(c.Profile)
		if err != nil {
			return Config{}, err
		}
		c.ID = configID(encoded)
	}
	c.Address, c.Port = serverAddress(o["settings"])
	var stream struct {
		Network  string `json:"network"`
		Security string `json:"security"`
	}
	_ = json.Unmarshal(o["streamSettings"], &stream)
	c.Network, c.Security = stream.Network, stream.Security
	if c.Network == "" || c.Network == "raw" { // xray renamed tcp to raw; users know it as tcp
		c.Network = "tcp"
	}
	if c.Security == "" {
		c.Security = "none"
	}
	if c.Name == "" {
		c.Name = fmt.Sprintf("%s %s:%d", c.Protocol, c.Address, c.Port)
	}
	return c, nil
}

func stripDialerProxy(o map[string]json.RawMessage) {
	var stream map[string]json.RawMessage
	if json.Unmarshal(o["streamSettings"], &stream) != nil {
		return
	}
	var sockopt map[string]json.RawMessage
	if json.Unmarshal(stream["sockopt"], &sockopt) != nil {
		return
	}
	if _, ok := sockopt["dialerProxy"]; !ok {
		return
	}
	delete(sockopt, "dialerProxy")
	stream["sockopt"], _ = json.Marshal(sockopt)
	o["streamSettings"], _ = json.Marshal(stream)
}

// configID hashes the canonical outbound, so it survives refreshes and renames
// and changes only when the server's settings do.
func configID(outbound json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(outbound))
	dec.UseNumber()
	var v any
	_ = dec.Decode(&v)
	canon, _ := json.Marshal(v) // map keys marshal sorted
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:6])
}

// serverAddress finds the server in the shapes Xray accepts: vnext, servers, or flat.
func serverAddress(settings json.RawMessage) (string, int) {
	var s struct {
		Address any `json:"address"`
		Port    any `json:"port"`
		Vnext   []struct {
			Address any `json:"address"`
			Port    any `json:"port"`
		} `json:"vnext"`
		Servers []struct {
			Address any `json:"address"`
			Port    any `json:"port"`
		} `json:"servers"`
	}
	if json.Unmarshal(settings, &s) != nil {
		return "", 0
	}
	switch {
	case len(s.Vnext) > 0:
		return anyString(s.Vnext[0].Address), anyInt(s.Vnext[0].Port)
	case len(s.Servers) > 0:
		return anyString(s.Servers[0].Address), anyInt(s.Servers[0].Port)
	default:
		return anyString(s.Address), anyInt(s.Port)
	}
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

func anyInt(v any) int {
	switch p := v.(type) {
	case float64:
		return int(p)
	case string:
		n, _ := strconv.Atoi(p)
		return n
	}
	return 0
}

func sourceID(raw json.RawMessage) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

// Normalize only recognized config fields. Provider metadata and dictionary
// keys are not Xray struct fields and retain their original spelling.
func normalizeRuntimeKeys(object map[string]json.RawMessage) error {
	fields := []string{"protocol", "remarks", "inbounds", "outbounds", "routing", "log", "dns", "policy", "api", "metrics", "stats", "reverse", "fakedns", "observatory", "burstObservatory", "transport", "env", "version", "geodata"}
	for _, field := range fields {
		var found string
		for key := range object {
			if !strings.EqualFold(key, field) {
				continue
			}
			if found != "" {
				return fmt.Errorf("subscription repeats %s field", field)
			}
			found = key
		}
		if found != "" && found != field {
			object[field] = object[found]
			delete(object, found)
		}
	}
	return nil
}
