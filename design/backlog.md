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
- **Depth range as a two-thumb slider.** Far edge / Near edge are two
  separate sliders today; a proper range control reads better. The base-ui
  Slider supports multiple thumbs — mostly UI work in AIShapeRows.
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

- **Closed-eye detection.** License vetting done 2026-07-14; model pair
  chosen:
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
  - Remaining before implementation: mirror both weights on
    marrasen/marraw-models with SHA-256 pins (Marcus).
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
  one burst of 3) and the `neardup` shot surface. Follow-ups if wanted:
  ScrubberDeck badge, a "collapse bursts to sharpest" filter in FilterBar.
- ~~**Subject-aware sharpness.**~~ Done 2026-07-14:
  `pyramid.SubjectSharpnessScore` (matte-weighted Laplacian variance, matte
  reoriented from display to sensor frame), `subject_sharpness` column
  (schema v9, -1 = no scoreable subject), backfilled by the calibrate pass
  when a matte is already on disk and scored immediately by GenerateAIMap —
  inference is never triggered by the pass. The grid badge and soft
  threshold judge `subjectSharpness ?? sharpness`; InfoPanel shows a
  "Subject focus" row. Verified by `node scripts/subjsharp-verify.mjs
  <raw-folder>` (seeds synthetic mattes, no model download needed).

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
