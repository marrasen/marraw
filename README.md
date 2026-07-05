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
  px + full, keyed by file identity (`sha256(path|size|mtime)`) and edit
  state hash. Grid thumbs come from the RAW's embedded JPEG (no decode,
  ~ms); larger levels from a `half_size` LibRaw decode. URLs are
  content-addressed, so the browser cache is always valid.
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
- **Editing** is non-destructive: LibRaw params (exposure, WB, highlights,
  brightness, NR, …) stored as JSON in SQLite. While a slider drags, the
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

- Custom white balance UI (Kelvin/tint) — needs camera-matrix math; only
  as-shot/auto exposed for now.
- XMP sidecar export for Lightroom/darktable interop.
- Crop tool (LibRaw `cropbox` is plumbed but has no UI).
- aprot wishlist that fell out of this project (filed upstream):
  [delta/patch subscriptions](https://github.com/marrasen/aprot/issues/237),
  [binary payloads](https://github.com/marrasen/aprot/issues/238),
  [stream flow control / chunked yields](https://github.com/marrasen/aprot/issues/239),
  [fixed-size arrays generate `any`](https://github.com/marrasen/aprot/issues/240).
