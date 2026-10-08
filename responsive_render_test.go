package main

import (
	"fmt"
	"image"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDetectAdaptiveLayoutModes(t *testing.T) {
	tests := []struct {
		w, h int
		want layoutMode
	}{
		{160, 45, modeFull},
		{120, 32, modeFull},
		{100, 32, modeMedium},
		{80, 24, modeMedium},
		{72, 22, modeMedium},
		{71, 45, modeCompact},
		{50, 25, modeCompact},
		{60, 18, modeCompact},
		{44, 45, modeUltra},
		{60, 15, modeUltra},
	}
	for _, tt := range tests {
		if got := detectLayoutMode(tt.w, tt.h); got != tt.want {
			t.Errorf("detectLayoutMode(%d, %d) = %s, want %s", tt.w, tt.h, got, tt.want)
		}
	}
}

func TestAdaptiveGeometryBounds(t *testing.T) {
	for w := 38; w <= 200; w++ {
		for h := 16; h <= 100; h++ {
			g := layout(w, h, 52)
			if g.narrow != (w < 72) || g.short != (h < 23) {
				t.Fatalf("width/height flags changed at %dx%d: %+v", w, h, g)
			}
			if g.artW < 1 || g.artH < 1 || g.artX < 0 || g.artX+g.artW > g.w ||
				g.infoX < 0 || g.infoX >= g.w || g.infoY < 0 || g.infoY >= g.h {
				t.Fatalf("art/info bounds at %dx%d: %+v", w, h, g)
			}
			if g.mode != modeUltra && g.infoX <= g.artX+g.artW {
				t.Fatalf("metadata overlaps artwork at %dx%d: %+v", w, h, g)
			}
			if g.spectrumX < 0 || g.spectrumW < 1 || g.spectrumX+g.spectrumW > g.w ||
				g.spectrumTop < 3+g.artH || g.spectrumHeight < 1 ||
				g.spectrumBottom != g.spectrumTop+g.spectrumHeight || g.spectrumBottom > h {
				t.Fatalf("spectrum bounds at %dx%d: %+v", w, h, g)
			}
			if g.showLists {
				if g.listY <= g.spectrumBottom || g.listY >= h || g.listRows < 0 ||
					g.listY+1+g.listRows+g.footerRows > h {
					t.Fatalf("list bounds at %dx%d: %+v", w, h, g)
				}
			} else if g.listY != 0 || g.listRows != 0 || g.footerRows != 0 {
				t.Fatalf("hidden regions reserved space at %dx%d: %+v", w, h, g)
			}
		}
	}
}

func TestAdaptiveModeGeometryIntent(t *testing.T) {
	if g := layout(60, 45, 52); g.mode != modeCompact || g.showLists || g.spectrumHeight != 14 {
		t.Fatalf("tall narrow pane should prioritize now-playing view: %+v", g)
	}
	if g := layout(80, 24, 52); g.mode != modeMedium || !g.showLists || !g.singleList {
		t.Fatalf("medium pane should keep one list panel: %+v", g)
	}
	if g := layout(160, 45, 52); g.mode != modeFull || !g.showLists || g.singleList {
		t.Fatalf("fullscreen should keep the full layout: %+v", g)
	}
	if g := layout(44, 45, 52); g.mode != modeUltra || g.showLists || g.showFooter || g.showStatus || g.spectrumHeight != g.h-g.spectrumTop-1 {
		t.Fatalf("ultra compact pane should hide secondary regions: %+v", g)
	}
}

func TestResponsiveFrameSpectrumAndLists(t *testing.T) {
	m := newModel(defaultConfig(), nil, "", "")
	m.current = Track{Title: strings.Repeat("Việt 日本 ", 12), Artist: "Artist", Duration: 180}
	m.results = []Track{{Title: "Result", Duration: 120}}
	m.queue = []Track{{Title: "Queued", Duration: 120}}
	m.bars = []float64{1, 1, 1}
	m.notice = "STATUS"
	for _, wh := range [][2]int{{1, 1}, {37, 15}, {38, 16}, {38, 60}, {55, 45}, {71, 32}, {72, 32}, {120, 16}, {120, 22}, {120, 23}, {120, 32}, {160, 60}} {
		for _, style := range []string{"spectrum", "mirror"} {
			for _, focus := range []bool{false, true} {
				m.cfg.VisualizerStyle, m.queueFocus = style, focus
				g := layout(wh[0], wh[1], 52)
				rows := m.frame(g)
				if len(rows) != g.h {
					t.Fatal("frame height")
				}
				for y, s := range rows {
					if ansi.StringWidth(s) > g.w {
						t.Fatalf("overflow %v row %d", wh, y)
					}
				}
				if wh[0] < 38 || wh[1] < 16 {
					continue
				}
				for y := g.spectrumTop; y < g.spectrumBottom; y++ {
					if !strings.Contains(rows[y], "█") {
						t.Fatalf("missing full-height spectrum: %v %s row %d", wh, style, y)
					}
				}
				if g.showLists {
					if strings.Contains(rows[g.listY], "█") || !strings.Contains(rows[g.h-2], "STATUS") {
						t.Fatalf("list/status overwritten: %v", wh)
					}
				} else if strings.Contains(strings.Join(rows, "\n"), "SEARCH RESULTS") {
					t.Fatalf("hidden list rendered in small mode: %v", wh)
				}
			}
		}
	}
}

func TestSpectrumHeightAndMirrorCoverage(t *testing.T) {
	for _, h := range []int{1, 2, 3, 9, 28, 49} {
		for _, v := range []float64{0, .2, .5, .8, 1} {
			lastPart := -1
			for y := 0; y < h; y++ {
				line := ansi.Strip(spectrumLine([]float64{v}, 4, h, y, "spectrum"))
				part := strings.IndexRune(" ▁▂▃▄▅▆▇█", []rune(line)[0])
				if part < lastPart {
					t.Fatalf("spectrum did not grow bottom-up: h=%d v=%v", h, v)
				}
				lastPart = part
				upper := spectrumLine([]float64{v}, 4, h, y, "mirror")
				lower := spectrumLine([]float64{v}, 4, h, h-1-y, "mirror")
				coverage := func(s string) int {
					r := []rune(ansi.Strip(s))[0]
					p := 0
					for i, glyph := range []rune(" ▁▂▃▄▅▆▇█") {
						if r == glyph {
							p = i
						}
					}
					if strings.Contains(s, "\x1b[7m") {
						p = 8 - p
					}
					return p
				}
				if coverage(upper) != coverage(lower) {
					t.Fatalf("asymmetric mirror h=%d v=%v row=%d", h, v, y)
				}
				if v == 1 && (coverage(upper) != 8 || coverage(lower) != 8) {
					t.Fatal("mirror did not reach bounds")
				}
			}
		}
	}
	for _, args := range [][3]int{{0, 4, 0}, {8, 0, 0}, {8, 4, -1}, {8, 4, 4}} {
		if spectrumLine(nil, args[0], args[1], args[2], "spectrum") != "" {
			t.Fatal("invalid bounds rendered")
		}
	}
}

func renderCapture(t *testing.T, m *model, f *os.File) string {
	t.Helper()
	if err := f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	m.draw()
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIncrementalSpectrumClearsBeyondArtwork(t *testing.T) {
	for _, wh := range [][2]int{{38, 60}, {55, 45}, {120, 45}, {120, 16}} {
		m := newModel(defaultConfig(), nil, "", "")
		f, err := os.CreateTemp(t.TempDir(), "draw")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		m.output, m.width, m.height = f, wh[0], wh[1]
		m.cfg.ImageMode = "sixel"
		m.art = image.NewRGBA(image.Rect(0, 0, 16, 9))
		m.bars = []float64{1, 1}
		renderCapture(t, &m, f)
		m.bars = nil
		out := renderCapture(t, &m, f)
		if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1bP") {
			t.Fatal("artwork redrawn on audio frame")
		}
		g := layout(m.width, m.height, 52)
		for y := g.spectrumTop; y < g.spectrumBottom; y++ {
			cursor := fmt.Sprintf("\x1b[%d;1H", y+1)
			start := strings.Index(out, cursor)
			if start < 0 {
				t.Fatalf("uncleared row %d at %v", y, wh)
			}
			end := strings.Index(out[start:], "\x1b[K")
			if end < 0 || strings.ContainsAny(ansi.Strip(out[start:start+end]), "▁▂▃▄▅▆▇█") {
				t.Fatal("old bars remain")
			}
		}
	}
}

func TestResizeRedrawBoundsAndArtwork(t *testing.T) {
	m := newModel(defaultConfig(), nil, "", "")
	f, err := os.CreateTemp(t.TempDir(), "resize")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m.output = f
	m.cfg.ImageMode = "sixel"
	m.art = image.NewRGBA(image.Rect(0, 0, 16, 9))
	m.bars = []float64{.2, .8, 1}
	cursorRE := regexp.MustCompile("\\x1b\\[([0-9]+);([0-9]+)H")
	for _, wh := range [][2]int{{120, 45}, {55, 45}, {38, 45}, {38, 16}, {120, 16}, {120, 23}, {55, 60}, {120, 60}, {37, 15}, {38, 16}, {120, 45}} {
		m.width, m.height = wh[0], wh[1]
		out := renderCapture(t, &m, f)
		if !strings.Contains(out, "\x1b[2J") {
			t.Fatalf("resize not cleared: %v", wh)
		}
		if wh[0] >= 38 && wh[1] >= 16 && !strings.Contains(out, "\x1bP0;1q") {
			t.Fatalf("art not restored: %v", wh)
		}
		for _, match := range cursorRE.FindAllString(out, -1) {
			var row, col int
			fmt.Sscanf(match, "\x1b[%d;%dH", &row, &col)
			if row < 1 || row > wh[1] || col < 1 || col > wh[0] {
				t.Fatalf("out of bounds cursor %q at %v", match, wh)
			}
		}
		if next := renderCapture(t, &m, f); strings.Contains(next, "\x1b[2J") || strings.Contains(next, "\x1bP") {
			t.Fatal("stable size forced art repaint")
		}
	}
}
