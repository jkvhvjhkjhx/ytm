package main

import (
	"bytes"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/sixel"
	"golang.org/x/sys/windows"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unsafe"
)

const reset = "\x1b[0m"
const dim = "\x1b[38;2;123;128;153m"
const bright = "\x1b[38;2;228;231;243m"
const accent = "\x1b[38;2;112;214;232m"
const purple = "\x1b[38;2;198;160;246m"

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(s))
}
func truncate(s string, n int) string {
	if n < 1 {
		return ""
	}
	return ansi.Truncate(clean(s), n, "…")
}
func pad(s string, n int) string { return s + strings.Repeat(" ", max(0, n-ansi.StringWidth(s))) }

type geometry struct {
	w, h, artW, artH, infoX, listY int
	infoH                          int
	spectrumX, spectrumW           int
	spectrumTop, spectrumBottom    int // terminal rows, bottom exclusive
	spectrumHeight                 int
	narrow, short                  bool
}

func layout(w, h, size int) geometry {
	g := geometry{w: max(1, w-1), h: max(1, h), narrow: w < 72, short: h < 23}
	if w < 38 || h < 16 {
		g.listY = max(0, g.h-3)
		return g
	}
	// Width chooses the columns; height independently chooses metadata density.
	if g.narrow {
		g.artW = min(24, max(10, w/3))
	} else {
		g.artW = max(20, w/2-3)
	}
	g.infoX = g.artW + 6
	g.infoH = 7
	if g.short {
		g.infoH = 5
		g.artH = min(7, max(3, h-12))
		g.listY = 6 + g.artH
	} else {
		baseH := min(14, max(8, h-17))
		if g.narrow {
			// A tall pane gives its surplus rows to the spectrum, not the image.
			g.artH = 7
			g.listY = max(14, h-10)
		} else {
			// Preserve the approved wide layout's artwork and list geometry.
			g.listY = max(6+baseH, h-10)
			g.artH = max(baseH, g.listY-6)
		}
	}
	// One blank row before the list; status/footer occupy the final two rows.
	g.listY = min(g.listY, h-5)
	g.spectrumBottom = g.listY - 1
	if g.narrow || g.short || g.artH <= 9 {
		// Once below both artwork and metadata the analyzer can span the pane.
		g.spectrumX, g.spectrumW = 2, g.w-4
		g.spectrumTop = max(3+g.artH, 3+g.infoH)
		if g.narrow && !g.short {
			g.spectrumTop++
		}
	} else {
		g.spectrumX, g.spectrumW = g.infoX, g.w-g.infoX-1
		g.spectrumTop = 3 + g.infoH + 2
	}
	g.spectrumTop = min(g.spectrumTop, g.spectrumBottom)
	g.spectrumHeight = max(0, g.spectrumBottom-g.spectrumTop)
	return g
}
func (m *model) frame(g geometry) []string {
	spectrumColor := spectrumTint()
	rows := make([]string, g.h)
	put := func(y int, s string) {
		if y >= 0 && y < len(rows) {
			rows[y] = s
		}
	}
	put(0, " "+purple+"Y T M"+reset+dim+"   /   YOUTUBE MUSIC"+reset)
	put(1, " "+dim+strings.Repeat("─", max(0, g.w-2))+reset)
	state := "STOPPED"
	if m.loading {
		state = "BUFFERING " + []string{"·", "··", "···"}[(time.Now().UnixMilli()/400)%3]
	} else if !m.stopped {
		state = "PLAYING"
		if m.snapshot.Paused {
			state = "PAUSED"
		}
	}
	infoW := max(1, g.w-g.infoX-1)
	title := m.current.Title
	if title == "" {
		title = "Your next favourite track"
	}
	artist := m.current.Artist
	if artist == "" {
		artist = "Search YouTube to start listening"
	}
	dur := m.snapshot.Duration
	if dur <= 0 {
		dur = m.current.Duration
	}
	prog := 0.
	if dur > 0 {
		prog = math.Max(0, math.Min(1, m.snapshot.Time/dur))
	}
	bw := max(1, infoW-14)
	filled := int(prog * float64(bw))
	info := map[int]string{0: accent + state + reset, 2: bright + truncate(title, infoW) + reset, 3: dim + truncate(artist, infoW) + reset, 5: accent + strings.Repeat("━", filled) + dim + strings.Repeat("─", bw-filled) + reset,
		6: dim + fmt.Sprintf("%s / %s   VOL %d%%", humanDuration(m.snapshot.Time), humanDuration(dur), m.volume) + reset}
	if g.short {
		info = map[int]string{0: accent + state + reset, 1: bright + truncate(title, infoW) + reset, 2: dim + truncate(artist, infoW) + reset, 4: accent + fmt.Sprintf("%s / %s · %d%%", humanDuration(m.snapshot.Time), humanDuration(dur), m.volume) + reset}
	}
	for r := 0; r < g.infoH; r++ {
		put(3+r, strings.Repeat(" ", g.infoX)+info[r])
	}
	for r := 0; r < g.spectrumHeight; r++ {
		put(g.spectrumTop+r, strings.Repeat(" ", g.spectrumX)+
			spectrumLine(m.bars, g.spectrumW, g.spectrumHeight, r, m.cfg.VisualizerStyle, spectrumColor))
	}
	searchLabel := "SEARCH RESULTS"
	queueLabel := m.playlistLabel()
	if m.queueFocus {
		queueLabel = "› " + queueLabel
	} else {
		searchLabel = "› " + searchLabel
	}
	wide := g.w >= 90
	half := (g.w - 5) / 2
	if wide {
		put(g.listY, " "+accent+pad(searchLabel, half)+dim+" │ "+purple+queueLabel+reset)
	} else {
		label := searchLabel
		if m.queueFocus {
			label = queueLabel
		}
		put(g.listY, " "+accent+label+reset)
	}
	// The main screen no longer reserves rows for persistent key hints. Those
	// rows are available to the results/playlist lists; help remains in T's
	// separate overlay.
	count := max(0, g.h-g.listY-3)
	list := func(ts []Track, selected int, focus, playlist bool, width int) []string {
		visible := count
		if playlist {
			visible = max(0, count-1)
		}
		out := make([]string, visible)
		start := max(0, selected-visible+1)
		for r := 0; r < visible; r++ {
			i := start + r
			if i >= len(ts) {
				break
			}
			t := ts[i]
			if playlist {
				out[r] = m.playlistTrackLine(t, i, len(ts), width, i == selected && focus)
				continue
			}
			mark := "  "
			col := dim
			if i == selected && focus {
				mark = "› "
				col = bright
			}
			text := fmt.Sprintf("%s%02d %s", mark, i+1, truncate(t.Title, max(1, width-13)))
			out[r] = col + pad(text, max(1, width-7)) + dim + humanDuration(t.Duration) + reset
		}
		return out
	}
	if wide {
		left := list(m.results, m.cursor, !m.queueFocus, false, half)
		right := m.playlistRows(list(m.queue, m.queueCursor, m.queueFocus, true, half), count, half)
		for r := 0; r < count; r++ {
			put(g.listY+1+r, " "+pad(left[r], half)+dim+" │ "+right[r])
		}
	} else {
		ts, sel := m.results, m.cursor
		if m.queueFocus {
			ts, sel = m.queue, m.queueCursor
		}
		entries := list(ts, sel, true, m.queueFocus, g.w-3)
		if m.queueFocus {
			entries = m.playlistRows(entries, count, g.w-3)
		}
		for r, s := range entries {
			put(g.listY+1+r, " "+s)
		}
	}
	status := m.notice
	col := dim
	if m.errText != "" {
		status = m.errText
		col = "\x1b[38;2;255;117;145m"
	}
	if m.searching {
		status = "Searching YouTube " + []string{"·", "··", "···"}[(time.Now().UnixMilli()/300)%3]
	}
	put(g.h-2, " "+col+truncate(status, g.w-2)+reset)
	if m.inputMode {
		put(g.h-2, " "+accent+"SEARCH  "+reset+m.editorView(max(1, g.w-10)))
		put(g.h-1, dim+" Ctrl+A select · Ctrl+C copy · Ctrl+V paste · Enter search · Esc back"+reset)
	} else {
		put(g.h-1, " "+dim+strings.Repeat("─", max(0, g.w-2))+reset)
	}
	for i, s := range rows {
		rows[i] = ansi.Truncate(s, g.w, "")
	}
	return rows
}
func (m *model) editorView(width int) string {
	e := m.edit
	a, b := e.bounds()
	start := 0
	for start < e.pos && ansi.StringWidth(string(e.text[start:e.pos])) >= width-1 {
		start++
	}
	var out strings.Builder
	for i := start; i <= len(e.text); i++ {
		if i == e.pos {
			out.WriteString(accent + "▏" + reset)
		}
		if i == len(e.text) {
			break
		}
		if i >= a && i < b {
			out.WriteString("\x1b[48;2;64;67;93m")
		}
		out.WriteRune(e.text[i])
		out.WriteString(reset)
	}
	return ansi.Truncate(out.String(), width, "")
}
func spectrumLine(bars []float64, w, h, row int, style string, tint ...[]string) string {
	if w < 1 || h < 1 || row < 0 || row >= h {
		return ""
	}
	// One glyph per band keeps the spectrum dense and fine-grained. CAVA emits
	// 64 bands, so wider terminals can show the full analyzer without the old
	// spacer column making it look sparse.
	n := min(96, max(1, (w-2)/2))
	var out strings.Builder
	for i := 0; i < n; i++ {
		v := 0.
		if len(bars) > 0 {
			lo := i * len(bars) / n
			hi := max(lo+1, (i+1)*len(bars)/n)
			for j := lo; j < min(hi, len(bars)); j++ {
				v = math.Max(v, bars[j])
			}
		}
		level := v * float64(h)
		if style == "mirror" {
			// Intersect this row with a centered bar. Full scale reaches both
			// edges, and partial cells have equal coverage above/below center.
			top, bottom := (float64(h)-level)/2, (float64(h)+level)/2
			level = math.Min(float64(row+1), bottom) - math.Max(float64(row), top)
		} else {
			level -= float64(h - 1 - row)
		}
		part := clamp(int(math.Round(level*8)), 0, 8)
		if part > 0 && len(tint) > 0 && len(tint[0]) > 0 {
			out.WriteString(tint[0][i*len(tint[0])/n])
		}
		if style == "mirror" && row >= (h+1)/2 && part > 0 && part < 8 {
			// Complement a lower block to draw the matching upper partial cell.
			out.WriteString("\x1b[7m")
			out.WriteRune([]rune(" ▁▂▃▄▅▆▇█")[8-part])
			out.WriteString("\x1b[27m")
		} else {
			out.WriteRune([]rune(" ▁▂▃▄▅▆▇█")[part])
		}
		// Keep the original one-cell gap between independent bars. This fills
		// the full visualizer span without drawing any guide/grid characters.
		if i+1 < n {
			out.WriteByte(' ')
		}
	}
	return accent + out.String() + reset
}
func (m *model) draw() {
	if m.width < 1 || m.height < 1 || m.output == nil {
		return
	}
	m.lastDraw = time.Now()
	g := layout(m.width, m.height, m.cfg.ThumbnailSize)
	rows := m.frame(g)
	if m.width < 38 || m.height < 16 {
		rows = make([]string, g.h)
		rows[0] = truncate("YTM · enlarge terminal to 38 × 16", g.w)
	}
	// Row count alone misses width-only resizes and their relocated art/spectrum.
	full := len(m.rendered) != len(rows) || m.renderedGeometry != g
	var out strings.Builder
	if full {
		out.WriteString("\x1b[2J")
		m.artDirty = true
	}
	for y, s := range rows {
		if !full && m.rendered[y] == s {
			continue
		}
		x := 0
		if y >= 3 && y < 3+g.artH && m.width >= 38 && m.height >= 16 && !full {
			// Protect only rows intersecting the artwork. Spectrum rows below
			// its bottom are refreshed from column zero, including blank bars.
			x = g.infoX
			s = ansi.Cut(s, x, g.w)
		}
		fmt.Fprintf(&out, "\x1b[%d;%dH%s\x1b[0m\x1b[K", y+1, x+1, s)
	}
	m.rendered = rows
	m.renderedGeometry = g
	if m.artDirty && m.width >= 38 && m.height >= 16 {
		// Clear only the artwork rectangle on image changes, never on spectrum frames.
		for r := 0; r < g.artH; r++ {
			fmt.Fprintf(&out, "\x1b[%d;3H%s", 4+r, strings.Repeat(" ", g.artW))
		}
		if m.art != nil && m.cfg.ImageMode != "off" {
			if m.cfg.ImageMode == "sixel" {
				cw, ch := cellSize()
				img := fitImage(m.art, g.artW*cw, g.artH*ch)
				var b bytes.Buffer
				if new(sixel.Encoder).Encode(&b, img) == nil {
					offX := max(0, (g.artW*cw-img.Bounds().Dx())/(2*cw))
					offY := max(0, (g.artH*ch-img.Bounds().Dy())/(2*ch))
					fmt.Fprintf(&out, "\x1b[%d;%dH\x1bP0;1q%s\x1b\\", 4+offY, 3+offX, b.String())
				} else {
					out.WriteString(artBlocks(m.art, g.artW, g.artH))
				}
			} else {
				out.WriteString(artBlocks(m.art, g.artW, g.artH))
			}
		} else {
			fmt.Fprintf(&out, "\x1b[6;5H%s%s%s", dim, truncate("Y T M  /  artwork", g.artW-4), reset)
		}
		m.artDirty = false
	}
	out.WriteString("\x1b[1;1H")
	_, _ = io.WriteString(m.output, out.String())
	if m.helpOpen {
		m.drawHelpOverlay()
	}
	if m.transfer.stage == "target" || m.transfer.stage == "name" {
		m.drawTransferOverlay()
	}
}
func fitImage(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	scale := math.Min(float64(w)/float64(b.Dx()), float64(h)/float64(b.Dy()))
	nw, nh := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	// Area sampling preserves detail when reducing large thumbnails.
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			x0, x1 := x*b.Dx()/nw, max(x*b.Dx()/nw+1, (x+1)*b.Dx()/nw)
			y0, y1 := y*b.Dy()/nh, max(y*b.Dy()/nh+1, (y+1)*b.Dy()/nh)
			var rr, gg, bb, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, bl, _ := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					rr += uint64(r)
					gg += uint64(g)
					bb += uint64(bl)
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(rr / n >> 8), uint8(gg / n >> 8), uint8(bb / n >> 8), 255})
		}
	}
	return dst
}
func artBlocks(src image.Image, w, h int) string {
	img := fitImage(src, w, h*2)
	b := img.Bounds()
	var out strings.Builder
	ox := (w - b.Dx()) / 2
	oy := (h - (b.Dy()+1)/2) / 2
	for y := 0; y < b.Dy(); y += 2 {
		fmt.Fprintf(&out, "\x1b[%d;%dH", 4+oy+y/2, 3+ox)
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			r2, g2, b2, _ := img.At(x, min(y+1, b.Dy()-1)).RGBA()
			fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", r>>8, g>>8, bl>>8, r2>>8, g2>>8, b2>>8)
		}
		out.WriteString(reset)
	}
	return out.String()
}
func cellSize() (int, int) {
	type fontInfo struct {
		Size, Index    uint32
		X, Y           int16
		Family, Weight uint32
		Name           [32]uint16
	}
	f := fontInfo{}
	f.Size = uint32(unsafe.Sizeof(f))
	r, _, _ := kernel32.NewProc("GetCurrentConsoleFontEx").Call(os.Stdout.Fd(), 0, uintptr(unsafe.Pointer(&f)))
	if r != 0 && f.X > 0 && f.Y > 0 {
		return int(f.X), int(f.Y)
	}
	return 8, 16
}
func imageMode(mode string) string {
	if mode != "auto" {
		return mode
	}
	if os.Getenv("WT_SESSION") == "" {
		return "halfblock"
	}
	file := filepath.Join(os.Getenv("USERPROFILE"), "scoop", "apps", "windows-terminal", "current", "WindowsTerminal.exe")
	size, e := windows.GetFileVersionInfoSize(file, nil)
	if e != nil || size == 0 {
		return "halfblock"
	}
	data := make([]byte, size)
	if windows.GetFileVersionInfo(file, 0, size, unsafe.Pointer(&data[0])) != nil {
		return "halfblock"
	}
	var p *uint32
	var length uint32
	if windows.VerQueryValue(unsafe.Pointer(&data[0]), "\\", unsafe.Pointer(&p), &length) != nil || length < 16 {
		return "halfblock"
	}
	ms := unsafe.Slice(p, 4)[2]
	major, minor := ms>>16, ms&65535
	if major > 1 || major == 1 && minor >= 22 {
		return "sixel"
	}
	return "halfblock"
}
