package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/windows"
	"os"
	"reflect"
	"strings"
	"testing"
)

func transferFixture(t *testing.T) model {
	t.Helper()
	t.Setenv("APPDATA", t.TempDir())
	m := playlistFixture()
	m.persist = true
	m.queueFocus = true
	m.playlists = append(m.playlists, Playlist{ID: "target", Name: "Laufey"})
	m.saveQueue()
	if m.errText != "" {
		t.Fatal(m.errText)
	}
	return m
}
func TestTransferMoveOrderPersistenceAndPlayback(t *testing.T) {
	m := transferFixture(t)
	before := copyTracks(m.playbackQueue)
	snapshot, current, generation := m.snapshot, m.current, m.generation
	m.beginTransfer(false)
	// Deliberately tick in reverse order; result follows source order.
	m.transfer.selected[m.queue[2].EntryID] = true
	m.transfer.selected[m.queue[0].EntryID] = true
	m.finishTransfer("target", "")
	if m.transfer.stage != "" || len(m.queue) != 1 || m.queue[0].ID != "two" {
		t.Fatal("move failed", m.transfer.err)
	}
	dest := m.playlists[1].Tracks
	if len(dest) != 2 || dest[0].ID != "one" || dest[1].ID != "three" {
		t.Fatal("relative order")
	}
	if !reflect.DeepEqual(before, m.playbackQueue) || snapshot != m.snapshot || current != m.current || generation != m.generation || m.stopped {
		t.Fatal("playback changed")
	}
	n := newModel(defaultConfig(), nil, "", "")
	n.restoreQueue()
	if n.errText != "" || len(n.queue) != 1 || !reflect.DeepEqual(n.playlists[1].Tracks, dest) {
		t.Fatal("disk persistence")
	}
	if !reflect.DeepEqual(n.playbackQueue, before) {
		t.Fatal("saved playback queue changed")
	}
	if n.chooseNext(true, 1) != 1 {
		t.Fatal("auto-next queue changed")
	}
}
func TestTransferCopyDuplicatesAndSameSource(t *testing.T) {
	m := transferFixture(t)
	m.playlists[1].Tracks = []Track{{ID: "one", EntryID: "existing"}}
	m.beginTransfer(true)
	for _, track := range m.queue {
		m.transfer.selected[track.EntryID] = true
	}
	source := copyTracks(m.queue)
	if err := m.commitTransfer(m.viewedID, ""); err == nil {
		t.Fatal("copy to self not reported")
	}
	if err := m.commitTransfer("target", ""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, m.queue) || len(m.playlists[1].Tracks) != 3 {
		t.Fatal("copy/duplicate")
	}
	if m.playlists[1].Tracks[1].EntryID == source[1].EntryID {
		t.Fatal("copy reused entry identity")
	}
	// Repeating copy must be idempotent for the destination.
	if err := m.commitTransfer("target", ""); err != nil {
		t.Fatal(err)
	}
	if len(m.playlists[1].Tracks) != 3 {
		t.Fatal("duplicate copy")
	}
	m.transfer.copying = false
	if err := m.commitTransfer(m.viewedID, ""); err == nil {
		t.Fatal("move to self")
	}
	if err := m.commitTransfer("target", ""); err != nil {
		t.Fatal(err)
	}
	if len(m.queue) != 0 || len(m.playlists[1].Tracks) != 3 {
		t.Fatal("move to existing duplicate")
	}
}
func TestTransferLockedDiskDoesNotLoseTracks(t *testing.T) {
	m := transferFixture(t)
	m.beginTransfer(false)
	m.transfer.selected[m.queue[0].EntryID] = true
	before, _ := os.ReadFile(queueFile())
	source := copyTracks(m.queue)
	path, _ := windows.UTF16PtrFromString(queueFile())
	// Real Windows sharing violation, not a mocked writer.
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	m.finishTransfer("", "New destination")
	windows.CloseHandle(h)
	after, _ := os.ReadFile(queueFile())
	if m.transfer.err == "" || m.transfer.stage == "" || !reflect.DeepEqual(source, m.queue) || len(m.playlists) != 2 || string(before) != string(after) {
		t.Fatal("failed transaction changed data", m.transfer.err)
	}
	if len(m.transfer.selected) != 1 {
		t.Fatal("selection lost on failure")
	}
	m.finishTransfer("", "New destination")
	if m.transfer.stage != "" || len(m.playlists) != 3 || len(m.queue) != 2 {
		t.Fatal("retry failed")
	}
}
func TestTransferKeyboardFlowAndIsolation(t *testing.T) {
	m := transferFixture(t)
	vol := m.volume
	current := m.current
	gen := m.generation
	m, _ = key(m, "m")
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeySpace})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = key(m, "J")
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.transfer.selected) != 2 || m.queueCursor != 2 || m.volume != vol {
		t.Fatal("selection/navigation")
	}
	m, _ = key(m, "n")
	m, _ = key(m, "s")
	m, _ = key(m, "/")
	m, _ = key(m, "d")
	if m.current != current || m.generation != gen || m.stopped || m.inputMode || len(m.queue) != 3 {
		t.Fatal("keys leaked")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.transfer.stage != "target" {
		t.Fatal("target popup")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.transfer.stage != "select" || len(m.transfer.selected) != 2 {
		t.Fatal("back lost selection")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if len(m.transfer.selected) != 0 {
		t.Fatal("clear")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if len(m.transfer.selected) != 3 {
		t.Fatal("select all")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.transfer.stage != "name" {
		t.Fatal("create")
	}
	m, _ = key(m, "Nhạc đêm")
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.transfer.stage != "target" || len(m.transfer.selected) != 3 {
		t.Fatal("name back")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = key(m, "Nhạc đêm")
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.playlists) != 3 || m.playlists[2].Name != "Nhạc đêm" || len(m.queue) != 0 || m.transfer.stage != "" {
		t.Fatal("create and transfer")
	}
	m.switchPlaylist(2)
	m, _ = key(m, "M")
	if !m.transfer.copying {
		t.Fatal("shift M")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.transfer.stage != "" || len(m.queue) != 3 {
		t.Fatal("cancel")
	}
}
func TestTransferRenderingAndOverlayBounds(t *testing.T) {
	m := transferFixture(t)
	for _, wh := range [][2]int{{8, 5}, {38, 16}, {80, 24}, {120, 32}, {180, 50}} {
		g := layout(wh[0], wh[1], 52)
		before := m.frame(g)
		m.beginTransfer(false)
		m.transfer.selected[m.queue[0].EntryID] = true
		after := m.frame(g)
		limit := min(g.listY, max(0, len(before)-2))
		if !reflect.DeepEqual(before[:limit], after[:limit]) {
			t.Fatal("other UI changed")
		}
		m.transfer.stage = "target"
		for _, stage := range []string{"target", "name"} {
			m.transfer.stage = stage
			rows := m.transferOverlayRows(wh[0], wh[1])
			if len(rows) > wh[1] {
				t.Fatal("height overflow")
			}
			for _, row := range rows {
				if ansi.StringWidth(row) > wh[0] {
					t.Fatal("width overflow", row)
				}
			}
		}
		m.transfer = trackTransfer{}
	}
	m.beginTransfer(false)
	m.transfer.selected[m.queue[0].EntryID] = true
	rows := m.frame(layout(120, 32, 52))
	if !strings.Contains(strings.Join(rows, ""), "✓") {
		t.Fatal("missing selection marker")
	}
}
