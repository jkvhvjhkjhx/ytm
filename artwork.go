package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var artCache = struct {
	sync.Mutex
	items map[string]image.Image
	order []string
}{items: make(map[string]image.Image)}
var artHTTP = &http.Client{Timeout: 8 * time.Second}

func fetchArtwork(url string) (image.Image, error) {
	return fetchArtworkContext(context.Background(), url)
}
func decodeArtwork(data []byte) (image.Image, error) {
	cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8000 || cfg.Height > 8000 || cfg.Width*cfg.Height > 16000000 {
		return nil, fmt.Errorf("unsupported artwork dimensions")
	}
	img, _, e := image.Decode(bytes.NewReader(data))
	return img, e
}
func fetchArtworkContext(ctx context.Context, url string) (image.Image, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return nil, e
	}
	resp, e := artHTTP.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("artwork HTTP %d", resp.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if e != nil {
		return nil, e
	}
	return decodeArtwork(data)
}
func rememberArtwork(key string, img image.Image) {
	artCache.Lock()
	defer artCache.Unlock()
	if _, ok := artCache.items[key]; ok {
		return
	}
	for len(artCache.order) >= 4 {
		delete(artCache.items, artCache.order[0])
		artCache.order = artCache.order[1:]
	}
	artCache.items[key] = img
	artCache.order = append(artCache.order, key)
}
func fetchTrackArtwork(t Track) image.Image {
	img, _ := trackArtwork(context.Background(), t)
	return img
}
func trackArtwork(parent context.Context, t Track) (image.Image, error) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(t.ID+"|"+t.Thumbnail)))
	artCache.Lock()
	img := artCache.items[key]
	artCache.Unlock()
	if img != nil {
		return img, nil
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "artwork")
	file := filepath.Join(dir, key+".png")
	if data, e := os.ReadFile(file); e == nil {
		if img, e = decodeArtwork(data); e == nil {
			rememberArtwork(key, img)
			_ = os.Chtimes(file, time.Now(), time.Now())
			return img, nil
		}
	}
	ctx, cancel := context.WithTimeout(parent, 16*time.Second)
	defer cancel()
	urls := []string{"https://i.ytimg.com/vi/" + t.ID + "/maxresdefault.jpg", t.Thumbnail, "https://i.ytimg.com/vi/" + t.ID + "/hqdefault.jpg"}
	var last error
	for _, url := range urls {
		if url == "" {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		img, last = fetchArtworkContext(ctx, url)
		if last == nil && img.Bounds().Dx() > 120 {
			rememberArtwork(key, img)
			if os.MkdirAll(dir, 0700) == nil {
				if f, e := os.CreateTemp(dir, "art-*.tmp"); e == nil {
					name := f.Name()
					e = png.Encode(f, img)
					ce := f.Close()
					if e == nil && ce == nil {
						_ = os.Rename(name, file)
					}
					_ = os.Remove(name)
					trimArtworkCache(dir, file)
				}
			}
			return img, nil
		}
	}
	return nil, fmt.Errorf("artwork unavailable: %v", last)
}
func trimArtworkCache(dir, keep string) {
	// Reuse the same bounded LRU pruning routine with a different extension.
	trimFiles(dir, "*.png", keep, 32<<20)
}
