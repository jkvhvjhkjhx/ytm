# Upstream projects

YTM uses Go dependencies listed in `go.mod` and `go.sum`, including:

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) and Charmbracelet's terminal libraries
- [go-winio](https://github.com/microsoft/go-winio) for Windows named pipes
- [BurntSushi/toml](https://github.com/BurntSushi/toml)
- [Go supplementary libraries](https://github.com/golang)

The release packaging script copies license/notice files from each resolved Go
module into `THIRD_PARTY_LICENSES`, and records its module path and version.

These programs run as separately installed dependencies and are not bundled
in the YTM release ZIP:

- [yt-dlp](https://github.com/yt-dlp/yt-dlp), including its EJS integration
- [Deno](https://github.com/denoland/deno) (or supported Node.js runtime)
- [mpv](https://github.com/mpv-player/mpv)
- [CAVA](https://github.com/karlstav/cava)

Each upstream project retains its own license and attribution.
