package main

import (
	"errors"
	"net/url"
	"testing"
	"time"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func TestParseDeeplink_Valid(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantURL      string
		wantName     string
		wantInterval uint32
	}{
		{
			name:         "url only",
			raw:          "masir://import?url=https%3A%2F%2Fexample.com%2Fsub",
			wantURL:      "https://example.com/sub",
			wantName:     "",
			wantInterval: 0,
		},
		{
			name:         "url and name",
			raw:          "masir://import?url=https%3A%2F%2Fexample.com%2Fsub&name=My%20Servers",
			wantURL:      "https://example.com/sub",
			wantName:     "My Servers",
			wantInterval: 0,
		},
		{
			name:         "url, name, and refresh above minimum",
			raw:          "masir://import?url=https%3A%2F%2Fexample.com%2Fsub&name=My%20Servers&refresh=30",
			wantURL:      "https://example.com/sub",
			wantName:     "My Servers",
			wantInterval: 30,
		},
		{
			name:         "refresh below 15 clamped to minimum 15",
			raw:          "masir://import?url=https%3A%2F%2Fexample.com%2Fsub&refresh=5",
			wantURL:      "https://example.com/sub",
			wantName:     "",
			wantInterval: 15,
		},
		{
			name:         "refresh 0 clamped to minimum 15",
			raw:          "masir://import?url=https%3A%2F%2Fexample.com%2Fsub&refresh=0",
			wantURL:      "https://example.com/sub",
			wantName:     "",
			wantInterval: 15,
		},
		{
			name:         "refresh exactly 15",
			raw:          "masir://import?url=https%3A%2F%2Fexample.com%2Fsub&refresh=15",
			wantURL:      "https://example.com/sub",
			wantName:     "",
			wantInterval: 15,
		},
		{
			name:         "empty refresh param treated as omitted (0)",
			raw:          "masir://import?url=https%3A%2F%2Fexample.com%2Fsub&refresh=",
			wantURL:      "https://example.com/sub",
			wantName:     "",
			wantInterval: 0,
		},
		{
			name:         "import with trailing slash",
			raw:          "masir://import/?url=https%3A%2F%2Fexample.com%2Fsub",
			wantURL:      "https://example.com/sub",
			wantName:     "",
			wantInterval: 0,
		},
		{
			name:         "import with triple slash",
			raw:          "masir:///import?url=https%3A%2F%2Fexample.com%2Fsub",
			wantURL:      "https://example.com/sub",
			wantName:     "",
			wantInterval: 0,
		},
		{
			name:         "case-insensitive scheme and action",
			raw:          "MASIR://IMPORT?url=https%3A%2F%2Fexample.com%2Fsub&name=Test&refresh=20",
			wantURL:      "https://example.com/sub",
			wantName:     "Test",
			wantInterval: 20,
		},
		{
			name:         "complex subscription URL with query params",
			raw:          "masir://import?url=" + url.QueryEscape("https://sub.example.com/api/v1/client/subscribe?token=secret123&flag=ir") + "&name=VIP",
			wantURL:      "https://sub.example.com/api/v1/client/subscribe?token=secret123&flag=ir",
			wantName:     "VIP",
			wantInterval: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dl, err := parseDeeplink(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dl.url != tc.wantURL {
				t.Errorf("url = %q, want %q", dl.url, tc.wantURL)
			}
			if dl.name != tc.wantName {
				t.Errorf("name = %q, want %q", dl.name, tc.wantName)
			}
			if dl.interval != tc.wantInterval {
				t.Errorf("interval = %d, want %d", dl.interval, tc.wantInterval)
			}
		})
	}
}

func TestParseDeeplink_Invalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty string", ""},
		{"wrong scheme", "https://import?url=https%3A%2F%2Fexample.com%2Fsub"},
		{"wrong action", "masir://export?url=https%3A%2F%2Fexample.com%2Fsub"},
		{"missing url param", "masir://import?name=my-sub"},
		{"empty url param", "masir://import?url="},
		{"invalid refresh param", "masir://import?url=https%3A%2F%2Fexample.com%2Fsub&refresh=notanumber"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDeeplink(tc.raw)
			if err == nil {
				t.Errorf("expected error for %q, got nil", tc.raw)
			}
		})
	}
}

func TestStartImportSubscription_Success(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)

	subURL := "https://example.com/sub/test"
	subName := "My Deep Link Sub"
	var refresh uint32 = 30

	a.startImportSubscription(subURL, subName, refresh)

	// Since newTestApp executes async inline, the RPC finishes synchronously:
	if !f.called("add " + subURL) {
		t.Fatalf("expected AddSubscription to be called for %q, calls: %v", subURL, f.calls)
	}

	// Verify subscription was added with right name, url, and interval:
	var found bool
	for _, s := range f.subs {
		if s.Url == subURL {
			found = true
			if s.Name != subName {
				t.Errorf("subscription name = %q, want %q", s.Name, subName)
			}
			if s.CustomUpdateIntervalMinutes != refresh {
				t.Errorf("subscription interval = %d, want %d", s.CustomUpdateIntervalMinutes, refresh)
			}
		}
	}
	if !found {
		t.Fatalf("added subscription not found in daemon subs")
	}

	// App state should have returned to main page with servers section open:
	if a.path != mainPath {
		t.Errorf("app path = %q, want %q", a.path, mainPath)
	}
	if a.open != sectionServers {
		t.Errorf("app open section = %v, want %v", a.open, sectionServers)
	}
	if a.subURL != "" || a.subName != "" || a.subCustomInterval != 0 {
		t.Errorf("import fields not cleared: url=%q, name=%q, interval=%d", a.subURL, a.subName, a.subCustomInterval)
	}

	// Success toast must be queued:
	wantToast := `Subscription "My Deep Link Sub" added`
	if len(a.notices) == 0 || a.notices[len(a.notices)-1] != wantToast {
		t.Errorf("expected success toast %q, got notices: %v", wantToast, a.notices)
	}
	if a.noticeTypes[wantToast] != "success" {
		t.Errorf("expected notice type 'success', got: %q", a.noticeTypes[wantToast])
	}
}

func TestStartImportSubscription_ImportingState(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)

	// Simulate an async RPC that is still running by replacing a.async with a no-op:
	var pendingTask func()
	a.async = func(fn func()) {
		pendingTask = fn
	}

	subURL := "https://example.com/sub/slow"
	subName := "Slow Sub"
	a.startImportSubscription(subURL, subName, 20)

	// Must be on the add subscription page:
	if a.path != addSubscriptionPath {
		t.Fatalf("app path = %q, want %q", a.path, addSubscriptionPath)
	}
	// Must be in importing state:
	if !a.subBusy {
		t.Fatalf("expected a.subBusy to be true during import")
	}

	// Check UI tester shows importing text:
	tt := tester(a)
	if !tt.HasText("Importing & Parsing...") {
		t.Fatalf("expected 'Importing & Parsing...' button text in importing state, got: %v", tt.Texts())
	}

	// Now complete the task:
	if pendingTask == nil {
		t.Fatal("expected pending async task")
	}
	pendingTask()

	// Once completed, it should return to main page and normal behavior:
	if a.subBusy {
		t.Fatalf("expected a.subBusy to be false after completion")
	}
	if a.path != mainPath {
		t.Fatalf("expected app path = %q, got %q", mainPath, a.path)
	}
	if a.open != sectionServers {
		t.Fatalf("expected open section = %v, got %v", sectionServers, a.open)
	}
}

func TestStartImportSubscription_Error(t *testing.T) {
	f := ukServers()
	f.failAdd = errors.New("network timeout")
	a := newTestApp(f)

	subURL := "https://example.com/sub/fail"
	a.startImportSubscription(subURL, "Failing Sub", 15)

	// Should not be busy anymore:
	if a.subBusy {
		t.Errorf("expected a.subBusy = false on error")
	}
	// Should have error message set:
	if a.subError == "" {
		t.Errorf("expected a.subError to be populated")
	}
	// Should stay on addSubscriptionPath so user sees the error:
	if a.path != addSubscriptionPath {
		t.Errorf("app path = %q, want %q", a.path, addSubscriptionPath)
	}
}

func TestStartImportSubscription_ScrollsToNewSubscription(t *testing.T) {
	f := ukServers()
	f.configs["s1"] = append(f.configs["s1"], manyConfigs(100)...)
	f.configs["s2"] = []*pb.SubscriptionConfig{
		{Id: "s2-c1", Name: "Tokyo Server", Protocol: "vless"},
	}
	a := newTestApp(f)
	a.open = sectionServers
	tt := tester(a)
	tt.Advance(time.Second)

	firstBefore, _ := a.serversList.Visible()
	if firstBefore != 0 {
		t.Fatalf("expected initial first visible item to be 0, got %d", firstBefore)
	}

	// Trigger import of second subscription
	subURL := "https://example.com/sub/s2"
	a.startImportSubscription(subURL, "Tokyo Sub", 0)

	// App state should be on main view with sectionServers open
	if a.path != mainPath {
		t.Fatalf("expected path to be mainPath, got %q", a.path)
	}
	if a.open != sectionServers {
		t.Fatalf("expected open section to be sectionServers, got %v", a.open)
	}
	if a.scrollToSubID != "s2" {
		t.Fatalf("expected scrollToSubID to be %q, got %q", "s2", a.scrollToSubID)
	}

	// Render the next frame so serverPicker lays out the list and applies immediate scroll
	tt.Frame()
	tt.Advance(time.Second)

	// scrollToSubID should have been consumed
	if a.scrollToSubID != "" {
		t.Fatalf("expected scrollToSubID to be cleared, got %q", a.scrollToSubID)
	}

	// The serversList must have scrolled to the start of the new imported subscription (item index > 100)
	firstAfter, _ := a.serversList.Visible()
	if firstAfter < 95 {
		t.Fatalf("expected serversList to scroll to new subscription (> 95), got first visible %d", firstAfter)
	}
	if !tt.HasText(ltr("TOKYO SUB")) {
		t.Fatalf("expected new subscription group header 'TOKYO SUB' to be visible, got texts: %v", tt.Texts())
	}
}

