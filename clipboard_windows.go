package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
	"golang.org/x/sys/windows"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

func withClipboard(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	owner, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if owner == 0 {
		class, _ := windows.UTF16PtrFromString("STATIC")
		owner, _, _ = user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
		if owner == 0 {
			return fmt.Errorf("cannot create clipboard owner")
		}
		defer user32.NewProc("DestroyWindow").Call(owner)
	}
	for i := 0; i < 10; i++ {
		r, _, _ := user32.NewProc("OpenClipboard").Call(owner)
		if r != 0 {
			defer user32.NewProc("CloseClipboard").Call()
			return fn()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("clipboard busy; retry Ctrl+V")
}
func clipboardRead() (text string, err error) {
	err = withClipboard(func() error {
		h, _, _ := user32.NewProc("GetClipboardData").Call(13)
		if h == 0 {
			return nil
		}
		p, _, _ := kernel32.NewProc("GlobalLock").Call(h)
		if p == 0 {
			return fmt.Errorf("clipboard lock failed")
		}
		defer kernel32.NewProc("GlobalUnlock").Call(h)
		size, _, _ := kernel32.NewProc("GlobalSize").Call(h)
		if size < 2 || size > 8<<20 {
			return fmt.Errorf("clipboard text too large")
		}
		data := make([]uint16, size/2)
		kernel32.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&data[0])), p, uintptr(len(data)*2))
		text = windows.UTF16ToString(data)
		return nil
	})
	return
}
func clipboardWrite(text string) error {
	data, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	return withClipboard(func() error {
		h, _, _ := kernel32.NewProc("GlobalAlloc").Call(0x42, uintptr(len(data)*2))
		if h == 0 {
			return fmt.Errorf("clipboard allocation failed")
		}
		owned := true
		defer func() {
			if owned {
				kernel32.NewProc("GlobalFree").Call(h)
			}
		}()
		p, _, _ := kernel32.NewProc("GlobalLock").Call(h)
		if p == 0 {
			return fmt.Errorf("clipboard lock failed")
		}
		kernel32.NewProc("RtlMoveMemory").Call(p, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
		kernel32.NewProc("GlobalUnlock").Call(h)
		if r, _, _ := user32.NewProc("EmptyClipboard").Call(); r == 0 {
			return fmt.Errorf("clipboard clear failed")
		}
		if r, _, _ := user32.NewProc("SetClipboardData").Call(13, h); r == 0 {
			return fmt.Errorf("clipboard write failed")
		}
		owned = false
		return nil
	})
}

type editor struct {
	text        []rune
	pos, anchor int
}

func (e *editor) bounds() (int, int) {
	if e.anchor < e.pos {
		return e.anchor, e.pos
	}
	return e.pos, e.anchor
}
func (e *editor) selected() string {
	a, b := e.bounds()
	if a == b {
		return string(e.text)
	}
	return string(e.text[a:b])
}
func (e *editor) insert(s string) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	a, b := e.bounds()
	r := []rune(s)
	limit := max(0, 4096-(len(e.text)-(b-a)))
	if len(r) > limit {
		r = r[:limit]
	}
	e.text = append(append(append([]rune{}, e.text[:a]...), r...), e.text[b:]...)
	e.pos = a + len(r)
	e.anchor = e.pos
}
func (e *editor) key(k tea.KeyMsg) {
	a, b := e.bounds()
	move := func(p int, shift bool) {
		e.pos = clamp(p, 0, len(e.text))
		if !shift {
			e.anchor = e.pos
		}
	}
	switch k.String() {
	case "ctrl+a":
		e.anchor = 0
		e.pos = len(e.text)
	case "left":
		if a != b {
			move(a, false)
		} else {
			move(e.previous(), false)
		}
	case "right":
		if a != b {
			move(b, false)
		} else {
			move(e.next(), false)
		}
	case "shift+left":
		move(e.previous(), true)
	case "shift+right":
		move(e.next(), true)
	case "home", "ctrl+home":
		move(0, false)
	case "end", "ctrl+end":
		move(len(e.text), false)
	case "shift+home":
		move(0, true)
	case "shift+end":
		move(len(e.text), true)
	case "ctrl+left":
		p := e.pos
		for p > 0 && unicode.IsSpace(e.text[p-1]) {
			p--
		}
		for p > 0 && !unicode.IsSpace(e.text[p-1]) {
			p--
		}
		move(p, false)
	case "ctrl+right":
		p := e.pos
		for p < len(e.text) && !unicode.IsSpace(e.text[p]) {
			p++
		}
		for p < len(e.text) && unicode.IsSpace(e.text[p]) {
			p++
		}
		move(p, false)
	case "backspace":
		if a == b && e.pos > 0 {
			e.anchor = e.previous()
		}
		e.insert("")
	case "delete":
		if a == b && e.pos < len(e.text) {
			e.anchor = e.next()
		}
		e.insert("")
	case "ctrl+u":
		e.text = nil
		e.pos = 0
		e.anchor = 0
	default:
		if k.Type == tea.KeyRunes {
			e.insert(string(k.Runes))
		} else if k.Type == tea.KeySpace {
			e.insert(" ")
		}
	}
}

func (e *editor) previous() int {
	g := uniseg.NewGraphemes(string(e.text))
	at, previous := 0, 0
	for g.Next() {
		at += utf8.RuneCountInString(g.Str())
		if at >= e.pos {
			return previous
		}
		previous = at
	}
	return previous
}
func (e *editor) next() int {
	g := uniseg.NewGraphemes(string(e.text))
	at := 0
	for g.Next() {
		at += utf8.RuneCountInString(g.Str())
		if at > e.pos {
			return at
		}
	}
	return len(e.text)
}
