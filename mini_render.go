package main

import (
	"image"
	"image/color"
	"math"
	"time"
)

type miniLayout struct {
	W, H, ArtX, ArtY, ArtSize, SpecX, SpecY, SpecW, SpecH, Count, Bar, Gap, Start int
}

func miniGeometry(dpi, available int, c MiniConfig, bands int) miniLayout {
	s := float64(clamp(dpi, 96, 480)) / 96
	px := func(v float64) int { return max(1, int(math.Round(v*s))) }
	g := miniLayout{W: min(px(200), available), H: px(44), ArtX: px(6), ArtY: px(4), ArtSize: px(36), SpecY: px(6), SpecH: px(32)}
	if available < px(60) {
		return miniLayout{}
	}
	g.SpecX = px(8)
	if c.Thumbnail {
		g.SpecX = g.ArtX + g.ArtSize + px(10)
	}
	if !c.Visualizer {
		g.W = min(available, px(48))
		g.ArtX = (g.W - g.ArtSize) / 2
	}
	g.SpecW = max(0, g.W-g.SpecX-px(8))
	g.Bar, g.Gap = px(3), px(2)
	g.Count = min(32, min(bands, (g.SpecW+g.Gap)/(g.Bar+g.Gap)))
	if !c.Visualizer {
		g.Count = 0
	}
	span := g.Count*g.Bar + max(0, g.Count-1)*g.Gap
	g.Start = g.SpecX + (g.SpecW-span)/2
	return g
}

type miniSignal struct {
	Levels, Peaks [32]float64
	Last          time.Time
	Gain          float64
	Count         int
}

func (s *miniSignal) step(in miniInput, n int, now time.Time, allowed bool) bool {
	dt := now.Sub(s.Last).Seconds()
	if s.Last.IsZero() {
		dt = 1. / 30
	}
	dt = math.Max(.001, math.Min(.1, dt))
	s.Last = now
	if s.Count != n {
		s.Levels = [32]float64{}
		s.Peaks = [32]float64{}
		s.Count = n
	}
	var target [32]float64
	fresh := allowed && in.Active && in.Count > 0 && now.Sub(in.Received) < 400*time.Millisecond
	if s.Gain == 0 {
		s.Gain = 1
	}
	desired := 1.
	if fresh {
		metrics := measureSpectrum(in.Bands[:in.Count])
		if metrics.RMS > .012 && metrics.Peak > .03 {
			desired = math.Min(1.8, math.Max(1, .55/math.Max(.03, math.Max(metrics.Peak*.7, metrics.RMS*2))))
		}
		for i := 0; i < n; i++ {
			target[i] = aggregateBands(in.Bands[i*in.Count/n : (i+1)*in.Count/n])
		}
	}
	tauGain := .8
	if desired < s.Gain {
		tauGain = .1
	}
	s.Gain += (desired - s.Gain) * (1 - math.Exp(-dt/tauGain))
	changed := false
	for i := 0; i < n; i++ {
		v := target[i]*.84 + target[max(0, i-1)]*.08 + target[min(n-1, i+1)]*.08
		if v < .01 {
			v = 0
		}
		v *= s.Gain
		if v > .72 {
			v = .72 + (v-.72)/(1+4*(v-.72))
		}
		v = math.Min(1, math.Max(0, v))
		tau := .24
		if v > s.Levels[i] {
			tau = .045
		}
		old := s.Levels[i]
		oldPeak := s.Peaks[i]
		s.Levels[i] += (v - s.Levels[i]) * (1 - math.Exp(-dt/tau))
		if s.Levels[i] < .001 {
			s.Levels[i] = 0
		}
		s.Peaks[i] = math.Max(s.Levels[i], s.Peaks[i]-.9*dt)
		changed = changed || math.Abs(s.Levels[i]-old) > .0001 || math.Abs(s.Peaks[i]-oldPeak) > .0001
	}
	return changed
}

// Premultiplied BGRA surface, suitable for UpdateLayeredWindow(ULW_ALPHA).
// The background is translucent pixels, not an opaque rectangle or colorkey.
type miniSurface struct {
	Pixels, Base []byte
	Layout       miniLayout
	art          image.Image
	settings     MiniConfig
	light        bool
}

func (r *miniSurface) prepare(g miniLayout, in miniInput, light bool) bool {
	if r.Layout == g && r.art == in.Art && r.settings == in.Settings && r.light == light && len(r.Base) > 0 {
		return false
	}
	r.Layout, r.art, r.settings, r.light = g, in.Art, in.Settings, light
	size := g.W * g.H * 4
	if len(r.Base) != size {
		r.Base = make([]byte, size)
		r.Pixels = make([]byte, size)
	} else {
		clear(r.Base)
	}
	if size == 0 {
		return true
	}
	scale := float64(g.H) / 44
	bg := color.NRGBA{R: 12, G: 23, B: 32, A: 204}
	if light {
		bg = color.NRGBA{R: 238, G: 245, B: 249, A: 224}
	}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			coverage := roundCoverage(float64(x)+.5, float64(y)+.5, 0, 0, float64(g.W), float64(g.H), 9*scale)
			putMiniPixel(r.Base, g.W, x, y, bg, coverage)
		}
	}
	if in.Settings.Thumbnail {
		if in.Art != nil {
			b := in.Art.Bounds()
			fit := math.Min(float64(g.ArtSize)/float64(max(1, b.Dx())), float64(g.ArtSize)/float64(max(1, b.Dy())))
			w, h := float64(b.Dx())*fit, float64(b.Dy())*fit
			left, top := float64(g.ArtX)+(float64(g.ArtSize)-w)/2, float64(g.ArtY)+(float64(g.ArtSize)-h)/2
			for y := g.ArtY; y < min(g.H, g.ArtY+g.ArtSize); y++ {
				for x := g.ArtX; x < min(g.W, g.ArtX+g.ArtSize); x++ {
					fx, fy := float64(x)+.5, float64(y)+.5
					if fx < left || fx >= left+w || fy < top || fy >= top+h {
						continue
					}
					cov := roundCoverage(fx, fy, left, top, w, h, math.Min(5*scale, math.Min(w, h)/2))
					c := miniSample(in.Art, (fx-left)/fit-.5+float64(b.Min.X), (fy-top)/fit-.5+float64(b.Min.Y))
					putMiniPixel(r.Base, g.W, x, y, c, cov)
				}
			}
		} else {
			// Static YTM mark for missing artwork; never presented as audio data.
			for i, h := range []float64{.35, .7, .9, .5} {
				x := g.ArtX + int((7+float64(i)*6)*scale)
				for y := g.ArtY + int((1-h)*26*scale) + int(5*scale); y < g.ArtY+int(31*scale); y++ {
					for k := 0; k < max(1, int(3*scale)); k++ {
						if x+k < g.W && y < g.H {
							putMiniPixel(r.Base, g.W, x+k, y, color.NRGBA{72, 200, 214, 230}, 1)
						}
					}
				}
			}
		}
	}
	return true
}
func miniSample(img image.Image, x, y float64) color.NRGBA {
	b := img.Bounds()
	x = math.Max(float64(b.Min.X), math.Min(float64(b.Max.X-1), x))
	y = math.Max(float64(b.Min.Y), math.Min(float64(b.Max.Y-1), y))
	x0, y0 := int(x), int(y)
	fx, fy := x-float64(x0), y-float64(y0)
	var channels [4]float64
	for dy := 0; dy < 2; dy++ {
		for dx := 0; dx < 2; dx++ {
			c := color.NRGBAModel.Convert(img.At(min(b.Max.X-1, x0+dx), min(b.Max.Y-1, y0+dy))).(color.NRGBA)
			wx, wy := 1-fx, 1-fy
			if dx == 1 {
				wx = fx
			}
			if dy == 1 {
				wy = fy
			}
			for k, v := range [4]uint8{c.R, c.G, c.B, c.A} {
				channels[k] += float64(v) * wx * wy
			}
		}
	}
	return color.NRGBA{uint8(channels[0]), uint8(channels[1]), uint8(channels[2]), uint8(channels[3])}
}
func roundCoverage(x, y, left, top, w, h, r float64) float64 {
	dx := math.Abs(x-left-w/2) - (w/2 - r)
	dy := math.Abs(y-top-h/2) - (h/2 - r)
	d := math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0) - r
	return math.Max(0, math.Min(1, .5-d))
}
func putMiniPixel(buf []byte, w, x, y int, c color.NRGBA, coverage float64) {
	i := (y*w + x) * 4
	a := float64(c.A) / 255 * coverage
	inv := 1 - a
	buf[i] = uint8(float64(c.B)*a + float64(buf[i])*inv)
	buf[i+1] = uint8(float64(c.G)*a + float64(buf[i+1])*inv)
	buf[i+2] = uint8(float64(c.R)*a + float64(buf[i+2])*inv)
	buf[i+3] = uint8(255*a + float64(buf[i+3])*inv)
}
func (r *miniSurface) paint(s *miniSignal) {
	copy(r.Pixels, r.Base)
	g := r.Layout
	for i := 0; i < g.Count; i++ {
		height := s.Levels[i] * float64(g.SpecH)
		x := g.Start + i*(g.Bar+g.Gap)
		for row := 0; row < g.SpecH; row++ {
			cov := math.Max(0, math.Min(1, height-float64(g.SpecH-row-1)))
			if cov == 0 {
				continue
			}
			t := float64(g.SpecH-row-1) / float64(max(1, g.SpecH-1))
			c := color.NRGBA{uint8(45 + 140*t), uint8(193 + 36*t), uint8(212 + 36*t), uint8(240 - 60*t)}
			if r.light {
				c = color.NRGBA{uint8(12 + 26*t), uint8(109 + 34*t), uint8(143 + 28*t), 245}
			}
			if r.settings.Color == "theme" {
				c = color.NRGBA{uint8(112 + 76*t), uint8(193 - 40*t), 240, 235}
			}
			for k := 0; k < g.Bar; k++ {
				putMiniPixel(r.Pixels, g.W, x+k, g.SpecY+row, c, cov)
			}
		}
		peak := int(math.Round(s.Peaks[i] * float64(g.SpecH)))
		if peak > int(math.Ceil(height))+1 && peak > 0 {
			y := g.SpecY + g.SpecH - min(g.SpecH, peak)
			for k := 0; k < g.Bar; k++ {
				putMiniPixel(r.Pixels, g.W, x+k, y, color.NRGBA{119, 209, 231, 100}, 1)
			}
		}
	}
}
