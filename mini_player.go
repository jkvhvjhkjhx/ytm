package main

import (
	"encoding/json"
	"image"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Scoop's mpv.exe may be a launcher shim. Ask the player over its private IPC
// pipe for the actual audio process instead of trusting exec.Cmd.Process.Pid.
func miniMPVPID(p *MPV) uint32 {
	data, err := p.request("get_property", "pid")
	var pid uint32
	if err != nil || json.Unmarshal(data, &pid) != nil {
		return 0
	}
	return pid
}

// These settings deliberately belong to the companion, not the TUI renderer.
type MiniConfig struct {
	Enabled           bool   `toml:"enabled"`
	ShowWhenMinimized bool   `toml:"show_when_minimized"`
	AlwaysVisible     bool   `toml:"always_visible"`
	Thumbnail         bool   `toml:"thumbnail"`
	Visualizer        bool   `toml:"visualizer"`
	Color             string `toml:"color"`
}

func defaultMiniConfig() MiniConfig {
	return MiniConfig{Enabled: true, ShowWhenMinimized: true, Thumbnail: true, Visualizer: true, Color: "automatic"}
}
func normalizeMiniConfig(c MiniConfig) MiniConfig {
	if c.Color != "theme" {
		c.Color = "automatic"
	}
	return c
}

type miniActionMsg int

const (
	miniPause miniActionMsg = iota + 1
	miniNext
	miniPrevious
	miniExit
	miniEnabled       = 100
	miniWhenMinimized = 101
	miniAlways        = 102
	miniThumbnail     = 103
	miniVisualizer    = 104
	miniColor         = 105
)

type miniInput struct {
	Settings   MiniConfig
	Art        image.Image // immutable image, owned by the existing artwork loader
	Generation int
	Title      string
	Active     bool
	Bands      [96]float64
	Count      int
	Received   time.Time
}

// Each YTM process owns one companion and one latest-frame mailbox. No IPC,
// extra audio capture, helper process, network access or unbounded frame queue.
type miniPlayer struct {
	mu                   sync.Mutex
	input                miniInput
	status               miniStatus
	actions              chan miniActionMsg
	done                 chan struct{}
	hwnd                 atomic.Uintptr
	closeOnce            sync.Once
	mpvPID               uint32
	capturePID           atomic.Uint32
	clockRate            chan time.Duration
	clockStop, clockDone chan struct{}
	tickPending          atomic.Bool
}

// Go uses the Windows high-resolution waitable timer where available. Win32
// SetTimer(33) is commonly quantized to ~47 ms on 15.6 ms system clocks.
// Coalesce posts so a busy UI thread can never build an unbounded tick queue.
func (p *miniPlayer) runClock(period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	defer close(p.clockDone)
	for {
		select {
		case <-ticker.C:
			if p.tickPending.CompareAndSwap(false, true) {
				if ok, _, _ := miniPostMessageProc.Call(p.hwnd.Load(), 0x113, 1, 0); ok == 0 {
					p.tickPending.Store(false)
				}
			}
		case next := <-p.clockRate:
			ticker.Reset(next)
		case <-p.clockStop:
			return
		}
	}
}

// Small diagnostic snapshot; no model or native window data is shared unsafely.
type miniStatus struct {
	Minimized, Hidden, Fullscreen, Cloaked bool
	ConflictingPID                         uint32
	Host, Overlay, Terminal                uintptr
	Visible, AudioAllowed, TrayAdded       bool
	AudioReason                            string
	Frames                                 uint64
	Width, Height, Count                   int
	Peak                                   float64
}

func (p *miniPlayer) Status() miniStatus { p.mu.Lock(); defer p.mu.Unlock(); return p.status }

func (p *miniPlayer) publish(in miniInput) {
	p.mu.Lock()
	if p.input.Generation == in.Generation {
		in.Bands, in.Count, in.Received = p.input.Bands, p.input.Count, p.input.Received
	}
	p.input = in
	p.mu.Unlock()
}
func (p *miniPlayer) pushBands(b []float64, now time.Time) {
	p.mu.Lock()
	p.input.Count = copy(p.input.Bands[:], b)
	p.input.Received = now
	p.mu.Unlock()
}
func (p *miniPlayer) read() miniInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input
}
func (p *miniPlayer) action(a miniActionMsg) {
	// Shell callbacks never wait on Bubble Tea or mpv's IPC worker.
	select {
	case p.actions <- a:
	default:
	}
}
func (m *model) publishMini() {
	if m.mini == nil {
		return
	}
	m.mini.publish(miniInput{Settings: m.cfg.MiniPlayer, Art: m.art, Generation: m.generation,
		Title: clean(m.current.Title), Active: !m.stopped && !m.loading && !m.snapshot.Paused && !m.snapshot.Idle && !m.snapshot.EOF})
}
func (m *model) handleMiniAction(a miniActionMsg) tea.Cmd {
	switch a {
	case miniPause:
		if m.player != nil && !m.stopped && !m.loading {
			return m.control(false, m.player.TogglePause)
		}
	case miniNext:
		return m.playNext(1)
	case miniPrevious:
		return m.playNext(-1)
	case miniExit:
		if m.searchCancel != nil {
			m.searchCancel()
		}
		if m.cancel != nil {
			m.cancel()
		}
		m.saveQueue()
		return tea.Quit
	case miniEnabled:
		m.cfg.MiniPlayer.Enabled = !m.cfg.MiniPlayer.Enabled
	case miniWhenMinimized:
		m.cfg.MiniPlayer.ShowWhenMinimized = !m.cfg.MiniPlayer.ShowWhenMinimized
	case miniAlways:
		m.cfg.MiniPlayer.AlwaysVisible = !m.cfg.MiniPlayer.AlwaysVisible
	case miniThumbnail:
		m.cfg.MiniPlayer.Thumbnail = !m.cfg.MiniPlayer.Thumbnail
	case miniVisualizer:
		m.cfg.MiniPlayer.Visualizer = !m.cfg.MiniPlayer.Visualizer
	case miniColor:
		if m.cfg.MiniPlayer.Color == "theme" {
			m.cfg.MiniPlayer.Color = "automatic"
		} else {
			m.cfg.MiniPlayer.Color = "theme"
		}
	default:
		return nil
	}
	if a >= miniEnabled {
		if err := saveConfig(m.cfg); err != nil {
			m.fail(err)
		}
	}
	return nil
}

func miniShouldShow(c MiniConfig, hostKnown, minimized, hidden, fullscreen, cloaked bool) bool {
	if !c.Enabled || fullscreen || cloaked {
		return false
	}
	return c.AlwaysVisible || hostKnown && c.ShowWhenMinimized && (minimized || hidden)
}
