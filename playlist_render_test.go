package main

import (
	"github.com/charmbracelet/x/ansi"
	"reflect"
	"strings"
	"testing"
)

func TestPlaylistTrackHierarchyFormatting(t *testing.T) {
	m := playlistFixture()
	track := m.queue[1]
	track.Title = "Valentine"
	plain := ansi.Strip(m.playlistTrackLine(track, 1, 3, 44, false))
	selected := ansi.Strip(m.playlistTrackLine(track, 1, 3, 44, true))
	if !strings.HasPrefix(plain, "      02. Valentine") || !strings.HasPrefix(selected, "    > 02. Valentine") {
		t.Fatal("indent/numbering", plain, selected)
	}
	for _, focus := range []bool{false, true} {
		line := m.playlistTrackLine(m.queue[0], 0, 3, 44, focus)
		if !strings.HasPrefix(ansi.Strip(line), "    ♪ 01. ") {
			t.Fatal("playing indicator", line)
		}
		expectedColor := accent
		if focus {
			expectedColor = bright
		}
		if !strings.HasPrefix(line, expectedColor) {
			t.Fatal("playing selection highlight")
		}
	}
	m.transfer = trackTransfer{stage: "select", selected: map[string]bool{track.EntryID: true}}
	ticked := ansi.Strip(m.playlistTrackLine(track, 1, 3, 44, true))
	if !strings.HasPrefix(ticked, "    ✓ 02. Valentine") {
		t.Fatal("tick alignment", ticked)
	}
}

func TestPlaylistTrackTruncationAndLargeNumberAlignment(t *testing.T) {
	m := playlistFixture()
	track := Track{Title: strings.Repeat("Âm nhạc 日本 ", 20), Duration: 123}
	for _, width := range []int{1, 8, 18, 24, 40, 80} {
		line := ansi.Strip(m.playlistTrackLine(track, 1, 120, width, true))
		if ansi.StringWidth(line) > width {
			t.Fatal("overflow", width, line)
		}
		if width >= 18 && !strings.HasPrefix(line, "    > 002. ") {
			t.Fatal("prefix changed", line)
		}
		if width >= 24 && (!strings.Contains(line, "...") || strings.Contains(line, "…")) {
			t.Fatal("ellipsis", line)
		}
	}
	for _, index := range []int{0, 9, 99, 119} {
		line := ansi.Strip(m.playlistTrackLine(Track{Title: "Title"}, index, 120, 44, false))
		if strings.Index(line, "Title") != 11 {
			t.Fatal("numbering shifted title", line)
		}
	}
}

func TestPlaylistParentRowsAndNoStateMutation(t *testing.T) {
	m := playlistFixture()
	m.queueFocus = true
	m.playlists = append(m.playlists, Playlist{ID: "other", Name: "Favorites"})
	beforeQueue := copyTracks(m.queue)
	beforePlayback := copyTracks(m.playbackQueue)
	rows := m.playlistRows([]string{"child"}, 5, 48)
	if !strings.HasPrefix(ansi.Strip(rows[0]), "▾ My Playlist") || rows[1] != "child" {
		t.Fatal("parent hierarchy", rows)
	}
	m.playlistMode = true
	rows = m.playlistRows(nil, 5, 48)
	if !strings.HasPrefix(ansi.Strip(rows[1]), "▾ My Playlist") || !strings.HasPrefix(ansi.Strip(rows[2]), "▸ Favorites") {
		t.Fatal("playlist markers", rows)
	}
	if !reflect.DeepEqual(beforeQueue, m.queue) || !reflect.DeepEqual(beforePlayback, m.playbackQueue) || m.playing != 0 {
		t.Fatal("renderer changed state")
	}
}
