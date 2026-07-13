# marraw ML roadmap

Status: planned · 2026-07-13
Scope: AI masks (semantic / subject / depth), shared inference foundation, and the
follow-on ML features (auto-tone, culling aids, ML denoise, super resolution,
raw-domain demosaic+denoise).

The ordering principle: build one generic inference module first, make AI masks its
first consumer (small models, small outputs, no GPU required), and let each later
feature reuse the layer below it. Denoise — not masks — is the feature that pays for
GPU execution providers and full-res tiling; nothing before it needs them.

---

## Milestone 0 — Inference foundation

Status: **implemented 2026-07-13** (`internal/infer`; `npm run setup:ort`; CI
runs the toy-model test on all three platforms; installer packaging of the
runtime deferred to 1b when the daemon first links it).

A generic `internal/infer` package. Deliberately feature-agnostic: masks is the first
consumer, denoise and super-resolution are known future consumers.

**Runtime.** ONNX Runtime shared library + `github.com/yalue/onnxruntime_go`.
Official prebuilt binaries per platform (win-x64, mac-arm64, linux-x64), shipped in
the app bundle next to the libraw artifacts; CI packaging mirrors what the
cross-platform port already does for libraw. CPU execution provider only in this
milestone.

**Model registry.** Models are *not* bundled (installer stays small). A static
registry maps `modelID@version` → download URL + SHA-256 + license tag. Downloaded on
first use to the daemon's data dir (`models/`), verified by hash, served with
progress + cancellation over aprot (same UX contract as long decodes). Follows the
daemon-served-assets pattern established by watermark fonts, including the
asset-name-regex traversal guard.

**API sketch.**

- `infer.Session(modelID)` — lazily loads, caches, and evicts ORT sessions (LRU,
  models are tens of MB of RAM each).
- `infer.Run(ctx, sess, input) (output, error)` — context-cancellable, reports
  progress via the caller's callback (mirror the libraw cancel contract: never leave
  a session mid-run on cancel).
- Pre/post helpers: RGBA↔NCHW float tensors, resize-with-padding, softmax/argmax.
- Tiling helper stub (interface only for now) — implemented for real in Milestone 3.

**Testing/CI.** Unit tests with a tiny toy ONNX model checked into `third_party/`
(identity conv, a few KB) so CI exercises the runtime on all three platforms without
downloading real models.

Size: **M** (the packaging, not the code, is the work).

---

## Milestone 1 — AI masks

Three new mask sources behind one mechanism: **semantic classes** (sky, people,
foliage, water, …), **subject** (saliency), and **depth range**. All three produce a
grayscale coverage bitmap that a new evaluator samples exactly the way `brushEval`
samples its 1024-plane.

### Models

| Purpose  | Model                              | Size    | Output |
|----------|------------------------------------|---------|--------|
| Semantic | SegFormer-B0 or B1, ADE20K (Apache-2.0) | ~15–55 MB | 150-class logits |
| Subject  | ISNet / DIS (Apache-2.0)           | ~170 MB | saliency matte |
| Depth    | Depth Anything V2 Small (Apache-2.0) | ~25 MB | relative depth map |

All run comfortably on CPU at 512–1024 px input in well under a second. Verify each
license before pinning weights (avoid RMBG-1.4 — non-commercial).

### Phase 1a — Raster mask type, zero ML (de-risk first)

Prove the evaluator/storage/hash plumbing with a hand-painted PNG before any model
exists.

- `internal/edit/edit.go`: add `MaskAI` (`"ai"`) to `MaskType`; extend `Mask` with:
  - `AIKind string` — `"subject" | "class" | "depth"`
  - `ClassID int` — for `class` (photographer-category ID, see 1c)
  - `DepthLo, DepthHi float64` — for `depth` (0..1 range window)
  - `MapVer string` — model version the cached map was generated with; part of the
    normalized JSON so `Params.Hash()` invalidates renders when a map is regenerated
    with a newer model, and sidecar round-trips are deterministic.
  - Shared `Threshold, Feather float64` refinement knobs.
- `normalizeMasks`: zero other types' geometry for `ai`, quantize the new floats.
- Map cache: `internal/pyramid` gains an AI-map store next to the pyramid cache —
  `masks/{photoCacheKey}/{modelID}-{ver}.png`. Semantic = one 8-bit **class-index**
  map (all 150 classes in one file); subject and depth = 8-bit grayscale. 1024 long
  edge (same resolution philosophy as `brushPlaneLongEdge`).
- `internal/pyramid/mask.go`: `aiEval` implementing `maskEvaluator` — loads the map
  (LRU-cached like `brushCache`), derives the coverage plane (class equality test /
  depth window / saliency threshold + feather), bilinear-samples in oriented-frame
  space via `maskFrame`.
- Regenerate `client/src/api/edit.ts`.
- Golden test: hand-painted class map + params → assert rendered pixels, using the CI
  ILCE-7M3 sample.

**Coordinate-space rule:** maps live in the *oriented frame* (post quarter-rotate/
flip, pre-straighten/crop) — the same space as all mask geometry — so masks stay
glued to content across crop and straighten changes.

Size: **M**.

### Phase 1b — Generation RPC

- New aprot method on `Edits`: `GenerateAIMap(ctx, photoID, kind) → {mapVer}` —
  runs inference, writes the cache file, returns the version to stamp into the mask
  params. Progress + cancel over the existing callback channel.
- **Inference input must be edit-independent:** render the full oriented frame from
  a *neutral* develop (as-shot WB, default look, no crop/straighten) at 1024 px, so
  the map never shifts when the user changes exposure or crop. Cheapest source: the
  half-size decode path (`Edits.previewDecode`) with neutral params, or the embedded
  thumb when its resolution suffices.
- Regenerate-on-demand: if a render (loupe or export) references an AI mask whose
  cache file is missing (e.g. sidecar came from another machine), regenerate
  synchronously if the model is present; otherwise render without the mask and
  surface a warning. Export (`renderFinal` → `ApplyFinish`) needs no other change.

Size: **S–M**.

### Phase 1c — Semantic classes: expose all of them, curated

One forward pass yields all 150 ADE20K classes; storage is the single class-index
map, so "support all classes" is free at the pixel level. The work is vocabulary:

- Static mapping table ADE20K(150) → ~12 photographer categories: Sky, People,
  Foliage (tree/grass/plant/palm/flower), Water (sea/river/lake/waterfall/pool),
  Ground (earth/sand/road/path/sidewalk/floor), Architecture (building/house/
  skyscraper/wall/bridge), Mountains/Rocks, Vehicles, Animals, Furniture/Objects
  (catch-all). `ClassID` in params = category ID, stable across model upgrades.
- Detection summary: after generation, compute per-category area fractions; the RPC
  returns categories present above ~1.5% of frame so the UI lists only what's in
  *this* photo.
- Known limits (documented, not fixed here): no per-person instances (all people =
  one mask); rare-class quality is poor — the category grouping hides most of that.

Size: **S**.

### Phase 1d — UI

- Masks tab (`EditPanel.tsx` `MasksPanel`): an "AI" add-group alongside
  linear/radial/brush — **Subject**, **Depth**, and the detected-category chips from
  1c. Clicking runs `GenerateAIMap` (spinner + cancel), then `esAddMask` with the
  stamped params.
- Threshold/Feather (and DepthLo/DepthHi for depth) as mask controls via the existing
  `MASK_CONTROL_SPECS` machinery; keyboard slider walking comes free.
- `MaskOverlay.tsx`: no new drawing needed — the red weight-tint preview already
  comes from the server render. Optional later: client-side map preview blob.
- ui-verify probes: seed a photo, add a Subject mask, screenshot via shot.mjs
  (remember the gotchas file: absolute out paths, __marraw seeding).

Size: **M**.

### Phase 1e — Edge refinement (quality gate for "shippable")

Raw 1024 px model output looks chunky at 24 MP around hair/branches. Add a guided
upsample: when `aiEval` samples for a high-res render, refine the coverage plane with
a guided filter using the target buffer's luma as guidance (radius/eps tied to the
Feather knob). Pure Go, no ML. This is the difference between demo and product —
budget it as first-class work, not polish.

Size: **M**.

**Milestone 1 exit criteria:** subject/sky/person/depth masks add in one click,
survive crop/rotate/sidecar round-trip, export identically to the loupe on all three
platforms, and pass a golden-mask CI test on the ILCE-7M3 sample.

---

## Milestone 2 — Cheap wins (no pixel-pipeline changes)

### 2a. Auto-tone
Small model (or initially a heuristic over the histogram + class map — the class map
from M1 already tells us "this is a backlit portrait" or "landscape with 40% sky")
that proposes an `edit.Params` delta. Output is just params, so history/undo/
sidecars/hash all come free. Ship as an "Auto" button in the Edit panel.
Size: **S–M** depending on heuristic vs learned.

### 2b. Culling aids
Blur/sharpness score, closed-eye flag, near-duplicate grouping. Runs on embedded
thumbs at import (ingest must keep skipping the job-slot), writes DB columns
(schema bump), surfaces as grid badges + filter. No rendering involvement.
Size: **M**.

---

## Milestone 3 — ML denoise (builds the heavy-compute layer)

Status: **infrastructure shipped 2026-07-13, user-facing feature HELD** —
tiled inference + GPU execution providers landed and tested, but measured
throughput (93 s/MP CPU, DirectML unstable on Arc) makes the feature a trap
on this hardware class. Full data, decision, and unlock criteria in
[ml-denoise.md](ml-denoise.md).

Restoration-model denoise (NAFNet/SCUNet-class): one joint denoise + detail-recovery
pass on **scene-linear** data, before look/masks/detail. Classical sharpening stays
on top as the taste control. Explicitly *not* bundled with masks — different pipeline
stage, different compute class, different caching.

New infrastructure this milestone pays for (and super-resolution reuses):

1. **GPU execution providers** — DirectML (Windows), CoreML (macOS), CPU fallback
   everywhere (Linux GPU deferred; CUDA/ROCm packaging is not worth it yet).
2. **Tiling** — full-res inference in overlapping tiles with seam blending
   (implements the M0 stub).
3. **Denoised-master cache** — output is replacement pixels, not a mask: a full-res
   intermediate cached on disk keyed by (photo cacheKey, model ver, strength),
   analogous to Lightroom writing a Denoise DNG. Interactive edits then run on top of
   it — this touches `LinearInputsHash`, the fold path, and renderVersion, the most
   carefully tuned code in the app. Design doc required before code.

UX: explicit "Denoise" action with strength + progress (seconds on GPU, tens of
seconds on CPU), not a live slider.

Size: **L** (the largest single item on this roadmap).

---

## Milestone 4 — Super resolution

Status: **same holding pattern as Milestone 3** (Swin2SR ×2 measured at
822 s/MP CPU / 148 s/MP DML-unstable; the tiling harness already supports
Scale=2, so wiring is trivial once the throughput unlock lands — see
ml-denoise.md).

2× upscale at export. Same compute class as denoise but architecturally simple: one
extra stage in `export.renderFinal` before output sharpening, reusing M3's tiling +
GPU providers wholesale. No interactive path, no cache design (export is already an
async job with progress). Interaction to specify: SR before output resize; watermark
math must use final dimensions (respect the TS/Go twin-math contract).
Size: **M** once M3 exists.

---

## Milestone 5 — Raw-domain demosaic+denoise (exploratory)

Status: **research done 2026-07-13** — no permissive hosted model exists;
in-house training would be required. Notes in ml-denoise.md.

DeepPRIME-class: the model consumes the Bayer mosaic and does demosaic + denoise
jointly — where the dramatic high-ISO wins live. Replaces part of libraw's job:
needs raw mosaic access from `internal/libraw`, per-CFA handling, and either a
trained-in-house or licensed model (the open-model landscape is thinner here —
research this first). Treat as R&D behind M3's UX: same denoised-master cache, so if
it pans out it slots in as a better backend for the same button.
Size: **XL / research**.

---

## Cross-cutting

- **Licensing:** every model gets a license check before pinning; record in
  `THIRD_PARTY_NOTICES.md`. Apache-2.0 only unless deliberately decided otherwise.
- **Model distribution hosting:** GitHub Releases on a `marraw-models` repo (or the
  main repo's releases) — versioned, hash-pinned URLs in the registry.
- **Determinism:** ORT CPU inference is deterministic per (model, version); GPU EPs
  are not bit-exact across vendors. Cached maps/masters make this a non-issue for
  renders (pixels come from the cache, not live inference), but never hash *pixels
  produced by GPU inference* into anything that must match across machines.
- **Disk budget:** AI maps are KBs; denoised masters are ~100 MB/photo — M3 needs an
  eviction policy (LRU by last-open, cap in settings).
- **Offline:** every feature must degrade cleanly when a model isn't downloaded:
  masks render without the AI mask + warn; denoise button offers the download.

## Sequence & rough sizing

| # | Item | Size | Depends on |
|---|------|------|-----------|
| 0 | Inference foundation | M | — |
| 1a | Raster mask type + evaluator + storage | M | — (parallel with 0) |
| 1b | GenerateAIMap RPC | S–M | 0, 1a |
| 1c | All-classes curation + detection chips | S | 1b |
| 1d | Masks UI | M | 1b |
| 1e | Guided edge refinement | M | 1a |
| 2a | Auto-tone | S–M | 1b (class map) |
| 2b | Culling aids | M | 0 |
| 3 | ML denoise | L | 0 (+ design doc) |
| 4 | Super resolution | M | 3 |
| 5 | Raw demosaic+denoise | XL | 3, research |

First shippable release: **0 + 1a–1e** (AI masks, CPU-only, all platforms).
