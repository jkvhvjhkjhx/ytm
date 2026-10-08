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

func TestResponsiveGeometryBounds(t *testing.T) {
	for w := 38; w <= 200; w++ {
		for h := 16; h <= 100; h++ {
			g := layout(w, h, 52)
			if g.narrow != (w < 72) || g.short != (h < 23) {
				t.Fatalf("width/height coupled at %dx%d: %+v", w, h, g)
			}
			if g.artW < 1 || g.artH < 1 || 2+g.artW >= g.infoX || g.infoX >= g.w || 3+g.artH >= g.listY {
				t.Fatalf("art bounds at %dx%d: %+v", w, h, g)
			}
			if g.spectrumX < 0 || g.spectrumW < 1 || g.spectrumX+g.spectrumW > g.w ||
				g.spectrumTop < 3+g.infoH || g.spectrumHeight < 1 ||
				g.spectrumBottom != g.listY-1 || g.spectrumHeight != g.spectrumBottom-g.spectrumTop || g.listY >= h-3 {
				t.Fatalf("spectrum/list bounds at %dx%d: %+v", w, h, g)
			}
			if g.spectrumTop < 3+g.artH && g.spectrumX < 2+g.artW {
				t.Fatalf("spectrum overlaps art at %dx%d: %+v", w, h, g)
			}
		}
	}
}

func TestTallNarrowPaneUsesRemainingHeight(t *testing.T) {
	for _, w := range []int{38, 55, 71} {
		var previous geometry
		for _, h := range []int{32, 45, 60, 90} {
			g := layout(w, h, 52)
			if !g.narrow || g.short || g.artH > 7 || g.spectrumHeight < h-23 || g.listY != h-10 {
				t.Fatalf("unused tall-pane space at %dx%d: %+v", w, h, g)
			}
			if previous.h > 0 && g.spectrumHeight-previous.spectrumHeight != h-previous.h {
				t.Fatalf("extra rows not assigned to spectrum: %+v -> %+v", previous, g)
			}
			previous = g
		}
	}
	for _, wh := range [][2]int{{80, 24}, {120, 32}, {160, 45}} {
		g := layout(wh[0], wh[1], 52)
		base := min(14, max(8, wh[1]-17))
		oldList := max(6+base, wh[1]-10)
		if g.artW != max(20, wh[0]/2-3) || g.artH != max(base, oldList-6) || g.listY != oldList {
			t.Fatalf("wide artwork/list moved: %+v", g)
		}
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
				if strings.Contains(rows[g.listY], "█") || !strings.Contains(rows[g.h-2], "STATUS") {
					t.Fatalf("list/status overwritten: %v", wh)
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
			x := 0
			if y < 3+g.artH {
				x = g.infoX
			}
			cursor := fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)
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
	cursorRE := regexp.MustCompile("\x1b\\[([0-9]+);([0-9]+)H")
	for _, wh := range [][2]int{{120, 45}, {55, 45}, {38, 45}, {38, 16}, {120, 16}, {120, 23}, {55, 60}, {120, 60}, {37, 15}, {38, 16}, {120, 45}} {
		// Direct geometry changes also exercise the safety net independently
		// of WindowSizeMsg's explicit rendered=nil invalidation.
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
