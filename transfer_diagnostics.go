package main

import (
	"fmt"
	"reflect"
	"time"
)

func checkLiveTrackTransfer(m *model) error {
	fmt.Println("      Move/copy while mpv is playing")
	m.switchPlaylist(0)
	// This diagnostic library is in memory only; use real, already playing tracks.
	m.queue = copyTracks(m.playbackQueue)
	m.syncViewed()
	before := m.player.Snapshot()
	queue := copyTracks(m.playbackQueue)
	current := m.current
	gen := m.generation
	m.beginTransfer(false)
	for _, track := range m.queue {
		m.transfer.selected[track.EntryID] = true
	}
	m.finishTransfer(m.playlists[1].ID, "")
	if m.transfer.stage != "" || len(m.queue) != 0 {
		return fmt.Errorf("live move failed: %s", m.transfer.err)
	}
	time.Sleep(400 * time.Millisecond)
	after := m.player.Snapshot()
	if after.Path != before.Path || after.Time <= before.Time || after.Idle || m.generation != gen || current != m.current || !reflect.DeepEqual(queue, m.playbackQueue) {
		return fmt.Errorf("transfer interrupted playback")
	}
	m.switchPlaylist(1)
	m.beginTransfer(true)
	for _, track := range m.queue {
		if track.ID == current.ID {
			m.transfer.selected[track.EntryID] = true
		}
	}
	original := copyTracks(m.queue)
	m.finishTransfer("", "Transfer copy check")
	if m.transfer.stage != "" || !reflect.DeepEqual(original, m.queue) || m.generation != gen || !reflect.DeepEqual(queue, m.playbackQueue) {
		return fmt.Errorf("live copy failed")
	}
	fmt.Println("      PASS: move current track + copy; mpv position advances, queue unchanged")
	return nil
}
