package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"io"
	"strings"
)

type trackTransfer struct {
	stage, sourceID string
	copying         bool
	deleting        bool
	selected        map[string]bool
	target          int
	name            editor
	err             string
}

func (m *model) beginTransfer(copying bool) {
	if len(m.queue) == 0 {
		m.notice = "No tracks to transfer"
		return
	}
	m.syncViewed()
	m.transfer = trackTransfer{stage: "select", sourceID: m.viewedID, copying: copying, selected: map[string]bool{}}
	m.transferHint()
}
func (m *model) transferHint() {
	verb := "Move"
	if m.transfer.copying {
		verb = "Copy"
	}
	if m.transfer.deleting {
		verb = "Delete"
	}
	m.notice = fmt.Sprintf("%s · %d selected · Space tick · Ctrl+A all · Ctrl+D clear · Enter continue · Esc cancel", verb, len(m.transfer.selected))
	if m.transfer.deleting {
		m.notice = fmt.Sprintf("Delete · %d selected · Space tick · Ctrl+A all · Ctrl+D clear · Enter delete · Esc cancel", len(m.transfer.selected))
	}
	if m.transfer.err != "" {
		m.notice = m.transfer.err
	}
}
func (m *model) closeTransferOverlay() {
	// Invalidate covered cells so both text and existing image renderer restore
	// the underlying screen on the next draw, without changing any geometry.
	m.rendered = nil
}
func (m *model) transferKey(k tea.KeyMsg) {
	key := k.String()
	switch m.transfer.stage {
	case "select":
		switch key {
		case "esc":
			deleting := m.transfer.deleting
			m.transfer = trackTransfer{}
			m.notice = "Transfer cancelled"
			if deleting {
				m.notice = "Deletion cancelled"
			}
		case "j", "J", "down":
			m.queueCursor = min(len(m.queue)-1, m.queueCursor+1)
		case "k", "K", "up":
			m.queueCursor = max(0, m.queueCursor-1)
		case " ":
			if m.queueCursor >= 0 && m.queueCursor < len(m.queue) {
				id := m.queue[m.queueCursor].EntryID
				if m.transfer.selected[id] {
					delete(m.transfer.selected, id)
				} else {
					m.transfer.selected[id] = true
				}
			}
		case "ctrl+a":
			for _, t := range m.queue {
				m.transfer.selected[t.EntryID] = true
			}
		case "ctrl+d":
			m.transfer.selected = map[string]bool{}
		case "enter":
			if len(m.transfer.selected) > 0 {
				if m.transfer.deleting {
					if err := m.commitDeleteTracks(); err != nil {
						m.transfer.err = err.Error()
						m.notice = m.transfer.err
					} else {
						m.transfer = trackTransfer{}
					}
					return
				}
				m.transfer.stage = "target"
				m.transfer.target = 0
				// Default to a usable destination; source remains visible but disabled.
				for m.transfer.target < len(m.playlists) && m.playlists[m.transfer.target].ID == m.transfer.sourceID {
					m.transfer.target++
				}
			}
		}
		if m.transfer.stage == "select" {
			m.transferHint()
		}
	case "target":
		switch key {
		case "esc":
			m.transfer.stage = "select"
			m.transfer.err = ""
			m.closeTransferOverlay()
			m.transferHint()
		case "j", "J", "down":
			m.transfer.target = min(len(m.playlists), m.transfer.target+1)
		case "k", "K", "up":
			m.transfer.target = max(0, m.transfer.target-1)
		case "enter":
			if m.transfer.target == len(m.playlists) {
				m.transfer.stage = "name"
				m.transfer.name = editor{}
				m.transfer.err = ""
			} else {
				m.finishTransfer(m.playlists[m.transfer.target].ID, "")
			}
		}
	case "name":
		switch key {
		case "esc":
			m.transfer.stage = "target"
			m.transfer.err = ""
		case "enter":
			name := strings.TrimSpace(clean(string(m.transfer.name.text)))
			if name == "" || len([]rune(name)) > 120 {
				m.transfer.err = "Enter a name (1–120 characters)"
				return
			}
			m.finishTransfer("", name)
		default:
			m.transfer.name.key(k)
		}
	}
}
func trackIdentity(t Track) string {
	if t.ID != "" {
		return "https://www.youtube.com/watch?v=" + t.ID
	}
	if u := canonicalYouTubeURL(t.URL); u != "" {
		return u
	}
	return "entry:" + t.EntryID
}
func (m *model) finishTransfer(targetID, newName string) {
	if err := m.commitTransfer(targetID, newName); err != nil {
		m.transfer.err = err.Error()
		return
	}
	m.transfer = trackTransfer{}
	m.closeTransferOverlay()
}
func (m *model) commitTransfer(targetID, newName string) error {
	if m.libraryReadOnly {
		return fmt.Errorf("Library cannot be saved; tracks unchanged")
	}
	if m.transfer.sourceID != m.viewedID {
		return fmt.Errorf("Source changed; cancel and try again")
	}
	if len(m.transfer.selected) == 0 {
		return fmt.Errorf("Select at least one track")
	}
	if targetID == m.transfer.sourceID {
		if m.transfer.copying {
			return fmt.Errorf("Already in this playlist; nothing copied")
		}
		return fmt.Errorf("Choose a different destination")
	}
	// Deep-copy everything that could change; no live data is mutated before
	// the disk transaction succeeds, including creation of a new playlist.
	candidate := make([]Playlist, len(m.playlists))
	source, dest := -1, -1
	for i, p := range m.playlists {
		candidate[i] = p
		candidate[i].Tracks = copyTracks(p.Tracks)
		if p.ID == m.transfer.sourceID {
			source = i
		}
		if p.ID == targetID {
			dest = i
		}
	}
	if source < 0 {
		return fmt.Errorf("Source playlist no longer exists")
	}
	candidate[source].Tracks = copyTracks(m.queue)
	if newName != "" {
		candidate = append(candidate, Playlist{ID: newPlaylistID(), Name: newName})
		dest = len(candidate) - 1
	}
	if dest < 0 {
		return fmt.Errorf("Destination no longer exists")
	}
	seen := map[string]bool{}
	for _, t := range candidate[dest].Tracks {
		seen[trackIdentity(t)] = true
	}
	kept := make([]Track, 0, len(m.queue))
	selected, added := 0, 0
	for _, t := range m.queue {
		if !m.transfer.selected[t.EntryID] {
			kept = append(kept, t)
			continue
		}
		selected++
		identity := trackIdentity(t)
		if !seen[identity] {
			entry := t
			if m.transfer.copying {
				entry.EntryID = newPlaylistID()
			}
			candidate[dest].Tracks = append(candidate[dest].Tracks, entry)
			seen[identity] = true
			added++
		}
		if m.transfer.copying {
			kept = append(kept, t)
		}
	}
	if selected != len(m.transfer.selected) {
		return fmt.Errorf("Selection changed; cancel and try again")
	}
	candidate[source].Tracks = kept
	normalizeTracks(candidate[dest].Tracks)
	if m.persist {
		position := 0.
		if !m.loading {
			position = m.snapshot.Time
		}
		state := savedPlaylist{Version: 3, Index: m.playing, Position: position, Playlists: candidate, ViewedID: m.viewedID, PlayingID: m.playingID, PlayingName: m.playingName, PlaybackQueue: copyTracks(m.playbackQueue)}
		if err := writePlaylistState(state); err != nil {
			return fmt.Errorf("Save failed; tracks unchanged: %w", err)
		}
	}
	m.playlists = candidate
	m.queue = copyTracks(candidate[source].Tracks)
	m.queueCursor = clamp(m.queueCursor, 0, max(0, len(m.queue)-1))
	verb, count := "Moved", selected
	if m.transfer.copying {
		verb, count = "Copied", added
	}
	m.notice = fmt.Sprintf("%s %d tracks to %s", verb, count, candidate[dest].Name)
	if skipped := selected - added; skipped > 0 {
		m.notice += fmt.Sprintf(" · %d already present", skipped)
	}
	return nil
}

func (m *model) transferOverlayRows(width, height int) []string {
	if width < 8 || height < 5 {
		return nil
	}
	w := min(64, width-2)
	h := min(12, height-2)
	if h < 5 {
		h = 5
	}
	inner := w - 4
	title := "Move selected tracks to:"
	if m.transfer.copying {
		title = "Copy selected tracks to:"
	}
	rows := []string{purple + "╭" + strings.Repeat("─", w-2) + "╮" + reset}
	line := func(s string) {
		rows = append(rows, dim+"│ "+reset+pad(ansi.Truncate(s, inner, ""), inner)+dim+" │"+reset)
	}
	line(accent + truncate(title, inner) + reset)
	visible := h - 4
	start := max(0, m.transfer.target-visible+1)
	for r := 0; r < visible; r++ {
		s := ""
		if m.transfer.stage == "name" {
			if r == 0 {
				tmp := *m
				tmp.edit = m.transfer.name
				s = tmp.editorView(inner)
			}
		} else {
			i := start + r
			if i <= len(m.playlists) {
				name := "+ Create New Playlist"
				if i < len(m.playlists) {
					name = m.playlists[i].Name
					if m.playlists[i].ID == m.transfer.sourceID {
						name += " (source)"
					}
				}
				mark := "  "
				col := dim
				if i == m.transfer.target {
					mark = "› "
					col = bright
				}
				s = col + truncate(mark+name, inner) + reset
			}
		}
		line(s)
	}
	footer := "Enter confirm · Esc back"
	if m.transfer.stage == "name" {
		footer = "Playlist name · Enter create · Esc back"
	}
	if m.transfer.err != "" {
		footer = m.transfer.err
	}
	line(dim + truncate(footer, inner) + reset)
	rows = append(rows, purple+"╰"+strings.Repeat("─", w-2)+"╯"+reset)
	return rows
}
func (m *model) drawTransferOverlay() {
	if m.output == nil {
		return
	}
	rows := m.transferOverlayRows(m.width, m.height)
	if len(rows) == 0 {
		return
	}
	w := ansi.StringWidth(rows[0])
	x := max(1, (m.width-w)/2+1)
	y := max(1, (m.height-len(rows))/2+1)
	var out strings.Builder
	for i, row := range rows {
		fmt.Fprintf(&out, "\x1b[%d;%dH%s%s", y+i, x, reset, row)
	}
	_, _ = io.WriteString(m.output, out.String())
}
