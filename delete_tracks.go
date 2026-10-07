package main

import "fmt"

func (m *model) beginDeleteTracks() {
	if len(m.queue) == 0 {
		m.notice = "No tracks to delete"
		return
	}
	m.syncViewed()
	m.transfer = trackTransfer{stage: "select", sourceID: m.viewedID, deleting: true, selected: map[string]bool{}}
	m.errText = ""
	m.transferHint()
}

// Publish deletions only after the complete library is safely saved. The
// playback snapshot, current track and transport are deliberately untouched.
func (m *model) commitDeleteTracks() error {
	if m.libraryReadOnly {
		return fmt.Errorf("Library cannot be saved; tracks unchanged")
	}
	if m.transfer.sourceID != m.viewedID {
		return fmt.Errorf("Source changed; cancel and try again")
	}
	if len(m.transfer.selected) == 0 {
		return fmt.Errorf("Select at least one track")
	}
	candidate := make([]Playlist, len(m.playlists))
	source := -1
	for i, p := range m.playlists {
		candidate[i] = p
		candidate[i].Tracks = copyTracks(p.Tracks)
		if p.ID == m.transfer.sourceID {
			source = i
		}
	}
	if source < 0 {
		return fmt.Errorf("Source playlist no longer exists")
	}
	kept := make([]Track, 0, len(m.queue))
	removed := 0
	for _, track := range m.queue {
		if m.transfer.selected[track.EntryID] {
			removed++
		} else {
			kept = append(kept, track)
		}
	}
	if removed != len(m.transfer.selected) {
		return fmt.Errorf("Selection changed; cancel and try again")
	}
	candidate[source].Tracks = kept
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
	m.queue = copyTracks(kept)
	m.queueCursor = clamp(m.queueCursor, 0, max(0, len(m.queue)-1))
	m.notice = fmt.Sprintf("Deleted %d tracks from %s · playback queue unchanged", removed, candidate[source].Name)
	return nil
}
