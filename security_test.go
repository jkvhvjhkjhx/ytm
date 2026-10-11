package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestArtworkNetworkBoundary(t *testing.T) {
	for _, raw := range []string{
		"http://i.ytimg.com/vi/abc/hqdefault.jpg", "https://127.0.0.1/private",
		"https://localhost/private", "https://169.254.169.254/latest/meta-data/",
		"https://i.ytimg.com.attacker.invalid/image", "https://user:password@i.ytimg.com/image",
		"https://i.ytimg.com:8080/image", "file:///C:/private.txt", "https://attacker.invalid/image",
	} {
		u, _ := url.Parse(raw)
		if err := validateArtworkURL(u); err == nil {
			t.Errorf("accepted unsafe artwork URL: %s", raw)
		}
		if artHTTP.CheckRedirect == nil {
			t.Fatal("artwork client has no redirect boundary")
		}
		if err := artHTTP.CheckRedirect(&http.Request{URL: u}, nil); err == nil {
			t.Errorf("accepted unsafe redirect: %s", raw)
		}
		// Must fail before network I/O, even with an already cancelled context.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := fetchArtworkContext(ctx, raw); err == nil || strings.Contains(err.Error(), "context canceled") {
			t.Errorf("did not reject URL before transport: %v", err)
		}
	}
	for _, raw := range []string{"https://i.ytimg.com/vi/abc/maxresdefault.jpg", "https://i.ytimg.com:443/image", "https://yt3.ggpht.com/image"} {
		u, _ := url.Parse(raw)
		if err := validateArtworkURL(u); err != nil {
			t.Errorf("rejected legitimate image: %v", err)
		}
	}
}

func TestAudioRejectsUntrustedTargetsBeforeProcess(t *testing.T) {
	for _, raw := range []string{"--exec=malicious", "file:///C:/private.txt", "https://attacker.invalid/audio", "https://user:pass@youtube.com/watch?v=abc", "https://youtube.com/watch?v=abc%26x", "ftp://youtube.com/watch?v=abc"} {
		if canonicalYouTubeURL(raw) != "" {
			t.Errorf("accepted unsafe video URL: %s", raw)
		}
		_, err := prepareAudio(context.Background(), "must-not-run.exe", "", raw)
		if err == nil || !strings.Contains(err.Error(), "valid YouTube") {
			t.Errorf("target reached process/cache boundary: %v", err)
		}
	}
}

func TestPlaylistDeletePromptStripsTerminalCommands(t *testing.T) {
	m := newModel(defaultConfig(), nil, "", "")
	m.playlists = []Playlist{{ID: "one", Name: "safe\x1b]52;c;c2VjcmV0\a\x1b[2Jname"}}
	m.playlistDialog = "delete"
	rows := m.playlistRows(nil, 3, 80)
	if strings.Contains(rows[1], "\x1b]52") || strings.Contains(rows[1], "\x1b[2J") {
		t.Fatal("playlist name injected a terminal control sequence")
	}
	if !strings.Contains(rows[1], "safename") {
		t.Fatalf("ordinary playlist name damaged: %q", rows[1])
	}
}
