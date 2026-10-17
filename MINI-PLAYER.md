# Windows mini-player

Run `ytm` normally. A YTM taskbar button and notification-area icon accompany
the terminal. Windows may initially put the tray icon in its overflow menu.
Minimize the terminal to reveal the companion; clicking the companion or YTM
taskbar button restores the identified terminal window. Right-click either the
tray icon or companion for Play/Pause, Next, Previous, Show, Hide, Minimize,
settings and Exit. Keyboard shortcuts inside YTM are unchanged.

Hold the left mouse button and drag the mini-player to reposition it. A short
click still restores YTM; a drag does not restore or activate the terminal.
The chosen position is retained for the current YTM session, including hiding
and showing the companion, and stays within the monitor work area. On restart
the default taskbar-adjacent placement is used.

Hide and Minimize leave audio running. Exit and the terminal's normal Q command
stop the application and clean up the companion. Closing the actual terminal
window remains termination, not hide-to-tray. Hide explicitly affects the entire
terminal window, including its other tabs and panes.

## Settings

Tray menu settings apply immediately and persist in `%APPDATA%\ytm\config.toml`.
Older config files retain their existing values and use these new defaults:

```toml
[mini_player]
enabled = true
show_when_minimized = true
always_visible = false
thumbnail = true
visualizer = true
color = "automatic"
```

`automatic` selects contrast for the Windows taskbar theme. `theme` uses the
YTM cyan/violet accent. Transparency, count, spacing, gain and animation quality
are automatic. Disabling Mini-player hides the overlay while retaining the tray
controls, so it can be re-enabled. Disabling both artwork and spectrum also
hides the empty overlay. Fullscreen browsers/video do not hide the companion.
Always Visible still yields to the host window being on another virtual desktop.

## Architecture

The companion is native Win32 inside the existing Go executable. It adds no
runtime dependency or helper process. A separate OS thread owns the message
loop, tray icon, taskbar proxy, layered tool window, GDI bitmap and Core Audio
session inspection. A bounded channel sends commands into the existing player
state manager and mpv command worker. Commands are not injected as keyboard
input, so an open search editor or help popup does not consume them.

The existing decoded artwork is shared as an immutable image and cached after
resizing for the current monitor. It is contained within a rounded 36-DIP area,
without stretching or cropping. The companion normally measures 200 x 44 DIP,
with 2–3 DIP bars and consistent integer pixel spacing. It uses a premultiplied
BGRA bitmap with `UpdateLayeredWindow(ULW_ALPHA)`: transparency is genuine pixel
alpha. No unsupported acrylic API, Explorer injection, shell modification or
administrator privilege is used. The mini-player does not change Windows
Terminal transparency settings.

Real CAVA frames are copied into a fixed-size latest-frame mailbox, without a
second capture or decoder. A separate compact display stage aggregates real
frequency bins using the existing RMS/peak method, applies small neighbour
weights, bounded adaptive gain, elapsed-time attack/release and peak decay.
Silence and stale frames decay to zero. Only the companion's visuals change;
terminal spectrum processing is unchanged. Active animation targets 30 FPS.
Hidden companions suspend rendering and poll shell state four times per second.
Settled paused output does not continuously upload bitmaps.

## Audio-source limitation

The installed Windows CAVA backend captures **device-wide WASAPI loopback**,
not mpv-only PCM. The companion reuses that stream. It checks the default render
device's Core Audio sessions against this instance's real mpv process ID (queried
over IPC, including when launched by a Scoop shim), ignores its own capture-only
CAVA session, and suppresses
its spectrum when another audible active session, an unknown session, different default
audio roles or a device switch is detected. The reason is visible in the tray
tooltip/menu. Foreign sessions are checked for mute, volume and peak level so a
paused browser with a silent active AudioContext need not disable the spectrum.
Sessions without a usable meter are conservatively treated as unverified. A
500 ms hold after foreign audio ends lets the mixed FFT tail settle.
After changing the default output device, restart YTM to reconnect CAVA.

This guard is **not process-isolated capture**: session enumeration and the
200–250 ms policy check can lag a new short sound. Brief mixed samples are still
possible. When two YTM instances play simultaneously the mini spectra suspend
rather than intentionally drawing each other's audio. Their metadata, command
queues and artwork remain separate. The terminal's existing visualizer retains
its original device-loopback behavior.

Guaranteeing isolation while other apps play requires a shared per-process
capture/FFT replacement (Microsoft's process-loopback API requires newer Windows
builds), or a PCM tap in mpv. That is not silently claimed by this implementation.

## Shell limitations and safeguards

- The overlay is placed **beside** the taskbar, within the monitor work area;
  it is not embedded inside the taskbar. It avoids taskbar buttons and reserves
  space for an auto-hidden taskbar to reveal itself.
- Per-monitor DPI, negative monitor coordinates, work-area changes and display
  changes are handled. Multiple companions stack when they would overlap.
- Normal, maximized and fullscreen browsers/video keep the companion visible
  when its visibility settings apply. Entering fullscreen or switching apps
  refreshes topmost placement without activating it. Exclusive fullscreen games
  or protected surfaces may bypass desktop composition and cannot be guaranteed
  to display an overlay; no game injection or shell modification is used.
- `TaskbarCreated` re-registers the tray icon after Explorer restarts, with
  retry when Explorer is not ready. Windows controls tray overflow placement.
- A classic console is identified directly. Windows Terminal uses a pseudoconsole,
  so YTM briefly sets a unique console title, locates the matching terminal
  window and restores the original title. Custom titles that suppress application
  titles, an inactive tab, remote sessions or unusual terminal hosts can prevent
  identification. YTM does not guess an unrelated foreground window. In that
  case tray transport and Always Visible still work, and Show/Hide are disabled.
- Restoring a window cannot select a particular Windows Terminal tab or pane
  through a supported Win32 API. Keep YTM in a dedicated window for predictable
  minimize/hide behavior. Closing that host terminates the session.
- The app creates a live taskbar icon; it does not install a permanent pinned
  shortcut. A companion initialization failure leaves the terminal player usable.

## Build and verification

No new dependencies are required. The existing Windows build/release workflow
includes the new Go files; the release ZIP includes this document.

```powershell
gofmt -w mini_*.go main.go ui.go
go test -buildvcs=false ./...
go vet ./...
go build -trimpath -buildvcs=false -o dist/ytm-mini.exe .
./package.ps1 -OutputDirectory ./dist/mini-release
```

Pure tests cover geometry at 96–480 DPI, narrow work areas, off-screen monitors,
visibility, alpha/letterboxing/bar gaps, frequency distribution, silence,
stale/mixed input gating, frame-rate-independent dynamics and old config files.

Opt-in integration tests create real windows/tray icons and play sound. Run in a
dedicated Windows Terminal window with other audio apps paused:

```powershell
$env:YTM_MINI_NATIVE_TEST = '1'
$env:YTM_MINI_MUSIC_TEST = '1' # also searches/streams four real YouTube recordings
$env:YTM_MINI_TEST_OUTPUT = "$PWD/dist/mini-verification"
go test -buildvcs=false -run TestMiniNativePlayback -v -timeout 5m
```

The integration suite uses actual mpv playback, CAVA FFT output, native window
messages and desktop captures. PCM tone fixtures verify low/mid/high ordering;
they are not fake visualizer frames. The music matrix samples electronic,
acoustic/vocal, vocal/piano and quiet piano recordings. TaskbarCreated replay
tests re-registration without killing the user's Explorer process. Real Explorer
restart, physical monitor hot-plug/DPI changes and hours-long playback need a
separate manual run; unit geometry checks do not prove those scenarios.

## Windows API references

- [Taskbar and notification-area lifecycle](https://learn.microsoft.com/en-us/windows/win32/shell/taskbar)
- [GetConsoleWindow and pseudoconsole limitations](https://learn.microsoft.com/en-us/windows/console/getconsolewindow)
- [UpdateLayeredWindow and per-pixel alpha](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-updatelayeredwindow)
- [Notification/fullscreen state](https://learn.microsoft.com/en-us/windows/win32/api/shellapi/nf-shellapi-shqueryusernotificationstate)
- [Audio session process identity](https://learn.microsoft.com/en-us/windows/win32/api/audiopolicy/nf-audiopolicy-iaudiosessioncontrol2-getprocessid)
- [Process loopback capture sample](https://learn.microsoft.com/en-us/samples/microsoft/windows-classic-samples/applicationloopbackaudio-sample/)
