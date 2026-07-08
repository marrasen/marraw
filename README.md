# marraw

Fast RAW photo organizing and editing for Windows. Go + LibRaw backend,
React/TypeScript client, [aprot](https://github.com/marrasen/aprot) RPC,
shipped as an Electron app.

## How it works

- **marrawd** (Go daemon) serves the aprot API over WebSocket and pyramid
  preview images over HTTP on one localhost port. LibRaw is statically
  linked via cgo; a worker pool (one LibRaw handle per core) feeds a
  priority queue (interactive > visible > prefetch > background).
- **Pyramid cache**: every photo gets JPEG renditions at 256/512/1024/2048
  px, keyed by file identity (`sha256(path|size|mtime)`) and edit state
  hash. Grid thumbs come from the RAW's embedded JPEG (no decode, ~ms);
  larger levels from a `half_size` LibRaw decode. URLs are
  content-addressed, so the browser cache is always valid. Past 2048 the
  loupe switches to full-resolution 1024 px tiles (one decode renders the
  whole set, tiles JPEG-encode in parallel): the client downloads and
  decodes only the visible crop over an upscaled-2048 underlay, and the
  neighboring photos' tile sets pre-render while browsing at 1:1.
- **Culling** is keyboard-first: arrows navigate, `1–5` rate, `P` pick,
  `X` exclude, `U` unflag, `Enter` loupe, `Ctrl+E` export. Rating changes
  broadcast granular patch events — no list re-fetch. The loupe zooms with
  `+`/`-`/`Z`/Ctrl+wheel or the zoom toolbar, and keeps zoom + pan position
  across arrow navigation so a burst series can be compared at the same
  crop. Grid thumbnail size has a slider in the filter bar.
- **Adaptive base look**: LibRaw output is flat next to camera JPEGs
  (manufacturer tone curves are proprietary and adaptive, e.g. Sony DRO).
  On the first RAW render of each photo, marraw calibrates a per-photo tone
  lift by matching mean luminance against the camera's own embedded JPEG,
  stores it (`photos.look_gamma`), and applies it consistently to previews,
  edit renders, and JPEG exports (TIFF16 stays neutral as a flat master).
  Bump `renderVersion` (Go) + `RENDER_VERSION` (TS) together when the
  render pipeline changes — image URLs are cached as immutable.
- **Editing** is non-destructive: LibRaw params (exposure, WB — as-shot /
  auto / absolute Kelvin / picked, highlight recovery, brightness, NR,
  demosaic choice, chromatic-aberration correction, …) plus look-stage
  adjustments computed after LibRaw (contrast, whites/blacks,
  shadows/highlights, saturation, vibrance, split toning, vignette), and a
  post-decode geometry stage (crop + straighten, edited with an interactive
  loupe overlay), all stored as JSON in SQLite. While a slider drags, the
  backend re-processes the photo's already-unpacked handle at half size
  (~400 ms warm on 42 MP files) and the loupe swaps in the new rendition
  flicker-free. Multi-select applies relative adjustments ("+0.5 EV on 10
  photos"); copy/paste edit settings with Ctrl+C/V.
- **Export** saturates all cores via an errgroup pool, full-quality AHD
  demosaic, JPEG or 16-bit TIFF, with streaming progress.

## Development setup

Prereqs: Go 1.26+, Node 24+, MinGW-w64 gcc in PATH.

```powershell
npm run setup:libraw   # download + build static libraw.a (one-time)
npm install
npm --prefix client install
npm run gen            # aprot codegen -> client/src/api
npm run dev            # marrawd on :8483 + Vite on :5173 (browser dev)
npm run dev:electron   # Electron shell attached to the dev servers
```

Backend smoke test (needs a folder of RAW files and a running dev server):

```powershell
node scripts/smoke.mjs "D:\Photos\some-shoot"
```

Go tests (the libraw wrapper tests need real RAW files; set
`MARRAW_TEST_RAW_DIR`):

```powershell
go test ./internal/...
```

## Packaging

```powershell
npm run dist           # -> dist/marraw-Setup-<version>.exe
```

The NSIS installer bundles `marrawd.exe` (LibRaw statically linked — no
DLLs) as an extra resource. The Electron main process spawns it with a
random port + auth token, waits for the `MARRAW_READY port=N` handshake,
and kills it on quit (the daemon also exits if its stdin closes, so a
crashed shell never leaves an orphan).

## Repo layout

```
cmd/marrawd/        daemon entrypoint (flags: --port, --dev, --data-dir)
internal/libraw/    cgo wrapper (the only package importing "C")
internal/decode/    priority worker pool + open-handle LRU for editing
internal/pyramid/   preview cache: generation, keying, size-cap janitor
internal/store/     SQLite (modernc, WAL): folders, photos, ratings, edits
internal/scan/      folder scan + background metadata/thumb backfill
internal/edit/      edit params <-> LibRaw params mapping + hashing
internal/api/       aprot handler groups (Library, Edits, Export)
internal/imghttp/   GET /img/{id}/{level}?v=&e= endpoint
internal/export/    parallel full-quality export
tools/generate/     aprot TypeScript codegen
client/             Vite + React + TS + Tailwind + shadcn/ui
electron/           main.cjs (spawn/handshake), preload.cjs
scripts/            setup-libraw.ps1, smoke.mjs
```

## Known gaps / next steps

- XMP sidecar export for Lightroom/darktable interop.
- Coarse 90°/180° rotation and flip (crop + straighten shipped; coarse
  rotation still needs care around the orientation-swap in the tile grid).
- Spatial adjustments: sharpening/clarity (unsharp mask), HSL color mixer,
  dehaze, lens-profile corrections, local masks.
- aprot wishlist that fell out of this project — all shipped in aprot
  v0.47.0 and adopted here:
  [delta/patch subscriptions](https://github.com/marrasen/aprot/issues/237)
  (photo rating/flag/edit patches),
  [binary payloads](https://github.com/marrasen/aprot/issues/238)
  (edit previews ride the WebSocket as JPEG blobs),
  [stream chunking](https://github.com/marrasen/aprot/issues/239)
  (enabled server-wide),
  [fixed-size array codegen](https://github.com/marrasen/aprot/issues/240)
  (`wbMul` is a typed tuple).

## Missing for professional-grade editing

The features below are the gap between marraw's current global-adjustment
pipeline and what a Lightroom/Capture One-class editor offers. They are
listed roughly in dependency order — local masking underpins most of the
rest.

### Local adjustments & masking

- **Mask engine**: a per-photo stack of masks, each carrying its own copy
  of the look-stage adjustments (exposure, contrast, WB, saturation, …)
  blended over the base render. Stored as JSON edit state, evaluated after
  the global look stage, previewed at half-size like the global sliders.
- **Radial and linear gradients**: elliptical and graduated masks with
  feather, invert, and rotation — the workhorses for skies, vignettes, and
  subject pop.
- **Brush masks**: freehand paint/erase with flow, size, and feather,
  stored as a resolution-independent stroke path (re-rasterized per preview
  level) rather than a baked bitmap.
- **AI / range masks**: subject, sky, and background auto-selection, plus
  luminance/color range masks and intersect/subtract combination so a
  gradient can be constrained to just the sky.

### Tone & color depth

- **HDR editing and output**: decode and edit in a scene-referred, >1.0
  headroom working space; gain-map (ISO 21496-1) or PQ/HLG export, and an
  HDR-capable preview path so highlights are graded rather than clipped.
- **Wide-gamut pipeline**: a color-managed working space (e.g. linear
  Rec.2020 / ACEScg) with display transforms, replacing the current
  sRGB-ish look math, so gamut-clipping and out-of-gamut handling are
  correct.
- **Tone curve and per-channel curves**: parametric + point RGB and
  per-channel R/G/B curves, the tool most conspicuously absent from the
  global look stage.
- **HSL / color grading**: eight-band HSL mixer and shadow/midtone/highlight
  color wheels (the split-toning already present, generalized).

### Detail, optics & geometry

- **Detail**: capture sharpening with masking, and modern denoise
  (luminance + color, ideally ML-assisted) — currently only LibRaw's NR is
  exposed.
- **Spatial look tools**: clarity/texture/dehaze (local-contrast at
  multiple radii), already flagged above under global gaps.
- **Lens corrections**: profile-based distortion, vignetting, and
  chromatic-aberration removal (LensFun/embedded profiles), plus manual
  defringe; only LibRaw's CA toggle exists today.
- **Geometry**: perspective/keystone correction and upright, coarse
  90°/180° rotation + flip (the crop + straighten stage is shipped; see the
  orientation-swap caveat above).
- **Healing & clone**: spot removal / content-aware heal for dust and
  blemishes.

### Workflow & interop

- **Secondary display**: a Lightroom-style second window pinned to another
  monitor (loupe/compare/grid) for tethered or dual-screen culling —
  distinct from the multi-window shell that already ships.
- **XMP sidecar + catalog interop**: read/write XMP so edits round-trip
  with Lightroom/darktable/Capture One (already noted under known gaps).
- **Presets, profiles & snapshots**: saveable develop presets, camera/
  creative profiles (DCP), edit history with named snapshots, and
  before/after compare.
- **Panorama & HDR merge**: multi-frame stitch and bracket merge producing a
  new editable RAW/DNG.
- **Watermarking**: text and image (logo/PNG) watermarks applied at export
  with position, scale, opacity, and inset controls, stored as a reusable
  preset and composited in the export pipeline.
- **Tethered capture** and **soft-proofing** for print/output profiles.
