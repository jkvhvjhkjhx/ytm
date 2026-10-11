package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Artwork metadata can come from a saved/shared playlist. Never let it make
// requests to arbitrary hosts, local network services, or credentialed URLs.
func validateArtworkURL(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("artwork requires an HTTPS YouTube image URL")
	}
	switch strings.ToLower(u.Hostname()) {
	case "i.ytimg.com", "i1.ytimg.com", "i2.ytimg.com", "i3.ytimg.com", "i4.ytimg.com",
		"img.youtube.com", "yt3.ggpht.com", "yt3.googleusercontent.com":
		return nil
	}
	return fmt.Errorf("artwork host is not a supported YouTube image host")
}

func checkArtworkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many artwork redirects")
	}
	return validateArtworkURL(req.URL)
}

func validVideoID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
