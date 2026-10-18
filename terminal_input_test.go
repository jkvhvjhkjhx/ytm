package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
)

// Use the production program options and terminal modes on an actual Windows
// Terminal desktop. A synthetic MouseMsg alone cannot detect host-side paste.
func TestNativeKeyboardOnlyTerminal(t *testing.T) {
	if os.Getenv("YTM_TEST_TERMINAL_INPUT") != "1" {
		t.Skip("requires interactive Windows Terminal and temporary clipboard use")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	report := map[string]any{}
	defer func() {
		report["failed"] = t.Failed()
		if path := os.Getenv("YTM_TEST_TERMINAL_REPORT"); path != "" {
			data, _ := json.MarshalIndent(report, "", "  ")
			_ = os.WriteFile(path, data, 0600)
		}
	}()
	oldDPI, _, _ := user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	defer user32.NewProc("SetThreadDpiAwarenessContext").Call(oldDPI)
	original, err := clipboardRead()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := clipboardWrite(original); err != nil {
			t.Errorf("restore clipboard: %v", err)
		}
	}()
	const pasted = "clipboard verification"
	if err := clipboardWrite(pasted); err != nil {
		t.Fatal(err)
	}
	terminal, _ := miniFindTerminal()
	if terminal == 0 {
		t.Fatal("dedicated Windows Terminal was not identified")
	}
	focusMiniTestWindow(t, terminal)
	var cursor miniPoint
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
	defer user32.NewProc("SetCursorPos").Call(uintptr(cursor.X), uintptr(cursor.Y))
	type mouse struct {
		X, Y              int32
		Data, Flags, Time uint32
		Extra             uintptr
	}
	type mouseInput struct {
		Type  uint32
		Mouse mouse
	}
	sendMouse := func(flags, data uint32) {
		t.Helper()
		in := mouseInput{Mouse: mouse{Flags: flags, Data: data}}
		if n, _, _ := user32.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in)); n != 1 {
			t.Fatal("cannot send test mouse input")
		}
	}
	type keyInput struct {
		Type    uint32
		Padding uint32
		Data    [32]byte
	}
	sendKey := func(vk byte, release bool) {
		t.Helper()
		in := keyInput{Type: 1}
		in.Data[0] = vk
		if release {
			in.Data[4] = 2
		}
		if n, _, _ := user32.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in)); n != 1 {
			t.Fatal("cannot send test keyboard input")
		}
	}
	type runResult struct {
		model tea.Model
		err   error
	}
	phase := func(search bool, action func()) model {
		t.Helper()
		m := newModel(defaultConfig(), nil, "", "")
		// The input verification does not start or control any audio process.
		m.player = &MPV{closed: make(chan struct{}), snapshot: PlayerSnapshot{Idle: true}}
		m.width, m.height = 80, 24
		m.inputMode = search
		if search {
			m.query = "typed "
			m.edit = editor{text: []rune(m.query), pos: len(m.query), anchor: len(m.query)}
		}
		p := newTerminalProgram(m)
		_, _ = io.WriteString(os.Stdout, terminalPlayerEnter)
		defer io.WriteString(os.Stdout, terminalPlayerExit)
		result := make(chan runResult, 1)
		go func() { final, err := p.Run(); result <- runResult{final, err} }()
		defer p.Quit()
		time.Sleep(500 * time.Millisecond)
		action()
		time.Sleep(500 * time.Millisecond)
		p.Quit()
		select {
		case result := <-result:
			if result.err != nil {
				t.Fatal(result.err)
			}
			return result.model.(model)
		case <-time.After(4 * time.Second):
			t.Fatal("terminal input did not shut down cleanly")
			return model{}
		}
	}
	m := phase(true, func() {
		var r miniRect
		user32.NewProc("GetWindowRect").Call(terminal, uintptr(unsafe.Pointer(&r)))
		user32.NewProc("SetCursorPos").Call(uintptr((r.Left+r.Right)/2), uintptr((r.Top+r.Bottom)/2))
		sendMouse(8, 0)
		sendMouse(16, 0) // right down/up
		sendMouse(2, 0)
		sendMouse(4, 0)       // left down/up
		sendMouse(0x800, 120) // wheel
	})
	report["after_mouse"] = m.query
	if m.query != "typed " || !m.inputMode || m.helpOpen || m.presetOpen || m.playlistDialog != "" || m.queueFocus {
		t.Fatalf("mouse changed terminal input or opened a menu: query=%q help=%v preset=%v dialog=%q", m.query, m.helpOpen, m.presetOpen, m.playlistDialog)
	}
	fmt.Fprintln(os.Stdout, "native mouse: right-click, left-click and wheel ignored; no clipboard insertion")
	m = phase(true, func() { sendKey(0x11, false); sendKey(0x56, false); sendKey(0x56, true); sendKey(0x11, true) })
	report["after_ctrl_v"] = m.query
	if m.query != "typed "+pasted {
		t.Fatalf("Ctrl+V failed: query=%q error=%s", m.query, m.errText)
	}
	fmt.Fprintln(os.Stdout, "native keyboard: Ctrl+V still pastes")
	m = phase(false, func() { sendKey(0x54, false); sendKey(0x54, true) })
	report["help_open"] = m.helpOpen
	if !m.helpOpen {
		t.Fatal("T did not open the keyboard help overlay")
	}
	fmt.Fprintln(os.Stdout, "native keyboard: T opens help; terminal modes restored on exit")
}
