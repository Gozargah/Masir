package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// latencyShimmer fades the cached badge, including its old latency and focus
// ring, without rebuilding the panel. Result changes still roll their digits.
type latencyShimmer struct {
	start time.Time
	still bool
}

func (latencyShimmer) EffectMargin() ui.Point { return ui.Point{X: 4, Y: 4} }

func (s latencyShimmer) PaintEffect(p *ui.Painter, box ui.Rect, content *ui.Bitmap, inset ui.Point) {
	opacity := float32(1)
	if !s.still {
		const period = 1100 * time.Millisecond
		phase := float32(p.Now().Sub(s.start)%period) / float32(period)
		opacity = 0.775 + 0.225*ui.EaseInOut(phase)
		p.AnimationFrameRate(60)
	}
	const margin = float32(4)
	dst := ui.Rect{X: box.X - margin, Y: box.Y - margin, W: box.W + 2*margin, H: box.H + 2*margin}
	scale := p.Scale()
	src := ui.Rect{X: inset.X - margin*scale, Y: inset.Y - margin*scale, W: dst.W * scale, H: dst.H * scale}
	p.ImageRegion(content, src, dst, ui.ImageOptions{Tint: ui.RGBA(255, 255, 255, opacity)})
}
