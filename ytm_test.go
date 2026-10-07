package main

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorUnicodeAndSelection(t *testing.T) {
	var e editor
	e.insert("âm nhạc 日本")
	e.key(tea.KeyMsg{Type: tea.KeyBackspace})
	if string(e.text) != "âm nhạc 日" {
		t.Fatal(string(e.text))
	}
	e.key(tea.KeyMsg{Type: tea.KeyHome})
	e.insert("♪ ")
	if !strings.HasPrefix(string(e.text), "♪ âm") {
		t.Fatal(string(e.text))
	}
	e.key(tea.KeyMsg{Type: tea.KeyCtrlA})
	e.insert("replacement\r\ntext\x1b")
	if string(e.text) != "replacement  text" {
		t.Fatal(string(e.text))
	}
	e.key(tea.KeyMsg{Type: tea.KeyLeft})
	e.key(tea.KeyMsg{Type: tea.KeyDelete})
	if string(e.text) != "replacement  tex" {
		t.Fatal(string(e.text))
	}
}
func TestPlaylistRemoveAndStaleMessages(t *testing.T) {
	m := newModel(defaultConfig(), nil, "", "")
	m.output = nil
	m.queue = []Track{{ID: "one"}, {ID: "two"}, {ID: "three"}}
	m.syncViewed()
	m.playbackQueue = copyTracks(m.queue)
	m.playing = 2
	m.queueCursor = 0
	m.removeSelected()
	if m.playing != 2 || m.queue[1].ID != "three" || m.playbackQueue[2].ID != "three" {
		t.Fatal(m.queue, m.playing)
	}
	m.generation = 3
	m, _ = updateModel(m, audioReady{path: "old", generation: 2})
	if m.current.ID != "" {
		t.Fatal("stale load applied")
	}
	m, _ = updateModel(m, artworkDone{image.NewRGBA(image.Rect(0, 0, 8, 8)), 2, nil})
	if m.art != nil {
		t.Fatal("stale artwork applied")
	}
}
func TestFrameFitsEverySize(t *testing.T) {
	m := newModel(defaultConfig(), nil, "", "")
	m.current = Track{Title: strings.Repeat("日本 Việt ", 20)}
	m.bars = []float64{0, .1, .6, 1}
	for _, wh := range [][2]int{{38, 16}, {55, 20}, {80, 24}, {120, 32}, {160, 45}} {
		g := layout(wh[0], wh[1], 52)
		rows := m.frame(g)
		if len(rows) != wh[1] {
			t.Fatal("height")
		}
		for _, r := range rows {
			if ansi.StringWidth(r) > wh[0]-1 {
				t.Fatalf("overflow %v %s", wh, r)
			}
		}
	}
}
func TestArtworkAspectAndNoRedraw(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	scaled := fitImage(src, 520, 224)
	ratio := float64(scaled.Bounds().Dx()) / float64(scaled.Bounds().Dy())
	if ratio < 1.77 || ratio > 1.79 {
		t.Fatal(ratio)
	}
	f, e := os.CreateTemp(t.TempDir(), "render")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	m := newModel(defaultConfig(), nil, "", "")
	m.output = f
	m.width = 120
	m.height = 32
	m.cfg.ImageMode = "sixel"
	m.art = src
	m.artDirty = true
	m.draw()
	first, _ := os.ReadFile(f.Name())
	if !strings.Contains(string(first), "\x1bP0;1q") {
		t.Fatal("sixel absent")
	}
	if e = f.Truncate(0); e != nil {
		t.Fatal(e)
	}
	f.Seek(0, 0)
	m.bars = []float64{.2, .8, 1}
	m.draw()
	second, _ := os.ReadFile(f.Name())
	if strings.Contains(string(second), "\x1bP") || strings.Contains(string(second), "\x1b[2J") {
		t.Fatal("art repainted on spectrum frame")
	}
}
func TestQueuePersistence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	os.MkdirAll(filepath.Join(dir, "ytm"), 0700)
	m := newModel(defaultConfig(), nil, "", "")
	m.persist = true
	m.queue = []Track{{ID: "abc", Title: "Tiếng Việt"}}
	m.saveQueue()
	if m.errText != "" {
		t.Fatal(m.errText)
	}
	n := newModel(defaultConfig(), nil, "", "")
	n.restoreQueue()
	if len(n.queue) != 1 || n.queue[0].Title != "Tiếng Việt" {
		t.Fatal(n.queue)
	}
}
func TestCancelledDownload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareAudio(ctx, "", "", "url"); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestCacheLimit(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.audio", "b.audio", "c.audio"} {
		os.WriteFile(filepath.Join(dir, name), make([]byte, 200), 0600)
	}
	keep := filepath.Join(dir, "c.audio")
	trimCache(dir, keep, 200)
	fs, _ := filepath.Glob(filepath.Join(dir, "*.audio"))
	if len(fs) != 1 || fs[0] != keep {
		t.Fatal(fs)
	}
}
func TestSpectrumNoSignal(t *testing.T) {
	s := ansi.Strip(spectrumLine(make([]float64, 48), 80, 4, 3, "spectrum"))
	if strings.TrimSpace(s) != "" || strings.ContainsAny(s, "▂▃▄▅▆▇█") {
		t.Fatal("invalid empty spectrum", s)
	}
}

func TestWindowsClipboardRoundTrip(t *testing.T) {
	if os.Getenv("YTM_TEST_CLIPBOARD") != "1" {
		t.Skip("opt-in native clipboard test")
	}
	original, err := clipboardRead()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := clipboardWrite(original); e != nil {
			t.Errorf("restore clipboard: %v", e)
		}
	}()
	m := newModel(defaultConfig(), nil, "", "")
	m.output = nil
	m, _ = key(m, "/")
	m, _ = key(m, "âm nhạc 日本")
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	m = applyCommand(m, cmd)
	if m.errText != "" {
		t.Fatal(m.errText)
	}
	got, e := clipboardRead()
	if e != nil || got != "âm nhạc 日本" {
		t.Fatalf("copy failed: %v", e)
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlV})
	m = applyCommand(m, cmd)
	if m.query != "âm nhạc 日本" || m.errText != "" {
		t.Fatal("paste failed", m.errText)
	}
}
