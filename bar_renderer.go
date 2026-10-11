package main

import (
	"fmt"
	"math"
	"os"
	"strings"
)

type barCaps struct{ TrueColor, Color, Unicode bool }

func detectBarCaps() barCaps {
	term := strings.ToLower(os.Getenv("TERM"))
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	if os.Getenv("WT_SESSION") != "" {
		term = "windows-terminal"
	}
	return barCaps{os.Getenv("WT_SESSION") != "" || strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit"), os.Getenv("NO_COLOR") == "" && term != "dumb", term != "dumb"}
}

type barCanvas struct {
	values       [96]float64
	count, width int
	barWidth     int
	startX       int
	preset       barPreset
	caps         barCaps
	colors       [24]string
}

// Fixed cell stride; any spare columns belong to the outer margins only.
// Never duplicate frequency bands to fill a terminal wider than the input.
func spectrumGeometry(width, bands int) (count, start, span int) {
	if width <= 0 || bands <= 0 {
		return 0, 0, 0
	}
	count = min(96, min(bands, (width+1)/2))
	span = count*2 - 1
	start = (width - span) / 2
	return
}

func makeBarCanvas(src []float64, w int, p barPreset, caps barCaps) barCanvas {
	c := barCanvas{width: w, preset: p, caps: caps}
	c.barWidth = 1
	c.count, c.startX, _ = spectrumGeometry(w, len(src))
	if c.count == 0 {
		return c
	}
	// RMS retains narrow-band energy; a modest peak contribution keeps tonal
	// transients visible without letting a lone spike dominate the whole group.
	for i := 0; i < c.count; i++ {
		lo, hi := i*len(src)/c.count, (i+1)*len(src)/c.count
		c.values[i] = aggregateBands(src[lo:hi])
	}
	for i := range c.colors {
		if !caps.Color {
			continue
		}
		if !caps.TrueColor {
			if i < 12 {
				c.colors[i] = "\x1b[36m"
			} else {
				c.colors[i] = "\x1b[37m"
			}
			continue
		}
		t := float64(i) / 23
		t = t * t * (3 - 2*t)
		var rgb [3]int
		for k := range rgb {
			rgb[k] = int(float64(p.Bottom[k])*(1-t) + float64(p.Top[k])*t)
		}
		c.colors[i] = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", rgb[0], rgb[1], rgb[2])
	}
	return c
}
func (c *barCanvas) line(h, row int, style string) string {
	if c.width < 1 || h < 1 || row < 0 || row >= h {
		return ""
	}
	var out strings.Builder
	out.Grow(c.width * 6)
	cursor := 0
	for i := 0; i < c.count; i++ {
		x := c.startX + i*2
		out.WriteString(strings.Repeat(" ", max(0, x-cursor)))
		level := c.values[i] * float64(h)
		distance := float64(h-row) - .5
		if style == "mirror" {
			distance = math.Abs(float64(row)+.5-float64(h)/2) * 2
		}
		glyph := ' '
		coverage := level - float64(h-1-row)
		if style == "mirror" {
			top, bottom := (float64(h)-level)/2, (float64(h)+level)/2
			coverage = math.Min(float64(row+1), bottom) - math.Max(float64(row), top)
		}
		part := clamp(int(math.Round(coverage*8)), 0, 8)
		if part > 0 {
			glyph = []rune(" ▁▂▃▄▅▆▇█")[part]
			// Upper half block has no background color and keeps mirrored tips
			// symmetric without the old inverse-video rectangle.
			if style == "mirror" && part < 8 {
				glyph = '▄'
				if row >= h/2 {
					glyph = '▀'
				}
			}
			if !c.caps.Unicode {
				glyph = '#'
			}
			fraction := math.Min(1, math.Max(0, distance/math.Max(.5, level)))
			out.WriteString(c.colors[clamp(int(fraction*23), 0, 23)])
		}
		for cell := 0; cell < c.barWidth; cell++ {
			out.WriteRune(glyph)
		}
		cursor = x + c.barWidth
	}
	out.WriteString(strings.Repeat(" ", max(0, c.width-cursor)))
	out.WriteString(reset)
	return out.String()
}

func aggregateBands(src []float64) float64 {
	if len(src) == 0 {
		return 0
	}
	sum, peak := 0., 0.
	for _, v := range src {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		v = math.Max(0, math.Min(1, v))
		sum += v * v
		peak = math.Max(peak, v)
	}
	return .65*math.Sqrt(sum/float64(len(src))) + .35*peak
}
