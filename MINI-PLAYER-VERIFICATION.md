# Mini-player verification — 2026-10-17

Local checkpoint before implementation: `checkpoint-20261017` at `d7b3c08`.
No new runtime dependencies were added.

## Executed checks

- `go test -buildvcs=false ./...`: passed.
- `go vet ./...`: passed.
- Native Windows integration with actual mpv/WASAPI and CAVA: passed.
  Real 90 Hz, 1 kHz and 8 kHz PCM fixtures peaked at CAVA bins 7, 49 and 84.
- Real YouTube playback through the existing application loader: Daft Punk,
  Laufey, Adele and Satie recordings; artwork changes and tray Next/Previous
  passed while search/help retained their input focus.
- Pause/resume, silence decay, minimize/restore, hide/restore, Always Visible,
  fullscreen suppression, tray settings persistence and native window cleanup
  passed. Foreign playback suppressed the shared-loopback spectrum, then it
  recovered when the conflicting playback stopped.
- TaskbarCreated message replay passed. This is not a real Explorer restart.
- Final native timing run: 789 bitmap uploads over 30 seconds, about 26.3/s,
  against a 30 Hz animation target. Whole test process CPU time was 1.906 s;
  Go heap changed from 580,104 to 576,616 bytes and GDI objects stayed at 14.
  This short sample is not an hours-long memory or performance soak.
- Pure compact-renderer benchmark: approximately 85 microseconds/frame,
  zero allocations/frame on this Ryzen 3 3200U.
- Normal application EXE, isolated profile and actual YouTube URL: passed.
  The application's own mpv IPC reported WASAPI output and advancing time
  (2.263 to 3.462 seconds). Artwork was loaded, 96 real CAVA bands received,
  and desktop screenshots confirmed both the actual thumbnail and changing
  spectrum. Minimize/restore and tray Exit completed; application exit code 0.

Local evidence is under ignored `dist/mini-verification`,
`dist/mini-clock-verification` and `dist/mini-app-check-final`.
An earlier `dist/mini-app-check` run ended before playback readiness and must
not be used as proof of successful audio/artwork verification.

The explicitly authorized test-only browser mute lease restored the original
mute state of its one browser session. No browser muting is included in YTM.

## Remaining manual checks and limitations

Physical monitor hot-plug, live cross-monitor DPI changes, real Explorer restart,
taskbar auto-hide transitions on different Windows builds, and prolonged playback
have not been exercised. Unit tests cover DPI/negative coordinates/work areas,
but do not replace those desktop scenarios.

CAVA remains device-wide loopback. Core Audio session checks suppress known
conflicting output; polling cannot guarantee process-isolated capture or prevent
every short mixed transient. Unknown session/device state suppresses the mini
spectrum rather than inventing data. See [MINI-PLAYER.md](MINI-PLAYER.md).

Windows Terminal restore operates on the containing window, not a specific
tab/pane. The companion is a supported overlay adjacent to the taskbar, not an
embedded taskbar widget. Pixel alpha is real; acrylic blur is not implemented.
