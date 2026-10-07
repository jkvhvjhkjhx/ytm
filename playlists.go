package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"net/url"
	"strings"
)

func canonicalYouTubeURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	id := ""
	switch host {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "www.youtube.com", "music.youtube.com", "m.youtube.com":
		id = u.Query().Get("v")
		if id == "" && (strings.HasPrefix(u.Path, "/shorts/") || strings.HasPrefix(u.Path, "/embed/")) {
			id = strings.TrimPrefix(strings.TrimPrefix(u.Path, "/shorts/"), "/embed/")
		}
	}
	if id == "" || strings.ContainsAny(id, "/?&= ") {
		return ""
	}
	return "https://www.youtube.com/watch?v=" + id
}
func (m *model) createPlaylist(name string) {
	m.syncViewed()
	m.playlists = append(m.playlists, Playlist{ID: newPlaylistID(), Name: name})
	m.switchPlaylist(len(m.playlists) - 1)
}
func (m *model) renamePlaylist(i int, name string) {
	if i < 0 || i >= len(m.playlists) {
		return
	}
	m.playlists[i].Name = name
	if m.playlists[i].ID == m.playingID {
		m.playingName = name
	}
	m.saveQueue()
}
func (m *model) deletePlaylist(i int) {
	if i < 0 || i >= len(m.playlists) {
		return
	}
	m.syncViewed()
	id := m.playlists[i].ID
	m.playlists = append(m.playlists[:i], m.playlists[i+1:]...)
	if id == m.playingID {
		m.playingName += " (deleted)"
	}
	if len(m.playlists) == 0 {
		m.queue = nil
		m.ensureLibrary()
	}
	if id == m.viewedID {
		m.viewedID = m.playlists[min(i, len(m.playlists)-1)].ID
		m.queue = copyTracks(m.playlists[m.viewedIndex()].Tracks)
		m.queueCursor = 0
	}
	m.playlistCursor = clamp(i, 0, len(m.playlists)-1)
	m.saveQueue()
}

// Only playlist-specific input is intercepted; search and help keep priority.
func (m *model) playlistKey(k tea.KeyMsg) bool {
	key := k.String()
	if m.transfer.stage != "" {
		m.transferKey(k)
		return true
	}
	if m.playlistDialog == "" && m.queueFocus && !m.playlistMode && (key == "d" || key == "D" || key == "delete") {
		m.beginDeleteTracks()
		return true
	}
	if m.playlistDialog == "" && m.queueFocus && !m.playlistMode && (key == "m" || key == "M") {
		m.beginTransfer(key == "M")
		return true
	}
	if m.playlistDialog != "" {
		switch key {
		case "esc":
			m.playlistDialog = ""
		case "enter":
			if m.playlistDialog == "delete" {
				m.deletePlaylist(m.playlistCursor)
			} else {
				name := strings.TrimSpace(clean(string(m.playlistEdit.text)))
				if name == "" {
					return true
				}
				if len([]rune(name)) > 120 {
					m.notice = "Playlist names are limited to 120 characters"
					return true
				}
				if m.playlistDialog == "create" {
					m.createPlaylist(name)
				} else {
					m.renamePlaylist(m.playlistCursor, name)
				}
			}
			m.playlistDialog = ""
		default:
			if m.playlistDialog == "delete" {
				if key == "y" || key == "Y" {
					m.deletePlaylist(m.playlistCursor)
					m.playlistDialog = ""
				}
				if key == "n" || key == "N" {
					m.playlistDialog = ""
				}
			} else {
				m.playlistEdit.key(k)
			}
		}
		return true
	}
	if key == "ctrl+n" {
		m.ensureLibrary()
		m.queueFocus = true
		m.playlistDialog = "create"
		m.playlistEdit = editor{}
		return true
	}
	if !m.queueFocus {
		return false
	}
	m.ensureLibrary()
	if key == "ctrl+d" {
		if !m.playlistMode {
			m.playlistCursor = m.viewedIndex()
		}
		m.playlistDialog = "delete"
		return true
	}
	if key == "backspace" || key == "esc" {
		if !m.playlistMode {
			m.playlistMode = true
			m.playlistCursor = m.viewedIndex()
			return true
		}
	}
	if key == "f2" {
		if !m.playlistMode {
			m.playlistCursor = m.viewedIndex()
		}
		name := m.playlists[m.playlistCursor].Name
		m.playlistEdit = editor{text: []rune(name), pos: len([]rune(name)), anchor: 0}
		m.playlistDialog = "rename"
		return true
	}
	if !m.playlistMode {
		return false
	}
	switch key {
	case "j":
		m.playlistCursor = min(len(m.playlists)-1, m.playlistCursor+1)
	case "k":
		m.playlistCursor = max(0, m.playlistCursor-1)
	case "enter":
		m.switchPlaylist(m.playlistCursor)
	case "delete":
		m.playlistDialog = "delete"
	case "d", "D":
		m.notice = "Enter to open playlist, then D to select tracks to delete"
	case "a":
		if len(m.results) > 0 {
			i := m.playlistCursor
			m.syncViewed()
			m.playlists[i].Tracks = append(m.playlists[i].Tracks, m.results[m.cursor])
			normalizeTracks(m.playlists[i].Tracks)
			if m.playlists[i].ID == m.viewedID {
				m.queue = copyTracks(m.playlists[i].Tracks)
			}
			m.saveQueue()
			m.notice = "Added to " + m.playlists[i].Name
		}
	case "J", "K", "alt+up", "alt+down", "C": // track-only commands
	default:
		return false
	}
	return true
}
func (m *model) isPlayingEntry(t Track) bool {
	return m.viewedID == m.playingID && m.playing >= 0 && m.playing < len(m.playbackQueue) && t.EntryID != "" && t.EntryID == m.playbackQueue[m.playing].EntryID
}
func (m *model) playlistLabel() string {
	label := "TRACKS"
	if m.playlistMode {
		label = "PLAYLISTS"
	}
	if m.shuffle {
		label += " · H"
	}
	if m.repeatMode == "queue" {
		label += " · R:ALL"
	} else if m.repeatMode == "track" {
		label += " · R:1"
	}
	return label
}

// Uses only rows already allocated to the playlist, never changes geometry.
func (m *model) playlistRows(tracks []string, count, width int) []string {
	rows := make([]string, count)
	if count == 0 {
		return rows
	}
	header := "Queue: —"
	if m.playingID != "" {
		header = fmt.Sprintf("▶ %s · queue %d/%d", m.playingName, m.playing+1, len(m.playbackQueue))
	}
	rows[0] = dim + truncate(header, width) + reset
	if !m.playlistMode && m.playlistDialog == "" {
		name := "My Playlist"
		if len(m.playlists) > 0 {
			name = m.playlists[m.viewedIndex()].Name
		}
		status := ""
		if m.playingID != "" {
			status = fmt.Sprintf(" · queue %d/%d", m.playing+1, len(m.playbackQueue))
			if m.playingID != m.viewedID {
				status = " · ♪ " + m.playingName + status
			}
		}
		rows[0] = purple + playlistText("▾ "+name, width) + dim + playlistText(status, max(0, width-visiblePlaylistWidth("▾ "+name))) + reset
	}
	if m.playlistDialog != "" {
		prompt := "Name: "
		if m.playlistDialog == "delete" {
			prompt = "Delete " + m.playlists[m.playlistCursor].Name + "? Enter/Y · Esc/N"
		} else {
			tmp := *m
			tmp.edit = m.playlistEdit
			prompt = m.playlistDialog + ": " + tmp.editorView(max(1, width-len(m.playlistDialog)-2))
		}
		if count > 1 {
			rows[1] = bright + prompt + reset
		}
		if count > 2 && m.playlistDialog != "delete" {
			rows[2] = dim + "Enter save · Esc cancel" + reset
		}
		return rows
	}
	if !m.playlistMode {
		copy(rows[1:], tracks)
		return rows
	}
	start := max(0, m.playlistCursor-(count-1)+1)
	for row := 1; row < count; row++ {
		i := start + row - 1
		if i >= len(m.playlists) {
			break
		}
		p := m.playlists[i]
		mark := "▸ "
		if p.ID == m.viewedID {
			mark = "▾ "
		}
		col := dim
		if m.queueFocus && i == m.playlistCursor {
			col = bright
		}
		playing := ""
		if p.ID == m.playingID {
			playing = " ♪"
		}
		rows[row] = col + playlistText(fmt.Sprintf("%s%s%s · %d", mark, p.Name, playing, len(p.Tracks)), width) + reset
	}
	return rows
}
