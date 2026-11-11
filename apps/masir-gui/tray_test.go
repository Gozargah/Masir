package main

import (
	"bytes"
	"image/png"
	"runtime"
	"testing"
)

func TestTrayIcon(t *testing.T) {
	for _, on := range []bool{false, true} {
		b, err := trayIcon(on)
		if err != nil {
			t.Fatalf("trayIcon(%v): unexpected error: %v", on, err)
		}
		if len(b) == 0 {
			t.Fatalf("trayIcon(%v): returned empty bytes", on)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("trayIcon(%v): failed to decode PNG: %v", on, err)
		}
		bounds := img.Bounds()
		if bounds.Dx() != 32 || bounds.Dy() != 32 {
			t.Errorf("trayIcon(%v): got size %dx%d, want 32x32", on, bounds.Dx(), bounds.Dy())
		}

		var hasColor, hasAlpha bool
		for y := range 32 {
			for x := range 32 {
				c := img.At(x, y)
				_, _, _, a := c.RGBA()
				if a > 0 {
					hasAlpha = true
				}
				if on {
					r, g, bl, _ := c.RGBA()
					// Check for non-zero green component corresponding to trayGreen
					if g > r && g > bl {
						hasColor = true
					}
				} else if runtime.GOOS != "darwin" {
					r, g, bl, _ := c.RGBA()
					if bl > r && bl > g {
						hasColor = true
					}
				} else {
					hasColor = true
				}
			}
		}
		if !hasAlpha {
			t.Errorf("trayIcon(%v): icon is completely transparent", on)
		}
		if !hasColor {
			t.Errorf("trayIcon(%v): icon does not contain expected color", on)
		}
	}
}
