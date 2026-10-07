package main

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strconv"
)

func visiblePlaylistWidth(s string) int { return ansi.StringWidth(clean(s)) }
func playlistText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	suffix := "..."
	if width < 3 {
		suffix = ""
	}
	return ansi.Truncate(clean(s), width, suffix)
}

// A fixed indentation and indicator slot keep the numbering/title aligned
// across selection, playback, and move/copy/delete selection states.
func (m *model) playlistTrackLine(t Track, index, total, width int, selected bool) string {
	mark, col := " ", dim
	if selected {
		mark = ">"
		col = bright
	}
	if m.isPlayingEntry(t) {
		mark = "♪"
		col = accent
		if selected {
			col = bright
		}
	}
	if m.transfer.stage != "" {
		if m.transfer.selected[t.EntryID] {
			mark = "✓"
		} else if !selected {
			mark = " "
		} else {
			mark = ">"
		}
		if selected {
			col = bright
		}
	}
	digits := max(2, len(strconv.Itoa(total)))
	prefix := fmt.Sprintf("    %s %0*d. ", mark, digits, index+1)
	duration := humanDuration(t.Duration)
	remaining := max(0, width-visiblePlaylistWidth(prefix))
	// On narrow terminals keep the hierarchy; omit duration before sacrificing
	// the fixed indentation/numbering or allowing either column to overlap.
	durationWidth := visiblePlaylistWidth(duration) + 2
	if remaining-durationWidth < 4 {
		duration = ""
		durationWidth = 0
	}
	titleWidth := max(0, remaining-durationWidth)
	title := playlistText(t.Title, titleWidth)
	line := col + prefix + title
	if duration != "" {
		line = col + prefix + pad(title, titleWidth) + dim + "  " + duration
	}
	return ansi.Truncate(line, width, "") + reset
}
