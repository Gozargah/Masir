package main

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestAddSubscriptionEnterFromEveryInput(t *testing.T) {
	for _, focus := range []string{"Label (Optional)", "Subscription URL"} {
		t.Run(focus, func(t *testing.T) {
			f := ukServers()
			a := newTestApp(f)
			a.subURL = "https://example.com/sub/test"
			a.openPage(addSubscriptionPath)
			tt := tester(a)
			tt.Frame()
			tt.Frame()
			if err := tt.Click(focus); err != nil {
				t.Fatal(err)
			}
			tt.KeyDown(ui.KeyEnter)
			tt.Frame()
			if n := f.count("add https://example.com/sub/test"); n != 1 {
				t.Fatalf("Enter submitted %d times; want 1", n)
			}
			if a.path != mainPath {
				t.Fatalf("form stayed on %s", a.path)
			}
		})
	}
}

func TestAddSubscriptionEnterDoesNotDuplicateBusyImport(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	a.subURL = "https://example.com/sub/test"
	a.openPage(addSubscriptionPath)
	tt := tester(a)
	tt.Frame()
	tt.Frame()
	var pending []func()
	a.async = func(fn func()) { pending = append(pending, fn) }
	if err := tt.Click("Label (Optional)"); err != nil {
		t.Fatal(err)
	}
	tt.KeyDown(ui.KeyEnter)
	tt.Frame()
	tt.KeyUp(ui.KeyEnter)
	tt.KeyDown(ui.KeyEnter)
	tt.Frame()
	if !a.subBusy || len(pending) != 1 {
		t.Fatalf("queued %d imports; busy=%v", len(pending), a.subBusy)
	}
}
func TestAddSubscriptionEnterWithEmptyURL(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	a.openPage(addSubscriptionPath)
	tt := tester(a)
	tt.Frame()
	tt.Frame()
	tt.KeyDown(ui.KeyEnter)
	tt.Frame()
	if a.subBusy || a.path != addSubscriptionPath || f.count("add ") != 0 {
		t.Fatal("empty URL submitted")
	}
}
