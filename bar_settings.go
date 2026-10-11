package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"io"
	"strings"
)

func (m *model) presetKey(k tea.KeyMsg) {
	switch k.String() {
	case "esc", "f4", "enter":
		m.presetOpen = false
		m.rendered = nil
		m.artDirty = true
	case "j", "down", "right", "k", "up", "left":
		current := presetFor(m.cfg.VisualizerEffect).ID
		index := 0
		for i, p := range barPresets {
			if p.ID == current {
				index = i
			}
		}
		delta := 1
		if k.String() == "k" || k.String() == "up" || k.String() == "left" {
			delta = -1
		}
		m.cfg.VisualizerEffect = barPresets[(index+delta+len(barPresets))%len(barPresets)].ID
		if err := saveConfig(m.cfg); err != nil {
			m.presetNotice = "Not saved: " + err.Error()
		} else {
			m.presetNotice = "Saved"
		}
	}
}

func (m *model) drawPresetOverlay() {
	if m.output == nil || m.width < 12 || m.height < 8 {
		return
	}
	w, h := min(56, m.width-2), min(14, m.height-2)
	x, y := (m.width-w)/2, (m.height-h)/2
	lines := make([]string, h)
	lines[0] = accent + "VISUALIZER EFFECT" + reset
	selected := 0
	for i, p := range barPresets {
		if p.ID == presetFor(m.cfg.VisualizerEffect).ID {
			selected = i
		}
	}
	count := min(5, h-4)
	start := max(0, selected-count+1)
	for i := 0; i < count && start+i < len(barPresets); i++ {
		p := barPresets[start+i]
		mark := "  "
		if p.ID == m.cfg.VisualizerEffect {
			mark = "> "
		}
		lines[1+i] = mark + p.Name
		if p.ID == "glass" {
			lines[1+i] += "  (recommended)"
		}
	}
	previewTop := 1 + count
	previewHeight := h - previewTop - 3
	if previewHeight > 0 {
		p := presetFor(m.cfg.VisualizerEffect)
		c := makeBarCanvas(m.bars, w-2, p, detectBarCaps())
		var tint []string
		if p.ID == "classic" {
			tint = spectrumTint()
		}
		for row := 0; row < previewHeight; row++ {
			line := c.line(previewHeight, row, m.cfg.VisualizerStyle)
			if p.ID == "classic" {
				line = spectrumLine(m.bars, w-2, previewHeight, row, m.cfg.VisualizerStyle, tint)
			}
			lines[previewTop+row] = line
		}
	}
	lines[h-3] = "J/K or arrows: preview · Enter/Esc/F4: close"
	lines[h-2] = "Auto-balanced · " + m.presetNotice
	lines[h-1] = "Audio: device loopback (includes other apps)"
	var out strings.Builder
	for row, line := range lines {
		fmt.Fprintf(&out, "\x1b[%d;%dH%s%s", y+row+1, x+1, pad(ansi.Truncate(line, w, ""), w), reset)
	}
	_, _ = io.WriteString(m.output, out.String())
}
