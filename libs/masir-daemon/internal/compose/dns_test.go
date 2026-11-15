package compose

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"

	"github.com/miekg/dns"
	"github.com/xtls/xray-core/core"
	xdns "github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/infra/conf/serial"
)

// Exercise the composed policy with real Xray DNS clients. Only the upstream
// transports are replaced, so the test needs neither Internet access nor TLS.
func TestDNSIPv4OnlyAndBootstrapIsolation(t *testing.T) {
	primary := dnsTestServer(t, "192.0.2.1", "no-answer.test.")
	bootstrap := dnsTestServer(t, "192.0.2.2", "")
	st := testState(t)
	out, _ := build(t, Input{State: st, Active: json.RawMessage(activeOut)})
	var raw map[string]any
	if err := json.Unmarshal(out.Config, &raw); err != nil {
		t.Fatal(err)
	}
	for _, item := range raw["dns"].(map[string]any)["servers"].([]any) {
		server := item.(map[string]any)
		addr := primary
		if server["tag"] == "dns-bootstrap" {
			addr = bootstrap
		}
		server["address"] = addr.IP.String()
		server["port"] = addr.Port
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := serial.LoadJSONConfig(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close() })
	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	resolver := inst.GetFeature(xdns.ClientType()).(xdns.Client)
	for _, tc := range []struct {
		domain string
		wantA  string
	}{
		{"ordinary.test", "192.0.2.1"},
		{"active.example.com", "192.0.2.2"},
		{"no-answer.test", ""},
	} {
		t.Run(tc.domain, func(t *testing.T) {
			ips, _, err := resolver.LookupIP(tc.domain, xdns.IPOption{IPv4Enable: true})
			if tc.wantA == "" {
				if len(ips) != 0 || err == nil {
					t.Errorf("ordinary A query fell back to bootstrap: ips=%v, err=%v", ips, err)
				}
			} else if err != nil || len(ips) != 1 || ips[0].String() != tc.wantA {
				t.Errorf("A query: ips=%v, err=%v; want %s", ips, err, tc.wantA)
			}
			ips, _, err = resolver.LookupIP(tc.domain, xdns.IPOption{IPv6Enable: true})
			if len(ips) != 0 || err == nil {
				t.Errorf("AAAA query: ips=%v, err=%v; want no addresses", ips, err)
			}
		})
	}
}

func dnsTestServer(t *testing.T, ipv4, emptyDomain string) *net.UDPAddr {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	server := &dns.Server{
		PacketConn:        pc,
		NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
			r := new(dns.Msg)
			r.SetReply(q)
			for _, question := range q.Question {
				if question.Name == emptyDomain {
					continue
				}
				h := dns.RR_Header{Name: question.Name, Rrtype: question.Qtype, Class: dns.ClassINET, Ttl: 60}
				switch question.Qtype {
				case dns.TypeA:
					r.Answer = append(r.Answer, &dns.A{Hdr: h, A: net.ParseIP(ipv4)})
				case dns.TypeAAAA:
					r.Answer = append(r.Answer, &dns.AAAA{Hdr: h, AAAA: net.ParseIP("2001:db8::1")})
				}
			}
			_ = w.WriteMsg(r)
		}),
	}
	go func() { _ = server.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = server.Shutdown() })
	return pc.LocalAddr().(*net.UDPAddr)
}
