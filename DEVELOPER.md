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
applies it consistently to previews, edit renders, and exports.

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

Local adjustment masks run between the look and the detail stage
(`internal/pyramid/mask.go`). Their tone/colour sliders are point operations
served by a per-row weight seam, but the **spatial effects** — blur, motion and
zoom smears, glow, light streaks, prism, mosaic — gather neighbouring pixels, so they run as
a separate later pass per mask (`internal/pyramid/maskfx.go`), after that mask's
own tone/colour sliders, so light added by glow or streaks is never re-darkened
by the mask's exposure. That pass
materializes the mask's weight plane and gathers *through* it: weight-normalized
(`Σw·c / Σw`, so a masked-out subject cannot bleed into the blurred surround)
and in linear light. That pass runs at a fixed `fxPlaneLongEdge` working
resolution, so the 1024 draft, the 2048 settle, the 1:1 tiles and the export all
compute the identical effect. `ApplyMasks` returns a detail-suppression plane
that damps `ApplyDetail` where a mask deliberately destroyed detail — otherwise
clarity re-etches a rim around a sharp subject against a defocused background.

While a slider drags, the backend re-processes the photo's already-unpacked
handle at half size (~400 ms warm on 42 MP files) and the loupe swaps in the
new rendition flicker-free. Transient drags decode once to scene-linear and
fold WB/exposure/brightness/gamma in Go without re-demosaicing; the WB
approximation there is deliberate and is corrected by the exact 2048 settle.

### ML fills (retouch spots and mask removals)

Fill-mode retouch spots and a mask's **Remove** flag share one path: the pixels
cannot be derived from the params, so an inference writes an RGBA patch to
`pyramid.FillStore` (its own directory, not the preview cache — patches cost a
model run, so they survive `Clear`/`Relocate`) and `ApplyHeal` composites it in
the **pre-look** stage, before `ApplyLook`, so synthesized pixels develop with
the frame they came from. Mask removals composite before spots, so a spot can
heal a seam an inpaint left. A missing patch composites nothing — never a hole,
never an error.

Both sides re-derive the context window and the model mask from the params
rather than storing them (`SpotFillWindow` / `MaskFillWindow`), so there is
nothing to drift. Two rules keep the cache honest:

- **The patch key** (`edit.SpotFillKey`, `edit.MaskFillKey`) covers the region
  geometry plus everything shaping the pre-look oriented frame — the LibRaw
  decode subset and the quarter-rotate/mirror. Composite-only fields stay OUT:
  feather, opacity, `Adjust`, `Disabled`, crop, straighten and the look stage
  must never cost an inference. The two seeds are domain-separated so a spot
  and a mask cannot collide.
- **A removal's region is a pure function of the mask params and its stored AI
  map** — never of rendered pixels. That is why `MaskRemoveAllowed` refuses
  range masks (their coverage is computed from developed pixels, so no stable
  key could exist), the soft/unbounded types (linear, radial, depth), and
  effectively inverted masks (whose region is everything *but* the subject).
  The region is binarized at 128 and dilated before it goes to the model;
  `Feather` softens only the composite edge. `client/src/lib/controlSpecs.ts`
  mirrors this rule as `maskCanRemove` and must stay in sync.

Generation is serialized daemon-wide by `fillSem` in `internal/api/fill.go`: a
run pins a LibRaw handle for its warm decode, and browsing must never queue
behind it.

## Prerequisites

- Go 1.26+
- Node 24+
- MinGW-w64 `gcc`/`g++` on `PATH` (Windows)

## Setup

```powershell
npm run setup:libraw   # download + build static libraw.a (one-time, few min)
npm run setup:ort      # download the ONNX Runtime shared lib (ML features/tests)
npm install
npm --prefix client install
npm run gen            # aprot codegen -> client/src/api
```

`setup:libraw` compiles with `ForEach-Object -Parallel` and so needs
PowerShell 7 (`pwsh`), not the Windows PowerShell 5.1 that `powershell` resolves
to.

marraw builds against the published `aprot` module in `go.mod`. To develop the
two side by side, create a `go.work` — it is deliberately untracked, because a
committed one would hardcode your checkout path and, worse, silently mask the
case where marraw uses `aprot` code that has not been released yet:

```powershell
go work init . ..\aprot
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
npm run typecheck            # tsc -b: the same program the build compiles
npm --prefix client run lint
npm --prefix client test     # vitest, client/src/**/*.test.ts
```

Every push to `main` runs all of this on Linux in CI (`.github/workflows/ci.yml`),
along with a check that the committed `client/src/api` still matches what the
pinned `aprot` generates.

Client tests are vitest, configured in `client/vitest.config.ts` and run in a
node environment: what is worth testing here is the pure logic — geometry,
navigation, formatting, the curve math that mirrors the backend's — rather
than rendered components. A test needing a DOM opts in per file with
`// @vitest-environment jsdom`. Put the test beside what it tests
(`src/lib/crop.ts` → `src/lib/crop.test.ts`).

Backend smoke test (needs a folder of RAW files and a running dev server):

```powershell
node scripts/smoke.mjs "D:\Photos\some-shoot"
```

Backend probes in `scripts/` talk to the daemon through `scripts/lib/rpc.mjs`,
which owns the wire protocol — positional params, binary frames for blob
methods, task-status polling. Use it rather than opening a socket by hand: the
copies that did drifted apart and failed as hangs, months after the change
that broke them.

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

Users can turn it off in **Settings → General → Automatic updates**. That
preference lives in `preferences.json` under `app.getPath('userData')`, *not*
in the daemon's `uiSettings`: the updater has to decide whether to run at
launch, before — and even if — `marrawd` ever comes up. The toggle is hidden
on macOS, where an unsigned bundle can never self-update.

Because the app is **not code-signed**:

- **Windows** — update works. `electron-updater` skips signature verification
  when no `nsis.publisherName` is configured. Users click through a SmartScreen
  warning on first install.
- **macOS** — auto-update is *impossible* unsigned; Squirrel.Mac hard-requires
  a valid signature. The updater is therefore not started on darwin.

To sign later: add the `CSC_LINK` / `CSC_KEY_PASSWORD` (Windows) or
`APPLE_ID` / `APPLE_APP_SPECIFIC_PASSWORD` / `APPLE_TEAM_ID` (macOS) secrets
and drop the `mac`/`win` `signAndEditExecutable: false` escape hatches.

## macOS and Linux

The release workflow builds all three platforms. Windows is the developed-on,
tested platform; **macOS and Linux installers are built and unit-tested in CI
but have never been run on real hardware** — treat user reports accordingly.

How the port is wired:

- **cgo** — `Open()` is split per-OS (`internal/libraw/open_windows.go` wide-char
  `libraw_open_wfile`, `open_unix.go` narrow `libraw_open_file`); LDFLAGS are
  per-OS `#cgo` lines in `libraw.go`. cgo rules out cross-compiling, so each
  platform builds on its own native runner.
- **LibRaw** — `scripts/setup-libraw.sh` (Unix twin of `setup-libraw.ps1`)
  builds a static `libraw.a` via LibRaw's dependency-free `Makefile.dist`,
  which is thread-safe on Unix by default.
- **Trash** — `trash_linux.go` prefers `gio trash` (full freedesktop spec,
  external drives included) with a home-trash fallback; `trash_darwin.go`
  calls `NSFileManager trashItemAtURL` via a small Objective-C cgo shim.
- **sysmem** — Linux reads `/proc/meminfo`; macOS has no implementation and
  the export memory governor uses its conservative fallback.
- **Packaging** — unsigned arm64 `.dmg` (Intel Macs are not built),
  `AppImage` + `.deb` on Linux (built on the oldest supported Ubuntu image so
  the daemon's glibc floor stays low). The daemon ships as `extraResources`
  per platform (`marrawd` / `marrawd.exe`).

Known caveats for user reports:

- **macOS Gatekeeper** — the dmg is unsigned and unnotarized, so on Apple
  Silicon the first launch fails with *"marraw is damaged and can't be
  opened"*. Neither right-click → Open nor allowing "Anywhere" in Privacy &
  Security clears it: that message is the quarantine bit plus a signature
  Gatekeeper won't validate, not a policy verdict. The fix users need is
  `xattr -dr com.apple.quarantine /Applications/marraw.app` (confirmed
  working, 2026-08-01). No auto-update (see above).
- **Linux trash without `gio`** — falls back to home-trash; deleting photos
  that live on a *different filesystem* than `$HOME` errors instead of
  trashing (deliberate: copying RAWs "to trash" would double disk usage).
- **AppImage on newer distros** — needs libfuse2 (`apt install libfuse2`),
  the usual AppImage runtime requirement.
