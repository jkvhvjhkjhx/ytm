package main

import (
	"bufio"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type spectrumMsg []float64
type spectrumErrorMsg string

// Windows CAVA captures WASAPI loopback automatically, so no [input] section
// belongs in this config ("method = auto" is not a valid input method).
func cavaConfigText(fps int) string {
	return fmt.Sprintf(`[general]
framerate = %d
	bars = 96
autosens = 1
sleep_timer = 2
lower_cutoff_freq = 50
higher_cutoff_freq = 16000

[smoothing]
monstercat = 0
waves = 0
noise_reduction = 45

[output]
method = raw
raw_target = /dev/stdout
data_format = ascii
ascii_max_range = 100
bar_delimiter = 59
frame_delimiter = 10
channels = mono
`, clamp(fps, 8, 60))
}

func parseSpectrum(line string) []float64 {
	parts := strings.Split(strings.TrimSpace(line), ";")
	bars := make([]float64, 0, len(parts))
	for _, part := range parts {
		if v, err := strconv.ParseFloat(strings.TrimSpace(part), 64); err == nil {
			bars = append(bars, v/100)
		}
	}
	return bars
}

func (m *model) startSpectrum(cavaPath string) error {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("ytm-cava-%d.ini", os.Getpid()))
	if err := os.WriteFile(tmp, []byte(cavaConfigText(m.cfg.VisualizerFPS)), 0o600); err != nil {
		return err
	}
	cmd := exec.Command(cavaPath, "-p", tmp)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	logPath := filepath.Join(os.Getenv("LOCALAPPDATA"), "ytm", "logs", "cava.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	m.cavaCmd, m.cavaConfig, m.cavaLog = cmd, tmp, logFile
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 256), 8192)
		for scanner.Scan() {
			bars := parseSpectrum(scanner.Text())
			if len(bars) > 0 && m.program != nil {
				m.program.Send(spectrumMsg(bars))
			}
		}
		if m.program != nil {
			if data, err := os.ReadFile(logPath); err == nil && len(data) > 0 {
				m.program.Send(spectrumErrorMsg(strings.TrimSpace(string(data))))
			} else {
				m.program.Send(spectrumErrorMsg("CAVA stopped streaming audio data"))
			}
		}
	}()
	return nil
}

func (m *model) stopSpectrum() {
	if m.cavaCmd != nil && m.cavaCmd.Process != nil {
		_ = m.cavaCmd.Process.Kill()
		_ = m.cavaCmd.Wait()
	}
	if m.cavaConfig != "" {
		_ = os.Remove(m.cavaConfig)
	}
	if m.cavaLog != nil {
		_ = m.cavaLog.Close()
	}
}

type playerTickMsg PlayerSnapshot

func (m *model) tickCmd() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
		return playerTickMsg(m.player.Snapshot())
	})
}
