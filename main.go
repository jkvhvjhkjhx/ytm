package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	tea "github.com/charmbracelet/bubbletea"
)

const appVersion = "0.2.0"

type Config struct {
	Theme              string `toml:"theme"`
	VisualizerStyle    string `toml:"visualizer_style"`
	VisualizerFPS      int    `toml:"visualizer_fps"`
	Volume             int    `toml:"volume"`
	ThumbnailSize      int    `toml:"thumbnail_size"`
	ImageMode          string `toml:"image_mode"`
	DefaultSearchLimit int    `toml:"default_search_limit"`
	MPVPath            string `toml:"mpv_path"`
	YTDLPPath          string `toml:"yt_dlp_path"`
	CavaPath           string `toml:"cava_path"`
	DenoPath           string `toml:"deno_path"`
	NodePath           string `toml:"node_path"`
}

func defaultConfig() Config {
	return Config{
		Theme: "nocturne", VisualizerStyle: "spectrum", VisualizerFPS: 24,
		Volume: 70, ThumbnailSize: 52, ImageMode: "auto", DefaultSearchLimit: 10,
	}
}

func configFile() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "ytm", "config.toml")
}

func loadConfig() (Config, error) {
	cfg := defaultConfig()
	path := configFile()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, saveConfig(cfg)
	} else if err != nil {
		return cfg, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("đọc config: %w", err)
	}
	defaults := defaultConfig()
	if cfg.Theme == "" {
		cfg.Theme = defaults.Theme
	}
	if cfg.VisualizerStyle == "" {
		cfg.VisualizerStyle = defaults.VisualizerStyle
	}
	if cfg.VisualizerFPS < 8 || cfg.VisualizerFPS > 60 {
		cfg.VisualizerFPS = defaults.VisualizerFPS
	}
	if cfg.Volume < 0 || cfg.Volume > 100 {
		cfg.Volume = defaults.Volume
	}
	if cfg.ThumbnailSize < 16 || cfg.ThumbnailSize > 64 {
		cfg.ThumbnailSize = defaults.ThumbnailSize
	}
	if cfg.DefaultSearchLimit < 1 || cfg.DefaultSearchLimit > 50 {
		cfg.DefaultSearchLimit = defaults.DefaultSearchLimit
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	path := configFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func findProgram(configured, name string, extra ...string) string {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, p := range extra {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func paths(cfg Config) (mpv, ytdlp, cava, deno string) {
	root := os.Getenv("USERPROFILE")
	mpv = findProgram(cfg.MPVPath, "mpv", filepath.Join(root, "scoop", "apps", "mpv", "current", "mpv.exe"))
	ytdlp = findProgram(cfg.YTDLPPath, "yt-dlp", filepath.Join(root, "scoop", "apps", "yt-dlp", "current", "yt-dlp.exe"))
	cava = findProgram(cfg.CavaPath, "cava", filepath.Join(root, "AppData", "Local", "cava", "cava.exe"))
	deno = findProgram(cfg.DenoPath, "deno", filepath.Join(root, "scoop", "apps", "deno", "current", "deno.exe"))
	return
}

func toolVersion(path string) string {
	if path == "" {
		return "not installed"
	}
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

func versionAtLeast(path string, major, minor int) bool {
	match := regexp.MustCompile(`[0-9]+\.[0-9]+(?:\.[0-9]+)?`).FindString(toolVersion(path))
	if match == "" {
		return false
	}
	parts := strings.Split(match, ".")
	maj, _ := strconv.Atoi(parts[0])
	min, _ := strconv.Atoi(parts[1])
	return maj > major || maj == major && min >= minor
}

func jsRuntime(deno, node string) string {
	if versionAtLeast(deno, 2, 3) {
		return "deno:" + deno
	}
	if versionAtLeast(node, 22, 0) {
		return "node:" + node
	}
	return ""
}

func printCheck(label, path string, required bool) {
	mark := "✓"
	if path == "" {
		mark = "·"
	}
	state := "OK"
	if path == "" {
		state = "not found"
	}
	requiredText := ""
	if required {
		requiredText = " required"
	}
	fmt.Printf("  %s  %-18s %s%s\n", mark, label, state, requiredText)
}

func installDependencies(missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	fmt.Printf("\nMissing: %s\n", strings.Join(missing, ", "))
	needsCava := false
	other := make([]string, 0, len(missing))
	for _, dep := range missing {
		if strings.EqualFold(dep, "cava") {
			needsCava = true
		} else {
			other = append(other, dep)
		}
	}
	fmt.Print("Install missing dependencies now? [Y/n] ")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		return fmt.Errorf("thiếu phụ thuộc: %s", strings.Join(missing, ", "))
	}
	if needsCava {
		winget, err := exec.LookPath("winget.exe")
		if err != nil {
			return fmt.Errorf("không tìm thấy WinGet để cài CAVA; cài bằng `winget install --id karlstav.cava -e` rồi chạy lại ytm")
		}
		cmd := exec.Command(winget, "install", "--id", "karlstav.cava", "-e", "--accept-source-agreements", "--accept-package-agreements")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("WinGet cài CAVA thất bại: %w", err)
		}
	}
	if len(other) > 0 {
		scoop, err := exec.LookPath("scoop")
		if err != nil {
			return fmt.Errorf("cần Scoop để tự cài các phụ thuộc: %s", strings.Join(other, ", "))
		}
		args := append([]string{"install"}, other...)
		cmd := exec.Command(scoop, args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Scoop cài phụ thuộc thất bại: %w", err)
		}
	}
	return nil
}

func startup() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Printf("ytm %s\n", appVersion)
			return errExit
		case "--doctor":
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			mpv, ytdlp, cava, deno := paths(cfg)
			node := findProgram(cfg.NodePath, "node", filepath.Join(os.Getenv("USERPROFILE"), "scoop", "apps", "nodejs", "current", "node.exe"))
			runtime := jsRuntime(deno, node)
			fmt.Println("YTM dependency check")
			printCheck("Windows Terminal", os.Getenv("WT_SESSION"), false)
			printCheck("mpv", mpv, true)
			printCheck("yt-dlp", ytdlp, true)
			printCheck("CAVA loopback", cava, true)
			printCheck("Deno", deno, false)
			printCheck("Node fallback >=22", node, false)
			printCheck("yt-dlp EJS solver", ytdlp, true)
			fmt.Printf("  JavaScript runtime  %s\n", runtime)
			fmt.Printf("  versions            yt-dlp %s · mpv %s · CAVA %s\n", toolVersion(ytdlp), toolVersion(mpv), toolVersion(cava))
			fmt.Printf("  JS versions         %s · %s\n", toolVersion(deno), toolVersion(node))
			fmt.Println("  EJS                 bundled solver + GitHub fallback enabled")
			fmt.Printf("  config             %s\n", configFile())
			return errExit
		case "--self-check":
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			mpv, ytdlp, cava, deno := paths(cfg)
			node := findProgram(cfg.NodePath, "node", filepath.Join(os.Getenv("USERPROFILE"), "scoop", "apps", "nodejs", "current", "node.exe"))
			runtime := jsRuntime(deno, node)
			if mpv == "" || ytdlp == "" || cava == "" || runtime == "" {
				return fmt.Errorf("thiếu mpv/yt-dlp/CAVA/Deno; chạy ytm --doctor")
			}
			query := "Daft Punk Instant Crush"
			if len(os.Args) > 2 {
				query = strings.Join(os.Args[2:], " ")
			}
			return runPipelineCheck(cfg, mpv, ytdlp, cava, runtime, query)
		case "--help", "-h":
			fmt.Printf("ytm %s — YouTube Music terminal player\n\nUsage: ytm [--doctor|--self-check|--version]\n", appVersion)
			return errExit
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	mpv, ytdlp, cava, deno := paths(cfg)
	node := findProgram(cfg.NodePath, "node", filepath.Join(os.Getenv("USERPROFILE"), "scoop", "apps", "nodejs", "current", "node.exe"))
	runtime := jsRuntime(deno, node)
	fmt.Printf("\x1b[38;2;189;147;249mY T M\x1b[0m  \x1b[38;2;139;233;253mYouTube Music for the terminal\x1b[0m\n\n")
	fmt.Println("Checking dependencies")
	printCheck("Windows Terminal", os.Getenv("WT_SESSION"), false)
	printCheck("mpv", mpv, true)
	printCheck("yt-dlp", ytdlp, true)
	printCheck("CAVA loopback", cava, true)
	printCheck("Deno", deno, false)
	printCheck("Node fallback >=22", node, false)
	printCheck("yt-dlp EJS solver", ytdlp, true)
	fmt.Printf("  versions            yt-dlp %s · mpv %s · CAVA %s\n", toolVersion(ytdlp), toolVersion(mpv), toolVersion(cava))
	fmt.Printf("  JS versions         %s · %s\n", toolVersion(deno), toolVersion(node))
	fmt.Println("  EJS                 bundled solver + GitHub fallback enabled")
	missing := []string{}
	if mpv == "" {
		missing = append(missing, "mpv")
	}
	if ytdlp == "" {
		missing = append(missing, "yt-dlp")
	}
	if cava == "" {
		missing = append(missing, "cava")
	}
	if runtime == "" {
		missing = append(missing, "deno")
	}
	if err := installDependencies(missing); err != nil {
		return err
	}
	mpv, ytdlp, cava, deno = paths(cfg)
	node = findProgram(cfg.NodePath, "node", filepath.Join(os.Getenv("USERPROFILE"), "scoop", "apps", "nodejs", "current", "node.exe"))
	runtime = jsRuntime(deno, node)
	if mpv == "" || ytdlp == "" || cava == "" || runtime == "" {
		return fmt.Errorf("mở terminal mới để cập nhật PATH rồi chạy lại ytm")
	}
	_ = cava
	return runPlayer(cfg, mpv, ytdlp, cava, runtime)
}

var errExit = errors.New("ytm:exit")

func main() {
	if err := startup(); err != nil && !errors.Is(err, errExit) {
		fmt.Fprintf(os.Stderr, "\n\x1b[38;2;255;85;85mYTM\x1b[0m  %v\n", err)
		os.Exit(1)
	}
}

func runPlayer(cfg Config, mpvPath, ytdlpPath, cavaPath, runtime string) error {
	player, err := newMPV(mpvPath, ytdlpPath, runtime)
	if err != nil {
		return err
	}
	defer player.Close()
	if err := player.Start(); err != nil {
		return err
	}
	_ = player.SetVolume(cfg.Volume)

	model := newModel(cfg, player, ytdlpPath, runtime)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model.ctx = ctx
	model.cfg.ImageMode = imageMode(cfg.ImageMode)
	model.persist = true
	model.restoreQueue()
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		model.query = strings.Join(os.Args[1:], " ")
		model.autoPlay = true
		model.searching = true
		model.searchGeneration = 1
	}
	program := tea.NewProgram(model, tea.WithoutRenderer(), tea.WithInput(os.Stdin), tea.WithOutput(io.Discard))
	model.program = program
	if cavaPath != "" {
		if err := model.startSpectrum(cavaPath); err != nil {
			go program.Send(spectrumErrorMsg(err.Error()))
		}
	}
	defer model.stopSpectrum()

	// Bubble Tea owns keyboard decoding; the app paints a fixed screen so Sixel
	// artwork can be positioned in the reserved player pane without a GUI layer.
	_, _ = io.WriteString(os.Stdout, "\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H")
	defer io.WriteString(os.Stdout, "\x1b[0m\x1b[?25h\x1b[?1049l")
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}

func humanDuration(seconds float64) string {
	if seconds <= 0 {
		return "--:--"
	}
	t := int(seconds)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%02d:%02d", t/60, t%60)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func tickEvery(d time.Duration, msg tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}
