package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

type PlayerSnapshot struct {
	Time          float64
	Duration      float64
	Volume        int
	Paused        bool
	Title         string
	EOF           bool
	Idle          bool
	Audio         string
	PlaybackError string
	Path          string
	Updated       time.Time
}

type mpvReply struct {
	Event     string          `json:"event"`
	RequestID int             `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	Reason    string          `json:"reason"`
	FileError string          `json:"file_error"`
	Name      string          `json:"name"`
}

type MPV struct {
	path       string
	ytdlp      string
	runtime    string
	pipe       string
	cmd        *exec.Cmd
	conn       io.ReadWriteCloser
	writeMu    sync.Mutex
	mu         sync.Mutex
	seq        int
	pending    map[int]chan mpvReply
	closed     chan struct{}
	logFile    *os.File
	lastError  string
	snapshot   PlayerSnapshot
	epoch      atomic.Uint64
	closeOnce  sync.Once
	jobs       chan playbackJob
	workerDone chan struct{}
}

func newMPV(path, ytdlp, runtime string) (*MPV, error) {
	logDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(logDir, "mpv.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &MPV{
		path: path, ytdlp: ytdlp, runtime: runtime,
		pipe:    fmt.Sprintf("%c%c.%c%s%c%s-%d-%d", '\\', '\\', '\\', "pipe", '\\', "ytm-mpv", os.Getpid(), time.Now().UnixNano()),
		pending: make(map[int]chan mpvReply), closed: make(chan struct{}), logFile: f,
		snapshot: PlayerSnapshot{Idle: true}, jobs: make(chan playbackJob, 32), workerDone: make(chan struct{}),
	}, nil
}

func (p *MPV) Start() error {
	args := []string{
		"--no-config", "--idle=yes", "--keep-open=yes", "--no-terminal", "--no-video", "--force-window=no", "--audio-display=no",
		"--msg-level=all=warn", "--ytdl=no", "--load-scripts=no",
		"--cache=yes", "--demuxer-readahead-secs=18", "--input-ipc-server=" + p.pipe,
	}
	p.cmd = exec.Command(p.path, args...)
	p.cmd.Stdout, p.cmd.Stderr = p.logFile, p.logFile
	p.cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("khởi động mpv: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var conn io.ReadWriteCloser
	var err error
	for {
		conn, err = winio.DialPipeContext(ctx, p.pipe)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return fmt.Errorf("mpv không mở IPC pipe: %w", err)
		}
		time.Sleep(80 * time.Millisecond)
	}
	p.conn = conn
	go p.readLoop()
	go p.commandLoop()
	for i, name := range []string{"time-pos", "duration", "volume", "pause", "eof-reached", "idle-active", "current-ao", "path", "media-title"} {
		if _, err := p.request("observe_property", i+1, name); err != nil {
			return err
		}
	}
	return nil
}

func (p *MPV) readLoop() {
	scanner := bufio.NewScanner(p.conn)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var reply mpvReply
		if json.Unmarshal(scanner.Bytes(), &reply) != nil {
			continue
		}
		if reply.RequestID == 0 {
			p.mu.Lock()
			if reply.Event == "start-file" {
				p.lastError = ""
				p.snapshot.PlaybackError = ""
				p.snapshot.EOF = false
				p.snapshot.Time = 0
			}
			if reply.Event == "end-file" && reply.Reason == "error" {
				p.lastError = "mpv: " + reply.FileError
				p.snapshot.PlaybackError = p.lastError
			}
			if reply.Event == "property-change" {
				p.observe(reply.Name, reply.Data)
			}
			p.mu.Unlock()
			continue
		}
		p.mu.Lock()
		ch := p.pending[reply.RequestID]
		delete(p.pending, reply.RequestID)
		p.mu.Unlock()
		if ch != nil {
			ch <- reply
		}
	}
	close(p.closed)
}

func (p *MPV) request(command ...any) (json.RawMessage, error) {
	if p.conn == nil {
		return nil, fmt.Errorf("mpv is not connected")
	}
	p.mu.Lock()
	p.seq++
	id := p.seq
	ch := make(chan mpvReply, 1)
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	payload, _ := json.Marshal(map[string]any{"command": command, "request_id": id})
	p.writeMu.Lock()
	_, err := p.conn.Write(append(payload, '\n'))
	p.writeMu.Unlock()
	if err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, err
	}
	select {
	case reply := <-ch:
		if reply.Error != "success" {
			return nil, fmt.Errorf("mpv: %s", reply.Error)
		}
		return reply.Data, nil
	case <-time.After(1500 * time.Millisecond):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("mpv IPC timeout")
	case <-p.closed:
		return nil, fmt.Errorf("mpv IPC disconnected")
	}
}

func (p *MPV) Load(url string, volume int) error {
	if _, err := p.request("set_property", "volume", volume); err != nil {
		return err
	}
	_, err := p.request("loadfile", url, "replace")
	if err == nil {
		err = p.SetPause(false)
	}
	return err
}

func (p *MPV) Stop() error { _, err := p.request("stop"); return err }

func (p *MPV) TogglePause() error { _, err := p.request("cycle", "pause"); return err }
func (p *MPV) SetPause(paused bool) error {
	_, err := p.request("set_property", "pause", paused)
	return err
}
func (p *MPV) SetVolume(volume int) error {
	_, err := p.request("set_property", "volume", volume)
	return err
}
func (p *MPV) Seek(delta float64) error {
	_, err := p.request("seek", delta, "relative+exact")
	return err
}

func (p *MPV) Snapshot() PlayerSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.snapshot
	select {
	case <-p.closed:
		s.PlaybackError = "mpv disconnected; restart YTM"
	default:
	}
	return s
}
func (p *MPV) observe(name string, data json.RawMessage) {
	// Called with mu held. No IPC requests or rendering on the reader goroutine.
	switch name {
	case "time-pos":
		if string(data) != "null" {
			_ = json.Unmarshal(data, &p.snapshot.Time)
			p.snapshot.Updated = time.Now()
		}
	case "duration":
		_ = json.Unmarshal(data, &p.snapshot.Duration)
	case "volume":
		var v float64
		_ = json.Unmarshal(data, &v)
		p.snapshot.Volume = int(v + .5)
	case "pause":
		_ = json.Unmarshal(data, &p.snapshot.Paused)
	case "eof-reached":
		_ = json.Unmarshal(data, &p.snapshot.EOF)
	case "idle-active":
		_ = json.Unmarshal(data, &p.snapshot.Idle)
	case "current-ao":
		_ = json.Unmarshal(data, &p.snapshot.Audio)
	case "path":
		_ = json.Unmarshal(data, &p.snapshot.Path)
	case "media-title":
		_ = json.Unmarshal(data, &p.snapshot.Title)
	}
}

func (p *MPV) Close() {
	p.closeOnce.Do(func() {
		if p.cmd != nil && p.cmd.Process != nil {
			if p.conn != nil {
				_, _ = p.request("quit")
			}
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
		}
		if p.conn != nil {
			_ = p.conn.Close()
		}
		if p.logFile != nil {
			_ = p.logFile.Close()
		}
	})
}

func searchYouTube(ytdlp, runtime, query string, limit int) ([]Track, error) {
	return searchYouTubeContext(context.Background(), ytdlp, runtime, query, limit)
}

func searchYouTubeContext(parent context.Context, ytdlp, runtime, query string, limit int) ([]Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	limit = clamp(limit, 1, 50)
	search := fmt.Sprintf("ytsearch%d:%s", limit, query)
	args := []string{"--js-runtimes", runtime, "--remote-components", "ejs:github", "--extractor-args", "youtube:player_client=web_embedded", "--dump-single-json"}
	u, _ := url.Parse(query)
	videoURL := u != nil && (u.Scheme == "https" || u.Scheme == "http") && (u.Hostname() == "youtube.com" || u.Hostname() == "www.youtube.com" || u.Hostname() == "music.youtube.com" || u.Hostname() == "m.youtube.com" || u.Hostname() == "youtu.be")
	if videoURL {
		search = query
		args = append(args, "--skip-download", "--no-warnings", "--no-progress", search)
	} else {
		args = append(args, "--flat-playlist", "--skip-download", "--no-warnings", "--no-progress", search)
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	args = append([]string{"--ignore-config", "--no-playlist", "--socket-timeout", "12", "--retries", "2"}, args...)
	cmd := exec.CommandContext(ctx, ytdlp, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp: %s", strings.TrimSpace(stderr.String()))
	}
	var playlist struct {
		Entries []map[string]any `json:"entries"`
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("đọc kết quả YouTube: %w", err)
	}
	if entries, ok := root["entries"].([]any); ok {
		for _, entry := range entries {
			if mapped, ok := entry.(map[string]any); ok {
				playlist.Entries = append(playlist.Entries, mapped)
			}
		}
	} else if _, ok := root["id"].(string); ok {
		playlist.Entries = append(playlist.Entries, root)
	}
	tracks := make([]Track, 0, len(playlist.Entries))
	for _, entry := range playlist.Entries {
		id, _ := entry["id"].(string)
		if id == "" {
			continue
		}
		title, _ := entry["title"].(string)
		album, _ := entry["album"].(string)
		source := "YouTube"
		if u != nil && u.Hostname() == "music.youtube.com" {
			source = "YouTube Music"
		}
		artist, _ := entry["artist"].(string)
		if artist == "" {
			artist, _ = entry["channel"].(string)
		}
		if artist == "" {
			artist, _ = entry["uploader"].(string)
		}
		thumb, _ := entry["thumbnail"].(string)
		if thumbs, ok := entry["thumbnails"].([]any); ok && len(thumbs) > 0 {
			if last, ok := thumbs[len(thumbs)-1].(map[string]any); ok {
				if u, ok := last["url"].(string); ok && u != "" {
					thumb = u
				}
			}
		}
		if thumb == "" {
			thumb = "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg"
		}
		duration, _ := entry["duration"].(float64)
		tracks = append(tracks, Track{ID: id, Title: title, Album: album, Source: source, Artist: artist, Duration: duration, Thumbnail: thumb, URL: "https://www.youtube.com/watch?v=" + id})
	}
	return tracks, nil
}
