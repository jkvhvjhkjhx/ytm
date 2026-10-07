# Contributing

Development currently targets Windows with the Go version in `go.mod`.

```powershell
go test -buildvcs=false ./...
go vet ./...
go build -buildvcs=false -o ytm.exe .
```

Tests use temporary directories for playlist data. Native clipboard testing is
opt-in using `YTM_TEST_CLIPBOARD=1`; it temporarily replaces then restores text
in the clipboard.

Real playback checks require yt-dlp, a supported JavaScript runtime, mpv, CAVA,
a working audio output device, and network access:

```powershell
.\ytm.exe --doctor
.\ytm.exe --self-check "Daft Punk Get Lucky"
```

The self-check plays real audio. Do not run it on a headless CI runner and label
mocked audio as a passing playback check.

Keep changes focused. For playlist changes, preserve the distinction between
the viewed library and the playback queue snapshot. For rendering changes,
preserve aspect ratios and avoid repaints of artwork on spectrum frames.
Update keyboard help and the README when changing a shortcut.

Do not commit executables, personal configuration, playlists, audio, tokens or
logs. Release binaries belong in GitHub Releases.
