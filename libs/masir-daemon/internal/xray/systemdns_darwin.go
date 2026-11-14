package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/systemdns"
)

// EnableSystemDNS is called only by the privileged daemon, after it owns its
// socket. Tests and temporary latency-test instances never alter system DNS.
func (c *Core) EnableSystemDNS() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := systemdns.New()
	if err != nil {
		return err
	}
	if err := m.Recover(); err != nil {
		return err
	}
	c.systemDNS = &macDNS{manager: m}
	return nil
}

type macDNS struct {
	manager interface {
		Start(context.Context, string, []string) error
		Stop() error
	}
	active string
}

func tunDNS(p *Profile) (raw string, name string, servers []string, err error) {
	for _, in := range p.inbounds {
		if in.Tag != "tun-in" {
			continue
		}
		var config struct {
			Protocol string `json:"protocol"`
			Settings struct {
				Name string   `json:"name"`
				DNS  []string `json:"dns"`
			} `json:"settings"`
		}
		if err := json.Unmarshal(in.JSON, &config); err != nil {
			return "", "", nil, err
		}
		if config.Protocol == "tun" {
			return string(in.JSON), config.Settings.Name, config.Settings.DNS, nil
		}
	}
	return "", "", nil, nil
}

func (m *macDNS) before(p *Profile) error {
	raw, _, _, err := tunDNS(p)
	if err != nil {
		return err
	}
	if m.active != "" && raw != m.active {
		return m.close() // restore before removing or replacing the inbound
	}
	return nil
}

func (m *macDNS) after(ctx context.Context, p *Profile) error {
	raw, name, servers, err := tunDNS(p)
	if err != nil || raw == "" || raw == m.active {
		return err
	}
	if err := m.manager.Start(ctx, name, servers); err != nil {
		return fmt.Errorf("configure macOS TUN DNS: %w", err)
	}
	m.active = raw
	return nil
}

func (m *macDNS) close() error {
	m.active = ""
	err := m.manager.Stop()
	if err != nil {
		log.Printf("system DNS: %v (recovery journal retained)", err)
	}
	return err
}
