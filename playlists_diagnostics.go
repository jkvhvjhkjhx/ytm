package main

import (
	"fmt"
	"time"
)

// Real EOF events and mpv output, using tracks already extracted by self-check.
func checkLivePlaylistModes(m *model) error {
	fmt.Println("      Multi-playlist: real EOF repeat-one / repeat-playlist / shuffle+repeat")
	if len(m.playbackQueue) != 2 {
		return fmt.Errorf("expected independent two-track playback queue")
	}
	m.switchPlaylist(1)
	finish := func() error {
		if err := waitPlayback(m.player, .3); err != nil {
			return err
		}
		if _, err := m.player.request("seek", m.player.Snapshot().Duration-.15, "absolute+exact"); err != nil {
			return err
		}
		until := time.Now().Add(5 * time.Second)
		for !m.player.Snapshot().EOF && time.Now().Before(until) {
			time.Sleep(50 * time.Millisecond)
		}
		snap := m.player.Snapshot()
		if !snap.EOF {
			return fmt.Errorf("missing real EOF")
		}
		cmd := m.acceptSnapshot(snap)
		*m = applyCommand(*m, cmd)
		if m.errText != "" {
			return fmt.Errorf("playlist playback: %s", m.errText)
		}
		if m.viewedID == m.playingID || len(m.queue) != 1 || m.queue[0].ID != "unplayed" {
			return fmt.Errorf("auto-next changed viewed playlist")
		}
		return nil
	}
	m.repeatMode = "track"
	old := m.current.ID
	if err := finish(); err != nil {
		return err
	}
	if m.current.ID != old || m.stopped {
		return fmt.Errorf("repeat one failed")
	}
	m.repeatMode = "queue"
	for i := 0; i < 2; i++ {
		expected := (m.playing + 1) % 2
		if err := finish(); err != nil {
			return err
		}
		if m.playing != expected {
			return fmt.Errorf("repeat playlist failed")
		}
	}
	m.shuffle = true
	m.resetOrder()
	// First cycle includes the currently playing entry, then one remaining entry.
	seen := map[int]bool{m.playing: true}
	if err := finish(); err != nil {
		return err
	}
	if seen[m.playing] {
		return fmt.Errorf("shuffle repeated before cycle completed")
	}
	seen = map[int]bool{}
	for i := 0; i < 2; i++ {
		if err := finish(); err != nil {
			return err
		}
		if seen[m.playing] {
			return fmt.Errorf("shuffle-repeat omitted/repeated entry")
		}
		seen[m.playing] = true
	}
	m.repeatMode = "track"
	old = m.current.ID
	if err := finish(); err != nil {
		return err
	}
	if m.current.ID != old {
		return fmt.Errorf("shuffle + repeat one failed")
	}
	m.shuffle = false
	m.repeatMode = "off"
	m.resetOrder()
	if err := waitPlayback(m.player, .3); err != nil {
		return err
	}
	fmt.Println("      PASS: viewed playlist independent; active queue and repeat cycles play real audio")
	return nil
}
