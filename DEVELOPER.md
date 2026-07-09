# Developing marraw

Everything you need to build, test, package and release marraw. For what the
app *does*, see the [README](README.md).

## Architecture

marraw is an Electron shell around two processes:

- **marrawd** (Go daemon) serves the [aprot](https://github.com/marrasen/aprot)
  API over WebSocket and pyramid preview images over HTTP on one localhost
  port. LibRaw is statically linked via cgo; a worker pool (one LibRaw handle
  per core) feeds a priority queue (interactive > visible > prefetch >
  background).
- **client** (Vite + React + TypeScript) talks to it over a generated,
  type-safe API surface.

The Electron main process spawns the daemon with a random port + auth token,
waits for the `MARRAW_READY port=N` handshake on stdout, and kills it on quit.
The daemon also exits if its stdin closes, so a crashed shell never leaves an
orphan. Relaunching the exe opens a new window in the running instance rather
than a second process — two daemons on one SQLite file clobbered each other's
settings.

### Pyramid cache

Every photo gets JPEG renditions at 256/512/1024/2048 px, keyed by file
identity (`sha256(path|size|mtime)`) and edit-state hash. Grid thumbs come from
the RAW's embedded JPEG (no decode, ~ms); larger levels from a `half_size`
LibRaw decode. URLs are content-addressed, so the browser cache is always
valid.

Past 2048 the loupe switches to full-resolution 1024 px tiles: one decode
renders the whole set and tiles JPEG-encode in parallel. The client downloads
and decodes only the visible crop over an upscaled-2048 underlay, and the
neighboring photos' tile sets pre-render while browsing at 1:1.

### Adaptive base look

LibRaw output is flat next to camera JPEGs — manufacturer tone curves are
proprietary and adaptive (e.g. Sony DRO). On the first RAW render of each
photo, marraw calibrates a per-photo tone lift by matching mean luminance
against the camera's own embedded JPEG, stores it (`photos.look_gamma`), and
applies it consistently to previews, edit renders, and JPEG exports. TIFF16
stays neutral as a flat master.

> **Bump `renderVersion` (Go) and `RENDER_VERSION` (TS) together whenever the
> render pipeline changes.** Image URLs are served as immutable and will
> otherwise serve stale pixels forever.

### Edit pipeline

Non-destructive, stored as JSON in SQLite. Three stages, in order:

1. **LibRaw decode stage** — exposure, white balance (as-shot / auto / Kelvin /
   picked), highlight recovery, brightness, gamma, shadow slope, noise
   reduction, FBDD, median passes, demosaic choice, manual CA correction.
2. **Look stage** (`internal/pyramid/look.go`) — a LUT built after LibRaw:
   contrast, whites/blacks, shadows/highlights, saturation, vibrance, split
   toning, vignette.
3. **Geometry + detail stage** — crop + straighten
   (`internal/pyramid/geometry.go`), then clarity / texture / dehaze / sharpen
   (`internal/pyramid/detail.go`).

`sharpen` and `texture` use fixed *output-pixel* radii, so a fit-to-screen
preview is only indicative; the true result appears at 1:1 and on export.

While a slider drags, the backend re-processes the photo's already-unpacked
handle at half size (~400 ms warm on 42 MP files) and the loupe swaps in the
new rendition flicker-free. Transient drags decode once to scene-linear and
fold WB/exposure/brightness/gamma in Go without re-demosaicing; the WB
approximation there is deliberate and is corrected by the exact 2048 settle.

## Prerequisites

- Go 1.26+
- Node 24+
- MinGW-w64 `gcc`/`g++` on `PATH` (Windows)

## Setup

```powershell
npm run setup:libraw   # download + build static libraw.a (one-time, few min)
npm install
npm --prefix client install
npm run gen            # aprot codegen -> client/src/api
```

## Running

```powershell
npm run dev            # marrawd on :8483 + Vite on :5173 (browser dev)
npm run dev:electron   # Electron shell attached to the dev servers
npm run preview        # production build, run from the repo, no Vite/HMR
```

`MARRAW_VITE_PORT` overrides 5173 if it is taken.

## Testing

Go tests (the libraw wrapper tests need real RAW files; set
`MARRAW_TEST_RAW_DIR`):

```powershell
go test ./internal/...
npm run typecheck
```

Backend smoke test (needs a folder of RAW files and a running dev server):

```powershell
node scripts/smoke.mjs "D:\Photos\some-shoot"
```

UI verification harnesses live in `scripts/` (`ui-verify.mjs`, `shot.mjs`,
`auto-verify.mjs`, …). Kill any user-launched Electron first — the GPU cache
lock will stall rAF.

## Repo layout

```
cmd/marrawd/        daemon entrypoint (flags: --port, --dev, --data-dir)
internal/libraw/    cgo wrapper (the only package importing "C")
internal/decode/    priority worker pool + open-handle LRU for editing
internal/pyramid/   preview cache: generation, keying, size-cap janitor
internal/store/     SQLite (modernc, WAL): folders, photos, ratings, edits
internal/scan/      folder scan + background metadata/thumb backfill
internal/edit/      edit params <-> LibRaw params mapping + hashing
internal/sidecar/   .marraw.json sidecar read/write
internal/trash/     move-to-recycle-bin (Win32 SHFileOperationW)
internal/api/       aprot handler groups (Library, Edits, Export)
internal/imghttp/   GET /img/{id}/{level}?v=&e= endpoint
internal/export/    parallel full-quality export
tools/generate/     aprot TypeScript codegen
client/             Vite + React + TS + Tailwind + shadcn/ui
electron/           main.cjs (spawn/handshake), preload.cjs
scripts/            setup-libraw.ps1, smoke.mjs, verification harnesses
```

## Packaging

```powershell
npm run dist           # -> dist/marraw-Setup-<version>.exe
```

The NSIS installer bundles `marrawd.exe` (LibRaw statically linked — no DLLs)
as an extra resource.

## Cutting a release

Releases are built by [`.github/workflows/release.yml`](.github/workflows/release.yml)
on any `v*` tag.

1. Bump `version` in `package.json`.
2. Commit, then tag with a **matching** version:
   ```powershell
   git tag v0.2.0 && git push origin main --tags
   ```
   The workflow refuses to build if the tag and `package.json` disagree —
   a mismatch would publish an installer whose `latest.yml` never matches what
   the auto-updater looks for.
3. The workflow builds LibRaw, the daemon, the client, and the NSIS installer,
   then uploads them to a **draft** GitHub Release.
4. Review the draft and press **Publish**. Installed clients only see published
   releases, so nothing ships until you do.

### Auto-update

The packaged app checks GitHub Releases on launch via `electron-updater`
(wired up in `electron/main.cjs`), downloads a newer version in the background,
and installs it on quit. It is disabled in dev, preview and UI-test runs.

Because the app is **not code-signed**:

- **Windows** — update works. `electron-updater` skips signature verification
  when no `nsis.publisherName` is configured. Users click through a SmartScreen
  warning on first install.
- **macOS** — auto-update is *impossible* unsigned; Squirrel.Mac hard-requires
  a valid signature. The updater is therefore not started on darwin.

To sign later: add the `CSC_LINK` / `CSC_KEY_PASSWORD` (Windows) or
`APPLE_ID` / `APPLE_APP_SPECIFIC_PASSWORD` / `APPLE_TEAM_ID` (macOS) secrets
and drop the `mac`/`win` `signAndEditExecutable: false` escape hatches.

## Porting to macOS and Linux

The release workflow has macOS and Linux jobs scaffolded but commented out.
They are blocked on the Go/cgo layer, which is currently Windows-only:

1. **`internal/libraw/libraw.go`** — `Open()` calls `syscall.UTF16PtrFromString`
   and `libraw_open_wfile`, both Windows-only, from an untagged file. Split
   into `libraw_windows.go` (keep the wide-char path) and `libraw_unix.go`
   (`//go:build !windows`, use `C.CString` + `libraw_open_file`).
2. **cgo LDFLAGS**, same file — `-lws2_32` is Winsock and `-static` has no
   macOS equivalent. Split per-OS:
   ```
   #cgo windows LDFLAGS: -lraw -lstdc++ -lws2_32 -lm -static
   #cgo linux   LDFLAGS: -lraw -lstdc++ -lm
   #cgo darwin  LDFLAGS: -lraw -lc++ -lm
   ```
3. **`third_party/libraw/lib/libraw.a`** is a MinGW artifact. Port
   `scripts/setup-libraw.ps1` to a `setup-libraw.sh` and build a `libraw.a` per
   target (linux-amd64, darwin-arm64, darwin-amd64). This is the bulk of the
   work.
4. **`electron/main.cjs`** — hardcodes `marrawd.exe` in both the unpackaged and
   packaged paths. Derive it:
   `process.platform === 'win32' ? 'marrawd.exe' : 'marrawd'`.
5. **`package.json`** — `build:server` writes `build/marrawd.exe`; the
   `build` block has only a `win` target. Add `mac`/`linux` targets, per-platform
   `extraResources`, and `.icns` / `.png` icons.
6. **`internal/trash/trash_other.go`** compiles but returns
   `trash: not supported`. Wire up `gio trash` (Linux) and
   `NSFileManager trashItemAtURL` (macOS), or delete becomes a hard error.

`cmd/marrawd/main.go` already uses `os.UserConfigDir()`, and all path handling
goes through `path/filepath`, so the daemon's storage layer is portable as-is.
Note that cgo rules out cross-compiling: each platform must build on its own
native runner.
