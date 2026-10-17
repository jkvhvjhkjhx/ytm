//go:build windows

package main

import (
	"fmt"
	"image/color"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var miniShell = windows.NewLazySystemDLL("shell32.dll")
var miniGDI = windows.NewLazySystemDLL("gdi32.dll")
var miniDWM = windows.NewLazySystemDLL("dwmapi.dll")
var miniShcore = windows.NewLazySystemDLL("shcore.dll")
var miniWindows sync.Map
var miniWindowCallback = windows.NewCallback(miniWindowProc)
var miniPostMessageProc = user32.NewProc("PostMessageW")
var miniUpdateLayerProc = user32.NewProc("UpdateLayeredWindow")
var miniSetPositionProc = user32.NewProc("SetWindowPos")
var miniCopyMemoryProc = kernel32.NewProc("RtlMoveMemory")
var miniGetMessageProc = user32.NewProc("GetMessageW")
var miniTranslateProc = user32.NewProc("TranslateMessage")
var miniDispatchProc = user32.NewProc("DispatchMessageW")

const miniTrayMessage = 0x8001
const miniShutdownMessage = 0x8002

type miniPoint struct{ X, Y int32 }
type miniRect struct{ Left, Top, Right, Bottom int32 }
type miniMonitor struct {
	Size          uint32
	Monitor, Work miniRect
	Flags         uint32
}
type miniWndClass struct {
	Size, Style                                               uint32
	Proc                                                      uintptr
	ClassExtra, WindowExtra                                   int32
	Instance, Icon, Cursor, Background, Menu, Name, SmallIcon uintptr
}
type miniMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          miniPoint
	Private        uint32
}
type miniNotify struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	Balloon             uintptr
}
type miniBitmap struct {
	Size                   uint32
	Width, Height          int32
	Planes, Bits           uint16
	Compression, ImageSize uint32
	XPels, YPels           int32
	Colors, Important      uint32
}
type miniAppbar struct {
	Size           uint32
	Window         uintptr
	Callback, Edge uint32
	Rect           miniRect
	Param          uintptr
}

type miniDIB struct {
	dc, bitmap, old, bits uintptr
	w, h                  int
}

func (d *miniDIB) close() {
	if d.dc != 0 {
		miniGDI.NewProc("SelectObject").Call(d.dc, d.old)
	}
	if d.bitmap != 0 {
		miniGDI.NewProc("DeleteObject").Call(d.bitmap)
	}
	if d.dc != 0 {
		miniGDI.NewProc("DeleteDC").Call(d.dc)
	}
	*d = miniDIB{}
}
func (d *miniDIB) resize(w, h int) error {
	if d.w == w && d.h == h && d.bitmap != 0 {
		return nil
	}
	d.close()
	if w < 1 || h < 1 || w > 4096 || h > 1024 {
		return fmt.Errorf("invalid mini-player size")
	}
	d.dc, _, _ = miniGDI.NewProc("CreateCompatibleDC").Call(0)
	bi := miniBitmap{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, Bits: 32}
	d.bitmap, _, _ = miniGDI.NewProc("CreateDIBSection").Call(d.dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&d.bits)), 0, 0)
	if d.dc == 0 || d.bitmap == 0 || d.bits == 0 {
		d.close()
		return fmt.Errorf("cannot allocate mini-player surface")
	}
	d.old, _, _ = miniGDI.NewProc("SelectObject").Call(d.dc, d.bitmap)
	d.w, d.h = w, h
	return nil
}
func (d *miniDIB) upload(pixels []byte) {
	if len(pixels) == d.w*d.h*4 && len(pixels) > 0 {
		miniCopyMemoryProc.Call(d.bits, uintptr(unsafe.Pointer(&pixels[0])), uintptr(len(pixels)))
	}
}
func miniWide(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", ""))
	return p
}
func miniBool(proc string, args ...uintptr) bool {
	r, _, _ := user32.NewProc(proc).Call(args...)
	return r != 0
}
func miniClass(h uintptr) string {
	var b [256]uint16
	user32.NewProc("GetClassNameW").Call(h, uintptr(unsafe.Pointer(&b[0])), 256)
	return windows.UTF16ToString(b[:])
}

// A WT pseudoconsole HWND is not the visible terminal. Locate the owning
// window with a temporary, unique console title, then immediately restore it.
// Never bind an arbitrary foreground window when identification fails.
func miniFindTerminal() (uintptr, uint32) {
	h, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if h != 0 && miniClass(h) == "ConsoleWindowClass" {
		var pid uint32
		user32.NewProc("GetWindowThreadProcessId").Call(h, uintptr(unsafe.Pointer(&pid)))
		return h, pid
	}
	var original [1024]uint16
	kernel32.NewProc("GetConsoleTitleW").Call(uintptr(unsafe.Pointer(&original[0])), 1024)
	marker := fmt.Sprintf("YTM-window-%d-%d", os.Getpid(), time.Now().UnixNano())
	if ok, _, _ := kernel32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(miniWide(marker)))); ok == 0 {
		return 0, 0
	}
	defer kernel32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(&original[0])))
	var found uintptr
	callback := windows.NewCallback(func(w, l uintptr) uintptr {
		if miniClass(w) != "CASCADIA_HOSTING_WINDOW_CLASS" {
			return 1
		}
		var title [1024]uint16
		user32.NewProc("GetWindowTextW").Call(w, uintptr(unsafe.Pointer(&title[0])), 1024)
		if strings.Contains(windows.UTF16ToString(title[:]), marker) {
			found = w
			return 0
		}
		return 1
	})
	for i := 0; i < 8; i++ {
		user32.NewProc("EnumWindows").Call(callback, 0)
		if found != 0 {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	var pid uint32
	if found != 0 {
		user32.NewProc("GetWindowThreadProcessId").Call(found, uintptr(unsafe.Pointer(&pid)))
	}
	return found, pid
}

type miniNative struct {
	owner                                  *miniPlayer
	host, overlay, terminal, icon          uintptr
	terminalPID                            uint32
	instance                               uintptr
	classes                                [2]*uint16
	taskbarCreated                         uint32
	nid                                    miniNotify
	trayAdded, visible                     bool
	closed                                 bool
	lastPolicy                             time.Time
	lastTooltip                            string
	interval                               uintptr
	x, y, dpi, available                   int
	allowAudio, light, show                bool
	audioReason                            string
	surface                                miniSurface
	signal                                 miniSignal
	dib                                    miniDIB
	guard                                  miniAudioGuard
	frames                                 uint64
	clockStarted                           bool
	presentX, presentY, presentW, presentH int
	presentForeground                      uintptr
	minimized, hidden, fullscreen, cloaked bool
	drag                                   miniDrag
	manualPosition, positionDirty          bool
	manualX, manualY                       int
}

// Screen coordinates and the system drag threshold distinguish a click from
// a drag. Crossing the threshold once suppresses click-to-restore on release.
type miniDrag struct {
	pressed, moved         bool
	start, origin          miniPoint
	thresholdX, thresholdY int
}

func (d *miniDrag) begin(point, origin miniPoint, thresholdX, thresholdY int) {
	*d = miniDrag{pressed: true, start: point, origin: origin,
		thresholdX: max(1, thresholdX), thresholdY: max(1, thresholdY)}
}

func (d *miniDrag) move(point miniPoint) (int, int, bool) {
	dx, dy := int(point.X-d.start.X), int(point.Y-d.start.Y)
	if !d.pressed {
		return 0, 0, false
	}
	if dx >= d.thresholdX || dx <= -d.thresholdX || dy >= d.thresholdY || dy <= -d.thresholdY {
		d.moved = true
	}
	return int(d.origin.X) + dx, int(d.origin.Y) + dy, d.moved
}

func miniClampPosition(work miniRect, x, y, width, height int) (int, int) {
	return clamp(x, int(work.Left), max(int(work.Left), int(work.Right)-width)),
		clamp(y, int(work.Top), max(int(work.Top), int(work.Bottom)-height))
}

func startMiniPlayer(cfg MiniConfig, mpvPID uint32) (*miniPlayer, error) {
	p := &miniPlayer{actions: make(chan miniActionMsg, 32), done: make(chan struct{}), mpvPID: mpvPID, input: miniInput{Settings: cfg}, clockRate: make(chan time.Duration, 1), clockStop: make(chan struct{}), clockDone: make(chan struct{})}
	ready := make(chan error, 1)
	go p.runNative(ready)
	if err := <-ready; err != nil {
		<-p.done
		return nil, err
	}
	return p, nil
}
func (p *miniPlayer) Close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		if h := p.hwnd.Load(); h != 0 {
			user32.NewProc("PostMessageW").Call(h, miniShutdownMessage, 0, 0)
		}
	})
	<-p.done
}
func (p *miniPlayer) runNative(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.done)
	if proc := user32.NewProc("SetThreadDpiAwarenessContext"); proc.Find() == nil {
		old, _, _ := proc.Call(^uintptr(3))
		defer proc.Call(old)
	}
	n := &miniNative{owner: p, dpi: 96}
	defer n.cleanup()
	n.terminal, n.terminalPID = miniFindTerminal()
	n.instance, _, _ = kernel32.NewProc("GetModuleHandleW").Call(0)
	n.icon = miniCreateIcon()
	if n.icon == 0 {
		ready <- fmt.Errorf("cannot create YTM icon")
		return
	}
	for i, name := range []string{"YTM.Mini.Host", "YTM.Mini.Overlay"} {
		n.classes[i] = miniWide(name)
		wc := miniWndClass{Proc: miniWindowCallback, Instance: n.instance, Icon: n.icon, SmallIcon: n.icon, Name: uintptr(unsafe.Pointer(n.classes[i]))}
		wc.Size = uint32(unsafe.Sizeof(wc))
		wc.Cursor, _, _ = user32.NewProc("LoadCursorW").Call(0, 32512)
		if r, _, _ := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			ready <- fmt.Errorf("cannot register mini-player window")
			return
		}
	}
	// A separate minimized app window supplies a taskbar button; the layered
	// overlay remains a tool window and never activates on display/click.
	n.host, _, _ = user32.NewProc("CreateWindowExW").Call(0x08040000, uintptr(unsafe.Pointer(n.classes[0])), uintptr(unsafe.Pointer(miniWide("YTM"))), 0x00CA0000, 0, 0, 200, 44, 0, 0, n.instance, 0)
	if n.host == 0 {
		ready <- fmt.Errorf("cannot create YTM taskbar window")
		return
	}
	miniWindows.Store(n.host, n)
	p.hwnd.Store(n.host)
	n.overlay, _, _ = user32.NewProc("CreateWindowExW").Call(0x08080080, uintptr(unsafe.Pointer(n.classes[1])), uintptr(unsafe.Pointer(miniWide("YTM Mini-Player"))), 0x80000000, 0, 0, 200, 44, 0, 0, n.instance, 0)
	if n.overlay == 0 {
		ready <- fmt.Errorf("cannot create mini-player overlay")
		return
	}
	miniWindows.Store(n.overlay, n)
	// AppUserModelID affects this Go process, never the hosting terminal.
	miniShell.NewProc("SetCurrentProcessExplicitAppUserModelID").Call(uintptr(unsafe.Pointer(miniWide("YTM.Player"))))
	user32.NewProc("ShowWindow").Call(n.host, 7) // SW_SHOWMINNOACTIVE
	r, _, _ := user32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(miniWide("TaskbarCreated"))))
	n.taskbarCreated = uint32(r)
	n.nid = miniNotify{Window: n.host, ID: 1, Flags: 1 | 2 | 4 | 0x80, Callback: miniTrayMessage, Icon: n.icon}
	n.nid.Size = uint32(unsafe.Sizeof(n.nid))
	n.setTip("YTM · right-click for controls")
	n.addTray()
	n.guard.init()
	n.setTimer(250)
	n.publishStatus()
	ready <- nil
	var msg miniMessage
	for {
		r, _, _ := miniGetMessageProc.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		miniTranslateProc.Call(uintptr(unsafe.Pointer(&msg)))
		miniDispatchProc.Call(uintptr(unsafe.Pointer(&msg)))
	}
}
func (n *miniNative) cleanup() {
	if n.validTerminal() && !miniBool("IsWindowVisible", n.terminal) {
		user32.NewProc("ShowWindow").Call(n.terminal, 8)
	}
	if n.clockStarted {
		close(n.owner.clockStop)
		<-n.owner.clockDone
	}
	if n.trayAdded {
		miniShell.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&n.nid)))
	}
	n.guard.close()
	n.dib.close()
	for _, h := range []uintptr{n.overlay, n.host} {
		if h != 0 {
			miniWindows.Delete(h)
			user32.NewProc("DestroyWindow").Call(h)
		}
	}
	n.owner.hwnd.Store(0)
	for _, c := range n.classes {
		if c != nil {
			user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(c)), n.instance)
		}
	}
	if n.icon != 0 {
		user32.NewProc("DestroyIcon").Call(n.icon)
	}
}
func (n *miniNative) setTip(text string) {
	n.lastTooltip = text
	clear(n.nid.Tip[:])
	v, _ := windows.UTF16FromString(text)
	copy(n.nid.Tip[:127], v)
	// Never split a surrogate pair at the tray's UTF-16 length boundary.
	if n.nid.Tip[126] >= 0xD800 && n.nid.Tip[126] <= 0xDBFF {
		n.nid.Tip[126] = 0
	}
	if n.trayAdded {
		miniShell.NewProc("Shell_NotifyIconW").Call(1, uintptr(unsafe.Pointer(&n.nid)))
	}
}
func (n *miniNative) addTray() {
	r, _, _ := miniShell.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&n.nid)))
	if r == 0 {
		r, _, _ = miniShell.NewProc("Shell_NotifyIconW").Call(1, uintptr(unsafe.Pointer(&n.nid)))
	}
	n.trayAdded = r != 0
	if n.trayAdded {
		n.nid.Version = 4
		miniShell.NewProc("Shell_NotifyIconW").Call(4, uintptr(unsafe.Pointer(&n.nid)))
	}
}
func (n *miniNative) setTimer(ms uintptr) {
	if n.interval != ms {
		n.interval = ms
		period := time.Duration(ms) * time.Millisecond
		if ms == 33 {
			period = time.Second / 30
		}
		if !n.clockStarted {
			n.clockStarted = true
			go n.owner.runClock(period)
			return
		}
		select {
		case <-n.owner.clockRate:
		default:
		}
		n.owner.clockRate <- period
	}
}
func miniWindowProc(h uintptr, msg uint32, w, l uintptr) uintptr {
	v, ok := miniWindows.Load(h)
	if !ok {
		r, _, _ := user32.NewProc("DefWindowProcW").Call(h, uintptr(msg), w, l)
		return r
	}
	n := v.(*miniNative)
	if msg == n.taskbarCreated && msg != 0 {
		n.trayAdded = false
		n.addTray()
		n.lastPolicy = time.Time{}
		return 0
	}
	switch msg {
	case miniShutdownMessage:
		n.closed = true
		user32.NewProc("PostQuitMessage").Call(0)
		return 0
	case 0x10:
		n.owner.action(miniExit)
		return 0 // WM_CLOSE means quit, not hide
	case 0x113:
		n.owner.tickPending.Store(false)
		n.tick()
		return 0
	case 0x111:
		n.command(w & 0xffff)
		return 0
	case 0x21:
		return 3 // MA_NOACTIVATE
	case 0x5: // A taskbar activation can restore the proxy without SC_RESTORE.
		if h == n.host && w != 1 {
			n.restore()
			user32.NewProc("ShowWindow").Call(n.host, 7)
			return 0
		}
	case 0x201:
		if h == n.overlay {
			point := miniMouseScreen(h, l)
			var rect miniRect
			user32.NewProc("GetWindowRect").Call(h, uintptr(unsafe.Pointer(&rect)))
			tx, ty := miniDragThreshold(n.dpi)
			n.drag.begin(point, miniPoint{rect.Left, rect.Top}, tx, ty)
			user32.NewProc("SetCapture").Call(h)
			n.setTimer(33)
		}
		return 0
	case 0x200: // WM_MOUSEMOVE, including movement outside a captured window.
		if h == n.overlay && n.drag.pressed {
			n.moveDrag(miniMouseScreen(h, l))
		}
		return 0
	case 0x202: // Restore on a click, never at the end of a drag.
		if h == n.overlay && n.drag.pressed {
			n.moveDrag(miniMouseScreen(h, l))
			click := !n.drag.moved
			n.drag.pressed = false
			user32.NewProc("ReleaseCapture").Call()
			if click {
				n.restore()
			} else {
				n.tick()
			}
		}
		return 0
	case 0x215, 0x1f: // Capture lost / WM_CANCELMODE: cancel without restoring.
		if h == n.overlay {
			n.drag.pressed = false
			if msg == 0x1f {
				user32.NewProc("ReleaseCapture").Call()
			}
		}
		return 0
	case 0x205:
		n.menu()
		return 0
	case 0x112:
		if w&0xfff0 == 0xf120 || w&0xfff0 == 0xf030 {
			n.restore()
			return 0
		}
	case miniTrayMessage:
		switch uint32(l) & 0xffff {
		case 0x400, 0x401, 0x203:
			n.restore()
		case 0x7b, 0x205:
			n.menu()
		}
		return 0
	case 0x2e0, 0x7e, 0x1a:
		n.lastPolicy = time.Time{}
		return 0
	}
	r, _, _ := user32.NewProc("DefWindowProcW").Call(h, uintptr(msg), w, l)
	return r
}

func miniMouseScreen(h uintptr, packed uintptr) miniPoint {
	// GET_X/Y_LPARAM semantics: captured mouse coordinates may be negative.
	point := miniPoint{int32(int16(packed & 0xffff)), int32(int16((packed >> 16) & 0xffff))}
	user32.NewProc("ClientToScreen").Call(h, uintptr(unsafe.Pointer(&point)))
	return point
}

func miniDragThreshold(dpi int) (int, int) {
	proc := user32.NewProc("GetSystemMetricsForDpi")
	if proc.Find() == nil {
		x, _, _ := proc.Call(68, uintptr(dpi)) // SM_CXDRAG / SM_CYDRAG
		y, _, _ := proc.Call(69, uintptr(dpi))
		return int(x), int(y)
	}
	x, _, _ := user32.NewProc("GetSystemMetrics").Call(68)
	y, _, _ := user32.NewProc("GetSystemMetrics").Call(69)
	return int(x), int(y)
}

func (n *miniNative) moveDrag(point miniPoint) {
	if x, y, moved := n.drag.move(point); moved {
		n.manualX, n.manualY = x, y
		n.manualPosition, n.positionDirty = true, true
	}
}
func (n *miniNative) validTerminal() bool {
	if n.terminal == 0 || !miniBool("IsWindow", n.terminal) {
		return false
	}
	var pid uint32
	user32.NewProc("GetWindowThreadProcessId").Call(n.terminal, uintptr(unsafe.Pointer(&pid)))
	return pid == n.terminalPID
}
func (n *miniNative) restore() {
	if !n.validTerminal() {
		return
	}
	if miniBool("IsIconic", n.terminal) {
		user32.NewProc("ShowWindowAsync").Call(n.terminal, 9)
	} else {
		user32.NewProc("ShowWindowAsync").Call(n.terminal, 5)
	}
	user32.NewProc("SetForegroundWindow").Call(n.terminal)
	n.lastPolicy = time.Time{}
}
func (n *miniNative) menu() {
	menu, _, _ := user32.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		return
	}
	defer user32.NewProc("DestroyMenu").Call(menu)
	add := func(id uintptr, label string, checked, disabled bool) {
		flags := uintptr(0)
		if checked {
			flags |= 8
		}
		if disabled {
			flags |= 1
		}
		user32.NewProc("AppendMenuW").Call(menu, flags, id, uintptr(unsafe.Pointer(miniWide(label))))
	}
	sep := func() { user32.NewProc("AppendMenuW").Call(menu, 0x800, 0, 0) }
	add(1, "Play / Pause", false, false)
	add(2, "Next", false, false)
	add(3, "Previous", false, false)
	sep()
	known := n.validTerminal()
	add(10, "Show YTM terminal", false, !known)
	add(11, "Hide terminal window (all tabs)", false, !known)
	add(12, "Minimize terminal window", false, !known)
	sep()
	c := n.owner.read().Settings
	add(100, "Mini-player", c.Enabled, false)
	add(101, "Show when minimized / hidden", c.ShowWhenMinimized, false)
	add(102, "Always visible", c.AlwaysVisible, false)
	add(103, "Thumbnail", c.Thumbnail, false)
	add(104, "Visualizer", c.Visualizer, false)
	add(105, "Color: YTM theme (unchecked = automatic)", c.Color == "theme", false)
	sep()
	add(0, "Audio: guarded device loopback", false, true)
	if n.audioReason != "" {
		add(0, n.audioReason, false, true)
	}
	if !known {
		add(0, "Terminal window not identified", false, true)
	}
	sep()
	add(4, "Exit YTM", false, false)
	var point miniPoint
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	user32.NewProc("SetForegroundWindow").Call(n.host)
	id, _, _ := user32.NewProc("TrackPopupMenuEx").Call(menu, 0x102, uintptr(point.X), uintptr(point.Y), n.host, 0)
	user32.NewProc("PostMessageW").Call(n.host, 0, 0, 0)
	n.command(id)
}
func (n *miniNative) command(id uintptr) {
	switch id {
	case 0:
	case 10:
		n.restore()
	case 11:
		if n.validTerminal() {
			user32.NewProc("ShowWindowAsync").Call(n.terminal, 0)
		}
	case 12:
		if n.validTerminal() {
			user32.NewProc("ShowWindowAsync").Call(n.terminal, 6)
		}
	default:
		n.owner.action(miniActionMsg(id))
	}
	n.lastPolicy = time.Time{}
}

func (n *miniNative) tick() {
	defer n.publishStatus()
	if n.closed {
		return
	}
	now := time.Now()
	in := n.owner.read()
	if now.Sub(n.lastPolicy) >= 200*time.Millisecond {
		n.lastPolicy = now
		n.policy(in)
		if !n.trayAdded {
			n.addTray()
		}
	}
	if n.positionDirty {
		n.layout(in)
	}
	if !n.show {
		if n.visible {
			user32.NewProc("ShowWindow").Call(n.overlay, 0)
			n.visible = false
		}
		n.signal = miniSignal{}
		n.setTimer(250)
		return
	}
	g := miniGeometry(n.dpi, n.available, in.Settings, min(96, max(0, in.Count)))
	if g.W == 0 || (!in.Settings.Thumbnail && !in.Settings.Visualizer) {
		user32.NewProc("ShowWindow").Call(n.overlay, 0)
		n.visible = false
		n.setTimer(250)
		return
	}
	changed := n.surface.prepare(g, in, n.light)
	moving := n.signal.step(in, g.Count, now, n.allowAudio)
	if n.dib.resize(g.W, g.H) != nil {
		n.show = false
		n.setTimer(250)
		return
	}
	if changed || moving || !n.visible {
		n.surface.paint(&n.signal)
		n.dib.upload(n.surface.Pixels)
		pos, sz, src := miniPoint{int32(n.x), int32(n.y)}, miniPoint{int32(g.W), int32(g.H)}, miniPoint{}
		blend := [4]byte{0, 0, 255, 1}
		r, _, _ := miniUpdateLayerProc.Call(n.overlay, 0, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&sz)), n.dib.dc, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), 2)
		if r == 0 {
			user32.NewProc("ShowWindow").Call(n.overlay, 0)
			n.visible = false
			n.setTimer(250)
			return
		}
		n.frames++
	}
	foreground, _, _ := user32.NewProc("GetForegroundWindow").Call()
	if !n.visible || n.presentX != n.x || n.presentY != n.y || n.presentW != g.W || n.presentH != g.H || n.presentForeground != foreground {
		miniSetPositionProc.Call(n.overlay, ^uintptr(0), uintptr(n.x), uintptr(n.y), uintptr(g.W), uintptr(g.H), 0x10|0x40)
		n.presentX, n.presentY, n.presentW, n.presentH = n.x, n.y, g.W, g.H
		n.presentForeground = foreground
	}
	n.visible = true
	if moving || in.Active || n.drag.pressed {
		n.setTimer(33)
	} else {
		n.setTimer(250)
	}
}
func (n *miniNative) publishStatus() {
	peak := 0.
	for _, v := range n.signal.Levels {
		if v > peak {
			peak = v
		}
	}
	n.owner.mu.Lock()
	n.owner.status = miniStatus{Minimized: n.minimized, Hidden: n.hidden, Fullscreen: n.fullscreen, Cloaked: n.cloaked, ConflictingPID: n.guard.conflictPID, Host: n.host, Overlay: n.overlay, Terminal: n.terminal, Visible: n.visible,
		AudioAllowed: n.allowAudio, TrayAdded: n.trayAdded, AudioReason: n.audioReason, Frames: n.frames,
		Width: n.surface.Layout.W, Height: n.surface.Layout.H, Count: n.signal.Count, Peak: peak}
	n.owner.mu.Unlock()
}
func (n *miniNative) policy(in miniInput) {
	n.layout(in)
	n.light = false
	if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE); err == nil {
		v, _, e := key.GetIntegerValue("SystemUsesLightTheme")
		n.light = e == nil && v != 0
		key.Close()
	}
	n.allowAudio = false
	n.audioReason = "Paused / stopped"
	if n.show && in.Active && in.Settings.Visualizer {
		n.allowAudio, n.audioReason = n.guard.allow(n.owner.mpvPID, n.owner.capturePID.Load())
	}
	text := "YTM · " + in.Title
	if n.audioReason != "" {
		text += " · " + n.audioReason
	}
	if !n.validTerminal() {
		text += " · terminal not identified"
	}
	if text != n.lastTooltip {
		n.setTip(text)
	}
}

func (n *miniNative) layout(in miniInput) {
	n.positionDirty = false
	known := n.validTerminal()
	anchor := n.terminal
	if !known {
		anchor = 0
	}
	mon, _, _ := user32.NewProc("MonitorFromWindow").Call(anchor, 2)
	if n.manualPosition {
		r := miniRect{int32(n.manualX), int32(n.manualY),
			int32(n.manualX + max(1, n.surface.Layout.W)), int32(n.manualY + max(1, n.surface.Layout.H))}
		mon, _, _ = user32.NewProc("MonitorFromRect").Call(uintptr(unsafe.Pointer(&r)), 2)
	}
	info := miniMonitor{}
	info.Size = uint32(unsafe.Sizeof(info))
	if !miniBool("GetMonitorInfoW", mon, uintptr(unsafe.Pointer(&info))) {
		n.show = false
		return
	}
	dpi := uint32(96)
	var dy uint32
	if p := miniShcore.NewProc("GetDpiForMonitor"); p.Find() == nil {
		p.Call(mon, 0, uintptr(unsafe.Pointer(&dpi)), uintptr(unsafe.Pointer(&dy)))
	}
	n.dpi = clamp(int(dpi), 96, 480)
	var cloaked uint32
	if known {
		miniDWM.NewProc("DwmGetWindowAttribute").Call(n.terminal, 14, uintptr(unsafe.Pointer(&cloaked)), 4)
	}
	fullscreen := miniFullscreen(mon, info.Monitor, n.host, n.overlay)
	n.minimized, n.hidden, n.fullscreen, n.cloaked = known && miniBool("IsIconic", n.terminal), known && !miniBool("IsWindowVisible", n.terminal), fullscreen, cloaked != 0
	n.show = miniShouldShow(in.Settings, known, n.minimized, n.hidden, fullscreen, cloaked != 0)
	work := info.Work
	// Auto-hide bars reserve no rcWork area. Leave room for their reveal strip
	// and their expanded controls, using documented per-monitor appbar lookup.
	for edge := uint32(0); edge < 4; edge++ {
		data := miniAppbar{Edge: edge, Rect: info.Monitor}
		data.Size = uint32(unsafe.Sizeof(data))
		h, _, _ := miniShell.NewProc("SHAppBarMessage").Call(0xb, uintptr(unsafe.Pointer(&data)))
		if h == 0 {
			continue
		}
		reserve := int32(48 * n.dpi / 96)
		switch edge {
		case 0:
			work.Left = max(work.Left, info.Monitor.Left+reserve)
		case 1:
			work.Top = max(work.Top, info.Monitor.Top+reserve)
		case 2:
			work.Right = min(work.Right, info.Monitor.Right-reserve)
		case 3:
			work.Bottom = min(work.Bottom, info.Monitor.Bottom-reserve)
		}
	}
	pad := 8 * n.dpi / 96
	n.available = max(0, int(work.Right-work.Left)-2*pad)
	g := miniGeometry(n.dpi, n.available, in.Settings, 96)
	n.x, n.y = miniPosition(info.Monitor, work, g.W, g.H, pad)
	if n.manualPosition {
		n.x, n.y = miniClampPosition(work, n.manualX, n.manualY, g.W, g.H)
		if !n.drag.pressed {
			n.manualX, n.manualY = n.x, n.y
		}
	}
	if int(work.Bottom-work.Top) < g.H+2*pad {
		n.show = false
	}
	// Stack companions on the same monitor in stable HWND order.
	for pass := 0; pass < 8; pass++ {
		moved := false
		var after uintptr
		for count := 0; count < 64; count++ {
			h, _, _ := user32.NewProc("FindWindowExW").Call(0, after, uintptr(unsafe.Pointer(n.classes[1])), 0)
			if h == 0 {
				break
			}
			after = h
			if h >= n.overlay || !miniBool("IsWindowVisible", h) {
				continue
			}
			var r miniRect
			user32.NewProc("GetWindowRect").Call(h, uintptr(unsafe.Pointer(&r)))
			if n.x < int(r.Right) && n.x+g.W > int(r.Left) && n.y < int(r.Bottom) && n.y+g.H > int(r.Top) {
				n.y = int(r.Top) - g.H - pad
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	if n.y < int(work.Top) {
		n.show = false
	}
}
func miniPosition(monitor, work miniRect, w, h, pad int) (int, int) {
	x, y := int(work.Right)-w-pad, int(work.Bottom)-h-pad
	if work.Left > monitor.Left {
		x = int(work.Left) + pad
	}
	if work.Top > monitor.Top {
		y = int(work.Top) + pad
	}
	x = max(int(work.Left), min(x, int(work.Right)-w))
	y = max(int(work.Top), min(y, int(work.Bottom)-h))
	return x, y
}
func miniFullscreen(mon uintptr, bounds miniRect, host, overlay uintptr) bool {
	var state uint32
	// QUNS_BUSY (2) is a notification policy, not proof that the foreground
	// window is fullscreen. Check its actual frame below. Keep exclusive D3D
	// and explicit presentation mode suppression.
	if r, _, _ := miniShell.NewProc("SHQueryUserNotificationState").Call(uintptr(unsafe.Pointer(&state))); int32(r) >= 0 && miniExclusivePresentation(state) {
		return true
	}
	f, _, _ := user32.NewProc("GetForegroundWindow").Call()
	if f == 0 || f == host || f == overlay {
		return false
	}
	switch miniClass(f) {
	case "Progman", "WorkerW", "Shell_TrayWnd", "Shell_SecondaryTrayWnd":
		return false
	}
	fm, _, _ := user32.NewProc("MonitorFromWindow").Call(f, 2)
	if fm != mon {
		return false
	}
	var rect miniRect
	r, _, _ := miniDWM.NewProc("DwmGetWindowAttribute").Call(f, 9, uintptr(unsafe.Pointer(&rect)), unsafe.Sizeof(rect))
	if int32(r) < 0 || rect.Right <= rect.Left || rect.Bottom <= rect.Top {
		if !miniBool("GetWindowRect", f, uintptr(unsafe.Pointer(&rect))) {
			return false
		}
	}
	style, _, _ := user32.NewProc("GetWindowLongPtrW").Call(f, ^uintptr(15)) // GWL_STYLE
	return miniFrameFullscreen(bounds, rect, style)
}

func miniExclusivePresentation(state uint32) bool {
	return state == 3 || state == 4
}

func miniFrameFullscreen(bounds, frame miniRect, style uintptr) bool {
	// A normal maximized Chromium window can extend past monitor bounds due
	// to invisible resize borders, or fill them with taskbar auto-hide enabled.
	// Its caption identifies a regular window, unlike F11/video borderless mode.
	if style&0x00c00000 == 0x00c00000 { // WS_CAPTION
		return false
	}
	return frame.Left <= bounds.Left && frame.Top <= bounds.Top && frame.Right >= bounds.Right && frame.Bottom >= bounds.Bottom
}
func miniCreateIcon() uintptr {
	var d miniDIB
	if d.resize(32, 32) != nil {
		return 0
	}
	defer d.close()
	pix := make([]byte, 32*32*4)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			putMiniPixel(pix, 32, x, y, color.NRGBA{16, 32, 43, 255}, roundCoverage(float64(x)+.5, float64(y)+.5, 0, 0, 32, 32, 7))
		}
	}
	for i, h := range []int{9, 17, 23, 13} {
		for y := 27 - h; y < 27; y++ {
			for x := 5 + i*6; x < 8+i*6; x++ {
				putMiniPixel(pix, 32, x, y, color.NRGBA{79, 218, 233, 255}, 1)
			}
		}
	}
	d.upload(pix)
	mask, _, _ := miniGDI.NewProc("CreateBitmap").Call(32, 32, 1, 1, 0)
	if mask == 0 {
		return 0
	}
	defer miniGDI.NewProc("DeleteObject").Call(mask)
	ii := struct {
		Icon, X, Y  uint32
		Mask, Color uintptr
	}{Icon: 1, Mask: mask, Color: d.bitmap}
	icon, _, _ := user32.NewProc("CreateIconIndirect").Call(uintptr(unsafe.Pointer(&ii)))
	return icon
}
