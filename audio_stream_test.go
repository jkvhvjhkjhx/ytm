package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStreamURLBoundary(t *testing.T) {
	for _, raw := range []string{"http://rr1.googlevideo.com/audio", "https://googlevideo.com.evil.invalid/audio", "file:///C:/private", "https://localhost/audio", "https://user:pass@rr1.googlevideo.com/audio", "https://rr1.googlevideo.com:8080/audio"} {
		u, _ := url.Parse(raw)
		if validAudioStreamURL(u) {
			t.Errorf("accepted untrusted stream: %s", raw)
		}
	}
	for _, raw := range []string{"https://rr1.googlevideo.com/videoplayback?signature=example", "https://manifest.googlevideo.com/audio"} {
		u, _ := url.Parse(raw)
		if !validAudioStreamURL(u) {
			t.Errorf("rejected media CDN: %s", raw)
		}
	}
}

func TestAudioCacheAndCancellation(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	u := "https://www.youtube.com/watch?v=example"
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "cache")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%x.audio", sha256.Sum256([]byte(u))))
	if err := os.WriteFile(path, make([]byte, 2048), 0600); err != nil {
		t.Fatal(err)
	}
	got, stream, err := resolveAudioSource(context.Background(), "must-not-run.exe", "", u)
	if err != nil || got != path || stream {
		t.Fatalf("cache not reused: stream=%t err=%v", stream, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := resolveAudioSource(ctx, "must-not-run.exe", "", u); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestStreamFallbackState(t *testing.T) {
	m := newModel(defaultConfig(), nil, "", "")
	m.audioStream = true
	m.resumeAt = 17
	m.audioCtx, m.cancel = context.WithCancel(m.ctx)
	m.cancel()
	if m.fallbackToDownload() == nil {
		t.Fatal("missing fallback command")
	}
	defer m.cancel()
	if m.audioCtx.Err() != nil || m.resumeAt != 17 || !m.loading || !m.audioFallback {
		t.Fatal("fallback lost resume position or reused expired context")
	}
	if m.fallbackToDownload() != nil {
		t.Fatal("fallback loop")
	}
	m.stop()
	if !errors.Is(m.audioCtx.Err(), context.Canceled) {
		t.Fatal("stop did not cancel fallback")
	}
}

func TestStopReturnsControlAcknowledgement(t *testing.T) {
	p := &MPV{jobs: make(chan playbackJob, 1), closed: make(chan struct{})}
	m := newModel(defaultConfig(), p, "", "")
	cmd := m.stop()
	if cmd == nil {
		t.Fatal("stop discarded its acknowledgement command")
	}
	job := <-p.jobs
	if job.epoch != uint64(m.generation) {
		t.Fatal("stop used stale playback generation")
	}
	want := errors.New("stop failure")
	job.reply <- want
	result, ok := cmd().(controlDone)
	if !ok || !errors.Is(result.err, want) || result.generation != m.generation {
		t.Fatal("stop did not report acknowledgement/error")
	}
}

// Opt-in integration check: uses YouTube, real mpv IPC and the Windows audio
// device. No cached audio or download fallback can make this check pass.
func TestLiveAudioStreaming(t *testing.T) {
	if os.Getenv("YTM_LIVE_STREAM_TEST") != "1" {
		t.Skip("requires explicit live-audio opt-in")
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	mpv, ytdlp, _, deno := paths(cfg)
	runtime := jsRuntime(deno, findProgram(cfg.NodePath, "node"))
	t.Setenv("LOCALAPPDATA", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	stream, err := extractAudioStreamURL(ctx, ytdlp, runtime, "https://www.youtube.com/watch?v=hrNvtRzEta4")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fresh audio URL resolved in %.2fs", time.Since(start).Seconds())
	p, err := newMPV(mpv, ytdlp, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Start(); err != nil {
		t.Fatal(err)
	}
	if err = p.Load(stream, 25); err != nil {
		t.Fatal(err)
	}
	if err = p.waitLoaded(stream, 0, true); err != nil {
		t.Fatal(err)
	}
	ready := time.Since(start)
	if err = waitPlayback(p, 2); err != nil {
		t.Fatal(err)
	}
	t.Logf("stream ready in %.2fs; progress advanced past 2s (AO=%s)", ready.Seconds(), p.Snapshot().Audio)
	if os.Getenv("YTM_LIVE_STREAM_BASELINE") == "1" {
		if err = p.Stop(); err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		local, err := prepareAudio(ctx, ytdlp, runtime, "https://www.youtube.com/watch?v=hrNvtRzEta4")
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Load(local, 25); err != nil {
			t.Fatal(err)
		}
		if err = p.waitLoaded(local, 0, false); err != nil {
			t.Fatal(err)
		}
		if err = waitPlayback(p, 1); err != nil {
			t.Fatal(err)
		}
		t.Logf("full-download baseline ready and playing past 1s in %.2fs", time.Since(start).Seconds())
	}
}
