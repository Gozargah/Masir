package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// statusDot keeps its glow in a tiny texture and animates only its painting.
// Element.Loop would rebuild and lay out the whole panel on every refresh.
func statusDot(c *ui.Context, dot ui.Element, color ui.Color, connecting, connected bool, palette palette) {
	color = dot.AnimateColor("c", color, appMotion(motionDuration))
	if !connecting && !connected {
		dot.Background(color)
		return
	}
	period, glow, key := 2400*time.Millisecond, palette.healthy.Alpha(0.7), "breathe"
	if connecting {
		period, glow, key = 2*time.Second, palette.degraded.Alpha(0.7), "pulse"
	}
	start := *ui.Local(dot, key, func() time.Time { return c.Now() })
	texture := ui.Local(dot, "glow-texture", func() dotTexture { return dotTexture{} })
	still := c.Preferences().ReduceMotion
	// Reserve the glow's maximum painted extent for culling and damage tracking.
	dot.Shadow(0, 0, 12, 0, ui.Transparent).Draw(func(p *ui.Painter, r ui.Rect) {
		phase := triangle(float32(p.Now().Sub(start)%period) / float32(period))
		scale, opacity := float32(1), float32(1)
		if !still {
			if connecting {
				opacity = 1 - 0.5*phase
			} else {
				scale, opacity = 1+0.15*phase, 1-0.18*phase
			}
			// The slow pulse needs no more than 60 Hz; other motion can still
			// request the full display rate. MyGo repaints only this drawing.
			p.AnimationFrameRate(60)
		}
		texture.draw(p, r, color, glow, scale, opacity)
	})
}

type dotTexture struct {
	canvas                 *ui.Canvas
	color, glow            ui.Color
	density, width, height float32
}

func (t *dotTexture) draw(p *ui.Painter, r ui.Rect, color, glow ui.Color, scale, opacity float32) {
	// Render at the largest animated size so the breathing dot stays sharp.
	density := p.Scale() * 1.15
	const pad = float32(12)
	w, h := int(math.Ceil(float64((r.W+2*pad)*density))), int(math.Ceil(float64((r.H+2*pad)*density)))
	changed := t.canvas == nil || t.density != density || t.width != r.W || t.height != r.H
	if changed {
		t.canvas = ui.NewCanvas(w, h)
	}
	if changed || t.color != color || t.glow != glow {
		t.canvas.Draw(p, func(cp *ui.Painter) {
			box := ui.Rect{X: pad * density, Y: pad * density, W: r.W * density, H: r.H * density}
			cp.Shadow(box, box.W/2, 0, 0, 8*density, 0, glow)
			cp.Fill(box, color, box.W/2)
		})
		t.color, t.glow, t.density, t.width, t.height = color, glow, density, r.W, r.H
	}
	dst := ui.Rect{X: r.X - pad, Y: r.Y - pad, W: float32(w) / density, H: float32(h) / density}
	p.Transform(ui.Scaling(scale, scale), r.X+r.W/2, r.Y+r.H/2, func() {
		p.ImageRegion(t.canvas.Bitmap(), ui.Rect{}, dst, ui.ImageOptions{Tint: ui.RGBA(255, 255, 255, opacity)})
	})
}
