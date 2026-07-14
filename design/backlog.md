# Backlog

Small, unscheduled improvements noted while shipping the ML roadmap
(2026-07-13). None are started; none block anything. The big held items
(ML denoise, super resolution, raw-domain demosaic) live in
[ml-denoise.md](ml-denoise.md) with their unlock criteria — this list is the
smaller stuff.

## AI features

- **Downloaded-models management in Settings.** Consent-gated downloads can
  accumulate ~1.6 GB under `<dataDir>/models` with no in-app way to see or
  delete them. A "AI models" row in the Settings cache section (list, size,
  delete per model) fits the existing GetCacheInfo pattern. Deleting a model
  just re-triggers the consent dialog on next use.
- **Per-person instance masks.** Semantic segmentation lumps every person
  into one People mask; "the person on the left" needs an instance/panoptic
  model (Mask2Former-class — heavier, and license needs vetting). Deferred
  in the roadmap.
- **Depth range as a two-thumb slider.** Far edge / Near edge are two
  separate sliders today; a proper range control reads better. The base-ui
  Slider supports multiple thumbs — mostly UI work in AIShapeRows.
- **Grid thumbnails can stay stale after a map restore.** Cache.InvalidateEdit
  fixes the server files (and so the loupe), but /img URLs are browser-cached
  immutable and keyed by edit hash, which a map regeneration doesn't change.
  Only bites sidecar-import flows; self-heals on the next edit. A real fix
  needs a cache-buster component in the URL for map-bearing edits.

## Culling

- **Closed-eye detection + near-duplicate grouping.** The remaining culling
  aids from the roadmap; both want a small face model (license-vet first).
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
- **Focus-prioritized pre-render order** — the remaining fit-loupe lead:
  background pre-render should prioritize outward from the focused photo.
