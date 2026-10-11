# Visualizer presets

Open YTM, press **F4**, use **Up/Down, Left/Right or J/K** to preview an effect,
then **Enter, Esc or F4** to close. Every selection is applied immediately and
saved to `%APPDATA%\ytm\config.toml` as `visualizer_effect`. Esc closes the
picker; it does not undo the selection. Save failures appear in the picker.
F4 is inactive during search/name editing, transfer dialogs and keyboard help.
No advanced controls or extra dependencies are required.

| Preset | Appearance |
| --- | --- |
| Glass Bars (default) | Solid 1-cell cyan bars, fractional block tips, soft-blue gradient |
| Classic Bars | Previous rainbow block spectrum |
| Soft Fade Bars | Finer blue-white strokes with a deeper fade |
| Neon Bars | Brighter mint/cyan strokes, quick attack |
| Mist Bars | Continuous silver/blue strokes with gentle decay; no dots or particles |

Each preset selects its palette, attack and decay. All presets use exactly
1-cell bars and 1-cell gaps. The complete spectrum is centered using integer
cell positions, with spare space only at the outer margins.
Bar count is bounded by terminal width and available real CAVA frequency bands
(maximum 96). Bars spread across the existing region with a minimum cell gap;
smaller panes aggregate frequencies using 65% RMS + 35% peak rather than an
arithmetic mean, preserving tonal energy. Heights use the existing responsive
layout. Gain adapts slowly to measured input, is limited to 1–3x, and releases
quickly on louder audio. Near-silent input is gated; large peaks use a soft knee.
There are no floating peak particles or synthetic
color pulses. Temporal smoothing uses elapsed time, independent of display FPS.
V retains the existing normal/mirror mode toggle. Playback and artwork layout
are unchanged. Legacy configs without `visualizer_effect` use Glass Bars;
unknown names fall back to Glass Bars. Select Classic for the previous look.

Glass/Fade/Neon/Mist render foreground-only Unicode block glyphs. The background
remains untouched; no inverse-video or opaque rectangle is used. This is a
glass-like color treatment, **not true alpha transparency or a blur effect**.
Windows Terminal's own background opacity/acrylic supplies desktop transparency.
True color is detected via WT_SESSION/COLORTERM, otherwise ANSI cyan/white is
used. NO_COLOR removes colors. TERM=dumb outside Windows Terminal uses ASCII
strokes. Use a monospace font with box-drawing glyphs (e.g. Cascadia Mono).

Windows Terminal: Ctrl+, → your profile → Appearance → Background opacity
(try 80%) and Enable acrylic material. YTM does not modify these settings.
https://learn.microsoft.com/en-us/windows/terminal/customize-settings/profile-appearance

The existing CAVA/WASAPI source is **device-wide loopback**, not isolated mpv
audio. Other applications on that device can contribute while YTM plays. This
limitation is shown in the picker. Pause/stop and stale frames decay to silence.

Validation covers config migration/restart, foreground-only rendering, bar
continuity, mirror symmetry, narrow/wide bounds, popup resize/key isolation,
and elapsed-time smoothing. The optional TestExportBarPreview exports actual
renderer output for a labelled fixed-envelope visual fixture; it is not proof
of live audio playback or a screenshot of Windows Terminal. The requested
reference screenshots were not available for direct comparison.

Latest raw CAVA measurements are written once per second to
`%LOCALAPPDATA%\ytm\logs\spectrum-metrics.json`: band count, peak, RMS,
active percentage (above 0.015), and eight low-to-high logarithmic band groups.
These values describe CAVA's normalized output, not calibrated audio dBFS.
Set `YTM_SPECTRUM_TRACE=1` before starting YTM to capture at most 240 frames in
`spectrum-frames.jsonl` in the same folder. Normal operation does not record frames.
The committed `testdata/cava-live-spectrum.json` contains measured WASAPI/CAVA
frames from the real-audio review, with no audio waveform or personal metadata.
