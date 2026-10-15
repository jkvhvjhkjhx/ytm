package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"golang.org/x/sys/windows"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Use yt-dlp's downloader as well as its extractor. Passing a signed CDN URL
// to a different HTTP stack loses site download handling and can produce 403s.
// Only complete files are published; mpv can seek reliably without transcoding.
var mediaMu sync.Mutex

// resolveAudioSource returns an already cached file when available; otherwise
// it asks yt-dlp only for a fresh, short-lived media URL so mpv can start
// buffering immediately instead of waiting for the entire song to download.
// Full-file download remains available as a playback fallback.
func resolveAudioSource(ctx context.Context, ytdlp, runtime, videoURL string) (string, bool, error) {
	videoURL = canonicalYouTubeURL(videoURL)
	if videoURL == "" {
		return "", false, fmt.Errorf("audio requires a valid YouTube video URL")
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "cache")
	target := filepath.Join(dir, fmt.Sprintf("%x.audio", sha256.Sum256([]byte(videoURL))))
	if st, err := os.Stat(target); err == nil && st.Mode().IsRegular() && st.Size() > 1024 {
		_ = os.Chtimes(target, time.Now(), time.Now())
		return target, false, nil
	}
	stream, err := extractAudioStreamURL(ctx, ytdlp, runtime, videoURL)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		// Preserve the established, yt-dlp-managed download path if stream URL
		// extraction is unavailable for a particular video or extractor response.
		path, fallbackErr := prepareAudio(ctx, ytdlp, runtime, videoURL)
		if fallbackErr != nil {
			return "", false, fmt.Errorf("stream resolve failed (%v); download fallback failed: %w", err, fallbackErr)
		}
		return path, false, nil
	}
	return stream, true, nil
}

func extractAudioStreamURL(ctx context.Context, ytdlp, runtime, videoURL string) (string, error) {
	videoURL = canonicalYouTubeURL(videoURL)
	if videoURL == "" {
		return "", fmt.Errorf("audio requires a valid YouTube video URL")
	}
	args := []string{"--ignore-config", "--no-plugin-dirs", "--js-runtimes", runtime, "--remote-components", "ejs:github",
		"--extractor-args", "youtube:player_client=web_embedded", "--no-playlist", "--no-progress", "--no-warnings",
		"--socket-timeout", "10", "--retries", "2", "--fragment-retries", "2", "--match-filter", "!is_live",
		"-f", "bestaudio", "--get-url", "--", videoURL}
	cmd := exec.CommandContext(ctx, ytdlp, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		text := strings.TrimSpace(stderr.String())
		if len(text) > 1800 {
			text = text[len(text)-1800:]
		}
		return "", fmt.Errorf("yt-dlp: %s", text)
	}
	// --get-url may emit multiple lines for formats composed of separate
	// streams. We explicitly select bestaudio and accept only YouTube's media CDN.
	lines := strings.Fields(string(output))
	for _, candidate := range lines {
		u, parseErr := url.Parse(candidate)
		if parseErr == nil && validAudioStreamURL(u) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("yt-dlp returned no trusted YouTube audio stream URL")
}

func validAudioStreamURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "googlevideo.com" || strings.HasSuffix(host, ".googlevideo.com")
}

func prepareAudio(ctx context.Context, ytdlp, runtime, url string) (string, error) {
	url = canonicalYouTubeURL(url)
	if url == "" {
		return "", fmt.Errorf("audio requires a valid YouTube video URL")
	}
	mediaMu.Lock()
	defer mediaMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "cache")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, fmt.Sprintf("%x.audio", sha256.Sum256([]byte(url))))
	if st, err := os.Stat(target); err == nil && st.Size() > 1024 {
		_ = os.Chtimes(target, time.Now(), time.Now())
		return target, nil
	}
	tmp, err := os.MkdirTemp(dir, "download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp) // only our unique staging directory
	args := []string{"--ignore-config", "--no-plugin-dirs", "--js-runtimes", runtime, "--remote-components", "ejs:github",
		"--extractor-args", "youtube:player_client=web_embedded", "--no-playlist", "--no-progress", "--no-warnings",
		"--socket-timeout", "15", "--retries", "3", "--fragment-retries", "3", "--max-filesize", "128M",
		"--match-filter", "!is_live", "-f", "bestaudio", "-o", filepath.Join(tmp, "audio.%(ext)s"), "--", url}
	cmd := exec.CommandContext(ctx, ytdlp, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		text := strings.TrimSpace(string(out))
		if len(text) > 1800 {
			text = text[len(text)-1800:]
		}
		return "", fmt.Errorf("yt-dlp: %s", text)
	}
	files, _ := filepath.Glob(filepath.Join(tmp, "audio.*"))
	for _, file := range files {
		ext := filepath.Ext(file)
		if ext != ".webm" && ext != ".m4a" && ext != ".opus" && ext != ".mp3" && ext != ".ogg" {
			continue
		}
		st, e := os.Stat(file)
		if e != nil || st.Size() < 1024 {
			continue
		}
		if e = os.Rename(file, target); e != nil {
			return "", e
		}
		trimCache(dir, target, 256<<20)
		return target, nil
	}
	return "", fmt.Errorf("no audio downloaded (live streams and files above 128 MiB are not supported)")
}

func trimCache(dir, keep string, limit int64) {
	trimFiles(dir, "*.audio", keep, limit)
}
func trimFiles(dir, pattern, keep string, limit int64) {
	files, _ := filepath.Glob(filepath.Join(dir, pattern))
	type entry struct {
		path string
		info os.FileInfo
	}
	var entries []entry
	var size int64
	for _, p := range files {
		if s, e := os.Stat(p); e == nil {
			entries = append(entries, entry{p, s})
			size += s.Size()
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].info.ModTime().Before(entries[j].info.ModTime()) })
	for _, e := range entries {
		if size <= limit {
			break
		}
		if e.path != keep {
			if os.Remove(e.path) == nil {
				size -= e.info.Size()
			}
		}
	}
}
