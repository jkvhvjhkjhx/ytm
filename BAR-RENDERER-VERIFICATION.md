# Glass Bars correction

Checkpoint: `63d8830`.

## Findings and changes

The previous renderer drew one narrow line glyph in a cell regardless of pane
width. It also averaged bands after two spatial smoothing stages, reducing
narrow peaks. Its dark tips and whole-cell height steps compounded the flat look.

The renderer now uses 2-cell blocks (3 cells at widths of at least 100) and a
minimum 1-cell gap. Normal mode uses eight partial-height block levels. Mirror
uses symmetric foreground half-block tips; no inverse video/background fill.
The cyan/soft-blue gradient remains visible at the tip. Width adaptation covers
the entire existing spectrum region without changing any panel geometry.

Aggregation comparison for a six-band group `[0,0,0.8,0,0,0]`:

| Method | Output |
| --- | --- |
| Arithmetic mean | 0.133 |
| RMS | 0.327 |
| Maximum | 0.800 |
| Selected: 65% RMS + 35% maximum | 0.492 |

On the low-level noise fixture the selected output is only 0.007. Spatial
smoothing is applied once, with 0.84 center / 0.08 neighbours. Glass no longer
gets the unconditional 0.88 attenuation. A 1–3x adaptive gain uses measured peak
and RMS, rises over 0.9 s and falls over 0.12 s. A near-silence gate and soft knee
prevent noise pumping and clipping. Attack/decay remain time-based. CAVA's
noise_reduction=45 is retained: the real capture supplied strong, populated
spectra and did not justify blindly lowering its noise suppression.

## Verification

- Real YouTube search, 1280x720 thumbnail, mpv WASAPI playback, advancing time,
  CAVA peak 1.00, pause/resume/seek/volume, playlists and restart passed the
  existing `--self-check` with Daft Punk - Instant Crush.
- A separate Windows Terminal test launch captured 96-band real CAVA frames.
  One measured frame: RMS 0.269, peak 1.00, active bands 90.6%. Low-to-high
  eight-group RMS: 0.459, 0.401, 0.313, 0.160, 0.276, 0.077, 0.019, 0.022.
- 76 active captured frames are committed in `testdata/cava-live-spectrum.json`.
  Tests feed these through actual processing and rendering at 33x3, 50x8,
  80x12, 120x14 and 160x12. Fixture previews are renderer rasterizations, not
  Windows Terminal screenshots.
- Added assertions for 2–3-cell width, at least 60% horizontal bar coverage on
  a broadband spectrum, fractional tips, preserved narrow peaks, zero silence,
  weak-signal visibility, real-frame visibility, bounds and mirror symmetry.
- Existing configuration, F4 isolation/persistence, resize, playback and
  playlist tests remain in the full test suite.

## Outstanding visual comparison

The screenshot tool could not obtain the visible YTM test window: it found a
hidden Terminal helper instead. That capture was discarded. No reference images
were attached in the available conversation. Therefore a direct Windows Terminal
screenshot-to-reference comparison is **not verified**. The inspected preview
uses the real renderer and captured audio data but does not establish font/acrylic
appearance on the user's terminal.

CAVA remains device-wide WASAPI loopback; other applications may contribute.
The raw measurements are normalized CAVA levels, not calibrated dBFS or isolated
mpv frequency measurements. Transparent gaps preserve emulator acrylic, but block
foreground pixels do not have actual alpha transparency.
