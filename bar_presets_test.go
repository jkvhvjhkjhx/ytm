package main

import (
	"encoding/json"
	"fmt"
	"github.com/BurntSushi/toml"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBarConfigCompatibility(t *testing.T) {
	cfg := defaultConfig()
	_, err := toml.Decode("volume=45\nvisualizer_style='mirror'", &cfg)
	if err != nil || cfg.VisualizerEffect != "glass" || cfg.VisualizerStyle != "mirror" {
		t.Fatal(cfg, err)
	}
	if presetFor("invalid").ID != "glass" {
		t.Fatal("invalid preset fallback")
	}
	t.Setenv("APPDATA", t.TempDir())
	cfg.VisualizerEffect = "mist"
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	restored, err := loadConfig()
	if err != nil || restored.VisualizerEffect != "mist" {
		t.Fatal(restored, err)
	}
}
func TestBarBoundsSilenceAndBackground(t *testing.T) {
	src := make([]float64, 96)
	for i := range src {
		src[i] = .7
	}
	for _, p := range barPresets {
		for _, w := range []int{1, 2, 3, 33, 55, 80, 120, 250} {
			for _, h := range []int{1, 2, 3, 14, 45} {
				for _, caps := range []barCaps{{true, true, true}, {false, true, true}, {false, false, false}} {
					c := makeBarCanvas(src, w, p, caps)
					if c.count > len(src) || c.count > 96 || (c.count > 1 && c.count*(c.barWidth+1)-1 > w) {
						t.Fatal("invalid count", c.count, w)
					}
					for _, style := range []string{"spectrum", "mirror"} {
						for row := 0; row < h; row++ {
							line := c.line(h, row, style)
							if ansi.StringWidth(line) != w || strings.Contains(line, "\x1b[48;") || strings.Contains(line, "\x1b[7m") {
								t.Fatal("bounds/background", line)
							}
							if !caps.Color && strings.Contains(line, "38;") {
								t.Fatal("color fallback")
							}
						}
					}
				}
			}
		}
	}
	c := makeBarCanvas(make([]float64, 96), 70, presetFor("glass"), barCaps{true, true, true})
	for row := 0; row < 12; row++ {
		if strings.TrimSpace(ansi.Strip(c.line(12, row, "spectrum"))) != "" {
			t.Fatal("silent bars")
		}
	}
}
func TestBarContinuityAndMirror(t *testing.T) {
	c := makeBarCanvas([]float64{.4, .7, .6, 1, .3}, 40, presetFor("glass"), barCaps{true, true, true})
	lines := make([][]rune, 14)
	for row := range lines {
		lines[row] = []rune(ansi.Strip(c.line(14, row, "spectrum")))
	}
	for x := 0; x < 40; x++ {
		seen := false
		for row := 0; row < 14; row++ {
			if lines[row][x] != ' ' {
				seen = true
			}
			if seen && lines[row][x] == ' ' {
				t.Fatal("broken column")
			}
		}
	}
	for row := 0; row < 14; row++ {
		a := []rune(ansi.Strip(c.line(14, row, "mirror")))
		b := []rune(ansi.Strip(c.line(14, 13-row, "mirror")))
		for x := range a {
			if (a[x] == ' ') != (b[x] == ' ') {
				t.Fatal("asymmetric mirror")
			}
		}
	}
	if c.colors[0] == c.colors[23] {
		t.Fatal("gradient absent")
	}
}
func TestPresetPopupIsolationPersistenceAndResize(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	m := newModel(defaultConfig(), nil, "", "")
	m.output = nil
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyF4})
	m = next.(model)
	if !m.presetOpen {
		t.Fatal("F4")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.volume != 70 || m.cfg.VisualizerEffect != "soft_fade" {
		t.Fatal("hotkey leaked")
	}
	cfg, err := loadConfig()
	if err != nil || cfg.VisualizerEffect != "soft_fade" {
		t.Fatal("not persisted", err)
	}
	f, err := os.CreateTemp(t.TempDir(), "popup")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m.output = f
	for _, wh := range [][2]int{{38, 16}, {80, 32}, {160, 45}, {38, 16}} {
		m.width, m.height = wh[0], wh[1]
		out := renderCapture(t, &m, f)
		if !strings.Contains(out, "VISUALIZER EFFECT") {
			t.Fatal("missing popup")
		}
	}
	m.presetKey(tea.KeyMsg{Type: tea.KeyEsc})
	if out := renderCapture(t, &m, f); !strings.Contains(out, "\x1b[2J") || strings.Contains(out, "VISUALIZER EFFECT") {
		t.Fatal("stale popup")
	}
}
func TestPresetElapsedSmoothing(t *testing.T) {
	start := time.Now()
	p := presetFor("glass")
	run := func(fps int) float64 {
		s := spectrumState{Last: start}
		for i := 1; i <= fps; i++ {
			now := start.Add(time.Duration(float64(time.Second) * float64(i) / float64(fps)))
			s.Push([]float64{.7}, now)
			s.StepPreset(now, true, p)
		}
		return s.Levels[0]
	}
	if math.Abs(run(24)-run(60)) > .001 {
		t.Fatal("frame rate dependent envelope")
	}
}

// Optional visual audit exports actual renderer output, not a substitute renderer.
// The fixed envelope is a labelled test fixture, never used by the application.
func TestExportBarPreview(t *testing.T) {
	dir := os.Getenv("YTM_BAR_PREVIEW")
	if dir == "" {
		t.Skip("optional visual audit")
	}
	bands := make([]float64, 96)
	for i := range bands {
		x := float64(i) / 96
		bands[i] = .12 + .55*math.Exp(-math.Pow((x-.3)/.2, 2)) + .4*math.Exp(-math.Pow((x-.8)/.12, 2))
	}
	previews := map[string][]string{}
	for _, p := range barPresets {
		c := makeBarCanvas(bands, 76, p, barCaps{true, true, true})
		var tint []string
		if p.ID == "classic" {
			tint = spectrumTint()
		}
		for r := 0; r < 12; r++ {
			line := c.line(12, r, "spectrum")
			if p.ID == "classic" {
				line = spectrumLine(bands, 76, 12, r, "spectrum", tint)
			}
			previews[p.Name] = append(previews[p.Name], line)
		}
	}
	data, err := json.Marshal(previews)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "bar-preview.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func BenchmarkGlassBars(b *testing.B) {
	src := make([]float64, 96)
	for i := range src {
		src[i] = .7
	}
	p := presetFor("glass")
	for i := 0; i < b.N; i++ {
		c := makeBarCanvas(src, 120, p, barCaps{true, true, true})
		for row := 0; row < 14; row++ {
			c.line(14, row, "spectrum")
		}
	}
}

func TestAggregationComparison(t *testing.T) {
	cases := map[string][]float64{"narrow_peak": {0, 0, .8, 0, 0, 0}, "broadband": {.35, .4, .45, .4, .35, .4}, "noise": {.004, .009, .006, .003, .008, .002}, "bass": {.9, .7, .2, .06, .03, .01}}
	for name, src := range cases {
		mean, sum, peak := 0., 0., 0.
		for _, v := range src {
			mean += v
			sum += v * v
			peak = math.Max(peak, v)
		}
		mean /= float64(len(src))
		rms := math.Sqrt(sum / float64(len(src)))
		got := aggregateBands(src)
		t.Logf("%s mean=%.3f rms=%.3f peak=%.3f chosen=%.3f", name, mean, rms, peak, got)
		if got < rms-1e-9 || got > peak+1e-9 {
			t.Fatal("invalid aggregation")
		}
		if name == "narrow_peak" && got < mean*2 {
			t.Fatal("narrow peak suppressed")
		}
		if name == "noise" && got > .01 {
			t.Fatal("noise exaggerated")
		}
	}
}

func TestAdaptiveGainQuietSignalAndSilence(t *testing.T) {
	start := time.Now()
	s := spectrumState{Last: start}
	p := presetFor("glass")
	for i := 1; i <= 120; i++ {
		now := start.Add(time.Duration(i) * time.Second / 24)
		s.Push([]float64{0, .08, .12, .08, 0}, now)
		s.StepPreset(now, true, p)
	}
	if s.Gain <= 1.5 || s.Gain > 3 || s.Levels[2] < .23 || s.Levels[0] > .002 {
		t.Fatalf("weak signal/zero bands: gain %.3f levels %v", s.Gain, s.Levels)
	}
	for i := 121; i <= 300; i++ {
		now := start.Add(time.Duration(i) * time.Second / 24)
		s.Push([]float64{0, 0, 0, 0, 0}, now)
		s.StepPreset(now, true, p)
	}
	for _, v := range s.Levels {
		if v != 0 {
			t.Fatal("silence creates activity")
		}
	}
}

func TestSingleCellBarsVisualRegression(t *testing.T) {
	for _, wh := range [][2]int{{33, 3}, {45, 8}, {70, 12}, {100, 14}, {160, 12}} {
		src := make([]float64, 96)
		for i := range src {
			src[i] = .55
		}
		c := makeBarCanvas(src, wh[0], presetFor("glass"), barCaps{true, true, true})
		if c.barWidth != 1 {
			t.Fatal("bar width must be exactly one cell")
		}
		bottom := []rune(ansi.Strip(c.line(wh[1], wh[1]-1, "spectrum")))
		occupied := 0
		run := 0
		for _, r := range append(bottom, ' ') {
			if r != ' ' {
				occupied++
				run++
			} else if run > 0 {
				if run != c.barWidth {
					t.Fatal("broken bar width", run)
				}
				run = 0
			}
		}
		if occupied != c.count {
			t.Fatal("incorrect occupied cell count", wh, occupied)
		}
		// Every bar should reach roughly 55% of the canvas, using fractional tips.
		height := 0
		for row := 0; row < wh[1]; row++ {
			if strings.TrimSpace(ansi.Strip(c.line(wh[1], row, "spectrum"))) != "" {
				height++
			}
		}
		if height < int(float64(wh[1])*.5) {
			t.Fatal("flattened bars")
		}
	}
	c := makeBarCanvas([]float64{.3, .3, .3}, 8, presetFor("glass"), barCaps{true, true, true})
	if got := ansi.Strip(c.line(4, 2, "spectrum")); got != " ▂ ▂ ▂  " {
		t.Fatalf("fractional tip snapshot: %q", got)
	}
	if got := ansi.Strip(c.line(4, 3, "spectrum")); got != " █ █ █  " {
		t.Fatalf("bar spacing snapshot: %q", got)
	}
}

func TestCapturedAudioRenderer(t *testing.T) {
	data, err := os.ReadFile("testdata/cava-live-spectrum.json")
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]float64
	if err = json.Unmarshal(data, &frames); err != nil {
		t.Fatal(err)
	}
	s := spectrumState{}
	start := time.Now()
	p := presetFor("glass")
	for i, f := range frames {
		m := measureSpectrum(f)
		if m.Bands != 96 || m.Peak <= .025 || m.RMS <= 0 {
			t.Fatal("invalid live capture", m)
		}
		now := start.Add(time.Duration(i) * time.Second / 24)
		s.Push(f, now)
		s.StepPreset(now, true, p)
	}
	t.Logf("live frame raw=%+v display=%+v gain=%.3f", s.Metrics, measureSpectrum(s.Levels), s.Gain)
	previews := map[string][]string{}
	for _, wh := range [][2]int{{33, 3}, {50, 8}, {80, 12}, {120, 14}, {160, 12}} {
		c := makeBarCanvas(s.Levels, wh[0], p, barCaps{true, true, true})
		used := 0
		name := fmt.Sprintf("Live CAVA %dx%d", wh[0], wh[1])
		for row := 0; row < wh[1]; row++ {
			line := c.line(wh[1], row, "spectrum")
			previews[name] = append(previews[name], line)
			if ansi.StringWidth(line) != wh[0] {
				t.Fatal("overflow")
			}
			if strings.TrimSpace(ansi.Strip(line)) != "" {
				used++
			}
		}
		if used < max(1, wh[1]/4) {
			t.Fatalf("real audio nearly flat at %v: %d rows", wh, used)
		}
	}
	if dir := os.Getenv("YTM_BAR_PREVIEW"); dir != "" {
		data, _ := json.Marshal(previews)
		if err = os.WriteFile(filepath.Join(dir, "bar-live-preview.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUniformCenteredGeometryEveryPreset(t *testing.T) {
	// Odd/even widths, sub-cell boundary cases, and widths exceeding 96 bands.
	for _, bands := range []int{0, 1, 3, 96} {
		src := make([]float64, bands)
		for i := range src {
			src[i] = 1
		}
		for w := 1; w <= 260; w++ {
			n := min(96, min(bands, (w+1)/2))
			span := max(0, n*2-1)
			start := (w - span) / 2
			expected := strings.Repeat(" ", w)
			if n > 0 {
				expected = strings.Repeat(" ", start) + strings.TrimSuffix(strings.Repeat("█ ", n), " ") + strings.Repeat(" ", w-start-span)
			}
			for _, p := range barPresets {
				c := makeBarCanvas(src, w, p, barCaps{true, true, true})
				for _, style := range []string{"spectrum", "mirror"} {
					line := c.line(6, 3, style)
					if p.ID == "classic" {
						line = spectrumLine(src, w, 6, 3, style, spectrumTint())
					}
					if ansi.StringWidth(line) != w || ansi.Strip(line) != expected {
						t.Fatalf("spacing/alignment %s %s w=%d bands=%d: %q want %q", p.ID, style, w, bands, ansi.Strip(line), expected)
					}
				}
			}
		}
	}
}

func TestGeometryDoesNotReorderFrequencies(t *testing.T) {
	src := make([]float64, 96)
	src[2] = .8
	src[48] = .7
	src[94] = .9
	for _, w := range []int{33, 50, 80, 120, 160, 250} {
		c := makeBarCanvas(src, w, presetFor("glass"), barCaps{true, true, true})
		for i := 0; i < c.count; i++ {
			lo, hi := i*len(src)/c.count, (i+1)*len(src)/c.count
			if c.values[i] != aggregateBands(src[lo:hi]) {
				t.Fatal("frequency order/aggregation changed")
			}
		}
	}
}
