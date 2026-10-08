package main

import (
	"context"

	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"image"
	"os"
	"os/exec"

	"time"
)

type Track struct {
	EntryID                           string `json:",omitempty"`
	ID, Title, Artist, Thumbnail, URL string
	Duration                          float64
	Album, Source                     string
}
type model struct {
	transfer                               trackTransfer
	playlists                              []Playlist
	viewedID, playingID, playingName       string
	playbackQueue                          []Track
	playlistMode                           bool
	playlistCursor                         int
	playlistDialog                         string
	playlistEdit                           editor
	libraryReadOnly                        bool
	cfg                                    Config
	player                                 *MPV
	ytdlpPath, jsRuntime                   string
	program                                *tea.Program
	cavaCmd                                *exec.Cmd
	cavaConfig                             string
	cavaLog                                *os.File
	width, height                          int
	current                                Track
	snapshot                               PlayerSnapshot
	queue, results                         []Track
	cursor, queueCursor, playing           int
	queueFocus                             bool
	bars                                   []float64
	query                                  string
	edit                                   editor
	searching, inputMode, loading, stopped bool
	notice, errText                        string
	art                                    image.Image
	artDirty                               bool
	volume                                 int
	lastDraw                               time.Time
	generation, searchGeneration           int
	cancel                                 context.CancelFunc
	searchCancel                           context.CancelFunc
	ctx                                    context.Context
	rendered                               []string
	renderedGeometry                       geometry
	output                                 *os.File
	persist                                bool
	autoPlay                               bool
	audioPath                              string
	shuffle                                bool
	repeatMode                             string
	history, future, shuffleBag            []int
	resumeAt                               float64
	helpOpen                               bool
	helpOffset                             int
	spectrum                               spectrumState
	peaks                                  []float64
}
type searchDone struct {
	tracks     []Track
	err        error
	generation int
}
type artworkDone struct {
	img        image.Image
	generation int
	err        error
}
type audioReady struct {
	path       string
	err        error
	generation int
}
type clipboardMsg struct {
	text       string
	err        error
	paste      bool
	generation int
}

func newModel(cfg Config, p *MPV, ytdlp, runtime string) model {
	return model{cfg: cfg, player: p, ytdlpPath: ytdlp, jsRuntime: runtime, volume: cfg.Volume, playing: -1, stopped: true, notice: "/ search YouTube   ·   Tab playlist", output: os.Stdout, repeatMode: "off", ctx: context.Background()}
}
func (m model) Init() tea.Cmd {
	if m.autoPlay {
		return tea.Batch(m.tickCmd(), m.frameCmd(), func() tea.Msg {
			ts, e := searchYouTubeContext(m.ctx, m.ytdlpPath, m.jsRuntime, m.query, m.cfg.DefaultSearchLimit)
			return searchDone{ts, e, m.searchGeneration}
		})
	}
	return tea.Batch(m.tickCmd(), m.frameCmd())
}
func (m model) View() string { return "" }
func (m *model) fail(err error) {
	if err != nil {
		m.errText = err.Error()
	}
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		if m.width != v.Width || m.height != v.Height {
			m.width, m.height = v.Width, v.Height
			m.rendered = nil
			m.artDirty = true
		}
	case clipboardMsg:
		if v.err != nil {
			m.fail(v.err)
		} else if v.paste && m.inputMode && v.generation == m.searchGeneration {
			m.edit.insert(v.text)
			m.query = string(m.edit.text)
		}
	case tea.KeyMsg:
		if m.helpOpen {
			switch v.String() {
			case "t", "T", "esc":
				m.helpOpen = false
				m.helpOffset = 0
				m.rendered = nil
			case "j", "down":
				m.helpOffset++
			case "k", "up":
				if m.helpOffset > 0 {
					m.helpOffset--
				}
			case "pgdown":
				m.helpOffset += max(1, m.height/2)
			case "pgup":
				m.helpOffset = max(0, m.helpOffset-max(1, m.height/2))
			}
			m.draw()
			return m, nil
		}
		if !m.inputMode && m.playlistKey(v) {
			m.draw()
			return m, nil
		}
		if m.inputMode {
			switch v.String() {
			case "esc":
				m.inputMode = false
			case "enter":
				if m.query != "" {
					m.inputMode = false
					m.searching = true
					m.searchGeneration++
					g := m.searchGeneration
					q := m.query
					yp, rt, limit := m.ytdlpPath, m.jsRuntime, m.cfg.DefaultSearchLimit
					m.notice = "Searching YouTube…"
					m.errText = ""
					if m.searchCancel != nil {
						m.searchCancel()
					}
					ctx, cancel := context.WithCancel(m.ctx)
					m.searchCancel = cancel
					cmd = func() tea.Msg {
						defer cancel()
						ts, e := searchYouTubeContext(ctx, yp, rt, q, limit)
						return searchDone{ts, e, g}
					}
				}
			case "ctrl+v", "shift+insert":
				g := m.searchGeneration
				cmd = func() tea.Msg { s, e := clipboardRead(); return clipboardMsg{s, e, true, g} }
			case "ctrl+c", "ctrl+x":
				text := m.edit.selected()
				if v.String() == "ctrl+x" {
					a, b := m.edit.bounds()
					if a == b {
						m.edit.anchor = 0
						m.edit.pos = len(m.edit.text)
					}
					m.edit.insert("")
					m.query = string(m.edit.text)
				}
				cmd = func() tea.Msg { return clipboardMsg{err: clipboardWrite(text)} }
			default:
				m.edit.key(v)
				m.query = string(m.edit.text)
			}
		} else {
			switch v.String() {
			case "t", "T":
				m.helpOpen = true
				m.helpOffset = 0
			case "q", "ctrl+c":
				if m.searchCancel != nil {
					m.searchCancel()
				}
				if m.cancel != nil {
					m.cancel()
				}
				m.saveQueue()
				return m, tea.Quit
			case "/":
				if m.searchCancel != nil {
					m.searchCancel()
				}
				m.searching = false
				m.inputMode = true
				m.searchGeneration++
				m.edit = editor{text: []rune(m.query), pos: len([]rune(m.query)), anchor: len([]rune(m.query))}
			case "h", "H":
				m.shuffle = !m.shuffle
				m.resetOrder()
				m.saveQueue()
			case "r", "R":
				switch m.repeatMode {
				case "off":
					m.repeatMode = "queue"
				case "queue":
					m.repeatMode = "track"
				default:
					m.repeatMode = "off"
				}
				m.saveQueue()
			case "J", "alt+down":
				if m.queueFocus {
					m.moveSelected(1)
				}
			case "K", "alt+up":
				if m.queueFocus {
					m.moveSelected(-1)
				}
			case "C":
				if m.queueFocus {
					cmd = m.clearQueue()
				}
			case "tab":
				m.queueFocus = !m.queueFocus
			case "esc":
				m.errText = ""
				m.queueFocus = false
			case " ":
				if m.player != nil && !m.stopped && !m.loading {
					cmd = m.control(false, m.player.TogglePause)
				}
			case "s":
				cmd = m.stop()
			case "n":
				cmd = m.playNext(1)
			case "p":
				cmd = m.playNext(-1)
			case "left":
				if m.player != nil {
					cmd = m.control(false, func() error { return m.player.Seek(-5) })
				}
			case "right":
				if m.player != nil {
					cmd = m.control(false, func() error { return m.player.Seek(5) })
				}
			case "up", "down":
				delta := 5
				if v.String() == "down" {
					delta = -5
				}
				m.volume = clamp(m.volume+delta, 0, 100)
				if m.player != nil {
					volume := m.volume
					cmd = m.control(false, func() error { return m.player.SetVolume(volume) })
				}
			case "a":
				if len(m.results) > 0 {
					m.queue = append(m.queue, m.results[m.cursor])
					m.notice = "Added to playlist · " + m.results[m.cursor].Title
					m.saveQueue()
				}
			case "enter":
				if m.queueFocus {
					if len(m.queue) > 0 {
						cmd = m.startPlaylist(m.queueCursor)
					}
				} else if len(m.results) > 0 {
					t := m.results[m.cursor]
					index := -1
					for i, q := range m.queue {
						if q.ID == t.ID {
							index = i
							break
						}
					}
					if index < 0 {
						m.queue = append(m.queue, t)
						index = len(m.queue) - 1
					}
					m.queueCursor = index
					cmd = m.startPlaylist(index)
				}
			case "j", "k":
				d := 1
				if v.String() == "k" {
					d = -1
				}
				if m.queueFocus {
					m.queueCursor = clamp(m.queueCursor+d, 0, max(0, len(m.queue)-1))
				} else {
					m.cursor = clamp(m.cursor+d, 0, max(0, len(m.results)-1))
				}
			case "v":
				if m.cfg.VisualizerStyle == "mirror" {
					m.cfg.VisualizerStyle = "spectrum"
				} else {
					m.cfg.VisualizerStyle = "mirror"
				}
			}
		}
	case searchDone:
		if v.generation != m.searchGeneration {
			return m, nil
		}
		m.searching = false
		if v.err != nil {
			m.fail(v.err)
		} else {
			m.results = v.tracks
			m.cursor = 0
			m.queueFocus = false
			m.notice = fmt.Sprintf("%d results · Enter play · A add", len(v.tracks))
			if m.autoPlay && len(m.results) > 0 {
				m.autoPlay = false
				t := m.results[0]
				idx := -1
				for i, q := range m.queue {
					if q.ID == t.ID {
						idx = i
						break
					}
				}
				if idx < 0 {
					m.queue = append(m.queue, t)
					idx = len(m.queue) - 1
				}
				m.queueCursor = idx
				cmd = m.startPlaylist(idx)
			}
		}
	case artworkDone:
		if v.generation == m.generation {
			m.art = v.img
			m.artDirty = true
			if v.err != nil {
				m.notice = "Artwork unavailable · audio continues"
			}
		}
	case audioReady:
		if v.generation != m.generation {
			return m, nil
		}
		if v.err != nil {
			m.loading = false
			m.fail(v.err)
			m.stopped = true
		} else if m.player != nil {
			m.audioPath = v.path
			path, volume, g, p := v.path, m.volume, m.generation, m.player
			m.notice = "Opening audio…"
			cmd = m.control(true, func() error {
				if err := p.Load(path, volume); err != nil {
					return err
				}
				if err := p.waitLoaded(path, uint64(g)); err != nil {
					return err
				}
				if m.resumeAt > 0 {
					_, err := p.request("seek", m.resumeAt, "absolute+exact")
					return err
				}
				return nil
			})
		}
	case controlDone:
		if v.generation != m.generation {
			return m, nil
		}
		if v.err != nil {
			m.fail(v.err)
			if v.loaded {
				m.loading = false
				m.stopped = true
			}
		} else if v.loaded {
			m.resumeAt = 0
			m.loading = false
			m.stopped = false
			m.snapshot = m.player.Snapshot()
			m.notice = "Playing · " + m.current.Title
		}
	case spectrumMsg:
		m.spectrum.Push([]float64(v), time.Now())
		return m, nil
	case frameTickMsg:
		m.spectrum.Step(time.Time(v), !m.stopped && !m.loading && !m.snapshot.Paused)
		m.bars = m.spectrum.Levels
		m.peaks = m.spectrum.Peaks
		cmd = m.frameCmd()

	case spectrumErrorMsg:
		m.notice = "Visualizer: " + string(v)
	case playerTickMsg:
		cmd = tea.Batch(m.tickCmd(), m.acceptSnapshot(PlayerSnapshot(v)))
		if m.output != nil {
			if w, h, e := term.GetSize(m.output.Fd()); e == nil && (w != m.width || h != m.height) {
				m.width, m.height = w, h
				m.rendered = nil
				m.artDirty = true
			}
		}
	}
	if m.transfer.stage != "" {
		m.queueFocus = true
	}
	m.draw()
	return m, cmd
}
func (m *model) stop() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	m.generation++
	m.loading = false
	m.stopped = true
	m.snapshot = PlayerSnapshot{Volume: m.volume}
	if m.player != nil {
		m.player.epoch.Store(uint64(m.generation))
		_ = m.control(false, m.player.Stop)
	}
	m.notice = "Stopped · Enter to play"
	return nil
}
func (m *model) loadTrack(t Track) tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	m.generation++
	g := m.generation
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
	m.cancel = cancel
	if m.player != nil {
		m.player.epoch.Store(uint64(m.generation))
		_ = m.control(false, m.player.Stop)
	}
	if t.ID != m.current.ID {
		m.resumeAt = 0
	}
	m.audioPath = ""
	m.current = t
	m.loading = true
	m.stopped = true
	m.snapshot = PlayerSnapshot{Volume: m.volume}
	m.art = nil
	m.artDirty = true
	m.errText = ""
	m.notice = "Buffering audio… (S cancels)"
	yp, rt := m.ytdlpPath, m.jsRuntime
	return tea.Batch(func() tea.Msg {
		path, e := prepareAudio(ctx, yp, rt, t.URL)
		return audioReady{path, e, g}
	}, func() tea.Msg { img, e := trackArtwork(ctx, t); return artworkDone{img, g, e} })
}
