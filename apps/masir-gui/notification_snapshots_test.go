package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNotificationSnapshots(t *testing.T) {
	dir := os.Getenv("MASIR_TOAST_SNAPSHOTS")
	if dir == "" {
		t.Skip("set MASIR_TOAST_SNAPSHOTS to render the toast styles")
	}
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		for _, s := range []struct{ kind, message string }{
			{"error", "Couldn't connect: permission denied"},
			{"warning", "Adding rules from the panel isn't available yet"},
			{"success", "Settings restored to their defaults"},
			{"info", "Copied masir logs --follow"},
			{"long-error", strings.Repeat("The provider could not be reached. ", 30) + "End of error."},
		} {
			t.Run(theme+"/"+s.kind, func(t *testing.T) {
				a := newTestApp(ukServers())
				tt := ui.NewTester(a.view, panelWidth, 240)
				tt.SetDark(dark)
				tt.Frame()
				tt.Advance(time.Second) // settle control colors before capturing the toast
				kind := s.kind
				if kind == "long-error" {
					kind = "error"
				}
				a.queueNotice(s.message, kind)
				tt.Frame()
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				f, err := os.Create(filepath.Join(dir, s.kind+"-"+theme+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, tt.Image())
				closeErr := f.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
			})
		}
	}
}
