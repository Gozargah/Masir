package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	latencyPulsePeriod = 1100 * time.Millisecond
	latencyPulseSettle = 180 * time.Millisecond
)

// latencyShimmer fades the cached status dot without rebuilding the badge.
type latencyShimmer struct {
	start   time.Time
	from    float32
	testing bool
}

func (s latencyShimmer) opacity(now time.Time) float32 {
	elapsed := max(0, now.Sub(s.start))
	target := float32(1)
	if s.testing {
		phase := float64(elapsed%latencyPulsePeriod) / float64(latencyPulsePeriod)
		target = 0.775 + 0.225*float32(math.Cos(2*math.Pi*phase))
	}
	if elapsed >= latencyPulseSettle {
		return target
	}
	progress := easeApp(float32(elapsed) / float32(latencyPulseSettle))
	return s.from + (target-s.from)*progress
}

func (latencyShimmer) EffectMargin() ui.Point { return ui.Point{} }

func (s latencyShimmer) PaintEffect(p *ui.Painter, box ui.Rect, content *ui.Bitmap, inset ui.Point) {
	now := p.Now()
	if s.testing || now.Sub(s.start) < latencyPulseSettle {
		p.AnimationFrameRate(60)
	}
	scale := p.Scale()
	src := ui.Rect{X: inset.X, Y: inset.Y, W: box.W * scale, H: box.H * scale}
	p.ImageRegion(content, src, box, ui.ImageOptions{Tint: ui.RGBA(255, 255, 255, s.opacity(now))})
}
