package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
		logStream("%s resolve: using cached file (no badge, no yt-dlp)", videoID(videoURL))
		return target, false, nil
	}
	stream, err := extractAudioStreamURL(ctx, ytdlp, runtime, videoURL)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		// Preserve the established, yt-dlp-managed download path if stream URL
		// extraction is unavailable for a particular video or extractor response.
		logStream("%s resolve: stream failed, using download fallback", videoID(videoURL))
		path, fallbackErr := prepareAudio(ctx, ytdlp, runtime, videoURL)
		if fallbackErr != nil {
			return "", false, fmt.Errorf("stream resolve failed (%v); download fallback failed: %w", err, fallbackErr)
		}
		return path, false, nil
	}
	return stream, true, nil
}

// audioFormat prefers Opus (what YouTube serves browsers) and falls back to
// the best available audio when no Opus stream exists.
const audioFormat = "bestaudio[acodec=opus]/bestaudio"

// ytCookiesBrowser is the browser whose logged-in YouTube session yt-dlp reads
// (config key yt_cookies_browser). Only allowlisted names are accepted; cookies
// are read locally by yt-dlp and are never stored, copied or logged by ytm.
var (
	ytCookiesMu      sync.RWMutex
	ytCookiesBrowser string
)

func normalizeCookiesBrowser(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == "firefox" {
		return "firefox"
	}
	return ""
}

func setCookiesBrowser(v string) {
	ytCookiesMu.Lock()
	ytCookiesBrowser = normalizeCookiesBrowser(v)
	ytCookiesMu.Unlock()
}

func cookiesBrowser() string {
	ytCookiesMu.RLock()
	defer ytCookiesMu.RUnlock()
	return ytCookiesBrowser
}

// withCookies inserts --cookies-from-browser after the leading options when a
// browser is given; the client settings stay unchanged.
// musicURL turns a canonical watch URL into its music.youtube.com form. Premium
// formats (774) are only offered on the music host, so the cookie attempt uses
// it; the no-cookie fallback keeps the www.youtube.com URL. Invalid ids give "".
func musicURL(canonical string) string {
	u, err := url.Parse(canonical)
	if err != nil {
		return ""
	}
	id := u.Query().Get("v")
	if !validVideoID(id) {
		return ""
	}
	return "https://music.youtube.com/watch?v=" + id
}

func withCookies(args []string, browser string) []string {
	if browser == "" {
		return args
	}
	return append([]string{"--cookies-from-browser", browser}, args...)
}

func streamArgs(runtime, videoURL string) []string {
	return []string{"--ignore-config", "--no-plugin-dirs", "--js-runtimes", runtime, "--remote-components", "ejs:github",
		"--extractor-args", "youtube:player_client=web_embedded", "--no-playlist", "--no-progress", "--no-warnings",
		"--socket-timeout", "10", "--retries", "2", "--fragment-retries", "2", "--match-filter", "!is_live",
		"-f", audioFormat, "--print", "ytm-fmt:%(format_id)s %(acodec)s %(abr)s", "--get-url", "--", videoURL}
}

func extractAudioStreamURL(ctx context.Context, ytdlp, runtime, videoURL string) (string, error) {
	videoURL = canonicalYouTubeURL(videoURL)
	if videoURL == "" {
		return "", fmt.Errorf("audio requires a valid YouTube video URL")
	}
	cookieFailed := false
	b := cookiesBrowser()
	logStream("%s stream: cookies configured=%t browser=%q", videoID(videoURL), b != "", b)
	if mu := musicURL(videoURL); b != "" && mu != "" {
		u, info, err := extractStreamWith(ctx, ytdlp, withCookies(streamArgs(runtime, mu), b), true)
		if err == nil {
			logStream("%s stream: attempt=with-cookies host=music result=ok %s", videoID(videoURL), infoLog(info))
			setStreamInfo(videoURL, info)
			return u, nil
		} else if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// Premium login unavailable: fall back to the normal free-quality call.
		logStream("%s stream: attempt=with-cookies host=music result=fail class=%s; cookies unavailable, using free quality", videoID(videoURL), failClass(err))
		cookieFailed = true
	}
	u, info, err := extractStreamWith(ctx, ytdlp, streamArgs(runtime, videoURL), false)
	if err == nil {
		info.CookieFailed = cookieFailed
		setStreamInfo(videoURL, info)
		logStream("%s stream: attempt=no-cookies result=ok %s", videoID(videoURL), infoLog(info))
	} else {
		logStream("%s stream: attempt=no-cookies result=fail class=%s", videoID(videoURL), failClass(err))
	}
	return u, err
}

// streamInfo describes the stream chosen for the current track (badge only).
type streamInfo struct {
	Codec        string
	Kbps         int
	CookieFailed bool
}

var (
	streamInfoMu sync.Mutex
	streamInfos  = map[string]streamInfo{}
)

func setStreamInfo(videoURL string, in streamInfo) {
	streamInfoMu.Lock()
	if len(streamInfos) > 64 {
		streamInfos = map[string]streamInfo{}
	}
	streamInfos[canonicalYouTubeURL(videoURL)] = in
	streamInfoMu.Unlock()
}

func streamInfoFor(videoURL string) (streamInfo, bool) {
	streamInfoMu.Lock()
	defer streamInfoMu.Unlock()
	in, ok := streamInfos[canonicalYouTubeURL(videoURL)]
	return in, ok
}

// parseStreamInfo reads the "ytm-fmt:<id> <acodec> <abr>" line; missing or
// unparsable data just yields a zero value.
func parseStreamInfo(out string) streamInfo {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ytm-fmt:") {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(line, "ytm-fmt:"))
		var in streamInfo
		if len(f) >= 2 && f[1] != "NA" && f[1] != "none" {
			in.Codec = strings.ToLower(f[1])
		}
		if len(f) >= 3 {
			if v, err := strconv.ParseFloat(f[2], 64); err == nil && v > 0 {
				in.Kbps = int(v + 0.5)
			}
		}
		return in
	}
	return streamInfo{}
}

// streamBadge is the short quality label for the now-playing line.
func streamBadge(in streamInfo, ok bool) string {
	if !ok {
		return ""
	}
	name := ""
	switch {
	case strings.HasPrefix(in.Codec, "opus"):
		name = "Opus"
	case strings.HasPrefix(in.Codec, "mp4a"), in.Codec == "aac":
		name = "AAC"
	case in.Codec != "":
		name = strings.ToUpper(in.Codec)
	}
	label := name
	if in.Kbps > 0 {
		label = strings.TrimSpace(name + " " + strconv.Itoa(in.Kbps) + "k")
	}
	switch {
	case in.Kbps >= 200:
		return strings.TrimSpace(label + " · PREMIUM")
	case in.CookieFailed:
		if label == "" {
			return "FREE (cookies failed)"
		}
		return strings.TrimSpace(label + " · FREE (cookies failed)")
	case in.Kbps > 0:
		return label + " · FREE"
	}
	if in.CookieFailed {
		return "FREE (cookies failed)"
	}
	return "?"
}

func extractStreamWith(ctx context.Context, ytdlp string, args []string, cookies bool) (string, streamInfo, error) {
	cmd := exec.CommandContext(ctx, ytdlp, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", streamInfo{}, ctx.Err()
		}
		text := strings.TrimSpace(stderr.String())
		if cookies {
			text = "yt-dlp failed with cookies"
		}
		if len(text) > 1800 {
			text = text[len(text)-1800:]
		}
		return "", streamInfo{}, fmt.Errorf("yt-dlp: %s", text)
	}
	// --get-url may emit multiple lines for formats composed of separate
	// streams. We explicitly select bestaudio and accept only YouTube's media CDN.
	lines := strings.Fields(string(output))
	for _, candidate := range lines {
		u, parseErr := url.Parse(candidate)
		if parseErr == nil && validAudioStreamURL(u) {
			return candidate, parseStreamInfo(string(output)), nil
		}
	}
	return "", streamInfo{}, fmt.Errorf("yt-dlp returned no trusted YouTube audio stream URL")
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
	args := downloadArgs(runtime, tmp, url)
	var out []byte
	usedCookies := false
	if mu, b := musicURL(url), cookiesBrowser(); b != "" && mu != "" {
		usedCookies = true
		logStream("%s download: attempt=with-cookies host=music", videoID(url))
		out, err = runDownload(ctx, ytdlp, withCookies(downloadArgs(runtime, tmp, mu), b))
		if err != nil && ctx.Err() == nil {
			logStream("%s download: with-cookies failed, retrying without; cookies unavailable, using free quality", videoID(url))
			usedCookies = false
			out, err = runDownload(ctx, ytdlp, args)
		}
	} else {
		out, err = runDownload(ctx, ytdlp, args)
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		text := strings.TrimSpace(string(out))
		if usedCookies {
			text = "yt-dlp failed with cookies"
		}
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

func downloadArgs(runtime, tmp, url string) []string {
	return []string{"--ignore-config", "--no-plugin-dirs", "--js-runtimes", runtime, "--remote-components", "ejs:github",
		"--extractor-args", "youtube:player_client=web_embedded", "--no-playlist", "--no-progress", "--no-warnings",
		"--socket-timeout", "15", "--retries", "3", "--fragment-retries", "3", "--max-filesize", "128M",
		"--match-filter", "!is_live", "-f", audioFormat, "-o", filepath.Join(tmp, "audio.%(ext)s"), "--", url}
}

func runDownload(ctx context.Context, ytdlp string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, ytdlp, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.CombinedOutput()
}

// debugf records a short generic note in the stream log (never cookie data).
func debugf(msg string) { logStream("%s", msg) }

var logMu sync.Mutex

// logStream appends one short line to %LOCALAPPDATA%\ytm\logs\stream.log
// (capped at ~200 KB). Callers must pass only generic facts: no stderr text,
// cookies or signed URLs.
func logStream(format string, a ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "logs")
	if os.MkdirAll(dir, 0700) != nil {
		return
	}
	path := filepath.Join(dir, "stream.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > 200*1024 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}

func videoID(videoURL string) string {
	if u, err := url.Parse(videoURL); err == nil {
		if v := u.Query().Get("v"); len(v) <= 20 {
			return v
		}
	}
	return "?"
}

func infoLog(in streamInfo) string {
	return fmt.Sprintf("codec=%q abr=%d cookieFailed=%t", in.Codec, in.Kbps, in.CookieFailed)
}

// failClass maps an error to a generic class so no yt-dlp output is logged.
func failClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case strings.Contains(err.Error(), "no trusted"):
		return "no-trusted-url"
	}
	return "yt-dlp-exit"
}
