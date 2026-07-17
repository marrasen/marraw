# marraw

Fast RAW photo culling and editing. Built for photographers who shoot a lot,
keep a little, and want the boring part to be over quickly.

marraw is a desktop app: a Go daemon that talks to LibRaw does the pixel work,
a React front-end does the rest, and Electron holds it together.

![Cull mode: a full-bleed photo of a Spitfire on an airfield, a pick/reject bar below it, and a filmstrip deck along the bottom broken into time-gap groups](screenshots/marraw_cull.jpg)

<sup>Cull mode. The scrubber deck has already broken 191 frames into 31 groups —
note the `+6 min gap`, `+3 min gap` dividers between runs.</sup>

> **Status: early.**
> marraw is usable daily, and the scope has been growing fast — smart culling
> and AI-masked local editing are in. The gaps that remain are real ones,
> though: read [What marraw does *not* do](#what-marraw-does-not-do) before
> you invest time in it. Windows is the primary platform; the macOS and Linux
> builds are newer and less travelled — issue reports are very welcome.

---

## Install

Grab your platform's package from the
[latest release](https://github.com/marrasen/marraw/releases/latest):

- **Windows** — `marraw-Setup-<version>.exe`. Not code-signed yet, so
  SmartScreen will warn you once — **More info → Run anyway**.
- **Linux** — `marraw-<version>-x86_64.AppImage` (auto-updating) or the
  `.deb` package.
- **macOS** — `marraw-<version>-arm64.dmg` (Apple Silicon). Unsigned, so
  first launch needs right-click → **Open** (or "Open Anyway" under
  Privacy & Security on macOS 15+); auto-update is not available on macOS.

On Windows and Linux (AppImage) the app updates itself: it checks GitHub on
launch, downloads new versions in the background, and installs them when you
quit.

**Requirements:** Windows 10/11 64-bit, a recent x86-64 Linux distro, or an
Apple Silicon Mac. No LibRaw DLLs, no runtime, no Python. One installer.

---

## What makes it different

**It is fast, and it stays fast at 1:1.**
Grid thumbnails come straight from the RAW's embedded JPEG — no decode, so a
folder of 1,500 frames is browsable immediately. Every photo is cached as a
pyramid of JPEG renditions (256 → 2048 px). Zoom past 2048 and marraw switches
to full-resolution tiles, downloading and decoding only the crop you are
actually looking at, over an upscaled underlay, while the *next* photo's tiles
pre-render in the background. Comparing a burst at 100% doesn't stutter.

**It knows a shoot is made of bursts, not files.**
Point marraw at a folder and it reads the capture timestamps, then breaks the
grid wherever you stopped shooting for longer than a threshold you pick. Each
run of frames gets a header with its time range, its frame count, and — the
useful part — *how long the dead time before it was*: `+42 min gap before`.
A wedding, a match, a hike down a trail all arrive pre-segmented into the
moments you actually shot, without stacks to expand, albums to build, or a
single click. The Cull contact sheet (`G`) shows the same groups as sections,
so you can see a whole day's structure at once.

![The contact sheet: a dark grid of aerial photos under a header reading 10:04 – 10:06, 46 frames, with a +2 min gap before badge on the right](screenshots/marraw_contact_sheet.jpg)

<sup>The contact sheet (`G`). Each section header carries its time range, its
frame count, and how long the dead time before it was.</sup>

**The machine does the pixel-peeping first.**
Within those groups, near-duplicate frames collapse into burst stacks with the
sharpest frame on top: an auto-judge weighs sharpness and subject focus, and
`⇧P`/`⇧X` pick or reject around a stack's best frame. Folder-wide analysis
scores every photo for sharpness and subject focus — a **Soft** filter sweeps
the out-of-focus rejects in one pass — and closed-eye detection badges the
blinks, with a **Blinks** filter that narrows the grid to just those frames.
All of it runs on your machine: the models download only after you say yes,
nothing leaves your computer, and Settings shows the downloaded weights and
lets you delete them.

**Culling is keyboard-first, and the loupe remembers where you were.**
Arrows navigate, `1`–`5` rate, `P` picks, `X` excludes, `Enter` goes deeper.
Zoom and pan position persist across arrow navigation, so stepping through a
burst series compares the *same* crop of every frame — which is the whole point
of pixel-peeping a burst.

**RAWs don't look flat out of the box.**
LibRaw's default output is noticeably duller than your camera's JPEG, because
manufacturer tone curves are proprietary and adaptive (Sony DRO, and friends).
On a photo's first render marraw measures the camera's own embedded JPEG,
calibrates a per-photo tone lift to match its mean luminance, and applies that
consistently everywhere — previews, edits, exports. You start from something
that looks like what you saw on the back of the camera.

**Editing is non-destructive and responsive.**
Drag a slider and the backend re-processes the already-unpacked RAW handle at
half size (~400 ms warm on 42 MP files); transient drags skip re-demosaicing
entirely. Every photo carries its own undo/redo stack and a clickable history
timeline.

**It does not hold your photos hostage.**
Edits, ratings and flags are written to a `.marraw.json` sidecar next to each
RAW (toggleable). Delete a photo and it goes to the Recycle Bin, not a void.
There is no catalog to import into and nothing to migrate out of.

**Batch work is first-class.**
Select ten frames and apply *relative* adjustments — "+0.5 EV on all of these"
— rather than stamping one absolute value over ten different exposures. Copy
and paste edit settings with `Ctrl+C`/`Ctrl+V`. `Ctrl+U` auto-tones — metering
the detected subject when one exists, not just the frame average — and
auto-crop frames that subject; `Ctrl+1`–`9` apply your own saved creative
auto-presets, `Ctrl+⇧+1`–`9` your saved presets by position, and any look can
be saved as a named preset and applied from the Presets tab (the photo keeps
its own crop).

**`Ctrl+K` jumps to anything** — any mode, any panel, any single develop
control, any preset.

![A command palette floating over a dimmed Develop window, prompting Jump to any mode, control, or action, listing Go to Library, Go to Cull, Go to Develop, Contact sheet, Export, Add folder to library, Settings, Keyboard shortcuts](screenshots/marraw_jump.jpg)

---

## The workspace

| Mode | For |
| --- | --- |
| **Library** | Virtualized grid, adjustable thumbnail size, time-gap grouping, multi-select, rating/flag badges. |
| **Cull** | Full-bleed cinema loupe, scrubber deck, pick/reject bar, burst stacks with best-frame auto-judge, sharpness/blink badges, contact sheet (`G`). |
| **Develop** | Darkroom canvas, pinnable panel, floating quick-dials you choose, crop and white-balance overlays. |
| **Export** | JPEG, lossless TIFF / PNG, or RAW + XMP handoff — batched across every core. |

![Develop mode: the photo on a darkroom canvas, a floating quick-dial strip beneath it, and a right-hand panel with an RGB histogram over crop, tone, presence and white-balance sliders](screenshots/marraw_develop.jpg)

<sup>Develop mode. The panel is pinned open; the quick-dials under the canvas
(exposure, shadows, vibrance) are the three controls you chose to keep at hand.</sup>

### Editing tools

- **Geometry** — crop & straighten via an interactive overlay (±15°), 90°
  rotation and mirroring from the overlay toolbar.
- **Retouch** — spot removal (`Q`): click a dust spot or blemish (drag to
  size it), or paint over any shape with the heal brush, and it fills from a
  source patch marraw picks for you — heal (source texture tone-matched to
  the surroundings) or clone (verbatim) — with a draggable source, per-spot
  feather, and `1`–`9`/`0` setting a selected spot's opacity. Visualize
  spots (`A`) flips to a high-contrast dust view with a sensitivity slider
  for hunting sensor spots. Spots are anchored to image content, so they
  survive recrops and re-straightens and render identically in previews,
  1:1 tiles and exports.
- **Local adjustments** — linear (graduated) filters, radial filters, a
  feathered brush with flow and eraser, and AI masks: Subject, Depth (a
  two-thumb near/far range) and Scene selections, with mask edges refined
  automatically at high zoom and a mask row's region tinted on hover. Each
  mask carries its own exposure, contrast, highlights/shadows, whites/blacks,
  temperature, tint and saturation. Masks are anchored to image content, so
  they survive recrops and re-straightens, and render identically in
  previews, 1:1 tiles and exports.
- **Tone** — exposure (±5 EV), preserve highlights, brightness, gamma, shadow
  slope, contrast, whites, blacks, shadows, highlights.
- **Presence** — clarity, texture, dehaze.
- **White balance** — as shot / auto / Kelvin, temperature, tint, and an
  eyedropper (`W`).
- **Color** — saturation, vibrance, split toning (shadow + highlight tint),
  and an 8-band HSL color mixer (per-band hue / saturation / luminance).
- **Effects** — creative vignette.
- **Detail** — sharpen, highlight recovery (clip/unclip/blend/rebuild), noise
  reduction, FBDD denoise, median passes, demosaic algorithm (VNG/PPG/AHD/DHT),
  manual chromatic-aberration correction.

Every slider has a letter shortcut: press it, then `+`/`-` to adjust
(`Shift` for a big step), `Esc` to release. Press `?` for the full list.

### Export

JPEG (quality 1–100), or lossless TIFF or PNG for when you want no
compression artifacts at all. All three render exactly what the loupe showed you —
crop, look, detail — with optional long-edge resize, sRGB / Adobe RGB /
ProPhoto with an embedded ICC profile, output sharpening tuned for screen,
matte or glossy paper, and an optional watermark — any stack of text and image
elements, sized relative to the export so it reads the same at full resolution
and at web sizes. Output files take a name template — `{name}`, `{seq}`,
`{date}`, `{time}` — and you choose what metadata they carry: everything the
RAW knew (body, exposure triangle, lens, capture time, GPS), copyright only,
or nothing, with persisted artist/copyright credit fields and a one-switch
location strip. Runs in the background across all cores at full AHD demosaic
quality. A single photo can also go straight to the clipboard (`Ctrl+⇧+C`),
rendered exactly like an export, for pasting into a chat or a doc.

If you want to finish a photo in another editor, don't export an intermediate
— nothing marraw can render carries more information than the file your camera
already made. Instead, **RAW + XMP** export copies the original RAWs untouched
and writes Adobe-compatible `.xmp` sidecars next to them: rating, flag, and
your develop settings translated to Camera Raw's own tags (intent-level —
close, not pixel-identical), ready for Lightroom to pick up. It can also write
just the sidecars in place, next to the originals.

### Supported files

Sony `.arw` `.sr2` `.srf` · Canon `.cr2` `.cr3` `.crw` · Nikon `.nef` `.nrw` ·
Fuji `.raf` · Olympus `.orf` · Panasonic `.rw2` · Pentax `.pef` ·
Samsung `.srw` · Sigma `.x3f` · Hasselblad `.3fr` `.fff` · Phase One `.iiq` ·
Adobe/various `.dng` · plus `.erf` `.mef` `.mos` `.mrw` `.rwl`

---

## What marraw does *not* do

Read this list before you install. These are absences, not bugs — if one of
them is load-bearing for your work, marraw is not ready for you yet.

**Editing**

- ❌ **No luminance or color range masks.** AI masks (subject/depth/scene)
  and drawn masks exist — see Editing tools — but there is no luma/color
  range selection, and local adjustments are not carried into RAW + XMP
  handoffs.
- ❌ **No tone curve.** Contrast and the whites/blacks/shadows/highlights
  sliders drive a fixed parametric curve. There is no point curve and no
  per-channel R/G/B curves.
- ⚠️ **Retouch is heal/clone only.** Spots and the heal brush fill from
  another patch of the same photo — there is no content-aware/ML fill, and
  retouch is not carried into the RAW + XMP handoff.
- ❌ **No lens profile corrections.** No distortion or vignetting profiles, no
  automatic defringe — only a manual CA slider and a creative vignette.
- ❌ **No modern denoise.** You get LibRaw's wavelet/FBDD/median, not
  ML-assisted luminance and color NR.
- ❌ **No HDR editing or output**, and no wide-gamut working space. The look
  math is sRGB-ish and highlights clip rather than grade.

**Library**

- ❌ **No XMP round-trip.** Working sidecars are marraw's own `.marraw.json`,
  and marraw never *reads* XMP. Export can write Adobe-compatible `.xmp`
  sidecars (see RAW + XMP above), but that is a one-way handoff, not sync.
- ❌ **No photo search, keywords, or collections.** You filter by rating,
  flag, and the culling signals (soft frames, blinks) within a folder.
  That's it.
- ❌ **No DCP camera profiles.**

**Export & output**

- ❌ **No keyword or IPTC metadata in exports.** EXIF (camera, exposure, lens,
  GPS) and your copyright credit are written, but the library has no keywords
  to carry.

**Platform & workflow**

- ❌ **Not code-signed.** Expect a SmartScreen warning on first install
  (Windows) or a right-click → Open dance on first launch (macOS).
- ❌ **No second-display window**, no tethered capture, no soft-proofing.
- ❌ **No panorama stitch or HDR bracket merge.**

Multi-window (several windows, one library) *does* work. So do light/dark
themes, per-photo edit undo/redo, one-`Ctrl+Z`-per-stroke culling undo, and a
cache with a configurable size cap.

---

## Where your data lives

- **Catalog** — SQLite under `%APPDATA%\marraw` (Windows), `~/.config/marraw`
  (Linux), or `~/Library/Application Support/marraw` (macOS).
- **Preview cache** — same place, with a size cap you set in Settings.
- **Your edits** — in the catalog *and* in a `.marraw.json` next to each RAW,
  unless you turn sidecars off.

Your RAW files are never modified or moved.

---

## Contributing

Want to build this yourself or contribute? Everything is in
**[DEVELOPER.md](DEVELOPER.md)** — architecture, setup, tests, packaging, and
the release process.

Issues and pull requests are welcome, especially reports from the newer macOS
and Linux builds and anything on the missing list above.

---

## License

marraw is [MIT licensed](LICENSE).

It statically links **LibRaw**, used here under its **CDDL-1.0** option, and
ships inside **Electron** (MIT). Full attribution and source-availability
notices are in **[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)**.
