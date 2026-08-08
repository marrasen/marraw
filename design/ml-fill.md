# ML content-aware fill — implementation plan

Planned 2026-07-27, after Marcus field-tested the spot-removal MVP. Fill is
the third way to fill a retouch region: heal (membrane fit) and clone (copy)
take pixels from a source patch; fill inpaints the region with an ML model —
no source needed. Backlog entry: design/backlog.md → Retouch.

## Model: MI-GAN-512 (decided; vetted 2026-07-27)

**MI-GAN** (Picsart-AI-Research/MI-GAN, ICCV 2023) — **MIT license covering
the pretrained weights** (no separate weights license in the repo), 512×512
Places2-trained variant, **pre-exported ONNX ~29 MB**, designed for
mobile-class speed (an order of magnitude smaller/faster than LaMa). This
dissolves both open questions from the backlog: license is unambiguous
(the org that trained it published the weights under MIT) and the download
is 29 MB, not ~200 MB.

Rejected/fallback:

- **Original big-LaMa** (advimman/lama): code Apache-2.0 but the pretrained
  weights are CC BY-NC-SA — the RMBG trap. Rejected.
- **Carve/LaMa-ONNX + the OpenCV Zoo mirror** claim Apache-2.0 on the same
  ported weights; a port cannot relicense NC weights, so treat as tainted
  unless vetted much harder. Only revisit if MI-GAN quality disappoints in
  the field.

Distribution (the established pattern):

- Spec ID `migan`, Version `1` → on-disk `migan-1.onnx`. Mirror the ONNX to
  `marrasen/marraw-models` release `models-v1` with a SHA-256 pin before
  implementation starts (same as every other model; do not point at HF).
- Add a row to THIRD_PARTY_NOTICES.md (`## ML runtime and model weights`
  table, ~line 115) and name the new package in the prose (~line 111).
- Add the file to `modelCatalog()` in `internal/api/models.go:34` or it
  shows nameless in Settings → Models.
- **First implementation step: inspect the ONNX I/O empirically** (names,
  shapes, dtype — expected: image + mask in, composited image out, fixed
  512²; `infer.Session` discovers I/O names via `ort.GetInputOutputInfo`,
  and `Session.Run` takes inputs in the model's declared order, so
  multi-input works without infra changes).

## Data model: `Mode: "fill"`, not `Kind: "fill"`

`Spot.Kind` discriminates region *shape* ("" = circle, "stroke" = painted);
`Mode` discriminates how the region is *filled* (heal/clone). Fill is a
third way to fill either shape → it's a Mode. (The backlog said Kind, but
both reservations exist in the compat tests and Mode composes: brush-paint a
region and fill it.)

- `internal/edit/edit.go:151`: add `SpotFill SpotMode = "fill"`.
- `normalizeSpots` (edit.go:431): accept `SpotFill` in the mode switch.
  Fill spots keep CX/CY/Radius/Strokes/Feather/Opacity; SX/SY stay zeroed
  (normalize them to 0 for fill so the hash is stable).
- `healModeKnown` (internal/pyramid/heal.go:54): add `SpotFill`.
- Update both unknown-compat test tables — `pyramid/heal_test.go:104-117`
  and `edit/edit_test.go:322-327` currently use `"fill"` as the unknown
  probe; swap those probes to `"bogus"`-style values and add positive fill
  cases. The forward-compat contract itself is already proven: an old build
  renders a fill spot as nothing and old-build Normalize drops it on save
  (accepted, same as any schema addition).

## Caching: FillStore (fills are not parametrically re-renderable)

New `pyramid.FillStore` at `<dataDir>/fills`, an AIMapStore clone
(pyramid/aimap.go) storing RGBA PNG patches:

- Path: `<dir>/<photoKey[:2]>/<photoKey>_fill-<spotHash>_<fillVer>.png`.
- **Key**: `spotHash` = sha256[:12] of the canonical JSON of the spot's
  *geometry only* (kind, cx, cy, radius, strokes) **plus the decode-subset
  hash** (`Params.LibrawInputsHash()` precedent, edit.go:629 — the fill is
  computed on developed pre-look pixels, so a WB/exposure change must miss
  the cache). Feather/opacity/disabled/mode stay out of the key — they're
  composite-time parameters. `fillVer` = `migan-1` (spec ID + version),
  entering the key the way `MapVerFor` does.
- Patch content: the spot's bounding region + margin in **oriented-frame
  space, base orientation** (the AIMap precedent — `loadOriented` permutes
  at load so a later quarter-rotate doesn't orphan it), at generation-native
  resolution capped (~1024 long edge per patch; spots are small).
- Unlike aimaps, fills need an **LRU disk cap** — the Janitor only walks the
  pyramid cache dir, and RGBA patches are much bigger than gray maps. Use
  the size-capped LRU design already written down for `<dataDir>/denoised/`
  in design/ml-denoise.md.

## Inference: new `internal/inpaint` package

`aimask` pattern, not `RunTiled` (RunTiled is single-input image-to-image
with no mask plumbing and zero production callers; a fill is one windowed
pass, not a tiling problem):

- `Spec() infer.ModelSpec` / `FillVer() string`.
- `Generate(ctx, mgr *infer.Manager, src *image.RGBA, mask *image.Gray,
  progress infer.Progress) (*image.RGBA, error)`: window the source around
  the spot (window side ≈ 2.5× the spot's bounding box, min 512 px, taken
  from the pre-look full-res or 2048 render — whichever the geometry gives
  cheaply), resample window → 512², build image + mask tensors, one
  `sess.Run`, take the model output inside the spot region, resample back
  to window resolution. Small dust spots get effectively 1:1 quality; very
  large fills degrade gracefully (accepted).
- Input pixels: post-geometry, **pre-look** — the same stage ApplyHeal
  composites at, so generation and composite agree by construction.

## Rendering: composite the cached patch in ApplyHeal

- Thread a `FillSet` through `ApplyFinish` exactly the way `AIMapSet` is
  threaded (`ApplyHeal(img, e, fills FillSet)`); 5 call sites to update:
  pyramid/cache.go:444, :467, :604, fold.go:179, export/export.go:258.
- `applyFillSpot` / fill branch in `applyHealStroke`: same coverage math as
  heal (disc feather LUT / brush coverage plane), but sample the patch
  (bilinear, through `framePoint` — the `brushEval`/`aiEval` sampling
  precedent) instead of translated source pixels; same Q8 feather/opacity
  blend. Missing patch → composite nothing (render never triggers
  inference — the calibrate-pass principle).
- No `renderVersion` bump: fill spots change `Params.Hash()`, so stale
  cached renders can't be confused with new ones. But `GenerateFill` must
  call `Cache.InvalidateEdit` after saving a patch (aimasks.go:363
  precedent) — the edit hash doesn't change when the patch appears.

## API: `internal/api/fill.go`

Mirror `aimasks.go`:

- `Edits.GenerateFill(ctx, photoID int64, params edit.Params, spotIndex,
  allowDownload bool) (*FillResult{FillVer string; Generated bool}, error)`
  — Normalize first; `FillStore.Has` fast path (no task, no toast); consent
  check reusing the `aiModelNotDownloadedMsg` sentinel (aimasks.go:270);
  `tasks.StartTask[TaskMeta]` with new `Kind: "fill"` (jobs.go:30 union)
  carrying model-download progress in MB; render the pre-look window,
  `inpaint.Generate`, `FillStore.Save`, `Cache.InvalidateEdit`,
  `aprot.TriggerRefresh(ctx, modelsInfoKey)` after a download.
  `Generated` drives the client-side `bumpImgBust`, same contract as
  `GenerateAIMap`.
- `Edits.FillModelStatus(ctx) (*AIModelInfo, error)` — `AIModelStatus`
  shape (aimasks.go:256), for the consent dialog's byte count.
- Regenerate the client bindings with `npm run gen`; never hand-edit
  client/src/api. (This once needed a `go.work` overlay to an unreleased
  aprot; the pin in go.mod is current again, so plain `npm run gen` is right.)

Staleness loop: the client calls `GenerateFill` for each fill spot on every
commit (`esCommit` path) — the `Has` fast path makes it free when nothing
changed, and a WB/exposure change re-keys and regenerates automatically.

## Client

- `SpotMode` union gains `'fill'`; third item in both mode ToggleGroups
  (RetouchSection EditPanel.tsx:902, SpotRow :1045; row label already
  derives from mode).
- `editSession.ts`: `esBeginSpot` (:691) branches on `spotMode === 'fill'`
  — emit `mode:'fill'`, skip the `interimSource` SX/SY seeding.
  `esFinishSpot` (:721) — skip `suggestHealSource`, call `generateFill`
  (consent-aware) around the commit. Spinner state on the spot row while
  the RPC is in flight (the `generating` + `Loader2` pattern,
  EditPanel.tsx:1279).
- Consent: generalize `AIModelDialog` (its `AI_KIND_INFO` is keyed by
  AIKind) to accept a fill entry — title/copy/bytes from `FillModelStatus`;
  `isModelNotDownloaded(err)` (lib/aiConsent.ts) already matches the shared
  sentinel for the restore path.
- `HealOverlay.tsx`: source UI is currently keyed off `spot.kind`/`active`
  only. Add `const hasSource = spot.mode !== 'fill'` and gate the ~8 source
  sites: connector line, dashed source ring/paths, the `source` grip Dot,
  and the `hideDest`-during-source-drag logic (moot for fill). Fill spots
  render just the dest ring/paths + dest/radius dots.
- `bumpImgBust(photoId)` when `GenerateFill` returns `Generated: true`
  (imgCacheBust.ts contract, EditPanel.tsx:1179 precedent).

## Verification

- Go: `internal/inpaint` unit tests (windowing/resampling with a fake
  session), FillStore round-trip + LRU cap, heal_test fill-composite cases
  + revised unknown tables, edit_test Normalize keeps/canonicalizes fill.
- `scripts/fill-verify.mjs` (spot-verify.mjs pattern, real WS): consent
  gate fires without the model; with the model — generate, patch on disk,
  512 render differs from baseline, re-generate is a fast-path no-op,
  WB change re-keys, sidecar round-trips, unknown-mode compat preserved.
- Shot surface `healfill` in shot.renderer.js: seed a fill spot via the
  `window.__marraw` bridge, wait `esPreviewSettled`, probe overlay state
  (no source ring) + capture.
- `npx tsc -b` in client (not `npm run typecheck`) + eslint; never
  `prettier --write`.
- Update README.md:232-234 (currently: "no content-aware/ML fill") and the
  design/backlog.md Retouch entry when shipping.

## Suggested order

1. Mirror + pin `migan-1.onnx`; inspect I/O; THIRD_PARTY_NOTICES +
   modelCatalog rows.
2. `internal/edit` mode + Normalize + tests (smallest reviewable unit,
   unblocks everything).
3. `internal/inpaint` + FillStore + `ApplyHeal` compositing + go tests.
4. `internal/api/fill.go` + `npm run gen`.
5. Client (session, panel, overlay, consent, bust).
6. fill-verify.mjs + `healfill` shot + README/backlog updates.
