package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"

	appdns "github.com/xtls/xray-core/app/dns"
	"github.com/xtls/xray-core/common"
	xnet "github.com/xtls/xray-core/common/net"
	xserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/infra/conf"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type liveDNS struct {
	current atomic.Pointer[appdns.DNS]
}

var registerLiveDNS = sync.OnceFunc(func() {
	common.Must(common.RegisterConfig((*wrapperspb.BytesValue)(nil), func(ctx context.Context, raw interface{}) (interface{}, error) {
		cfg := new(appdns.Config)
		if err := proto.Unmarshal(raw.(*wrapperspb.BytesValue).Value, cfg); err != nil {
			return nil, err
		}
		client, err := appdns.New(ctx, cfg)
		if err != nil {
			return nil, err
		}
		slot := new(liveDNS)
		slot.current.Store(client)
		return slot, nil
	}))
})

func (*liveDNS) Type() interface{}                    { return dns.ClientType() }
func (d *liveDNS) Start() error                       { return d.current.Load().Start() }
func (d *liveDNS) Close() error                       { return d.current.Load().Close() }
func (d *liveDNS) IsOwnLink(ctx context.Context) bool { return d.current.Load().IsOwnLink(ctx) }
func (d *liveDNS) MayUseSystemResolver() bool         { return d.current.Load().MayUseSystemResolver() }
func (d *liveDNS) LookupIP(host string, option dns.IPOption) ([]xnet.IP, uint32, error) {
	return d.current.Load().LookupIP(host, option)
}

func installLiveDNS(cfg *core.Config) {
	registerLiveDNS()
	name := string((&appdns.Config{}).ProtoReflect().Descriptor().FullName())
	for i, app := range cfg.App {
		if app.Type == name {
			cfg.App[i] = xserial.ToTypedMessage(wrapperspb.Bytes(app.Value))
			return
		}
	}
}

func prepareDNS(inst *core.Instance, raw json.RawMessage) (*appdns.DNS, error) {
	var settings conf.DNSConfig
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, err
	}
	cfg, err := settings.Build()
	if err != nil {
		return nil, err
	}
	obj, err := core.CreateObject(inst, cfg)
	if err != nil {
		return nil, err
	}
	client := obj.(*appdns.DNS)
	return client, client.Start()
}

// dnsPolicy excludes only the exact endpoint names Masir generated. All
// resolver addresses and user DNS settings remain in the restart fingerprint.
func dnsPolicy(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(raw, &settings) != nil || settings == nil {
		return raw
	}
	var servers []json.RawMessage
	if json.Unmarshal(settings["servers"], &servers) != nil {
		return raw
	}
	for i, server := range servers {
		var fields map[string]json.RawMessage
		if json.Unmarshal(server, &fields) == nil && string(fields["tag"]) == `"dns-bootstrap"` {
			delete(fields, "domains")
			servers[i] = marshal(fields)
		}
	}
	settings["servers"] = marshal(servers)
	return marshal(settings)
}

func (c *Core) refreshBootstrapDNS(profile *Profile, leaving []item) error {
	raw := profile.base["dns"]
	if len(raw) == 0 {
		return nil
	}
	outbounds := make([]json.RawMessage, 0, len(c.retired)+len(leaving))
	for _, r := range c.retired {
		outbounds = append(outbounds, r.item.JSON)
	}
	for _, o := range leaving {
		outbounds = append(outbounds, o.JSON)
	}
	if domains := BootstrapDomains(outbounds); len(domains) > 0 {
		slices.Sort(domains)
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(raw, &settings); err != nil {
			return err
		}
		var servers []json.RawMessage
		if json.Unmarshal(settings["servers"], &servers) == nil {
			for i, server := range servers {
				var fields map[string]json.RawMessage
				if json.Unmarshal(server, &fields) != nil || string(fields["tag"]) != `"dns-bootstrap"` {
					continue
				}
				var current []string
				if err := json.Unmarshal(fields["domains"], &current); err != nil {
					return err
				}
				for _, domain := range domains {
					if !slices.Contains(current, domain) {
						current = append(current, domain)
					}
				}
				fields["domains"] = marshal(current)
				servers[i] = marshal(fields)
			}
			settings["servers"] = marshal(servers)
			raw = marshal(settings)
		}
	}
	if bytes.Equal(c.liveDNSConfig, raw) {
		return nil
	}
	client, err := prepareDNS(c.inst, raw)
	if err != nil {
		return err
	}
	slot := c.inst.GetFeature(dns.ClientType()).(*liveDNS)
	slot.current.Store(client)
	c.liveDNSConfig = raw
	return nil
}
