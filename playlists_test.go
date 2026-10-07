package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func playlistFixture() model {
	m := newModel(defaultConfig(), nil, "", "")
	m.output = nil
	m.queue = []Track{{ID: "one", Title: "Một", Artist: "Artist", Duration: 10}, {ID: "two", Title: "Hai"}, {ID: "three", Title: "Ba"}}
	m.syncViewed()
	m.playbackQueue = copyTracks(m.queue)
	m.playingID = m.viewedID
	m.playingName = m.playlists[0].Name
	m.playing = 0
	m.current = m.playbackQueue[0]
	m.stopped = false
	m.snapshot.Time = 42
	m.audioPath = "audio"
	return m
}
func TestMultiPlaylistEditingAndPersistence(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	m := playlistFixture()
	m.persist = true
	first := m.viewedID
	original := copyTracks(m.playbackQueue)
	m.createPlaylist("Nhạc đêm 日本")
	if len(m.playlists) != 2 || m.viewedID == first || m.current.ID != "one" || m.snapshot.Time != 42 || m.stopped {
		t.Fatal("create/switch interrupted playback")
	}
	m.queue = []Track{{ID: "four", Title: "Four"}, {ID: "five", Title: "Five"}}
	m.saveQueue()
	m.queueCursor = 0
	m.moveSelected(1)
	if m.queue[0].ID != "five" {
		t.Fatal("reorder")
	}
	m.removeSelected()
	if len(m.queue) != 1 || m.queue[0].ID != "five" {
		t.Fatal("remove")
	}
	m.renamePlaylist(1, "Đổi tên")
	if !reflect.DeepEqual(original, m.playbackQueue) {
		t.Fatal("library edits mutated playback queue")
	}
	m.shuffle = true
	m.repeatMode = "track"
	m.saveQueue()
	n := newModel(defaultConfig(), nil, "", "")
	n.restoreQueue()
	if n.errText != "" || len(n.playlists) != 2 || n.playlists[1].Name != "Đổi tên" || len(n.queue) != 1 || n.queue[0].ID != "five" {
		t.Fatalf("restore: %+v %s", n.playlists, n.errText)
	}
	if n.shuffle || n.repeatMode != "off" || n.playingID != first || n.current.ID != "one" || n.snapshot.Time != 42 {
		t.Fatal("restart playback/defaults")
	}
	if !reflect.DeepEqual(original, n.playbackQueue) {
		t.Fatal("queue snapshot not persisted")
	}
	n.switchPlaylist(0)
	if n.queue[0].Title != "Một" || n.queue[0].Artist != "Artist" || n.queue[0].Duration != 10 {
		t.Fatal("metadata lost")
	}
	n.clearQueue()
	if len(n.queue) != 0 || len(n.playbackQueue) != 3 {
		t.Fatal("clear changed playback")
	}
	n.deletePlaylist(0)
	if len(n.playlists) != 1 || n.playingID != first || len(n.playbackQueue) != 3 {
		t.Fatal("delete active source interrupted queue")
	}
	n.deletePlaylist(0)
	if len(n.playlists) != 1 || len(n.queue) != 0 {
		t.Fatal("delete final playlist")
	}
}
func TestPlaylistKeyboardDialogsAndRouting(t *testing.T) {
	m := playlistFixture()
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlN})
	m, _ = key(m, "Nhạc")
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.playlists) != 2 || m.playlists[1].Name != "Nhạc" {
		t.Fatal("create dialog")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = key(m, "Renamed")
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.playlists[1].Name != "Renamed" {
		t.Fatal("rename dialog")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.playlistMode {
		t.Fatal("back")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDelete})
	m, _ = key(m, "n")
	if len(m.playlists) != 2 {
		t.Fatal("cancel deletion")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDelete})
	before := m.volume
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	m, _ = key(m, "h")
	if m.volume != before || m.shuffle {
		t.Fatal("dialog leaked keys")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.playlists) != 1 {
		t.Fatal("confirmed delete")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.playlistMode || m.viewedID != m.playlists[0].ID {
		t.Fatal("open playlist")
	}
	for _, want := range []string{"queue", "track", "off"} {
		m, _ = key(m, "r")
		if m.repeatMode != want {
			t.Fatal("repeat cycle", m.repeatMode)
		}
	}
	m, _ = key(m, "H")
	if !m.shuffle {
		t.Fatal("shuffle hotkey")
	}
	m, _ = key(m, "/")
	m, _ = key(m, "hRnt")
	if m.query != "hRnt" || !m.shuffle || m.repeatMode != "off" {
		t.Fatal("search key routing")
	}
}
func TestShuffleAndRepeatCycles(t *testing.T) {
	for n := 1; n <= 20; n++ {
		m := playlistFixture()
		m.playbackQueue = make([]Track, n)
		m.playing = 0
		m.shuffle = true
		seen := map[int]bool{0: true}
		for step := 1; step < n; step++ {
			next := m.chooseNext(true, 1)
			if next < 0 || next >= n || seen[next] {
				t.Fatalf("duplicate/missing in first cycle n=%d", n)
			}
			seen[next] = true
			m.playing = next
		}
		if m.chooseNext(true, 1) != -1 {
			t.Fatal("shuffle off-repeat must end")
		}
		m.repeatMode = "queue"
		for cycle := 0; cycle < 10; cycle++ {
			seen = map[int]bool{}
			for step := 0; step < n; step++ {
				next := m.chooseNext(true, 1)
				if next < 0 || next >= n || seen[next] {
					t.Fatalf("bad full cycle n=%d cycle=%d next=%d", n, cycle, next)
				}
				seen[next] = true
				m.playing = next
			}
		}
		m.repeatMode = "track"
		for step := 0; step < 3; step++ {
			if m.chooseNext(true, 1) != m.playing {
				t.Fatal("repeat one + shuffle")
			}
		}
	}
	m := playlistFixture()
	m.playing = 2
	if m.chooseNext(true, 1) != -1 {
		t.Fatal("repeat off")
	}
	m.repeatMode = "queue"
	if m.chooseNext(true, 1) != 0 {
		t.Fatal("repeat playlist")
	}
	m.repeatMode = "track"
	if m.chooseNext(true, 1) != 2 {
		t.Fatal("repeat one")
	}
	m.repeatMode = "off"
	m.playing = 0
	m.playing = m.chooseNext(false, 1)
	if m.chooseNext(false, -1) != 0 {
		t.Fatal("previous")
	}
	m.playing = 0
	if m.chooseNext(false, 1) != 1 {
		t.Fatal("forward history")
	}
}
func TestAutoNextUsesPlayingPlaylist(t *testing.T) {
	m := playlistFixture()
	m.createPlaylist("Other")
	m.queue = []Track{{ID: "elsewhere"}}
	m.saveQueue()
	generation := m.generation
	if cmd := m.acceptSnapshot(PlayerSnapshot{Path: "audio", EOF: true}); cmd == nil {
		t.Fatal("missing auto-next")
	}
	if m.current.ID != "two" || m.playing != 1 || m.queue[0].ID != "elsewhere" || m.generation != generation+1 {
		t.Fatal("EOF followed viewed playlist")
	}
	if m.cancel != nil {
		m.cancel()
	}
}
func TestLibraryMigrationAndCanonicalURLs(t *testing.T) {
	for _, data := range []string{
		`[{"ID":"abc","Title":"old"}]`,
		`{"version":2,"tracks":[{"ID":"abc","Title":"old","URL":"https://rr.googlevideo.com/signed?expire=123"}],"index":0,"shuffle":true,"repeat":"track"}`,
	} {
		t.Run(data[:3], func(t *testing.T) {
			t.Setenv("APPDATA", t.TempDir())
			os.MkdirAll(filepath.Dir(queueFile()), 0700)
			os.WriteFile(queueFile(), []byte(data), 0600)
			m := newModel(defaultConfig(), nil, "", "")
			m.persist = true
			m.restoreQueue()
			m.saveQueue()
			b, _ := os.ReadFile(queueFile())
			if len(m.queue) != 1 || m.queue[0].URL != "https://www.youtube.com/watch?v=abc" || m.shuffle || m.repeatMode != "off" || strings.Contains(string(b), "googlevideo") {
				t.Fatal("migration")
			}
		})
	}
	for raw, want := range map[string]string{
		"https://music.youtube.com/watch?v=abc&list=xyz":      "https://www.youtube.com/watch?v=abc",
		"https://youtu.be/abc?t=20":                           "https://www.youtube.com/watch?v=abc",
		"https://rr.googlevideo.com/videoplayback?expire=123": "",
	} {
		if got := canonicalYouTubeURL(raw); got != want {
			t.Fatal(got)
		}
	}
}
func TestCorruptLibraryIsNotOverwritten(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	os.MkdirAll(filepath.Dir(queueFile()), 0700)
	original := []byte("{broken")
	os.WriteFile(queueFile(), original, 0600)
	m := newModel(defaultConfig(), nil, "", "")
	m.persist = true
	m.restoreQueue()
	m.createPlaylist("new")
	m.saveQueue()
	b, _ := os.ReadFile(queueFile())
	if string(b) != string(original) || !m.libraryReadOnly {
		t.Fatal("overwrote corrupt library")
	}
}

func TestCtrlDPlaylistDeleteConfirmAndTrackDeleteMode(t *testing.T) {
	m := playlistFixture()
	m.playlists = append(m.playlists, Playlist{ID: "other", Name: "Other", Tracks: copyTracks(m.queue)})
	m.queueFocus = true
	before := len(m.playlists)
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.playlistDialog != "delete" || len(m.playlists) != before {
		t.Fatal("Ctrl+D should ask before deleting the open playlist")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.playlistDialog != "" || len(m.queue) != 3 {
		t.Fatal("cancel changed playlist")
	}
	m.playlistMode = true
	m.playlistCursor = 0
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.playlistDialog != "delete" || len(m.playlists) != before {
		t.Fatal("Ctrl+D should confirm from PLAYLISTS")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.playlists) != before-1 {
		t.Fatal("confirmed Ctrl+D did not delete playlist")
	}
	m.playlistMode = false
	m.queueFocus = true
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if m.transfer.stage != "select" || !m.transfer.deleting || len(m.queue) != 3 {
		t.Fatal("D must enter track selection, not delete immediately")
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.queue) != 3 {
		t.Fatal("cancelled track selection removed tracks")
	}
}
func TestPlaylistRenderingLeavesOtherRegionsUntouched(t *testing.T) {
	m := playlistFixture()
	for _, wh := range [][2]int{{38, 16}, {80, 24}, {120, 32}, {180, 50}} {
		g := layout(wh[0], wh[1], 52)
		before := m.frame(g)
		m.createPlaylist("Another")
		m.playlistMode = true
		after := m.frame(g)
		if !reflect.DeepEqual(before[:g.listY], after[:g.listY]) {
			t.Fatal("changed non-playlist region", wh)
		}
	}
}
