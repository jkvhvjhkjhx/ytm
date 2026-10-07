package main

import (
	"bufio"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func updateModel(m model, msg tea.Msg) (model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(model), cmd
}
func applyCommand(m model, cmd tea.Cmd) model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			m = applyCommand(m, child)
		}
		return m
	}
	m, next := updateModel(m, msg)
	if _, tick := msg.(playerTickMsg); !tick && next != nil {
		return applyCommand(m, next)
	}
	return m
}
func key(m model, s string) (model, tea.Cmd) {
	return updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}
func waitPlayback(p *MPV, seconds float64) error {
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		s := p.Snapshot()
		if s.Time >= seconds && s.Audio != "" {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("no advancing audio output: %+v", p.Snapshot())
}
func runPipelineCheck(cfg Config, mpvPath, ytdlpPath, cavaPath, runtime, query string) error {
	fmt.Println("[1/8] Search using /, Unicode query, Enter")
	ui := newModel(cfg, nil, ytdlpPath, runtime)
	ui.output = nil
	ui, _ = key(ui, "/")
	ui, _ = key(ui, query)
	ui, cmd := updateModel(ui, tea.KeyMsg{Type: tea.KeyEnter})
	ui = applyCommand(ui, cmd)
	if len(ui.results) == 0 {
		return fmt.Errorf("search failed: %s", ui.errText)
	}
	track := ui.results[0]
	fmt.Printf("      %s (%s)\n", track.Title, track.ID)
	p, e := newMPV(mpvPath, ytdlpPath, runtime)
	if e != nil {
		return e
	}
	defer p.Close()
	if e = p.Start(); e != nil {
		return e
	}
	ui.player = p
	config := filepath.Join(os.TempDir(), fmt.Sprintf("ytm-check-%d.ini", os.Getpid()))
	if e = os.WriteFile(config, []byte(cavaConfigText(cfg.VisualizerFPS)), 0600); e != nil {
		return e
	}
	defer os.Remove(config)
	cava := exec.Command(cavaPath, "-p", config)
	cava.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	pipe, e := cava.StdoutPipe()
	if e != nil {
		return e
	}
	log, e := os.Create(filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "logs", "cava-check.log"))
	if e != nil {
		return e
	}
	defer log.Close()
	cava.Stderr = log
	if e = cava.Start(); e != nil {
		return e
	}
	defer func() { _ = cava.Process.Kill(); _ = cava.Wait() }()
	frames := make(chan []float64, 32)
	go func() {
		s := bufio.NewScanner(pipe)
		for s.Scan() {
			v := parseSpectrum(s.Text())
			select {
			case frames <- v:
			default:
			}
		}
	}()
	fmt.Println("[2/8] Enter → yt-dlp download → local audio → mpv; high-resolution thumbnail")
	ui, cmd = updateModel(ui, tea.KeyMsg{Type: tea.KeyEnter})
	ui = applyCommand(ui, cmd)
	if ui.errText != "" {
		return fmt.Errorf("load: %s", ui.errText)
	}
	if ui.art == nil {
		return fmt.Errorf("thumbnail failed")
	}
	fmt.Printf("      thumbnail %dx%d\n", ui.art.Bounds().Dx(), ui.art.Bounds().Dy())
	if e = waitPlayback(p, 1); e != nil {
		return e
	}
	start := p.Snapshot().Time
	fmt.Println("[3/8] WASAPI output, advancing progress and real CAVA spectrum")
	peak := 0.
	deadline := time.After(4 * time.Second)
loop:
	for {
		select {
		case bars := <-frames:
			for _, v := range bars {
				if v > peak {
					peak = v
				}
			}
			ui, _ = updateModel(ui, spectrumMsg(bars))
		case <-deadline:
			break loop
		}
	}
	end := p.Snapshot()
	if end.Time <= start || end.Audio == "" || peak <= 0 {
		return fmt.Errorf("audio check: start %.2f end %.2f AO=%s peak=%.2f", start, end.Time, end.Audio, peak)
	}
	fmt.Printf("      %.2fs → %.2fs · %s · spectrum peak %.2f\n", start, end.Time, end.Audio, peak)
	fmt.Println("[4/8] Pause, resume, volume readback and seek")
	ui, cmd = updateModel(ui, tea.KeyMsg{Type: tea.KeySpace})
	ui = applyCommand(ui, cmd)
	time.Sleep(250 * time.Millisecond)
	s1 := p.Snapshot()
	time.Sleep(400 * time.Millisecond)
	s2 := p.Snapshot()
	if !s2.Paused || s2.Time-s1.Time > .15 {
		return fmt.Errorf("pause failed")
	}
	ui, cmd = updateModel(ui, tea.KeyMsg{Type: tea.KeySpace})
	ui = applyCommand(ui, cmd)
	time.Sleep(400 * time.Millisecond)
	if p.Snapshot().Paused {
		return fmt.Errorf("resume failed")
	}
	ui, cmd = updateModel(ui, tea.KeyMsg{Type: tea.KeyDown})
	ui = applyCommand(ui, cmd)
	time.Sleep(120 * time.Millisecond)
	if p.Snapshot().Volume != clamp(cfg.Volume-5, 0, 100) {
		return fmt.Errorf("volume failed")
	}
	before := p.Snapshot().Time
	ui, cmd = updateModel(ui, tea.KeyMsg{Type: tea.KeyRight})
	ui = applyCommand(ui, cmd)
	time.Sleep(400 * time.Millisecond)
	if p.Snapshot().Time < before+4 {
		return fmt.Errorf("seek failed")
	}
	fmt.Println("[5/8] Playlist add/select, previous/next, EOF auto-advance")
	if len(ui.results) > 1 {
		ui.cursor = 1
	}
	ui, _ = key(ui, "a")
	if len(ui.queue) != 2 {
		return fmt.Errorf("queue add failed")
	}
	// Enter starts a fresh queue snapshot; editing the library alone does not.
	ui = applyCommand(ui, ui.startPlaylist(0))
	ui.createPlaylist("Diagnostic browsing")
	ui.queue = []Track{{ID: "unplayed", Title: "Must not auto-play"}}
	ui.saveQueue()
	beforeBrowse := p.Snapshot().Time
	time.Sleep(300 * time.Millisecond)
	if p.Snapshot().Time < beforeBrowse || ui.current.ID != track.ID || ui.stopped {
		return fmt.Errorf("browsing interrupted playback")
	}
	ui, cmd = key(ui, "n")
	ui = applyCommand(ui, cmd)
	if ui.playing != 1 {
		return fmt.Errorf("next failed")
	}
	if e = waitPlayback(p, .4); e != nil {
		return e
	}
	ui, cmd = key(ui, "p")
	ui = applyCommand(ui, cmd)
	if ui.playing != 0 {
		return fmt.Errorf("previous failed")
	}
	if e = waitPlayback(p, .4); e != nil {
		return e
	}
	dur := p.Snapshot().Duration
	_, e = p.request("seek", dur-.2, "absolute+exact")
	if e != nil {
		return e
	}
	time.Sleep(900 * time.Millisecond)
	snap := p.Snapshot()
	if !snap.EOF {
		return fmt.Errorf("no EOF event after end seek")
	}
	// Follow only the load command; a diagnostic must not recursively run the tick timer.
	ui, cmd = updateModel(ui, playerTickMsg(snap))
	if ui.playing != 1 {
		return fmt.Errorf("EOF did not advance playlist")
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch[1:] {
			ui = applyCommand(ui, c)
		}
	} else {
		return fmt.Errorf("expected EOF load command")
	}
	if e = waitPlayback(p, .4); e != nil {
		return e
	}
	fmt.Println("[6/8] Stop, replay and remove from playlist")
	ui.switchPlaylist(0)
	ui, cmd = key(ui, "s")
	ui = applyCommand(ui, cmd)
	time.Sleep(150 * time.Millisecond)
	if !p.Snapshot().Idle {
		return fmt.Errorf("stop failed")
	}
	ui.queueFocus = true
	ui.queueCursor = 0
	ui, cmd = updateModel(ui, tea.KeyMsg{Type: tea.KeyEnter})
	ui = applyCommand(ui, cmd)
	if e = waitPlayback(p, .4); e != nil {
		return e
	}
	ui.queueCursor = 1
	ui, cmd = updateModel(ui, tea.KeyMsg{Type: tea.KeyDelete})
	ui = applyCommand(ui, cmd)
	ui, _ = updateModel(ui, tea.KeyMsg{Type: tea.KeySpace})
	ui, cmd = updateModel(ui, tea.KeyMsg{Type: tea.KeyEnter})
	ui = applyCommand(ui, cmd)
	if len(ui.queue) != 1 {
		return fmt.Errorf("remove failed")
	}
	if e = checkLivePlaylistModes(&ui); e != nil {
		return e
	}
	if e = checkLiveTrackTransfer(&ui); e != nil {
		return e
	}
	fmt.Println("[7/8] Recreate mpv, replay cached audio after restart")
	ui.stop()
	p.Close()
	p2, e := newMPV(mpvPath, ytdlpPath, runtime)
	if e != nil {
		return e
	}
	defer p2.Close()
	if e = p2.Start(); e != nil {
		return e
	}
	ui.player = p2
	ui = applyCommand(ui, ui.loadTrack(track))
	if e = waitPlayback(p2, 1); e != nil {
		return e
	}
	fmt.Println("[8/8] Check playback log")
	data, _ := os.ReadFile(filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "logs", "mpv.log"))
	for _, marker := range []string{"http error 403", "failed to open", "[ytdl_hook] error"} {
		if strings.Contains(strings.ToLower(string(data)), marker) {
			return fmt.Errorf("mpv: %s", marker)
		}
	}
	fmt.Println("PASS · search / artwork / audio / spectrum / transport / playlist / restart")
	return nil
}
