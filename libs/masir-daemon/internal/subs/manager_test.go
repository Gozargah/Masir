package subs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// provider is a fake subscription server whose body and status can change between fetches.
type provider struct {
	mu     sync.Mutex
	body   string
	status int
	srv    *httptest.Server
}

func newProvider(t *testing.T, body string) *provider {
	p := &provider{body: body, status: http.StatusOK}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=100; expire=0")
		w.Header().Set("Profile-Update-Interval", "6")
		w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("Provider Title")))
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte(p.body))
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *provider) set(body string, status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.body, p.status = body, status
}

func newTestManager(t *testing.T, proxyDial func(context.Context, string, string) (net.Conn, error)) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(filepath.Join(dir, "subscriptions"), filepath.Join(dir, "active.json"), proxyDial)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAddAndList(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t, links())
	m := newTestManager(t, nil)

	s, err := m.Add(ctx, p.srv.URL+"/sub", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Provider Title" || len(s.Configs) != 4 || s.Usage == nil || s.Usage.Total != 100 {
		t.Fatalf("added = %+v", s)
	}
	if s.ProviderIntervalMinutes != 6*60 || m.Interval(s) != DefaultInterval {
		t.Fatalf("interval = %v (provider suggests %dm); the user's setting wins over the provider's", m.Interval(s), s.ProviderIntervalMinutes)
	}
	m.SetInterval(45 * time.Minute)
	if m.Interval(s) != 45*time.Minute {
		t.Fatalf("interval = %v, want the 45m setting", m.Interval(s))
	}
	info, err := os.Stat(m.path(s.ID))
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("subscription file: %v, %v", info, err)
	}

	if _, err := m.Add(ctx, p.srv.URL+"/sub", "dup", 0); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate url: got %v", err)
	}
	if _, err := m.Add(ctx, "ftp://x", "", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad url: got %v", err)
	}

	p.set("", http.StatusInternalServerError)
	if _, err := m.Add(ctx, p.srv.URL+"/other", "", 0); !errors.Is(err, ErrFetch) {
		t.Fatalf("failing provider: got %v", err)
	}
	all, err := m.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("a failed add must not be saved; list = %d, %v", len(all), err)
	}
}

func TestUpdateKeepsLatencyAndSurvivesFailure(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t, trojanWS+"\n"+ssLink())
	m := newTestManager(t, nil)
	s, err := m.Add(ctx, p.srv.URL, "mine", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Pretend trojan was tested, then the provider rotates the ss server.
	m.mu.Lock()
	cur, _ := m.load(s.ID)
	for i := range cur.Configs {
		cur.Configs[i].Latency = &Latency{Ms: 42}
	}
	_ = m.save(cur)
	m.mu.Unlock()
	p.set(trojanWS+"\n"+strings.Replace(ssLink(), "8388", "9999", 1), http.StatusOK)

	u, err := m.Update(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := byName(t, u.Configs)
	if got["Trojan WS"].Latency == nil || got["Trojan WS"].Latency.Ms != 42 {
		t.Error("unchanged config lost its latency")
	}
	if got["SS"].Latency != nil || got["SS"].Port != 9999 {
		t.Errorf("changed config = %+v", got["SS"])
	}

	p.set("", http.StatusBadGateway)
	if _, err := m.Update(ctx, s.ID); !errors.Is(err, ErrFetch) {
		t.Fatalf("got %v, want ErrFetch", err)
	}
	after, _ := m.Get(s.ID)
	if after.LastError == "" || len(after.Configs) != 2 || after.UpdatedAt.After(after.CheckedAt) {
		t.Fatalf("after failed update = %+v", after)
	}
}

func TestRefreshApplyUsesCallerContext(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprintf("background=%v", background), func(t *testing.T) {
			p := newProvider(t, trojanWS)
			m := newTestManager(t, nil)
			s, err := m.Add(context.Background(), p.srv.URL, "mine", 0)
			if err != nil {
				t.Fatal(err)
			}
			later := s.CreatedAt.Add(13 * time.Hour)
			m.now = func() time.Time { return later }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var applyCtx context.Context
			m.OnChange = func(ctx context.Context) { applyCtx = ctx }
			if background {
				m.refreshDue(ctx)
			} else if _, err := m.Update(ctx, s.ID); err != nil {
				t.Fatal(err)
			}
			if applyCtx == nil {
				t.Fatal("refresh did not apply the saved configs")
			}
			if _, ok := applyCtx.Deadline(); ok {
				t.Fatal("apply inherited the fetch deadline")
			}
			if err := applyCtx.Err(); err != nil {
				t.Fatalf("apply context already canceled: %v", err)
			}
			cancel()
			if !errors.Is(applyCtx.Err(), context.Canceled) {
				t.Fatal("apply context did not preserve caller cancellation")
			}
		})
	}
}

// lateSubscriptionTransport models work that completes successfully after the
// context expires, as parsing can: cancellation alone does not reject its result.
type lateSubscriptionTransport struct{ body string }

func (tr lateSubscriptionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tr.body)), Header: make(http.Header)}, nil
}

func TestUpdateRejectsSuccessfulResultAfterDeadline(t *testing.T) {
	p := newProvider(t, trojanWS)
	m := newTestManager(t, nil)
	s, err := m.Add(context.Background(), p.srv.URL, "mine", 0)
	if err != nil {
		t.Fatal(err)
	}
	previousTimeout := refreshTimeout
	refreshTimeout = 10 * time.Millisecond
	t.Cleanup(func() { refreshTimeout = previousTimeout })
	m.direct = &http.Client{Transport: lateSubscriptionTransport{body: ssLink()}}
	applied := false
	m.OnChange = func(context.Context) { applied = true }
	later := s.CheckedAt.Add(time.Minute)
	m.now = func() time.Time { return later }

	_, err = m.Update(context.Background(), s.ID)
	if !errors.Is(err, ErrFetch) || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("update = %v, want a fetch timeout", err)
	}
	after, err := m.Get(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after.LastError, context.DeadlineExceeded.Error()) || !after.CheckedAt.Equal(later) ||
		!after.UpdatedAt.Equal(s.UpdatedAt) || len(after.Configs) != 1 || after.Configs[0].ID != s.Configs[0].ID {
		t.Fatalf("late result did not preserve the subscription: %+v", after)
	}
	if applied {
		t.Fatal("late result was applied")
	}
}

func TestUpdateTimeoutKeepsExistingConfigs(t *testing.T) {
	previousTimeout := refreshTimeout
	refreshTimeout = 100 * time.Millisecond
	t.Cleanup(func() { refreshTimeout = previousTimeout })

	for _, viaProxy := range []bool{false, true} {
		t.Run(fmt.Sprintf("viaProxy=%v", viaProxy), func(t *testing.T) {
			var hang atomic.Bool
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hang.Load() {
					if viaProxy {
						w.WriteHeader(http.StatusBadGateway)
						return
					}
					<-r.Context().Done()
					return
				}
				_, _ = w.Write([]byte(trojanWS))
			}))
			t.Cleanup(provider.Close)
			m := newTestManager(t, nil)
			s, err := m.Add(context.Background(), provider.URL, "mine", 0)
			if err != nil {
				t.Fatal(err)
			}
			var proxied atomic.Bool
			if viaProxy {
				proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					proxied.Store(true)
					<-r.Context().Done()
				}))
				t.Cleanup(proxy.Close)
				m.viaProxy = &http.Client{Transport: &http.Transport{
					DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, proxy.Listener.Addr().String())
					},
				}}
			}
			hang.Store(true)
			// The caller allows much longer; Update must enforce its own deadline.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = m.Update(ctx, s.ID)
			if !errors.Is(err, ErrFetch) || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
				t.Fatalf("update = %v, want a fetch timeout", err)
			}
			if ctx.Err() != nil {
				t.Fatal("update waited for the caller's deadline")
			}
			if viaProxy && !proxied.Load() {
				t.Fatal("refresh did not retry through the proxy")
			}
			after, err := m.Get(s.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.LastError == "" || !after.CheckedAt.After(s.CheckedAt) || !after.UpdatedAt.Equal(s.UpdatedAt) ||
				len(after.Configs) != 1 || after.Configs[0].ID != s.Configs[0].ID {
				t.Fatalf("timeout did not preserve the subscription: %+v", after)
			}
		})
	}
}

func TestFetchRetriesThroughProxy(t *testing.T) {
	p := newProvider(t, trojanWS)
	var proxied bool
	m := newTestManager(t, func(ctx context.Context, network, _ string) (net.Conn, error) {
		proxied = true
		return (&net.Dialer{}).DialContext(ctx, network, p.srv.Listener.Addr().String())
	})
	// .invalid never resolves, so only the proxy path can reach the provider.
	s, err := m.Add(context.Background(), "http://sub.invalid/x", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !proxied || len(s.Configs) != 1 {
		t.Fatalf("proxied=%v configs=%d", proxied, len(s.Configs))
	}
}

func TestAlterAndRemove(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t, trojanWS)
	m := newTestManager(t, nil)
	s, _ := m.Add(ctx, p.srv.URL, "old", 0)

	name, interval := "new", uint32(90)
	a, err := m.Alter(ctx, s.ID, &name, nil, &interval)
	if err != nil || a.Name != "new" || m.Interval(a) != 90*time.Minute {
		t.Fatalf("alter = %+v, %v", a, err)
	}
	empty := " "
	if _, err := m.Alter(ctx, s.ID, &empty, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: got %v", err)
	}
	bad := "http://sub.invalid/x"
	if _, err := m.Alter(ctx, s.ID, nil, &bad, nil); !errors.Is(err, ErrFetch) {
		t.Fatalf("unreachable url: got %v", err)
	}
	if g, _ := m.Get(s.ID); g.URL != s.URL {
		t.Fatal("a url that failed to fetch was saved")
	}

	sel := Active{SubscriptionID: s.ID, ConfigID: s.Configs[0].ID, Name: s.Configs[0].Name, Outbound: s.Configs[0].Outbound}
	if err := m.SetActive(sel); err != nil {
		t.Fatal(err)
	}
	changes := 0
	m.OnChange = func(context.Context) { changes++ }
	if err := m.Remove(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after remove: %v", err)
	}
	if a, _ := m.Active(); a.ConfigID != sel.ConfigID || !sameJSON(a.Outbound, sel.Outbound) {
		t.Fatalf("the selection should keep running from its copy, got %+v", a)
	}
	if changes != 1 {
		t.Fatalf("OnChange called %d times, want 1", changes)
	}
	if err := m.Remove(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
}

func TestIDsCantEscapeDir(t *testing.T) {
	m := newTestManager(t, nil)
	for _, id := range []string{"../active", "../../etc/passwd", "", "ABCDEF0123456789"} {
		if _, err := m.Get(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestFollowActive(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t, trojanWS+"\n"+ssLink())
	m := newTestManager(t, nil)
	changes := 0
	m.OnChange = func(context.Context) { changes++ }
	s, _ := m.Add(ctx, p.srv.URL, "", 0)
	tr := byName(t, s.Configs)["Trojan WS"]
	_ = m.SetActive(Active{SubscriptionID: s.ID, ConfigID: tr.ID, Name: tr.Name, Outbound: tr.Outbound})

	// Same settings: the selection stays.
	if _, err := m.Update(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if a, _ := m.Active(); a.ConfigID != tr.ID || changes != 1 {
		t.Fatalf("active=%+v changes=%d", a, changes)
	}

	// Credentials rotated, same name: follow it, copy included.
	p.set(strings.Replace(trojanWS, "secret", "rotated", 1)+"\n"+ssLink(), http.StatusOK)
	u, _ := m.Update(ctx, s.ID)
	rotated := byName(t, u.Configs)["Trojan WS"]
	a, _ := m.Active()
	if a.ConfigID != rotated.ID || !sameJSON(a.Outbound, rotated.Outbound) {
		t.Fatalf("active=%+v, want the rotated config", a)
	}
	if c, ok := m.Find(s.ID, tr.ID, tr.Name); !ok || c.ID != rotated.ID {
		t.Fatalf("Find by the old ID and name = %v, %v", c.ID, ok)
	}

	// Gone entirely: the copy keeps running.
	p.set(ssLink(), http.StatusOK)
	_, _ = m.Update(ctx, s.ID)
	if b, _ := m.Active(); b.ConfigID != a.ConfigID || !sameJSON(b.Outbound, a.Outbound) {
		t.Fatalf("active=%+v, want it unchanged", b)
	}
	if _, ok := m.Find(s.ID, tr.ID, tr.Name); ok {
		t.Fatal("Find should fail once the config is gone")
	}
}

func TestDue(t *testing.T) {
	now := time.Now()
	s := Subscription{CreatedAt: now.Add(-time.Hour), CheckedAt: now.Add(-time.Hour)}
	if s.due(now, DefaultInterval) {
		t.Error("due an hour into a 12h interval")
	}
	if !s.due(now, 30*time.Minute) {
		t.Error("not due an hour into a 30m setting")
	}
	s.CustomIntervalMinutes = 30
	if !s.due(now, DefaultInterval) {
		t.Error("its own 30m interval wins over the setting")
	}
	s = Subscription{CheckedAt: now.Add(-45 * time.Minute), LastError: "boom"}
	if !s.due(now, DefaultInterval) {
		t.Error("a failed subscription should retry after 30m")
	}
}

func TestTestLatency(t *testing.T) {
	ctx := context.Background()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	p := newProvider(t, `[{"tag":"works","protocol":"freedom"},{"tag":"dead","protocol":"blackhole"}]`)
	m := newTestManager(t, nil)
	s, err := m.Add(ctx, p.srv.URL, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := NewManager(m.dir, m.activePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Latency{}
	if err := m.TestLatency(ctx, s.ID, nil, target.URL, nil, func(id string, l Latency) {
		got[id] = l
		c, err := reader.Config(s.ID, id)
		if err != nil || c.Latency == nil || c.Latency.Ms != l.Ms || c.Latency.Error != l.Error || !c.Latency.TestedAt.Equal(l.TestedAt) {
			t.Errorf("probe %s reported before its result was saved (read error: %v)", id, err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	cfg := byName(t, s.Configs)
	if l := got[cfg["works"].ID]; l.Error != "" {
		t.Errorf("freedom probe failed: %+v", l)
	}
	if l := got[cfg["dead"].ID]; l.Error == "" {
		t.Errorf("blackhole probe succeeded: %+v", l)
	}

	saved, _ := m.Get(s.ID)
	for _, c := range saved.Configs {
		if c.Latency == nil || c.Latency.TestedAt.IsZero() {
			t.Errorf("%s: latency not saved", c.Name)
		}
	}

	if err := m.TestLatency(ctx, s.ID, []string{"nope"}, target.URL, nil, func(string, Latency) {}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown config id: %v", err)
	}
}

// sameJSON compares by content: saved files are indented, freshly parsed configs aren't.
func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	return json.Compact(&ca, a) == nil && json.Compact(&cb, b) == nil && bytes.Equal(ca.Bytes(), cb.Bytes())
}

func TestBackgroundRefreshIsParallelWithATimeoutEach(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, nil)

	// One healthy provider and three that hang once added.
	good := newProvider(t, links())
	var hang atomic.Bool
	var active, peak atomic.Int32
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hang.Load() {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(links()))
	}))
	t.Cleanup(stuck.Close)

	goodSub, err := m.Add(ctx, good.srv.URL+"/sub", "good", 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := m.Add(ctx, fmt.Sprintf("%s/sub%d", stuck.URL, i), fmt.Sprintf("stuck%d", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	hang.Store(true)

	previousTimeout := refreshTimeout
	refreshTimeout = 2 * time.Second
	t.Cleanup(func() { refreshTimeout = previousTimeout })
	later := time.Now().Add(13 * time.Hour) // everything is due
	m.now = func() time.Time { return later }

	m.refreshDue(ctx)
	// Assert request overlap directly. Disk writes and GC after requests finish
	// can exceed a wall-clock threshold on shared CI runners.
	if got := peak.Load(); got != 3 {
		t.Fatalf("peak concurrent refreshes = %d, want 3", got)
	}
	all, _ := m.List()
	for _, s := range all {
		switch {
		case s.ID == goodSub.ID && (s.LastError != "" || !s.CheckedAt.Equal(later)):
			t.Errorf("the healthy subscription should refresh despite the stuck ones: %+v", s)
		case s.ID != goodSub.ID && s.LastError == "":
			t.Errorf("%s hung and should have timed out with an error", s.Name)
		}
	}
}

func TestFollowActiveDuplicateNames(t *testing.T) {
	ctx := context.Background()
	second := strings.ReplaceAll(strings.Replace(trojanWS, "Trojan%20WS", "آلمان", 1), "tr.example.com", "second.example.com")
	third := strings.ReplaceAll(second, "second.example.com", "third.example.com")
	p := newProvider(t, ssLink()+"\n"+second+"\n"+third)
	m := newTestManager(t, nil)
	s, err := m.Add(ctx, p.srv.URL, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetActive(s.Configs[2].Active(s.ID)); err != nil {
		t.Fatal(err)
	}
	p.set(strings.ReplaceAll(ssLink()+"\n"+second+"\n"+third, "secret", "rotated"), http.StatusOK)
	updated, err := m.Update(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	active, err := m.Active()
	if err != nil {
		t.Fatal(err)
	}
	if active.ConfigID != updated.Configs[2].ID {
		t.Fatal("refresh switched from the selected third server to the other server named آلمان")
	}
	p.set(strings.ReplaceAll(ssLink()+"\n"+third+"\n"+second, "secret", "rotated-again"), http.StatusOK)
	updated, err = m.Update(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	active, err = m.Active()
	if err != nil {
		t.Fatal(err)
	}
	if active.ConfigID != updated.Configs[1].ID {
		t.Fatal("reordering duplicate names changed the selected server")
	}
}

func TestActiveReplacementAmbiguousNames(t *testing.T) {
	old := Config{ID: "old", Name: "آلمان", Outbound: json.RawMessage(`{"protocol":"trojan","settings":{"servers":[{"address":"gone.example.com","port":443}]}}`)}
	configs := []Config{
		{ID: "second", Name: "آلمان", Address: "second.example.com", Port: 443},
		{ID: "third", Name: "آلمان", Address: "third.example.com", Port: 443},
	}
	if _, ok := activeReplacement(old.Active("sub"), configs); ok {
		t.Fatal("ambiguous names must keep the saved selection")
	}
}

func TestFollowActiveTransportIdentity(t *testing.T) {
	for _, tc := range []struct{ name, selected, other, updated string }{
		{"ws path", strings.Replace(trojanWS, "%2Fws", "%2Fchosen", 1), trojanWS, ""},
		{"ws host", strings.Replace(trojanWS, "path=%2Fws", "path=%2Fws&host=chosen.example.com", 1), strings.Replace(trojanWS, "path=%2Fws", "path=%2Fws&host=other.example.com", 1), ""},
		{"tcp raw alias", strings.Replace(vlessReality, "DE%20Reality", "same", 1), strings.ReplaceAll(strings.Replace(vlessReality, "DE%20Reality", "same", 1), "de.example.com", "other.example.com"), "raw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			p := newProvider(t, tc.other+"\n"+tc.selected)
			m := newTestManager(t, nil)
			s, err := m.Add(ctx, p.srv.URL, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.SetActive(s.Configs[1].Active(s.ID)); err != nil {
				t.Fatal(err)
			}
			rotated := strings.ReplaceAll(strings.ReplaceAll(tc.selected, "secret", "rotated"), "b831381d", "a831381d")
			if tc.updated != "" {
				var docs []map[string]json.RawMessage
				for _, c := range s.Configs {
					var doc map[string]json.RawMessage
					if err := json.Unmarshal(c.Outbound, &doc); err != nil {
						t.Fatal(err)
					}
					doc["tag"], _ = json.Marshal(c.Name)
					docs = append(docs, doc)
				}
				raw, err := json.Marshal(docs)
				if err != nil {
					t.Fatal(err)
				}
				body := strings.ReplaceAll(string(raw), "b831381d", "a831381d")
				body = strings.ReplaceAll(body, `"network":"tcp"`, `"network":"raw"`)
				p.set(body, http.StatusOK)
			}
			if tc.updated == "" {
				p.set(tc.other+"\n"+rotated, http.StatusOK)
			}
			u, err := m.Update(ctx, s.ID)
			if err != nil {
				t.Fatal(err)
			}
			a, err := m.Active()
			if err != nil {
				t.Fatal(err)
			}
			if a.ConfigID != u.Configs[1].ID {
				t.Fatal("did not follow the selected endpoint after credential rotation")
			}
		})
	}
}

func TestActiveReplacementWebSocketDefaults(t *testing.T) {
	makeConfig := func(id, path, host, sni string) Config {
		raw := fmt.Sprintf(`{"protocol":"trojan","settings":{"servers":[{"address":"server.example.com","port":443,"password":%q}]},"streamSettings":{"network":"ws","security":"tls","tlsSettings":{"serverName":%q},"wsSettings":{"path":%q,"host":%q}}}`, id, sni, path, host)
		return Config{ID: id, Name: "آلمان", Address: "server.example.com", Port: 443, Outbound: json.RawMessage(raw)}
	}
	for _, tc := range []struct{ name, oldPath, newPath, oldHost, newHost, sni string }{
		{"TLS host fallback", "/ws", "/ws", "", "a.example.com", "a.example.com"},
		{"destination host fallback", "/ws", "/ws", "", "server.example.com", ""},
		{"explicit host precedence", "/ws", "/ws", "a.example.com", "a.example.com", "different.example.com"},
		{"relative path", "/ws", "ws", "a.example.com", "a.example.com", ""},
		{"empty path", "", "/", "a.example.com", "a.example.com", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := makeConfig("old", tc.oldPath, tc.oldHost, tc.sni)
			want := makeConfig("rotated", tc.newPath, tc.newHost, tc.sni)
			other := makeConfig("other", "/ws", "", "b.example.com")
			if tc.name == "TLS host fallback" {
				t.Run("uppercase TLS", func(t *testing.T) {
					upper := func(c Config) Config {
						c.Outbound = bytes.ReplaceAll(c.Outbound, []byte(`"security":"tls"`), []byte(`"security":"TLS"`))
						return c
					}
					wrong := makeConfig("wrong", "/ws", "server.example.com", tc.sni)
					got, ok := activeReplacement(upper(old).Active("sub"), []Config{upper(wrong), upper(want)})
					if !ok || got.ID != want.ID {
						t.Fatalf("uppercase TLS replacement = %q, %v", got.ID, ok)
					}
					got, ok = activeReplacement(upper(old).Active("sub"), []Config{wrong, want})
					if !ok || got.ID != want.ID {
						t.Fatalf("mixed-case TLS replacement = %q, %v", got.ID, ok)
					}
				})
			}
			got, ok := activeReplacement(old.Active("sub"), []Config{other, want})
			if !ok || got.ID != want.ID {
				t.Fatalf("replacement = %q, %v; want rotated", got.ID, ok)
			}
		})
	}
}

func TestSharedSourceIsStoredOnce(t *testing.T) {
	var outbounds []json.RawMessage
	for i := range 1000 {
		outbounds = append(outbounds, json.RawMessage(fmt.Sprintf(`{"tag":"node-%d","protocol":"vless","settings":{"address":"node-%d.example.com","port":443,"id":"00000000-0000-4000-8000-000000000001","encryption":"none"},"streamSettings":{"security":"tls"}}`, i, i)))
	}
	source, err := json.Marshal(map[string]any{"notes": strings.Repeat("x", 1<<20), "outbounds": outbounds})
	if err != nil {
		t.Fatal(err)
	}
	configs, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1000 {
		t.Fatalf("configs: %d", len(configs))
	}
	m, err := NewManager(filepath.Join(t.TempDir(), "subscriptions"), filepath.Join(t.TempDir(), "active.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sub := &Subscription{ID: "0123456789abcdef", Configs: configs}
	if err := m.save(sub); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(m.path(sub.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 3*len(source) {
		t.Fatalf("duplicated source storage: input=%d saved=%d", len(source), len(data))
	}
	loaded, err := m.Get(sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Sources) != 1 {
		t.Fatalf("stored sources: %d", len(loaded.Sources))
	}
	for i, cfg := range loaded.Configs {
		if cfg.ID != configs[i].ID || cfg.SourceRef == "" || !bytes.Equal(cfg.SourceJSON, loaded.Sources[cfg.SourceRef]) {
			t.Fatalf("source or runtime ID lost for config %d", i)
		}
		if &cfg.SourceJSON[0] != &loaded.Configs[0].SourceJSON[0] {
			t.Fatal("loaded configs do not share source bytes")
		}
	}
	if err := m.save(loaded); err != nil {
		t.Fatal(err)
	}
	again, err := m.Get(sub.ID)
	if err != nil || len(again.Sources) != 1 {
		t.Fatalf("resave lost shared source: %v", err)
	}
}

// No request may finish until every kind of config has reached the target.
// This catches both a serial profile loop and a process-global single helper slot.
func TestLatencyMixRunsInParallel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	arrived := make(chan struct{}, 32)
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer target.Close()
	defer cancel()
	m := newTestManager(t, nil)
	s := &Subscription{ID: newID(), Name: "parallel"}
	for i := range 8 {
		c := Config{ID: fmt.Sprintf("c%d", i), Outbound: json.RawMessage(`{"protocol":"freedom"}`)}
		if i > 0 {
			c.Profile = &OutboundProfile{Outbounds: []json.RawMessage{c.Outbound}}
		}
		s.Configs = append(s.Configs, c)
	}
	if err := m.save(s); err != nil {
		t.Fatal(err)
	}
	// Profiles enter the managed engine path, while c0 uses a bare outbound.
	m.BuildProbeProfile = func(c Config) ([]byte, error) {
		return []byte(`{"outbounds":[{"tag":"ok","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}],"routing":{"rules":[{"network":"tcp,udp","outboundTag":"ok"}]}}`), nil
	}
	done := make(chan error, 1)
	got := map[string]Latency{}
	go func() {
		done <- m.TestLatency(ctx, s.ID, nil, target.URL, nil, func(id string, l Latency) { got[id] = l })
	}()
	for i := range len(s.Configs) {
		select {
		case <-arrived:
		case err := <-done:
			t.Fatalf("test ended before all configs arrived: %v; results=%+v", err, got)
		case <-ctx.Done():
			cancel()
			<-done
			t.Fatalf("configs did not reach target concurrently before any request completed; arrived=%d results=%+v", i, got)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(got) != len(s.Configs) {
		t.Fatalf("got %d results", len(got))
	}
	for id, l := range got {
		if l.Error != "" {
			t.Errorf("%s: %s", id, l.Error)
		}
	}
}
