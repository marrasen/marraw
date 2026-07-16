# Backlog

Small, unscheduled improvements noted while shipping the ML roadmap
(2026-07-13). None are started; none block anything. The big held items
(ML denoise, super resolution, raw-domain demosaic) live in
[ml-denoise.md](ml-denoise.md) with their unlock criteria — this list is the
smaller stuff.

## AI features

- ~~**Downloaded-models management in Settings.**~~ Done 2026-07-14 (commit
  `528a6f0`): `System.GetModelsInfo`/`DeleteModel` (`internal/api/models.go`),
  `infer.Manager.InstalledModels`/`DeleteModel`, Settings → Models section in
  `SettingsDialog.tsx`. Deleting a model re-triggers the consent dialog on
  next use. Verified by `scripts/models-verify.mjs` and the `models` shot
  surface.
- **Per-person instance masks.** Semantic segmentation lumps every person
  into one People mask; "the person on the left" needs an instance/panoptic
  model (Mask2Former-class — heavier, and license needs vetting). Deferred
  in the roadmap.
- ~~**Depth range as a two-thumb slider.**~~ Done 2026-07-14 (commit
  `e889169`): `EditRangeSlider` (EditSlider's two-thumb sibling), reset
  returns to the seed window via `DEPTH_WINDOW_DEFAULT`. Verified by the
  `depthrange` shot surface. (This entry was stale — marked done 2026-07-16.)
- ~~**Grid thumbnails can stay stale after a map restore.**~~ Done 2026-07-14:
  a per-photo cache-buster (`imgCacheBust.ts`, `b` query param — server-ignored,
  so no img hot-path cost) advances whenever `Edits.GenerateAIMap` returns
  `generated=true` (the only trigger of `Cache.InvalidateEdit`). `imgUrl`/
  `tileUrl` fold it in; `useImgBust` re-renders mounted thumbnails (grid, contact
  sheet, scrubber) so they refetch immediately, and the nonce is persisted to
  localStorage so the immutable stale entry can't resurface after a reload. The
  loupe still heals via its live preview blob. `bumpImgBust` is called from both
  restore paths in EditPanel (`runAI` and the mount effect).

## Culling

- ~~**Closed-eye detection.**~~ Done 2026-07-16: `internal/eyes` (YuNet face
  + eye landmarks at a 640² letterbox, per-eye 32×32 crops → open/closed
  classifier; photo score = max closed probability, `eyes_closed` column,
  schema v11, -1 = no judgeable face). Both weights mirrored on
  marrasen/marraw-models with SHA-256 pins; licenses recorded in
  THIRD_PARTY_NOTICES.md. Scoring backfills in the calibrate pass only once
  both models are on disk (never downloads uninvited); the consented
  download rides on `Library.AnalyzeEyes` — FilterBar's Eyes control →
  `EyeScanDialog`, the AnalyzeSubjects pattern. Client: `EyesBadge` (◡,
  ≥0.5) in GridView + ScrubberDeck, an Eyes row in InfoPanel. Empirical
  notes: the raw ONNX classifier output is **[closed, open]** — the OMZ
  README documents the reverse; the 2023mar YuNet export is fixed 640×640.
  Verified by `node scripts/eyes-verify.mjs /tmp/marraw-fixture` (consent
  gate, download, scan, sentinel) and an opt-in live test
  (`internal/eyes/live_test.go`, real portraits). Original vetting notes:
  - **YuNet** (`face_detection_yunet_2023mar.onnx`, ~350 KB) from
    [opencv_zoo](https://github.com/opencv/opencv_zoo/blob/main/models/face_detection_yunet/README.md)
    — **MIT** (README states MIT covers all files in the model dir, weights
    included; same exception class as `adeseg`, record in
    THIRD_PARTY_NOTICES.md). Detects faces + 5 landmarks including both eye
    centers, so no separate landmark model.
  - **open-closed-eye-0001** (`open_closed_eye.onnx`, ~1 MB) from
    [OpenVINO Open Model Zoo](https://github.com/openvinotoolkit/open_model_zoo/blob/master/models/public/open-closed-eye-0001/model.yml)
    — **Apache-2.0**, native ONNX, 32×32 eye-crop open/closed classifier.
    Proven pairing (FaceAiSharp ships exactly this combo).
  - Pipeline: YuNet box + eye landmarks → crop each eye at a fraction of
    interocular distance → 32×32 → classify. Treat as a soft signal like
    subject sharpness (sunglasses/profile/squint misfires).
  - Ruled out: InsightFace/SCRFD (weights non-commercial — the RMBG trap);
    MediaPipe Face Landmarker (Apache-2.0 and higher quality via
    `eyeBlink*` blendshapes, but a 3-model TFLite bundle needing conversion
    — the fallback if the 32×32 classifier is too noisy).
  - ~~Remaining before implementation: mirror both weights on
    marrasen/marraw-models with SHA-256 pins (Marcus).~~ Mirrored 2026-07-16
    (`yunet-2023mar.onnx`, `openclosedeye-0001.onnx` on the models-v1
    release, hashes verified against upstream).
- ~~**Near-duplicate grouping.**~~ Done 2026-07-14: `pyramid.DHash` (64-bit
  difference hash of the embedded thumb, computed by the calibrate pass in
  the same decode as sharpness — no extra I/O, no RAW decode), persisted as
  `phash` (schema v10). Groups are derived, never stored: `burstGroups`
  (`internal/api/neardup.go`) re-clusters on every `ListPhotos` (adjacent
  capture-ordered frames chain while ≤4 s apart AND Hamming ≤10), so
  arriving/leaving photos can't strand stale ids; `photo.groupId` = the lead
  frame's id. Client: `lib/bursts.ts` picks each group's sharpest member by
  `subjectSharpness ?? sharpness`; GridView and ContactSheet badge burst
  members (`⧉ N`, sharpest tinted success). Verified by
  `node scripts/neardup-verify.mjs /tmp/marraw-fixture` (identical copies →
  one burst of 3) and the `neardup` shot surface. ~~Follow-ups if wanted:
  ScrubberDeck badge, a "collapse bursts to sharpest" filter in FilterBar.~~
  Both done 2026-07-16: `BurstBadge` in ScrubberDeck thumbs, and a transient
  `collapseBursts` toggle (FilterBar → usePhotos) that keeps each group's
  sharpest member (lead frame until scores exist; `burstMap` moved into
  usePhotos so badges and the filter share one map). Also the "Burst
  grouping" slider ceiling was raised 30 → 64 dHash bits — at 64 the
  similarity gate is fully open and grouping is purely the ≤4 s time window.
  Plus "Auto-judge bursts" (FilterBar wand, `judgeAllBursts` in actions.ts):
  the folder-wide Shift+P — picks every burst's sharpest frame and rejects
  the rest as ONE cull-history undo entry, skipping unscored bursts and
  bursts where a non-sharpest member is already picked (a hand judgement).
  Feeds the "filter Excluded → delete" flow.
- ~~**Subject-aware sharpness.**~~ Done 2026-07-14:
  `pyramid.SubjectSharpnessScore` (matte-weighted Laplacian variance, matte
  reoriented from display to sensor frame), `subject_sharpness` column
  (schema v9, -1 = no scoreable subject), backfilled by the calibrate pass
  when a matte is already on disk and scored immediately by GenerateAIMap —
  inference is never triggered by the pass. The grid badge and soft
  threshold judge `subjectSharpness ?? sharpness`; InfoPanel shows a
  "Subject focus" row. Verified by `node scripts/subjsharp-verify.mjs
  <raw-folder>` (seeds synthetic mattes, no model download needed).

## Retouch (follow-ups to the 2026-07-16 spot-removal MVP)

The circular clone/heal spot tool shipped 2026-07-16 (commits `cf27117` +
review fixes `8c92509`): `Params.Spots`, `pyramid.ApplyHeal` (post-geometry,
pre-look, all render paths), `SuggestHealSource`, HealOverlay + Retouch group,
`Q` tool key. The data model was shaped for these next steps — `Spot.Kind`
discriminates the region shape ("" = circle) and unknown kinds are skipped at
render/normalize, so new kinds degrade gracefully in old builds.

- **Brush-shaped heal (`Kind:"stroke"`).** Paint an arbitrary region; the
  reserved `Spot.Strokes` field carries the polyline (reuse `edit.Stroke` +
  `rasterStrokes`/`stampStroke` for the coverage plane). Needs arbitrary-shape
  source matching and boundary blending — the annulus plane fit generalizes to
  a boundary-band fit, or use the guided filter (`guided.go`) for edge-aware
  seams.
- **ML content-aware fill (`Kind:"fill"`).** LaMa-class inpainting ONNX model
  slotted into the existing `infer.RunTiled` + `ModelSpec` download/consent
  infra (the aimask.Generate pattern). Open questions: model licensing (the
  RMBG trap — vet before mirroring), ~200 MB weights, and that fills are not
  parametrically re-renderable — output must be cached per (photo, edit) like
  AI maps (AIMapStore precedent), with a cache-buster on regenerate.
- **Spots in the RAW + XMP handoff.** Translate circular spots to Adobe
  `crs:RetouchAreas` (intent-level, like the existing slider mapping) in
  `internal/xmp` so Lightroom picks up the dust fixes.
- **MVP polish candidates, after field testing:** per-spot opacity is plumbed
  but has no keyboard path; the auto source picker only probes 3 rings × 16
  angles (could search smarter on busy textures); no "visualize spots"
  desaturated view for finding dust (Lightroom's A key).

## Pre-existing (not from the ML work)

- **5 environmental ui-verify failures** (thumbSliderWidth, contrastSteps,
  autoButtons, cropFitsAngle, crop-reset fatal timeout) — reproduce on
  pre-ML baseline 7bdff31, so they're machine/environment drift, not code
  regressions. The crop-reset fatal also leaves a stray 0.2 crop persisted
  on the probe photo. Needs its own debugging session.
- ~~**Focus-prioritized pre-render order**~~ Done 2026-07-14: the background
  `prerenderPass` (and opt-in `fullresPass`) now render outward from the
  client's focused photo instead of front-to-back. New `Library.SetFocus`
  RPC stores `Deps.focusPhotoID`; `scheduleOutwardFromFocus` runs the pool
  workers off a shared remaining-set, each claim picking the uncached photo
  nearest the current focus (re-read per claim, so the order tracks live
  navigation). The frontend fires `setFocus` from `Workspace` whenever
  `uiStore.focusId` changes. Unit-tested by the distance-non-decreasing
  invariant in `jobs_test.go`.
