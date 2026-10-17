//go:build windows

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Explicit user-authorized test-only lease. Never part of the shipped binary.
// Restore the original per-session mute value on completion, failure or 120s.
func TestMiniBrowserMuteLease(t *testing.T) {
	idText := os.Getenv("YTM_MINI_MUTE_BROWSER_PID")
	if idText == "" {
		t.Skip("requires explicit user-approved browser mute lease")
	}
	id, err := strconv.ParseUint(idText, 10, 32)
	if err != nil || id == 0 {
		t.Fatal("invalid browser PID")
	}
	dir := os.Getenv("YTM_MINI_LEASE_DIR")
	if dir == "" {
		t.Fatal("missing lease output")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var g miniAudioGuard
	g.init()
	defer g.close()
	d := g.defaultDevice(0)
	if d == nil {
		t.Fatal("default audio device unavailable")
	}
	defer d.release()
	var manager, enumeration *miniCOM
	if int32(d.call(3, uintptr(unsafe.Pointer(&miniSessionManagerIID)), 1, 0, uintptr(unsafe.Pointer(&manager)))) < 0 || manager == nil {
		t.Fatal("session manager unavailable")
	}
	defer manager.release()
	if int32(manager.call(5, uintptr(unsafe.Pointer(&enumeration)))) < 0 || enumeration == nil {
		t.Fatal("session enumeration unavailable")
	}
	defer enumeration.release()
	var count int32
	if int32(enumeration.call(3, uintptr(unsafe.Pointer(&count)))) < 0 {
		t.Fatal("session count unavailable")
	}
	restored := 0
	matched := 0
	defer func() {
		os.WriteFile(filepath.Join(dir, "restored.txt"), []byte(fmt.Sprintf("restored %d/%d browser sessions", restored, matched)), 0600)
	}()
	for i := int32(0); i < count; i++ {
		var s *miniCOM
		if int32(enumeration.call(4, uintptr(i), uintptr(unsafe.Pointer(&s)))) < 0 || s == nil {
			continue
		}
		_, pid, valid := miniSessionIdentity(s)
		if !valid || pid != uint32(id) {
			s.release()
			continue
		}
		var volume *miniCOM
		r := s.call(0, uintptr(unsafe.Pointer(&miniVolumeIID)), uintptr(unsafe.Pointer(&volume)))
		s.release()
		if int32(r) < 0 || volume == nil {
			t.Fatal("browser session volume unavailable")
		}
		var original uint32
		if int32(volume.call(6, uintptr(unsafe.Pointer(&original)))) < 0 {
			volume.release()
			t.Fatal("cannot read original mute state")
		}
		if int32(volume.call(5, 1, 0)) < 0 {
			volume.release()
			t.Fatal("cannot temporarily mute browser")
		}
		matched++
		defer func(v *miniCOM, old uint32) {
			defer v.release()
			if int32(v.call(5, uintptr(old), 0)) < 0 {
				t.Error("failed restoring original browser mute")
			} else {
				restored++
			}
		}(volume, original)
	}
	if matched == 0 {
		t.Fatal("approved browser session not found; nothing muted")
	}
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "active.txt"), []byte(fmt.Sprintf("browser PID %d; sessions %d; max 120 seconds", id, matched)), 0600)
	t.Logf("temporary browser mute lease active, PID=%d sessions=%d", id, matched)
	end := time.Now().Add(120 * time.Second)
	for time.Now().Before(end) {
		if _, e := os.Stat(filepath.Join(dir, "stop.txt")); e == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Explicit opt-in: creates actual native windows/tray and plays audible PCM
// through mpv/WASAPI. No fake FFT frames are fed to the companion here.
func TestMiniNativePlayback(t *testing.T) {
	if os.Getenv("YTM_MINI_NATIVE_TEST") != "1" {
		t.Skip("requires interactive Windows desktop and audio opt-in")
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	mpv, yt, cava, deno := paths(cfg)
	rt := jsRuntime(deno, findProgram(cfg.NodePath, "node"))
	if mpv == "" || cava == "" {
		t.Fatal("requires installed mpv and CAVA")
	}
	out := os.Getenv("YTM_MINI_TEST_OUTPUT")
	if out == "" {
		out = t.TempDir()
	}
	os.MkdirAll(out, 0700)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	p, err := newMPV(mpv, yt, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Start(); err != nil {
		t.Fatal(err)
	}
	cfg.MiniPlayer = defaultMiniConfig()
	cfg.MiniPlayer.AlwaysVisible = true
	mini, err := startMiniPlayer(cfg.MiniPlayer, miniMPVPID(p))
	if err != nil {
		t.Fatal(err)
	}
	defer mini.Close()
	ui := newModel(cfg, p, yt, rt)
	ui.output = nil
	ui.mini = mini
	ini := filepath.Join(t.TempDir(), "cava.ini")
	os.WriteFile(ini, []byte(cavaConfigText(30)), 0600)
	cmd := exec.Command(cava, "-p", ini)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(out, "cava-native.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Logf("audio processes: mpv=%d CAVA=%d", p.cmd.Process.Pid, cmd.Process.Pid)
	mini.capturePID.Store(uint32(cmd.Process.Pid))
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			b := parseSpectrum(scanner.Text())
			if len(b) > 0 {
				mini.pushBands(b, time.Now())
			}
		}
	}()
	// A single polling helper exercises the real model's transport commands.
	pump := func() {
		for {
			select {
			case a := <-mini.actions:
				ui = applyCommand(ui, ui.handleMiniAction(a))
			default:
				goto drained
			}
		}
	drained:
		if command := ui.acceptSnapshot(p.Snapshot()); command != nil {
			ui = applyCommand(ui, command)
		}
		ui.publishMini()
	}
	wait := func(label string, timeout time.Duration, condition func() bool) {
		t.Helper()
		until := time.Now().Add(timeout)
		for time.Now().Before(until) {
			pump()
			if condition() {
				return
			}
			time.Sleep(35 * time.Millisecond)
		}
		t.Fatalf("%s: status=%+v playback=%+v", label, mini.Status(), p.Snapshot())
	}
	play := func(path string) {
		t.Helper()
		if err := p.Load(path, 20); err != nil {
			t.Fatal(err)
		}
		if err := p.waitLoaded(path, p.epoch.Load(), false); err != nil {
			t.Fatal(err)
		}
		ui.audioPath = path
		ui.loading = false
		ui.stopped = false
		ui.snapshot = p.Snapshot()
		ui.publishMini()
	}
	var tonePaths []string
	for _, freq := range []float64{90, 1000, 8000} {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("tone-%g.wav", freq))
		writeMiniTone(t, path, freq, 60)
		tonePaths = append(tonePaths, path)
	}
	var maxima []int
	for i, path := range tonePaths {
		ui.current.Title = fmt.Sprintf("PCM frequency verification %d", i)
		play(path)
		wait("real CAVA / mpv spectrum", 6*time.Second, func() bool { return mini.Status().AudioAllowed && mini.Status().Peak > .05 && mini.Status().Visible })
		deadline := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(deadline) {
			pump()
			time.Sleep(35 * time.Millisecond)
		}
		in := mini.read()
		peak, index := 0., 0
		for j, v := range in.Bands[:in.Count] {
			if v > peak {
				peak, index = v, j
			}
		}
		maxima = append(maxima, index)
		captureMiniWindow(t, mini.Status().Overlay, filepath.Join(out, fmt.Sprintf("tone-%d.png", i)))
		t.Logf("real tone %d: peak band=%d, levels=%+v", i, index, mini.Status())
	}
	if !(maxima[0] < maxima[1] && maxima[1] < maxima[2]) {
		t.Fatalf("FFT frequencies not ordered: %v", maxima)
	}
	status := mini.Status()
	if !status.TrayAdded {
		t.Fatal("tray registration failed")
	}
	before := p.Snapshot().Time
	user32.NewProc("PostMessageW").Call(status.Host, 0x111, uintptr(miniPause), 0)
	wait("tray pause", 3*time.Second, func() bool { return p.Snapshot().Paused })
	wait("paused bars settle", 4*time.Second, func() bool { return mini.Status().Peak == 0 })
	frames := mini.Status().Frames
	time.Sleep(600 * time.Millisecond)
	pump()
	if mini.Status().Frames > frames+2 {
		t.Fatal("idle renderer continuously repaints")
	}
	user32.NewProc("PostMessageW").Call(status.Host, 0x111, uintptr(miniPause), 0)
	wait("tray resume", 4*time.Second, func() bool { return !p.Snapshot().Paused && p.Snapshot().Time > before+.2 && mini.Status().Peak > .05 })
	// Verify a second real audio process suppresses this device-wide spectrum.
	second, e := newMPV(mpv, yt, rt)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	if e = second.Start(); e != nil {
		t.Fatal(e)
	}
	if e = second.Load(tonePaths[0], 10); e != nil {
		t.Fatal(e)
	}
	wait("shared-output guard", 5*time.Second, func() bool {
		return !mini.Status().AudioAllowed && strings.Contains(mini.Status().AudioReason, "shared")
	})
	second.Close()
	wait("guard recovery", 5*time.Second, func() bool { return mini.Status().AudioAllowed })
	// Explorer notification replay tests recovery without restarting the user's shell.
	msg, _, _ := user32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(miniWide("TaskbarCreated"))))
	user32.NewProc("PostMessageW").Call(status.Host, msg, 0, 0)
	time.Sleep(300 * time.Millisecond)
	if !mini.Status().TrayAdded {
		t.Fatal("TaskbarCreated did not restore tray registration")
	}
	if status.Terminal != 0 {
		ui.cfg.MiniPlayer.AlwaysVisible = false
		ui.publishMini()
		wait("restored terminal hides overlay", 3*time.Second, func() bool { return !mini.Status().Visible })
		start := p.Snapshot().Time
		user32.NewProc("PostMessageW").Call(status.Host, 0x111, 12, 0)
		wait("minimized overlay policy", 4*time.Second, func() bool { s := mini.Status(); return s.Minimized && s.Visible })
		user32.NewProc("PostMessageW").Call(status.Overlay, 0x201, 0, 0)
		user32.NewProc("PostMessageW").Call(status.Overlay, 0x202, 0, 0)
		wait("overlay click restores", 4*time.Second, func() bool { return !miniBool("IsIconic", status.Terminal) && !mini.Status().Visible })
		user32.NewProc("PostMessageW").Call(status.Host, 0x111, 11, 0)
		wait("hidden overlay policy", 3*time.Second, func() bool { s := mini.Status(); return s.Hidden && s.Visible })
		user32.NewProc("PostMessageW").Call(status.Host, 0x111, 10, 0)
		wait("tray show restores", 3*time.Second, func() bool { return miniBool("IsWindowVisible", status.Terminal) && !mini.Status().Visible })
		user32.NewProc("PostMessageW").Call(status.Host, 0x111, 12, 0)
		wait("minimize before taskbar restore", 3*time.Second, func() bool { s := mini.Status(); return s.Minimized && s.Visible })
		user32.NewProc("ShowWindowAsync").Call(status.Host, 9)
		wait("taskbar proxy restore redirects", 3*time.Second, func() bool {
			return !miniBool("IsIconic", status.Terminal) && miniBool("IsIconic", status.Host) && !mini.Status().Visible
		})
		if p.Snapshot().Time <= start {
			t.Fatal("playback interrupted by window lifecycle")
		}
		t.Log("minimize/restore and hide/restore tested on actual host terminal")
	} else {
		t.Log("LIMITATION: no identified terminal; real host minimize/hide not exercised")
	}
	ui.cfg.MiniPlayer.AlwaysVisible = true
	ui.publishMini()
	if status.Terminal != 0 {
		focusMiniTestWindow(t, status.Terminal)
	}
	wait("always-visible", 3*time.Second, func() bool { return mini.Status().Visible })
	// An actual borderless foreground window verifies fullscreen visibility.
	func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		mon, _, _ := user32.NewProc("MonitorFromWindow").Call(status.Overlay, 2)
		info := miniMonitor{}
		info.Size = uint32(unsafe.Sizeof(info))
		user32.NewProc("GetMonitorInfoW").Call(mon, uintptr(unsafe.Pointer(&info)))
		r := info.Monitor
		h, _, _ := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(miniWide("STATIC"))), uintptr(unsafe.Pointer(miniWide("YTM fullscreen verification"))), 0x90000000, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0, 0, 0, 0)
		if h == 0 {
			t.Fatal("fullscreen test window could not be created")
		}
		defer user32.NewProc("DestroyWindow").Call(h)
		user32.NewProc("SetForegroundWindow").Call(h)
		wait("fullscreen visibility", 3*time.Second, func() bool { s := mini.Status(); return s.Fullscreen && s.Visible })
	}()
	if status.Terminal != 0 {
		focusMiniTestWindow(t, status.Terminal)
	}
	wait("fullscreen recovery", 3*time.Second, func() bool { return mini.Status().Visible })
	// Optional real music matrix; the audio URLs and pictures use production code.
	if os.Getenv("YTM_MINI_MUSIC_TEST") == "1" {
		var musicTracks []Track
		for i, query := range []string{"Daft Punk Instant Crush official", "Laufey From The Start official audio", "Adele Someone Like You official", "Erik Satie Gymnopedie No 1 piano"} {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
			tracks, e := searchYouTubeContext(ctx, yt, rt, query, 1)
			if e != nil || len(tracks) == 0 {
				cancel()
				t.Fatalf("music search %s: %v", query, e)
			}
			track := tracks[0]
			cancel()
			load := ui.loadTrack(track)
			ui.publishMini()
			ui = applyCommand(ui, load)
			if ui.errText != "" || ui.loading || ui.stopped || ui.art == nil {
				t.Fatalf("production load flow: error=%s loading=%v stopped=%v art=%v", ui.errText, ui.loading, ui.stopped, ui.art != nil)
			}
			musicTracks = append(musicTracks, track)
			wait("music spectrum", 8*time.Second, func() bool { return mini.Status().Peak > .02 && mini.Status().AudioAllowed })
			until := time.Now().Add(6 * time.Second)
			for time.Now().Before(until) {
				pump()
				time.Sleep(35 * time.Millisecond)
			}
			captureMiniWindow(t, status.Overlay, filepath.Join(out, fmt.Sprintf("music-%d.png", i)))
			if mini.read().Title != ui.current.Title || mini.read().Art != ui.art {
				t.Fatal("track/artwork not synchronized")
			}
			t.Logf("music %s: artwork=%v, mini=%+v", track.Title, ui.art.Bounds(), mini.Status())
		}
		// Native tray commands use the real production playback queue and loader.
		ui.queue = copyTracks(musicTracks)
		ui.syncViewed()
		ui.playbackQueue = copyTracks(musicTracks)
		ui.playing = len(musicTracks) - 1
		ui.repeatMode = "queue"
		ui.inputMode = true
		ui.helpOpen = true
		user32.NewProc("PostMessageW").Call(status.Host, 0x111, uintptr(miniNext), 0)
		wait("tray next through production queue", 30*time.Second, func() bool { return ui.current.ID == musicTracks[0].ID && !ui.loading && !ui.stopped })
		user32.NewProc("PostMessageW").Call(status.Host, 0x111, uintptr(miniPrevious), 0)
		wait("tray previous through production queue", 30*time.Second, func() bool { return ui.current.ID == musicTracks[len(musicTracks)-1].ID && !ui.loading && !ui.stopped })
		if !ui.inputMode || !ui.helpOpen {
			t.Fatal("tray transport stole TUI input mode")
		}
		t.Log("tray next/previous passed with real YouTube tracks while search/help retained focus")
	}
	// Resize/toggle settings through the actual native command path; check that
	// GDI handles do not grow with repeated bitmap replacement.
	beforeGDI, _, _ := user32.NewProc("GetGuiResources").Call(uintptr(windows.CurrentProcess()), 0)
	for i := 0; i < 3; i++ {
		for _, id := range []uintptr{103, 104, 103, 104} {
			user32.NewProc("PostMessageW").Call(status.Host, 0x111, id, 0)
			until := time.Now().Add(350 * time.Millisecond)
			for time.Now().Before(until) {
				pump()
				time.Sleep(35 * time.Millisecond)
			}
		}
	}
	afterGDI, _, _ := user32.NewProc("GetGuiResources").Call(uintptr(windows.CurrentProcess()), 0)
	if afterGDI > beforeGDI+2 {
		t.Fatalf("GDI objects grew: %d -> %d", beforeGDI, afterGDI)
	}
	saved, err := loadConfig()
	if err != nil || saved.MiniPlayer != ui.cfg.MiniPlayer {
		t.Fatalf("mini settings persistence: %v", err)
	}
	runtime.GC()
	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	var created, exited, kernelBefore, userBefore, kernelAfter, userAfter windows.Filetime
	windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernelBefore, &userBefore)
	start := time.Now()
	startFrames := mini.Status().Frames
	for time.Since(start) < 30*time.Second {
		pump()
		time.Sleep(35 * time.Millisecond)
	}
	windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernelAfter, &userAfter)
	runtime.GC()
	runtime.ReadMemStats(&memAfter)
	cpuNanos := kernelAfter.Nanoseconds() - kernelBefore.Nanoseconds() + userAfter.Nanoseconds() - userBefore.Nanoseconds()
	t.Logf("30s playback sample: uploaded frames=%d cpu=%.3fs Go heap before=%d after=%d GDI before=%d after=%d", mini.Status().Frames-startFrames, float64(cpuNanos)/1e9, memBefore.HeapAlloc, memAfter.HeapAlloc, beforeGDI, afterGDI)
	if memAfter.HeapAlloc > memBefore.HeapAlloc+(2<<20) {
		t.Fatal("unexpected retained Go heap growth during stable playback sample")
	}
	ui.stopped = true
	p.Stop()
	ui.publishMini()
	wait("silence", 4*time.Second, func() bool { return mini.Status().Peak < .003 })
	mini.Close()
	if miniBool("IsWindow", status.Host) || miniBool("IsWindow", status.Overlay) {
		t.Fatal("native window leaked after exit")
	}
	t.Log("native windows destroyed; native audio / tray / guard checks completed")
}

// Native mouse-message integration; no browser mute or audio playback needed.
func TestMiniNativeDrag(t *testing.T) {
	if os.Getenv("YTM_MINI_DRAG_TEST") != "1" {
		t.Skip("requires an interactive Windows desktop")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	oldDPI, _, _ := user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	defer user32.NewProc("SetThreadDpiAwarenessContext").Call(oldDPI)
	cfg := defaultMiniConfig()
	cfg.AlwaysVisible = true
	cfg.Visualizer = false
	p, err := startMiniPlayer(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if terminal := p.Status().Terminal; terminal != 0 {
		focusMiniTestWindow(t, terminal)
	}
	deadline := time.Now().Add(4 * time.Second)
	for !p.Status().Visible && time.Now().Before(deadline) {
		time.Sleep(40 * time.Millisecond)
	}
	s := p.Status()
	if !s.Visible {
		t.Fatal("mini not visible on test desktop")
	}
	getRect := func() miniRect {
		var r miniRect
		user32.NewProc("GetWindowRect").Call(s.Overlay, uintptr(unsafe.Pointer(&r)))
		return r
	}
	send := func(msg uint32, x, y int) {
		packed := uintptr(uint32(uint16(int16(x))) | uint32(uint16(int16(y)))<<16)
		user32.NewProc("SendMessageW").Call(s.Overlay, uintptr(msg), 0, packed)
	}
	before := getRect()
	foreground, _, _ := user32.NewProc("GetForegroundWindow").Call()
	send(0x201, 20, 20)
	send(0x200, -80, -40)
	// Wait for the normal render clock rather than pumping native state manually.
	time.Sleep(200 * time.Millisecond)
	send(0x202, 20, 20)
	after := getRect()
	if after.Left != before.Left-100 || after.Top != before.Top-60 {
		t.Fatalf("drag did not move overlay: before=%+v after=%+v", before, after)
	}
	time.Sleep(650 * time.Millisecond) // several automatic placement policy ticks
	if current := getRect(); current != after {
		t.Fatalf("automatic placement snapped back after drag: %+v vs %+v", current, after)
	}
	if now, _, _ := user32.NewProc("GetForegroundWindow").Call(); now != foreground {
		t.Fatal("drag stole focus")
	}
	type guiThreadInfo struct {
		Size, Flags                                        uint32
		Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
		CaretRect                                          miniRect
	}
	thread, _, _ := user32.NewProc("GetWindowThreadProcessId").Call(s.Overlay, 0)
	info := guiThreadInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	if !miniBool("GetGUIThreadInfo", thread, uintptr(unsafe.Pointer(&info))) {
		t.Fatal("cannot read overlay thread capture state")
	}
	if info.Capture != 0 {
		t.Fatal("drag left mouse capture held")
	}
	// Capture cancellation must not leave an armed click or keep moving.
	send(0x201, 20, 20)
	send(0x1f, 0, 0)
	send(0x200, -80, -40)
	send(0x202, -80, -40)
	time.Sleep(100 * time.Millisecond)
	if getRect() != after {
		t.Fatal("cancelled drag moved overlay")
	}
	// Also exercise genuine desktop input, including moving outside the original
	// overlay bounds while the left button is physically held via SendInput.
	var cursor miniPoint
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
	defer user32.NewProc("SetCursorPos").Call(uintptr(cursor.X), uintptr(cursor.Y))
	type mouse struct {
		X, Y              int32
		Data, Flags, Time uint32
		Extra             uintptr
	}
	type input struct {
		Type  uint32
		Mouse mouse
	}
	button := func(flags uint32) {
		in := input{Mouse: mouse{Flags: flags}}
		if sent, _, _ := user32.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in)); sent != 1 {
			t.Fatal("cannot inject verification mouse button")
		}
	}
	defer button(4)
	user32.NewProc("SetCursorPos").Call(uintptr(after.Left+20), uintptr(after.Top+20))
	button(2)
	time.Sleep(100 * time.Millisecond)
	user32.NewProc("SetCursorPos").Call(uintptr(after.Left-80), uintptr(after.Top-40))
	time.Sleep(200 * time.Millisecond)
	button(4)
	time.Sleep(300 * time.Millisecond)
	real := getRect()
	if real.Left != after.Left-100 || real.Top != after.Top-60 {
		t.Fatalf("real mouse drag failed: before=%+v after=%+v", after, real)
	}
	if now, _, _ := user32.NewProc("GetForegroundWindow").Call(); now != foreground {
		t.Fatal("real mouse drag stole focus")
	}
	t.Logf("drag moved %d,%d -> %d,%d; retained position, focus and capture cleanup verified", before.Left, before.Top, after.Left, after.Top)
	t.Logf("real desktop mouse drag moved overlay to %d,%d without activation", real.Left, real.Top)
}

func TestMiniNativeBrowserVisibility(t *testing.T) {
	if os.Getenv("YTM_MINI_BROWSER_TEST") != "1" {
		t.Skip("requires an interactive Windows desktop")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	oldDPI, _, _ := user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	defer user32.NewProc("SetThreadDpiAwarenessContext").Call(oldDPI)
	cfg := defaultMiniConfig()
	cfg.AlwaysVisible, cfg.Visualizer = true, false
	p, err := startMiniPlayer(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if h := p.Status().Terminal; h != 0 {
		focusMiniTestWindow(t, h)
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		until := time.Now().Add(4 * time.Second)
		for time.Now().Before(until) {
			if check() {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatalf("%s: %+v", label, p.Status())
	}
	wait("initial overlay", func() bool { return p.Status().Visible })
	s := p.Status()
	mon, _, _ := user32.NewProc("MonitorFromWindow").Call(s.Terminal, 2)
	info := miniMonitor{Size: uint32(unsafe.Sizeof(miniMonitor{}))}
	user32.NewProc("GetMonitorInfoW").Call(mon, uintptr(unsafe.Pointer(&info)))
	r := info.Monitor
	fixture, _, _ := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(miniWide("STATIC"))), uintptr(unsafe.Pointer(miniWide("YTM visibility test (temporary)"))), 0x10cf0000, uintptr(r.Left+100), uintptr(r.Top+100), 600, 400, 0, 0, 0, 0)
	if fixture == 0 {
		t.Fatal("cannot create test window")
	}
	defer user32.NewProc("DestroyWindow").Call(fixture)
	user32.NewProc("ShowWindow").Call(fixture, 3)
	focusMiniTestWindow(t, fixture)
	// Simulate a framed browser occupying every monitor pixel, including the
	// invisible resize borders that caused false fullscreen classification.
	miniSetPositionProc.Call(fixture, 0, uintptr(r.Left-8), uintptr(r.Top-8), uintptr(r.Right-r.Left+16), uintptr(r.Bottom-r.Top+16), 0x14)
	if miniFullscreen(mon, r, s.Host, s.Overlay) {
		t.Fatal("maximized framed window classified as fullscreen")
	}
	wait("maximized regular window keeps mini visible", func() bool { st := p.Status(); return st.Visible && !st.Fullscreen })
	// True borderless fullscreen must retain the overlay.
	user32.NewProc("SetWindowLongPtrW").Call(fixture, ^uintptr(15), 0x90000000)
	miniSetPositionProc.Call(fixture, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x34)
	wait("true fullscreen keeps mini", func() bool { st := p.Status(); return st.Fullscreen && st.Visible })
	assertAbove := func(window uintptr) {
		t.Helper()
		h, _, _ := user32.NewProc("GetTopWindow").Call(0)
		for i := 0; h != 0 && i < 2048; i++ {
			if h == s.Overlay {
				return
			}
			if h == window {
				t.Fatal("fullscreen/browser covers mini-player")
			}
			h, _, _ = user32.NewProc("GetWindow").Call(h, 2)
		}
		t.Fatal("overlay missing from native z-order")
	}
	assertAbove(fixture)
	user32.NewProc("SetWindowLongPtrW").Call(fixture, ^uintptr(15), 0x10cf0000)
	miniSetPositionProc.Call(fixture, 0, uintptr(r.Left+100), uintptr(r.Top+100), 600, 400, 0x34)
	wait("leaving fullscreen restores mini", func() bool { st := p.Status(); return st.Visible && !st.Fullscreen })
	if s.Terminal != 0 {
		cfg.AlwaysVisible = false
		p.publish(miniInput{Settings: cfg})
		user32.NewProc("ShowWindowAsync").Call(s.Terminal, 6)
		defer user32.NewProc("ShowWindowAsync").Call(s.Terminal, 9)
		wait("minimized terminal keeps mini visible", func() bool { st := p.Status(); return st.Minimized && st.Visible })
	}
	var browser uintptr
	var browserName string
	callback := windows.NewCallback(func(h, unused uintptr) uintptr {
		if miniClass(h) != "Chrome_WidgetWin_1" || !miniBool("IsWindowVisible", h) || miniBool("IsIconic", h) {
			return 1
		}
		var pid uint32
		user32.NewProc("GetWindowThreadProcessId").Call(h, uintptr(unsafe.Pointer(&pid)))
		process, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if e != nil {
			return 1
		}
		defer windows.CloseHandle(process)
		var path [1024]uint16
		size := uint32(len(path))
		if windows.QueryFullProcessImageName(process, 0, &path[0], &size) != nil {
			return 1
		}
		name := strings.ToLower(filepath.Base(windows.UTF16ToString(path[:size])))
		if name == "browser.exe" || name == "coccoc.exe" || name == "chrome.exe" || name == "msedge.exe" {
			browser, browserName = h, name
			return 0
		}
		return 1
	})
	user32.NewProc("EnumWindows").Call(callback, 0)
	if browser != 0 {
		focusMiniTestWindow(t, fixture)
		user32.NewProc("SetForegroundWindow").Call(browser)
		fg, _, _ := user32.NewProc("GetForegroundWindow").Call()
		if fg != browser {
			t.Fatal("could not activate actual browser for verification")
		}
		browserMon, _, _ := user32.NewProc("MonitorFromWindow").Call(browser, 2)
		if browserMon != mon {
			t.Log("actual browser is on a different monitor")
		}
		time.Sleep(700 * time.Millisecond)
		if st := p.Status(); !st.Visible {
			t.Fatalf("mini vanished over actual %s: %+v", browserName, st)
		}
		// Check z-order, not merely IsWindowVisible (a covered overlay is useless).
		h, _, _ := user32.NewProc("GetTopWindow").Call(0)
		for i := 0; h != 0 && i < 2048; i++ {
			if h == s.Overlay {
				break
			}
			if h == browser {
				t.Fatal("browser covers mini in native z-order")
			}
			h, _, _ = user32.NewProc("GetWindow").Call(h, 2)
		}
		if h != s.Overlay {
			t.Fatal("could not find overlay in z-order")
		}
		t.Logf("actual %s: mini visible above browser, terminal minimized=%v", browserName, p.Status().Minimized)
		if browserMon == mon && !p.Status().Fullscreen {
			f11 := func() {
				t.Helper()
				// Windows x64 INPUT: 8-byte aligned 32-byte union.
				type input struct {
					Type    uint32
					Padding uint32
					Data    [32]byte
				}
				keys := [2]input{{Type: 1}, {Type: 1}}
				keys[0].Data[0], keys[1].Data[0] = 0x7a, 0x7a
				keys[1].Data[4] = 2 // KEYEVENTF_KEYUP
				if sent, _, _ := user32.NewProc("SendInput").Call(2, uintptr(unsafe.Pointer(&keys[0])), unsafe.Sizeof(keys[0])); sent != 2 {
					t.Fatal("cannot send browser F11")
				}
			}
			f11()
			defer func() { user32.NewProc("SetForegroundWindow").Call(browser); f11(); time.Sleep(250 * time.Millisecond) }()
			wait("actual browser F11 retains mini", func() bool { st := p.Status(); return st.Fullscreen && st.Visible })
			time.Sleep(400 * time.Millisecond)
			assertAbove(browser)
			t.Logf("actual %s F11 fullscreen: mini remains above browser", browserName)
		}
	} else {
		t.Log("LIMITATION: no existing Chromium browser to activate")
	}
	t.Log("regular maximized window, true fullscreen, recovery and native z-order verified")
}

// Test-only real input focuses the dedicated verification terminal. Posted
// messages alone cannot grant foreground activation permission on Windows.
func focusMiniTestWindow(t *testing.T, h uintptr) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	oldDPI, _, _ := user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	defer user32.NewProc("SetThreadDpiAwarenessContext").Call(oldDPI)
	style, _, _ := user32.NewProc("GetWindowLongPtrW").Call(h, ^uintptr(19))
	var original miniPoint
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&original)))
	defer user32.NewProc("SetCursorPos").Call(uintptr(original.X), uintptr(original.Y))
	user32.NewProc("SetWindowPos").Call(h, ^uintptr(0), 0, 0, 0, 0, 0x53)
	defer func() {
		if style&8 == 0 {
			user32.NewProc("SetWindowPos").Call(h, ^uintptr(1), 0, 0, 0, 0, 0x13)
		}
	}()
	var r miniRect
	user32.NewProc("GetWindowRect").Call(h, uintptr(unsafe.Pointer(&r)))
	metric := func(i uintptr) int32 { v, _, _ := user32.NewProc("GetSystemMetrics").Call(i); return int32(v) }
	x, y := r.Left+80, r.Top+18
	dx := int32(float64(x-metric(76)) * 65535 / float64(max(int32(1), metric(78)-1)))
	dy := int32(float64(y-metric(77)) * 65535 / float64(max(int32(1), metric(79)-1)))
	type mouse struct {
		X, Y              int32
		Data, Flags, Time uint32
		Extra             uintptr
	}
	type input struct {
		Type  uint32
		Mouse mouse
	}
	inputs := [3]input{{Mouse: mouse{X: dx, Y: dy, Flags: 0xc001}}, {Mouse: mouse{Flags: 2}}, {Mouse: mouse{Flags: 4}}}
	if sent, _, _ := user32.NewProc("SendInput").Call(3, uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0])); sent != 3 {
		t.Fatal("cannot activate dedicated test terminal")
	}
	time.Sleep(150 * time.Millisecond)
}

func writeMiniTone(t *testing.T, path string, freq float64, seconds int) {
	t.Helper()
	const rate = 48000
	pcm := make([]int16, rate*seconds)
	for i := range pcm {
		at := float64(i) / rate
		amp := .12 + .08*math.Sin(2*math.Pi*2*at)
		pcm[i] = int16(32767 * amp * math.Sin(2*math.Pi*freq*at))
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString("RIFF")
	binary.Write(f, binary.LittleEndian, uint32(36+len(pcm)*2))
	f.WriteString("WAVEfmt ")
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, uint32(rate))
	binary.Write(f, binary.LittleEndian, uint32(rate*2))
	binary.Write(f, binary.LittleEndian, uint16(2))
	binary.Write(f, binary.LittleEndian, uint16(16))
	f.WriteString("data")
	binary.Write(f, binary.LittleEndian, uint32(len(pcm)*2))
	if err = binary.Write(f, binary.LittleEndian, pcm); err != nil {
		t.Fatal(err)
	}
}

func captureMiniWindow(t *testing.T, h uintptr, path string) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	proc := user32.NewProc("SetThreadDpiAwarenessContext")
	old, _, _ := proc.Call(^uintptr(3))
	defer proc.Call(old)
	var r miniRect
	if !miniBool("GetWindowRect", h, uintptr(unsafe.Pointer(&r))) {
		t.Fatal("capture window missing")
	}
	w, ht := int(r.Right-r.Left), int(r.Bottom-r.Top)
	var d miniDIB
	if err := d.resize(w, ht); err != nil {
		t.Fatal(err)
	}
	defer d.close()
	dc, _, _ := user32.NewProc("GetDC").Call(0)
	defer user32.NewProc("ReleaseDC").Call(0, dc)
	if ret, _, _ := miniGDI.NewProc("BitBlt").Call(d.dc, 0, 0, uintptr(w), uintptr(ht), dc, uintptr(r.Left), uintptr(r.Top), 0x40cc0020); ret == 0 {
		t.Fatal("desktop capture failed")
	}
	buf := make([]byte, w*ht*4)
	kernel32.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&buf[0])), d.bits, uintptr(len(buf)))
	img := image.NewNRGBA(image.Rect(0, 0, w, ht))
	for i := 0; i < len(buf); i += 4 {
		img.Pix[i] = buf[i+2]
		img.Pix[i+1] = buf[i+1]
		img.Pix[i+2] = buf[i]
		img.Pix[i+3] = 255
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
