# Security and privacy

YTM is a local Windows application. Its Go source contains no analytics service,
account login, browser-cookie extraction, playlist upload, or background clipboard
monitoring. Clipboard access is initiated by the editing shortcuts.

## Network boundaries

- Search and audio retrieval use yt-dlp and contact YouTube/Google. These services
  can observe the request, search terms, requested video and public IP address.
- Artwork is restricted to HTTPS YouTube image hosts, including redirects.
- yt-dlp configuration and plugin discovery are disabled for application requests.
  The official EJS GitHub fallback remains enabled for YouTube compatibility.
- mpv configuration and script loading are disabled. Control uses a local Windows
  named pipe, not a TCP listener. Same-user applications are not a security boundary.
- The installer contacts GitHub, Scoop and WinGet. ZIP checksums detect corruption;
  they do not independently authenticate a compromised release account.

## Local data

- `%APPDATA%\ytm`: settings and the playlist library, including track metadata and
  resume position. These are ordinary unencrypted files protected by Windows user
  directory permissions. Playback URLs are normalized YouTube URLs, not signed CDN
  stream URLs.
- `%LOCALAPPDATA%\ytm`: cached artwork/audio and diagnostics. Logs can contain local
  paths or playback errors. Do not post this whole directory in a public issue.
- The visualizer uses device-wide WASAPI loopback; other applications' output can
  influence the bars. Spectrum diagnostics are frequency measurements, not a PCM
  recording, and are not uploaded by YTM. `YTM_SPECTRUM_TRACE=1` explicitly enables
  a bounded local frame trace.
- Installer transcripts include machine/user information. Review and redact them
  before sharing. Screenshots may also expose filenames or desktop information.

## Building and reporting

Use Go 1.27.2 or newer and build distributable executables with `-trimpath` so source
paths from the build machine are not embedded. Keep mpv, yt-dlp and the JavaScript
runtime updated through their trusted distribution channels.

Run `go test -buildvcs=false ./...`, `go vet ./...` and
`go run golang.org/x/vuln/cmd/govulncheck@latest ./...` before a release.
No scanner can prove the absence of all vulnerabilities. Report security issues
privately through GitHub's security reporting feature when available; never post
tokens, cookies, private keys or unredacted personal logs in public issues.
