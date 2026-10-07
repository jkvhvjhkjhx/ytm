package main

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"time"
)

// One bounded command worker owns transport ordering. UI updates never wait
// for the named pipe; outdated track commands are discarded before execution.
type playbackJob struct {
	epoch  uint64
	action func() error
	reply  chan error
}
type controlDone struct {
	generation int
	loaded     bool
	err        error
}

func (p *MPV) commandLoop() {
	defer close(p.workerDone)
	for {
		select {
		case <-p.closed:
			return
		case j := <-p.jobs:
			if j.epoch != p.epoch.Load() {
				j.reply <- context.Canceled
				continue
			}
			j.reply <- j.action()
		}
	}
}
func (m *model) control(loaded bool, action func() error) tea.Cmd {
	if m.player == nil {
		return nil
	}
	g := m.generation
	reply := make(chan error, 1)
	p := m.player
	job := playbackJob{uint64(g), action, reply}
	select {
	case <-p.closed:
		m.fail(fmt.Errorf("mpv disconnected; restart YTM"))
		return nil
	case p.jobs <- job:
	default:
		m.fail(fmt.Errorf("player busy; retry control"))
		return nil
	}
	return func() tea.Msg {
		select {
		case e := <-reply:
			return controlDone{g, loaded, e}
		case <-p.closed:
			return controlDone{g, loaded, fmt.Errorf("mpv disconnected")}
		}
	}
}
func (p *MPV) waitLoaded(path string, epoch uint64) error {
	until := time.NewTimer(12 * time.Second)
	defer until.Stop()
	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-p.closed:
			return fmt.Errorf("mpv disconnected")
		case <-until.C:
			return fmt.Errorf("mpv did not open audio within 12 seconds")
		case <-tick.C:
			if p.epoch.Load() != epoch {
				return context.Canceled
			}
			s := p.Snapshot()
			if s.PlaybackError != "" {
				return fmt.Errorf("%s", s.PlaybackError)
			}
			if s.Path == path && !s.Idle && s.Audio != "" && s.Duration > 0 {
				return nil
			}
		}
	}
}

func (m *model) acceptSnapshot(s PlayerSnapshot) tea.Cmd {
	// An old file's EOF/property notification must not advance a newly queued file.
	if m.loading || m.stopped || m.audioPath == "" || s.Path != m.audioPath {
		return nil
	}
	m.snapshot = s
	if s.PlaybackError != "" {
		m.fail(fmt.Errorf("%s", s.PlaybackError))
		m.stopped = true
		return nil
	}
	if s.EOF {
		m.stopped = true
		return m.advance(true, 1)
	}
	return nil
}
func (m *model) playbackState() string {
	if m.loading {
		return "BUFFERING"
	}
	if m.errText != "" && m.stopped {
		return "UNAVAILABLE"
	}
	if m.stopped {
		return "STOPPED"
	}
	if m.snapshot.Paused {
		return "PAUSED"
	}
	return "PLAYING"
}
