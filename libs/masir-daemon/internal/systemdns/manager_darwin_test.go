package systemdns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	mu         sync.Mutex
	services   []service
	events     chan<- struct{}
	failSet    string
	failCommit bool
	failApply  bool
	beforeSet  func()
}

func (b *fakeBackend) edit(fn func([]service, func(string, []string) error) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	next := slices.Clone(b.services)
	err := fn(next, func(id string, servers []string) error {
		if b.beforeSet != nil {
			b.beforeSet()
		}
		if b.failSet == id {
			return errors.New("set failed")
		}
		for i := range next {
			if next[i].ID == id {
				next[i].Servers = slices.Clone(servers)
				return nil
			}
		}
		return errors.New("unknown service")
	})
	if err != nil {
		return err
	}
	if b.failCommit {
		return errors.New("commit failed")
	}
	b.services = next
	if b.failApply {
		return errors.New("apply failed after commit")
	}
	return nil
}
func (b *fakeBackend) watch(ctx context.Context, events chan<- struct{}) (func(), error) {
	b.events = events
	return func() {}, nil
}
func testManager(t *testing.T, b *fakeBackend) *Manager {
	t.Helper()
	m := &Manager{b: b, dir: t.TempDir(), checkRoutes: func(context.Context, string, []string) error { return nil }, lock: func(dir string) (func(), error) { return lockDirectory(dir, os.Geteuid()) }}
	t.Cleanup(func() {
		if err := m.Stop(); err != nil {
			t.Error(err)
		}
	})
	return m
}

var captureDNS = []string{"1.1.1.1"}

func start(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Start(context.Background(), "utun10", captureDNS); err != nil {
		t.Fatal(err)
	}
}
func TestRestoreAutomaticAndManualIPv6DNS(t *testing.T) {
	manual := []string{"10.0.0.1", "fe80::1%en0"}
	b := &fakeBackend{services: []service{{ID: "wifi", Managed: true}, {ID: "ethernet", Servers: manual, Managed: true}, {ID: "vpn", Servers: []string{"10.1.1.1"}}}}
	m := testManager(t, b)
	b.beforeSet = func() {
		raw, err := os.ReadFile(filepath.Join(m.dir, "recovery.json"))
		if err != nil || len(raw) == 0 {
			t.Fatal("DNS changed before journal", err)
		}
	}
	start(t, m)
	if !slices.Equal(b.services[0].Servers, captureDNS) || !slices.Equal(b.services[1].Servers, captureDNS) {
		t.Fatal(b.services)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if b.services[0].Servers != nil || !slices.Equal(b.services[1].Servers, manual) || b.services[2].Servers[0] != "10.1.1.1" {
		t.Fatal(b.services)
	}
}
func TestCrashRecoveryDoesNotClobberExternalEdits(t *testing.T) {
	b := &fakeBackend{services: []service{{ID: "wifi", Managed: true}, {ID: "ethernet", Servers: []string{"8.8.8.8"}, Managed: true}}}
	m := testManager(t, b)
	start(t, m)
	m.cancel()
	m.watchEnd()
	<-m.done
	m.cancel = nil
	m.release()
	b.services[1].Servers = []string{"9.9.9.9"}
	next := testManager(t, b)
	next.dir = m.dir
	if err := next.Recover(); err != nil {
		t.Fatal(err)
	}
	if b.services[0].Servers != nil || b.services[1].Servers[0] != "9.9.9.9" {
		t.Fatal(b.services)
	}
}
func TestPartialApplyRestoresAndLeavesOriginalDNS(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "commit"}[commit], func(t *testing.T) {
			b := &fakeBackend{services: []service{{ID: "wifi", Managed: true}, {ID: "ethernet", Managed: true}}}
			m := testManager(t, b)
			if commit {
				b.failCommit = true
			} else {
				b.failSet = "ethernet"
			}
			if err := m.Start(context.Background(), "utun10", captureDNS); err == nil {
				t.Fatal("expected apply failure")
			}
			b.failCommit = false
			b.failSet = ""
			if err := m.Recover(); err != nil {
				t.Fatal(err)
			}
			for _, s := range b.services {
				if s.Servers != nil {
					t.Fatal(b.services)
				}
			}
		})
	}
}
func TestNetworkReconcileNewServiceLocationAndExternalChange(t *testing.T) {
	b := &fakeBackend{services: []service{{ID: "wifi", Managed: true}}}
	m := testManager(t, b)
	start(t, m)
	b.services[0].Managed = false
	b.services = append(b.services, service{ID: "ethernet", Servers: []string{"10.2.2.2"}, Managed: true})
	m.mu.Lock()
	err := m.reconcile()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if b.services[0].Servers != nil || !slices.Equal(b.services[1].Servers, captureDNS) {
		t.Fatal(b.services)
	}
	b.services[1].Servers = []string{"9.9.9.9"}
	m.mu.Lock()
	err = m.reconcile()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if b.services[1].Servers[0] != "9.9.9.9" {
		t.Fatal("external edit overwritten")
	}
}
func TestRouteFailureDoesNotChangeDNS(t *testing.T) {
	b := &fakeBackend{services: []service{{ID: "wifi", Managed: true}}}
	m := testManager(t, b)
	m.checkRoutes = func(context.Context, string, []string) error { return errors.New("not TUN") }
	if err := m.Start(context.Background(), "utun10", captureDNS); err == nil {
		t.Fatal("expected route failure")
	}
	if b.services[0].Servers != nil {
		t.Fatal("DNS changed")
	}
}
func TestMachineWideLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockDirectory(dir, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if u, err := lockDirectory(dir, os.Geteuid()); err == nil {
		u()
		t.Fatal("second manager acquired lock")
	}
}
func TestRouteInterface(t *testing.T) {
	if got := routeInterface("gateway: 10.10.20.1\n interface: utun10\n flags: <UP>\n"); got != "utun10" {
		t.Fatal(got)
	}
}

func TestNetworkCommitFailureCanRetryWithoutLosingOwnership(t *testing.T) {
	b := &fakeBackend{services: []service{{ID: "wifi", Managed: true}}}
	m := testManager(t, b)
	start(t, m)
	b.services = append(b.services, service{ID: "ethernet", Managed: true})
	b.failCommit = true
	m.mu.Lock()
	err := m.reconcile()
	m.mu.Unlock()
	if err == nil {
		t.Fatal("commit failure hidden")
	}
	b.failCommit = false
	m.mu.Lock()
	err = m.reconcile()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.services[1].Servers, captureDNS) || m.ignored["ethernet"] {
		t.Fatal("pending override treated as external edit")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if b.services[1].Servers != nil {
		t.Fatal("original DNS not restored")
	}
}

func TestRecoveryReappliesOriginalAfterCommitSucceededButApplyFailed(t *testing.T) {
	b := &fakeBackend{services: []service{{ID: "wifi", Managed: true}}, failApply: true}
	m := testManager(t, b)
	if err := m.Start(context.Background(), "utun10", captureDNS); err == nil {
		t.Fatal("expected apply failure")
	}
	b.failApply = false
	sets := 0
	b.beforeSet = func() { sets++ }
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	if sets == 0 {
		t.Fatal("recovery failed to reapply committed original preferences")
	}
	if b.services[0].Servers != nil {
		t.Fatal("automatic DNS not restored")
	}
}
func TestNetworkNotificationCapturesNewService(t *testing.T) {
	b := &fakeBackend{services: []service{{ID: "wifi", Managed: true}}}
	m := testManager(t, b)
	start(t, m)
	applied := make(chan struct{})
	var once sync.Once
	b.mu.Lock()
	b.services = append(b.services, service{ID: "ethernet", Managed: true})
	b.beforeSet = func() { once.Do(func() { close(applied) }) }
	b.mu.Unlock()
	b.events <- struct{}{}
	select {
	case <-applied:
	case <-time.After(3 * time.Second):
		t.Fatal("network notification was not reconciled")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !slices.Equal(b.services[1].Servers, captureDNS) {
		t.Fatal("new Ethernet service not captured")
	}
}
