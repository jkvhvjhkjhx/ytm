package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/windows"
	"math/rand/v2"
	"os"
	"path/filepath"
)

type Playlist struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Tracks []Track `json:"tracks"`
}
type savedPlaylist struct {
	Version       int        `json:"version"`
	Tracks        []Track    `json:"tracks,omitempty"`
	Index         int        `json:"index"`
	Position      float64    `json:"position"`
	Playlists     []Playlist `json:"playlists"`
	ViewedID      string     `json:"viewed_id"`
	PlayingID     string     `json:"playing_id"`
	PlayingName   string     `json:"playing_name"`
	PlaybackQueue []Track    `json:"playback_queue"`
}

func newPlaylistID() string         { return fmt.Sprintf("%016x%016x", rand.Uint64(), rand.Uint64()) }
func copyTracks(ts []Track) []Track { return append([]Track(nil), ts...) }
func normalizeTracks(ts []Track) {
	seen := map[string]bool{}
	for i := range ts {
		t := &ts[i]
		if t.EntryID == "" || seen[t.EntryID] {
			t.EntryID = newPlaylistID()
		}
		seen[t.EntryID] = true
		// Signed stream URLs belong only to the extractor/player.
		if t.ID != "" {
			t.URL = "https://www.youtube.com/watch?v=" + t.ID
		} else {
			t.URL = canonicalYouTubeURL(t.URL)
		}
	}
}
func (m *model) ensureLibrary() {
	if len(m.playlists) == 0 {
		m.viewedID = newPlaylistID()
		m.playlists = []Playlist{{ID: m.viewedID, Name: "My Playlist"}}
	}
}
func (m *model) viewedIndex() int {
	for i := range m.playlists {
		if m.playlists[i].ID == m.viewedID {
			return i
		}
	}
	return 0
}
func (m *model) syncViewed() {
	m.ensureLibrary()
	normalizeTracks(m.queue)
	m.playlists[m.viewedIndex()].Tracks = copyTracks(m.queue)
}
func (m *model) switchPlaylist(i int) {
	m.syncViewed()
	if i < 0 || i >= len(m.playlists) {
		return
	}
	m.viewedID = m.playlists[i].ID
	m.queue = copyTracks(m.playlists[i].Tracks)
	m.queueCursor = 0
	m.playlistCursor = i
	m.playlistMode = false
	m.saveQueue()
}

// Playback is a snapshot: library edits cannot mutate its active permutation.
func (m *model) startPlaylist(i int) tea.Cmd {
	if i < 0 || i >= len(m.queue) {
		return nil
	}
	m.syncViewed()
	m.playbackQueue = copyTracks(m.queue)
	m.playingID = m.viewedID
	m.playingName = m.playlists[m.viewedIndex()].Name
	m.playing = i
	m.resetOrder()
	m.saveQueue()
	return m.loadTrack(m.playbackQueue[i])
}
func (m *model) resetOrder() { m.history = nil; m.future = nil; m.shuffleBag = nil }
func permutation(n, exclude int) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if i != exclude {
			out = append(out, i)
		}
	}
	// Fisher–Yates: every remaining index occurs exactly once.
	for i := len(out) - 1; i > 0; i-- {
		j := rand.IntN(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}
func (m *model) chooseNext(auto bool, d int) int {
	n := len(m.playbackQueue)
	if n == 0 {
		return -1
	}
	if auto && m.repeatMode == "track" && m.playing >= 0 {
		return m.playing
	}
	if d < 0 {
		if len(m.history) > 0 {
			i := m.history[len(m.history)-1]
			m.history = m.history[:len(m.history)-1]
			m.future = append(m.future, m.playing)
			return i
		}
		if m.playing > 0 {
			return m.playing - 1
		}
		if m.repeatMode == "queue" {
			return n - 1
		}
		return 0
	}
	if len(m.future) > 0 {
		i := m.future[len(m.future)-1]
		m.future = m.future[:len(m.future)-1]
		m.history = append(m.history, m.playing)
		return i
	}
	next := m.playing + 1
	if m.shuffle {
		if m.shuffleBag == nil {
			m.shuffleBag = permutation(n, m.playing)
		}
		if len(m.shuffleBag) == 0 {
			if m.repeatMode != "queue" {
				return -1
			}
			m.shuffleBag = permutation(n, -1)
		}
		next = m.shuffleBag[0]
		m.shuffleBag = m.shuffleBag[1:]
	} else if next >= n {
		if m.repeatMode == "queue" {
			next = 0
		} else {
			return -1
		}
	}
	if m.playing >= 0 {
		m.history = append(m.history, m.playing)
		if len(m.history) > 500 {
			m.history = m.history[1:]
		}
	}
	return next
}
func (m *model) advance(auto bool, d int) tea.Cmd {
	i := m.chooseNext(auto, d)
	if i < 0 {
		m.notice = "End of playback queue"
		return nil
	}
	m.playing = i
	m.saveQueue()
	return m.loadTrack(m.playbackQueue[i])
}
func (m *model) playNext(d int) tea.Cmd { return m.advance(false, d) }
func (m *model) moveSelected(d int) {
	i, j := m.queueCursor, m.queueCursor+d
	if i < 0 || j < 0 || i >= len(m.queue) || j >= len(m.queue) {
		return
	}
	m.queue[i], m.queue[j] = m.queue[j], m.queue[i]
	m.queueCursor = j
	m.saveQueue()
	m.notice = "Playlist reordered · playback queue unchanged"
}
func (m *model) clearQueue() tea.Cmd {
	m.queue = nil
	m.queueCursor = 0
	m.saveQueue()
	m.notice = "Playlist cleared · playback queue unchanged"
	return nil
}
func (m *model) removeSelected() tea.Cmd {
	i := m.queueCursor
	if i < 0 || i >= len(m.queue) {
		return nil
	}
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
	m.queueCursor = clamp(i, 0, max(0, len(m.queue)-1))
	m.saveQueue()
	return nil
}
func queueFile() string { return filepath.Join(filepath.Dir(configFile()), "playlist.json") }
func (m *model) saveQueue() {
	m.syncViewed()
	if !m.persist || m.libraryReadOnly {
		return
	}
	position := 0.
	if m.playing >= 0 && m.playing < len(m.playbackQueue) && m.current.ID == m.playbackQueue[m.playing].ID && !m.loading {
		position = m.snapshot.Time
	}
	state := savedPlaylist{Version: 3, Index: m.playing, Position: position, Playlists: m.playlists, ViewedID: m.viewedID, PlayingID: m.playingID, PlayingName: m.playingName, PlaybackQueue: copyTracks(m.playbackQueue)}
	m.fail(writePlaylistState(state))
}

// Replace the whole library in one transaction: destination additions and
// source removals can never be persisted separately.
func writePlaylistState(state savedPlaylist) error {
	normalizeTracks(state.PlaybackQueue)
	data, e := json.MarshalIndent(state, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(queueFile()), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(queueFile()), "playlist-*.tmp")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		from, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		to, err := windows.UTF16PtrFromString(queueFile())
		if err != nil {
			return err
		}
		e = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	}
	return e
}
func (m *model) restoreQueue() {
	m.ensureLibrary()
	data, e := os.ReadFile(queueFile())
	if os.IsNotExist(e) {
		return
	}
	if e != nil {
		m.libraryReadOnly = true
		m.fail(e)
		return
	}
	var state savedPlaylist
	if len(data) > 32<<20 {
		e = fmt.Errorf("playlist exceeds 32 MiB")
	} else if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		e = json.Unmarshal(data, &state.Tracks)
		state.Index = -1
	} else {
		e = json.Unmarshal(data, &state)
	}
	if e != nil || state.Version > 3 {
		m.libraryReadOnly = true
		m.fail(fmt.Errorf("playlist invalid or newer version; original file preserved"))
		return
	}
	if state.Version < 3 {
		m.queue = state.Tracks
		m.syncViewed()
		m.playbackQueue = copyTracks(m.queue)
		m.playingID = m.viewedID
		m.playingName = m.playlists[0].Name
	} else {
		if len(state.Playlists) == 0 {
			m.libraryReadOnly = true
			m.fail(fmt.Errorf("empty playlist library; original file preserved"))
			return
		}
		seen := map[string]bool{}
		for i := range state.Playlists {
			p := &state.Playlists[i]
			if p.ID == "" || seen[p.ID] {
				m.libraryReadOnly = true
				m.fail(fmt.Errorf("invalid playlist IDs; original file preserved"))
				return
			}
			seen[p.ID] = true
			normalizeTracks(p.Tracks)
		}
		m.playlists = state.Playlists
		m.viewedID = state.ViewedID
		if !seen[m.viewedID] {
			m.viewedID = m.playlists[0].ID
		}
		m.queue = copyTracks(m.playlists[m.viewedIndex()].Tracks)
		m.playlistCursor = m.viewedIndex()
		m.playbackQueue = state.PlaybackQueue
		normalizeTracks(m.playbackQueue)
		m.playingID = state.PlayingID
		m.playingName = state.PlayingName
	}
	m.playing = clamp(state.Index, -1, len(m.playbackQueue)-1)
	m.shuffle = false
	m.repeatMode = "off"
	m.resetOrder()
	if m.playing >= 0 {
		m.current = m.playbackQueue[m.playing]
		m.resumeAt = max(0, state.Position)
		m.snapshot.Time = m.resumeAt
	}
}
