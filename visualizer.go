package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"math"
	"time"
)

// CAVA performs FFT on WASAPI PCM and emits logarithmically spaced bands.
// This stage handles display dynamics only; it never synthesizes audio levels.
type spectrumState struct {
	Target, Levels, Peaks []float64
	Received, Last        time.Time
}

func (s *spectrumState) Push(v []float64, now time.Time) {
	s.Target = s.Target[:0]
	if len(v) == 0 {
		s.Received = now
		return
	}
	// Keep the center band dominant while borrowing a little energy from its
	// neighbours. This removes one-band spikes without flattening the profile.
	for i, level := range v {
		left, right := level, level
		if i > 0 {
			left = v[i-1]
		}
		if i+1 < len(v) {
			right = v[i+1]
		}
		level = .64*level + .18*left + .18*right
		level *= .88
		// Soft knee: large peaks still rise, but consume less height as they
		// approach the top instead of being hard-clamped.
		if level > .65 {
			excess := level - .65
			level = .65 + excess/(1+.75*excess)
		}
		s.Target = append(s.Target, level)
	}
	s.Received = now
}
func (s *spectrumState) Step(now time.Time, active bool) {
	n := len(s.Target)
	if len(s.Levels) != n {
		s.Levels = make([]float64, n)
		s.Peaks = make([]float64, n)
	}
	dt := now.Sub(s.Last).Seconds()
	if s.Last.IsZero() {
		dt = 1. / 24
	}
	dt = math.Min(.25, math.Max(.001, dt))
	s.Last = now
	fresh := active && now.Sub(s.Received) < 400*time.Millisecond
	for i := 0; i < n; i++ {
		target := 0.
		if fresh {
			target = math.Max(0, math.Min(1, s.Target[i]))
		}
		tau := .22
		if target > s.Levels[i] {
			tau = .035
		}
		s.Levels[i] += (target - s.Levels[i]) * (1 - math.Exp(-dt/tau))
		if s.Levels[i] < .002 {
			s.Levels[i] = 0
		}
		s.Peaks[i] = math.Max(s.Levels[i], s.Peaks[i]-.55*dt)
	}
}
func interpolateBands(src []float64, n int) []float64 {
	out := make([]float64, n)
	if len(src) == 0 || n == 0 {
		return out
	}
	for i := range out {
		x := (float64(i)+.5)*float64(len(src))/float64(n) - .5
		x = math.Max(0, math.Min(float64(len(src)-1), x))
		a := int(x)
		b := min(a+1, len(src)-1)
		out[i] = src[a] + (src[b]-src[a])*(x-float64(a))
	}
	return out
}

type frameTickMsg time.Time

func (m *model) frameCmd() tea.Cmd {
	interval := time.Second / time.Duration(clamp(m.cfg.VisualizerFPS, 8, 60))
	if m.stopped && !m.loading && !m.searching && !m.inputMode {
		interval = 200 * time.Millisecond
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg { return frameTickMsg(t) })
}
func (m *model) position() float64 {
	p := m.snapshot.Time
	if !m.loading && !m.stopped && !m.snapshot.Paused && !m.snapshot.EOF && !m.snapshot.Updated.IsZero() {
		p += math.Min(.4, math.Max(0, time.Since(m.snapshot.Updated).Seconds()))
	}
	if m.snapshot.Duration > 0 {
		p = math.Min(p, m.snapshot.Duration)
	}
	return p
}
