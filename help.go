package main

import (
	"fmt"
	"io"
	"strings"
)

type helpGroup struct {
	name string
	rows []string
}

// Keep the help text in one registry so the overlay remains the source of
// truth for the normal-mode key bindings.
var helpRegistry = []helpGroup{
	{"PLAYBACK · NORMAL MODE", []string{
		"Space        play / pause",
		"S            stop",
		"N / P        next / previous track",
		"Left / Right  seek -/+ 5 seconds",
		"Up / Down     volume +/− 5%",
		"H / R         shuffle / repeat",
	}},
	{"SEARCH · SEARCH EDITOR", []string{
		"/            open search editor",
		"Enter        search query",
		"Esc           close editor",
		"Ctrl+A        select query",
		"Ctrl+C / X    copy / cut",
		"Ctrl+V        paste",
		"Arrows        move caret / selection",
		"Home / End     start / end of query",
	}},
	{"PLAYLISTS · PLAYLIST BROWSER", []string{
		"Tab           focus playlist / results",
		"j / k         choose playlist",
		"Enter         open selected playlist",
		"Ctrl+N        create playlist",
		"F2            rename playlist",
		"Delete        delete selected playlist",
		"Ctrl+D        delete open playlist",
		"Enter / Y     confirm delete",
		"Esc / N       cancel delete",
		"Esc           from TRACKS: return to PLAYLISTS",
	}},
	{"TRACKS · PLAYLIST OPEN", []string{
		"j / k         choose track",
		"Enter         play selected track",
		"A             add selected search result",
		"D / Delete    select tracks to delete",
		"Ctrl+D        delete open playlist",
		"J / K         reorder track",
		"Alt+Down/Up    reorder track",
		"C             clear playlist",
		"Edits change playlist; active playback queue stays",
	}},
	{"MULTI-SELECT · MOVE / COPY / DELETE", []string{
		"m             move selected tracks",
		"Shift+M       copy selected tracks",
		"d / Delete    select tracks to delete",
		"j / k or ↑/↓   navigate tracks / targets",
		"Space         select / unselect track",
		"Ctrl+A        select all tracks",
		"Ctrl+D        clear selection",
		"Enter         choose target / apply delete",
		"Esc           cancel; target: go back",
	}},
	{"DESTINATION · MOVE / COPY", []string{
		"j / k or ↑/↓   choose destination",
		"Enter         confirm destination",
		"+ Create New   choose, then enter a name",
		"Esc           return; keep track selection",
	}},
	{"PLAYLIST NAME EDITOR", []string{
		"Type          enter playlist name",
		"Enter         save name",
		"Esc           cancel / go back",
	}},
	{"APP / HELP", []string{
		"V             switch spectrum style",
		"T             toggle this help",
		"J / K or ↑/↓   scroll help; Esc closes help",
		"Q             quit",
		"Keys act in the mode shown above",
	}},
}

func helpLines() []string {
	var lines []string
	for _, group := range helpRegistry {
		lines = append(lines, group.name)
		lines = append(lines, group.rows...)
		lines = append(lines, "")
	}
	return lines
}

func (m *model) drawHelpOverlay() {
	if !m.helpOpen || m.output == nil || m.width < 10 || m.height < 8 {
		return
	}
	lines := helpLines()
	boxW := min(76, max(30, m.width-4))
	boxH := min(len(lines)+4, max(6, m.height-4))
	innerH := boxH - 4
	maxOffset := max(0, len(lines)-innerH)
	m.helpOffset = clamp(m.helpOffset, 0, maxOffset)
	left := max(1, (m.width-boxW)/2)
	top := max(1, (m.height-boxH)/2)
	var out strings.Builder
	border := dim + "─" + reset
	fmt.Fprintf(&out, "\x1b[%d;%dH%s%s%s", top, left, purple, "╭"+strings.Repeat("─", boxW-2)+"╮", reset)
	for row := 0; row < boxH-2; row++ {
		text := ""
		if row == 0 {
			text = accent + "KEYBOARD HELP" + reset
		} else if row == boxH-3 {
			text = dim + "J/K or ↑/↓ scroll · T/Esc close" + reset
		} else {
			idx := m.helpOffset + row - 1
			if idx >= 0 && idx < len(lines) {
				if isHelpGroup(lines[idx]) {
					text = purple + lines[idx] + reset
				} else {
					text = dim + lines[idx] + reset
				}
			}
		}
		fmt.Fprintf(&out, "\x1b[%d;%dH%s%s%s", top+row+1, left, dim, "│", reset)
		fmt.Fprintf(&out, "\x1b[%d;%dH%s%s", top+row+1, left+2, text, strings.Repeat(" ", max(0, boxW-4-visibleHelpWidth(text))))
		fmt.Fprintf(&out, "\x1b[%d;%dH%s%s%s", top+row+1, left+boxW-1, dim, "│", reset)
	}
	fmt.Fprintf(&out, "\x1b[%d;%dH%s%s%s", top+boxH-1, left, purple, "╰"+strings.Repeat("─", boxW-2)+"╯", reset)
	_, _ = io.WriteString(m.output, out.String())
	_ = border
}

func isHelpGroup(s string) bool {
	for _, g := range helpRegistry {
		if s == g.name {
			return true
		}
	}
	return false
}

func visibleHelpWidth(s string) int {
	return len([]rune(clean(s)))
}
