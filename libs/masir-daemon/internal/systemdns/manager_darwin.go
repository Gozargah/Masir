// Package systemdns owns macOS network-service DNS overrides while Xray's TUN runs.
// It deliberately does not implement a firewall or change split-DNS resolvers.
package systemdns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

type service struct {
	ID      string
	Servers []string // configured addresses; nil means automatic, never DHCP's effective list
	Managed bool     // enabled physical service in the current network location
}

type backend interface {
	// edit holds the preferences lock through read, mutation, commit and apply.
	edit(func([]service, func(string, []string) error) error) error
	watch(context.Context, chan<- struct{}) (func(), error)
}

type record struct {
	Original  []string `json:"original"`
	Installed []string `json:"installed"`
	Pending   bool     `json:"pending,omitempty"` // commit/apply has not yet succeeded
}

// Manager serializes preferences edits, journals before mutation, and holds a
// machine-wide lock so two users' daemons cannot restore each other's overrides.
type Manager struct {
	mu          sync.Mutex
	b           backend
	dir         string
	records     map[string]record
	ignored     map[string]bool // an external DNS edit wins for this tunnel session
	servers     []string
	unlock      func()
	cancel      context.CancelFunc
	watchEnd    func()
	done        chan struct{}
	lock        func(string) (func(), error)
	checkRoutes func(context.Context, string, []string) error
}

func New() (*Manager, error) {
	b, err := newNative()
	if err != nil {
		return nil, err
	}
	return &Manager{b: b, dir: "/var/db/masir-dns", lock: rootLock, checkRoutes: checkRoutes}, nil
}

// Recover is also called on an idle/proxy-only daemon start. Persistent overrides
// from a crash must not depend on turning the tunnel back on to be restored.
func (m *Manager) Recover() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.acquire(); err != nil {
		return err
	}
	defer m.release()
	return m.restore()
}

func (m *Manager) Start(ctx context.Context, name string, servers []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		if slices.Equal(m.servers, servers) {
			return nil
		}
		return errors.New("system DNS already active with different servers")
	}
	if len(servers) == 0 {
		return errors.New("TUN has no DNS servers")
	}
	if err := m.acquire(); err != nil {
		return err
	}
	if err := m.restore(); err != nil {
		m.release()
		return err
	}
	// Check local routing before taking over the resolver; no upstream DNS probe.
	if err := m.checkRoutes(ctx, name, servers); err != nil {
		m.release()
		return err
	}
	m.servers = slices.Clone(servers)
	m.ignored = map[string]bool{}
	watchCtx, cancel := context.WithCancel(context.Background())
	events := make(chan struct{}, 1)
	end, err := m.b.watch(watchCtx, events)
	if err == nil {
		err = m.reconcile()
	}
	if err != nil {
		cancel()
		if end != nil {
			end()
		}
		err = errors.Join(err, m.restore())
		m.release()
		return err
	}
	m.cancel, m.watchEnd, m.done = cancel, end, make(chan struct{})
	go func() {
		defer close(m.done)
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		defer timer.Stop()
		var retry <-chan time.Time
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-events:
			case <-retry:
			}
			m.mu.Lock()
			if watchCtx.Err() == nil {
				if err := m.reconcile(); err != nil {
					log.Printf("system DNS: network change: %v; retrying", err)
					timer.Reset(2 * time.Second)
					retry = timer.C
				} else {
					timer.Stop()
					retry = nil
				}
			}
			m.mu.Unlock()
		}
	}()
	return nil
}

func (m *Manager) acquire() error {
	if m.unlock != nil {
		return nil
	}
	unlock, err := m.lock(m.dir)
	if err != nil {
		return err
	}
	m.unlock = unlock
	m.records = map[string]record{}
	raw, err := os.ReadFile(filepath.Join(m.dir, "recovery.json"))
	if err == nil {
		err = json.Unmarshal(raw, &m.records)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.release()
		return fmt.Errorf("read DNS recovery journal: %w", err)
	}
	if m.records == nil {
		m.records = map[string]record{}
	}
	return nil
}

func (m *Manager) release() {
	if m.unlock != nil {
		m.unlock()
		m.unlock = nil
	}
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
		end, done := m.watchEnd, m.done
		m.mu.Unlock()
		end()
		<-done
		m.mu.Lock()
		m.cancel, m.watchEnd, m.done = nil, nil, nil
	}
	defer m.mu.Unlock()
	if m.unlock == nil {
		return nil
	}
	err := m.restore()
	m.release()
	return err
}

func (m *Manager) reconcile() error {
	next := map[string]record{}
	err := m.b.edit(func(services []service, set func(string, []string) error) error {
		for _, s := range services {
			r, owned := m.records[s.ID]
			if owned && !slices.Equal(s.Servers, r.Installed) &&
				!(slices.Equal(s.Servers, r.Original) && (r.Pending || !s.Managed)) {
				// Do not fight another VPN, an administrator, or the user. In particular,
				// a later stop must not restore over their replacement configuration.
				m.ignored[s.ID] = true
				log.Printf("system DNS: service %s changed externally; leaving it alone until TUN reconnects", s.ID)
				continue
			}
			if !s.Managed {
				if owned {
					if err := set(s.ID, r.Original); err != nil {
						return err
					}
				}
				continue
			}
			if m.ignored[s.ID] {
				continue
			}
			if !owned {
				r = record{Original: slices.Clone(s.Servers), Installed: slices.Clone(m.servers), Pending: true}
				m.records[s.ID] = r
				if err := m.save(); err != nil { // durable before preferences can change
					return err
				}
			}
			if r.Pending || !slices.Equal(s.Servers, r.Installed) {
				if err := set(s.ID, r.Installed); err != nil {
					return err
				}
			}
			r.Pending = false
			next[s.ID] = r
		}
		return nil
	})
	if err != nil {
		return err // retain recovery records if commit/apply failed
	}
	m.records = next
	return m.save()
}

func (m *Manager) restore() error {
	if len(m.records) == 0 {
		return nil
	}
	if err := m.b.edit(func(services []service, set func(string, []string) error) error {
		for _, s := range services {
			if r, ok := m.records[s.ID]; ok && (slices.Equal(s.Servers, r.Installed) || slices.Equal(s.Servers, r.Original)) {
				// Re-apply even when preferences already show Original: a previous
				// restore may have committed but failed to apply to the running OS.
				if err := set(s.ID, r.Original); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("restore macOS DNS: %w", err)
	}
	m.records = map[string]record{}
	return m.save()
}

func (m *Manager) save() error {
	raw, err := json.Marshal(m.records)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(m.dir, ".dns-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(m.dir, "recovery.json"))
	}
	if err == nil {
		d, e := os.Open(m.dir)
		if e != nil {
			return e
		}
		err = errors.Join(d.Sync(), d.Close())
	}
	return err
}
