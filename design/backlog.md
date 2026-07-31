# Backlog

Small, unscheduled improvements noted while shipping the ML roadmap
(2026-07-13). None are started; none block anything. The big held items
(ML denoise, super resolution, raw-domain demosaic) live in
[ml-denoise.md](ml-denoise.md) with their unlock criteria — this list is the
smaller stuff.

## Editing (from the README "what marraw does not do" list)

- ~~**No lens profile corrections.**~~ Done 2026-07-29: distortion, lateral
  chromatic aberration and vignetting are now corrected from a matched lens
  profile, on by default. New `internal/lens` package: the Lensfun
  calibration database (CC-BY-SA 3.0) distilled by `tools/lensdb` into a
  437 KB gzipped JSON blob embedded via `go:embed` (1045 cameras, 1555
  calibrated lenses), plus a from-scratch Go implementation of Lensfun's
  math — the poly3/poly5/ptlens distortion models, poly3/linear TCA, the
  "pa" vignetting model, the Hermite-spline interpolation over focal length,
  the inverse-distance weighting over the (focal, aperture, distance)
  vignetting cloud, and the coefficient rescaling between Hugin's, "pa"'s
  and the normalized coordinate systems. No link against liblensfun.
  `pyramid.ApplyLens` runs FIRST in every render path — before
  `ApplyGeometry`, and before `lookGammaFor`, so the camera-mimic tone
  calibration compares a devignetted render against a camera JPEG that is
  also devignetted. Output dims are preserved via a bisection auto-scale
  (lensfun's `GetAutoScale`, with bisection replacing Newton so ultra-wide
  fits can't diverge). `edit.Params` gains `LensMode` (""=auto / "off") and
  `LensDistortion`/`LensVignetting`/`LensCA` as ±1 offsets from the
  profile's own figure, all `omitempty` so pre-existing edits keep their
  bytes — and, because the zero value means "corrected", *start* corrected.
  `Edits.LensProfile` reports the match to a Lens section in the Detail
  group. Verify: `node scripts/lens-verify.mjs /tmp/marraw-fixture` and
  `node scripts/shot.mjs /tmp/marraw-fixture lens`.

  **Design notes worth keeping:**
  - **Matching is deliberately stricter than Lensfun's.** Upstream takes the
    best fuzzy score whatever it is; a wrong profile silently warps every
    pixel, so here nothing the EXIF asserts may be contradicted by the
    candidate (focal range, descriptive words and remaining numbers must all
    be subsets), and a tie matches nothing. The case that motivated it:
    Nikon's 24-70/2.8 G and E VR are different optical designs whose names
    are near-subsets, and score-based matching picks the G for an E VR file.
  - **Fixed-lens compacts record no lens string at all.** They resolve
    through the body's pseudo-mount instead, guarded on that mount having
    exactly one lens — a real mount like "Sony E" has hundreds and never
    fires. The dev fixture (Panasonic DC-LX100M2) is one of these, so the
    verify script covers this path and not the interchangeable one.
  - **Vignetting is corrected in LINEAR light** (`linearCodec`, via the same
    dcraw gamma the decode carries). Applying the gain to gamma-encoded
    values roughly doubles the correction in stops — it looks plausible on
    screen and is wrong.
  - **An unknown camera body means no correction at all**, even for a lens
    that matched: without the body's crop factor there is no way to place
    the coefficients in the frame. That is also why `Correction` stores crop
    and real focal rather than a `NormScale` — it makes the resolved profile
    independent of render size, so one lookup serves every pyramid level.
  - **Cost:** ~1.1 s for a 42 MP frame (8 threads, i7-6700K), so the 1:1 and
    export paths pay it in full. The interactive path does not: `RenderPreview`
    downscales the frame first (to `previewFrameEdge`, which is larger than
    longEdge when a crop is set, so the crop still lands at full preview
    size) and corrects there, and the fold path corrects the already-
    downscaled `foldScale` buffer. `renderVersion` r8 → r9.
  - **Still absent:** axial CA / defringe, which no profile can describe.

- ~~**No luminance or color range masks.**~~ Done 2026-07-28: a new
  `MaskRange` ("range") mask type selects pixels by their own **developed
  value** — a soft luminance band-pass times a soft hue band-pass gated by a
  saturation floor — rather than by geometry or a model map. Coverage is
  computed analytically from the render's own pixels (no cached plane), reusing
  the seam `guidedEval` uses to read the image and the depth-window smoothstep
  from `deriveCoverage`; `ApplyMasks` snapshots the post-Look image once when a
  range mask is present so selection is independent of mask order. Fields
  (`RangeLumaLo/Hi`, `RangeHueLo/Hi`, `RangeSatMin`, reusing `Feather`) are
  `omitempty` so non-range masks stay byte-identical; the luma pair reorders in
  Normalize, the hue pair does **not** (it's circular — Hi < Lo wraps through
  red). The tint (`MaskTintPreview`) develops a masks-stripped preview for
  range masks (their coverage needs pixels, unlike parametric/AI); the client
  routes range through the same server-PNG path as AI. An **eyedropper**
  (`Edits.PickRangeColor`, mirroring `PickWhiteBalance`) samples the developed
  colour and seeds the hue window ±0.045 + a saturation floor. Client: a Range
  add button, `RangeShapeRows` (luminance two-thumb, hue centre on a rainbow
  track + hue range, min-saturation, feather, eyedropper), `rangePicking`
  session state + `esPickRangeColor` (WB-picker pattern). Range masks travel in
  presets like AI masks (content-relative, no model). Verified: `go test
  ./internal/edit ./internal/pyramid ./internal/api` (Normalize + coverage +
  hue-seeding), `tsc -b`/eslint, and `node scripts/rangemask-verify.mjs
  /tmp/marraw-fixture` (render/invert/hue-window/eyedropper end-to-end).
  **Design note:** hue is edited as centre + range, not a two-thumb band — a
  linear two-thumb slider can't express a window that wraps the 0/1 (red) seam,
  which the eyedropper produces for reds. **Footgun:** aprot blob RPCs
  (`MaskTintPreview`) return over the WS as **binary** frames — a JSON-only
  hand-rolled WS probe silently never resolves them (looks like a server hang;
  it isn't). The tint is covered by the app + the `MaskWeightPlane` unit tests.
- ~~**No tone curve.**~~ Done 2026-07-28: a point **tone curve** now composes
  into the look stage. `edit.Params.ToneCurve []CurvePoint` (`json:"toneCurve,
  omitempty"`, after Spots — the Masks/Spots byte-identical-when-empty
  precedent, kept out of the subset hashes, non-comparable so IsNeutral uses
  DeepEqual); `Normalize` sorts/clamps/quantizes and folds an identity
  (all-diagonal or <2-point) curve to nil; `HasToneCurve` gates the render.
  `pyramid.buildCurveLUT` samples the curve with monotone-cubic
  (Fritsch–Carlson) interpolation into the existing `[256]` look LUT, remapping
  the developed value after the parametric tone and before saturation, so the
  monotone clamp still guarantees no tone inversion. Presets carry it
  (`presetLook` tone block + `presetSections.ts` `PRESET_FIELDS`, treated like
  `wbMul` — position-valued/absolute). Client: `lib/toneCurve.ts` (client
  mirror of the curve math for the preview line + the dirty check) and a
  `ToneCurve` SVG widget in the Tone group (drag/add/double-click-remove,
  Reset folds to neutral), modeled on `ColorMixer`. Verified: `go test
  ./internal/edit ./internal/pyramid` (new curve LUT + Normalize tests),
  `tsc -b` + eslint, and `node scripts/shot.mjs /tmp/marraw-fixture tonecurve`
  (a midtone-lift curve brightened center luma 79→129, Reset → neutral).
- ~~**No per-channel R/G/B curves.**~~ Done 2026-07-28, same day, on the same
  plumbing: `ToneCurveR/G/B []CurvePoint` (same storage rules as the master),
  `CurveBends` extracted as a shared predicate with `HasToneCurve` (master) and
  `HasChannelCurves` (any channel). `buildLookLUT` is unchanged — the new
  `pyramid.buildLookLUTs` builds the master once and composes each channel's
  curve on top, returning three LUTs; **with no channel curve all three are the
  same array**, so channel-free edits stay byte-identical and the pixel loops
  cost the same (they already did three lookups, just into one array).
  Composition is exact (the master's output is an integer 0..255 that indexes
  the 256-sample channel curve directly) and monotone-through-monotone stays
  monotone, so the no-inversion invariant holds per channel. `applyLookSimple`/
  `applyLookFull` take `lutR, lutG, lutB` (the `buildMaskLUTs` idiom). Client:
  `CURVE_CHANNELS`/`CURVE_KEYS`/`curveOf` in `lib/toneCurve.ts`, RGB/R/G/B tabs
  in the widget (per-channel dot when bent, unselected channels drawn as faint
  guides, Reset clears only the selected channel). Verified by the extended
  `tonecurve` shot surface: a red-lift curve took the center pixel
  `[128,129,133]` → `[210,125,129]` (R−B −5 → 81) with G/B held, Reset cleared
  only R and kept the master.
  **Footgun that cost a debug cycle:** a stale `marrawd` from an earlier
  `npm run dev` still held :8483, so the new server silently failed to bind
  ("address already in use", only in the dev log) and the harness talked to the
  OLD binary — which dropped `toneCurveR` as an unknown JSON field. Symptom:
  the client draft carried the curve but pixels were byte-identical. Check
  `ss -ltnp | grep 8483` (and that the log says "listening") before believing a
  backend change didn't take.

## AI features

- **Disc bokeh for the mask blur.** The mask FX defocus
  (`internal/pyramid/maskfx.go`) is a 3-pass separable box, so out-of-focus
  highlights render as soft blobs where a real lens renders discs. The light
  streaks carry the look today; a true 2D disc gather (or the rotated-box
  trick) would make the defocus itself convincing. Deliberately deferred — the
  separable blur is O(px) per pass at any radius, a disc gather is not.
- **Zoom/spin blur.** `MaskAdjust.ZoomBlur` is 0..1 (zoom only). Widening the
  clamp to ±1 and flipping the tap offset's sign gives spin; back-compat safe
  in both directions (old sidecars never hold a negative, old builds clamp one
  away).

- ~~**Downloaded-models management in Settings.**~~ Done 2026-07-14 (commit
  `528a6f0`): `System.GetModelsInfo`/`DeleteModel` (`internal/api/models.go`),
  `infer.Manager.InstalledModels`/`DeleteModel`, Settings → Models section in
  `SettingsDialog.tsx`. Deleting a model re-triggers the consent dialog on
  next use. Verified by `scripts/models-verify.mjs` and the `models` shot
  surface.
- ~~**Per-person instance masks.**~~ Done 2026-07-27 (commit `15d4aa3`):
  new `AIKind "person"` whose 1024px map plane stores instance IDs (0 = bg,
  1..N left-to-right by centroid; `ClassID` doubles as instance index — no
  schema change). Model: RF-DETR-Seg-Large (Apache-2.0 code + weights;
  Mask2Former weights are CC-BY-NC, YOLO-seg/FastSAM are AGPL — both
  rejected), exported to ONNX (`rfdetrseg-1`, 138 MB, ~1 s CPU) and mirrored
  on marrasen/marraw-models. `internal/aimask/instances.go` composes the
  plane and detects instances; the client hit-tests hover locally via the
  `Edits.AIInstancePlane` blob RPC (`PersonPickOverlay`, People button +
  person chips in EditPanel). Presets keep person masks ("Nth from left").
  Verified by `node scripts/person-verify.mjs /tmp/marraw-fixture` and the
  `personpick` shot surface.
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

- ~~**Brush-shaped heal (`Kind:"stroke"`).**~~ Done 2026-07-16: paint an
  arbitrary region (Retouch → Brush tool); `Spot.Strokes` carries the
  polyline, rasterized through the shared brush-mask coverage plane
  (`brushPlaneFor`/`brushEval`, so previews/tiles/export agree by
  construction), and the annulus plane fit generalized to a boundary-band fit
  (`fitStrokeBandPlanes`: separable dilation of the coverage at plane
  resolution picks the clean ring just outside the paint; membranes fit on
  dest + translated source). Source region = painted region translated by
  (SX−CX, SY−CY); `SuggestHealSource` reduces the region to its enclosing
  circle (`StrokeSpotCircle`) and returns CX/CY too. One paint gesture = one
  spot; dest drag translates the strokes. Verified by `go test` stroke cases,
  `scripts/spot-verify.mjs` section 5, and the `healbrush` shot surface.
- ~~**ML content-aware fill.**~~ Done 2026-07-27, as `Mode:"fill"` (not a
  kind — it composes with both circle and brush regions): MI-GAN-512-Places2
  (MIT code+weights, ~28 MB — LaMa's weights are CC BY-NC-SA and were
  rejected), the authors' self-contained ONNX pipeline (uint8 image+mask in,
  crop/resize/blend inside the graph), mirrored as `migan-pipeline-v2.onnx` →
  `migan-1.onnx`. New `internal/inpaint` (aimask pattern, not RunTiled),
  `pyramid.FillStore` under `<dataDir>/fills` (256 MB LRU disk cap — the
  janitor doesn't reach it), patches keyed by `edit.SpotFillKey` (spot
  geometry + decode-subset hash + orientation, so WB changes regenerate),
  `FillSet` threaded through `ApplyFinish`'s five call sites, composite in
  `pyramid.applyFill` via the shared feather/opacity blend.
  `Edits.GenerateFill`/`FillModelStatus`; the client ensures patches after
  every commit (fast-path no-op), consent rides the AIModelDialog. Design
  notes in design/ml-fill.md; verify with `node scripts/fill-verify.mjs
  /tmp/marraw-fixture` + the `healfill` shot surface.
- **Spots in the RAW + XMP handoff.** _Assessed 2026-07-27 and **deferred** (not
  a code problem — a verifiability one)._ Translating circular spots to Adobe
  `crs:RetouchAreas` needs (1) a real writer change: `internal/xmp` is
  attribute-only by design and explicitly drops nested `rdf:Seq` structures (the
  same reason local-adjustment masks aren't exported), and RetouchAreas *is* such
  a structure; and (2) empirical calibration of the coordinate space, radius
  normalization, and feather/opacity ranges against a **real Lightroom-authored
  `.xmp`** — none exists in `testdata/`, and `cropToNative` already admits its
  own crop conventions are "NOT yet round-tripped through a real Lightroom." So we
  can't verify the output without a Lightroom instance, which cuts against the
  app's whole premise. Brush strokes map only lossily (Lightroom's per-mask single
  radius vs. per-stroke radii) and fill-mode has no XMP representation at all.
  Revisit only if a Lightroom calibration fixture becomes available; circles-only
  would then be ~a day's work once the writer supports nesting.
- ~~**Smarter heal-source picker.**~~ Done 2026-07-27: `SuggestHealSource`
  (`internal/pyramid/heal.go`) no longer scores candidates by a 32-point
  two-ring colour signature (which matched *mean colour*, ambiguous on busy
  grain). It now compares a **mean-subtracted dense annular texture patch** (3
  rings × 24 angles just outside the spot, per-channel mean removed so a
  brightness offset between donor and destination — which the render-time plane
  fit corrects anyway — doesn't mislead the search), over a wider coarse
  candidate set (4 ring distances × 24 angles), then **hill-climbs the best hit
  locally** (8-neighbourhood, halving step). Deterministic sampling order and the
  in-frame / clears-the-disc invariants are preserved. New busy-texture unit test
  (`TestSuggestHealSourceTexture`, stripes-over-a-brightness-ramp: the donor must
  keep the spot's stripe phase despite the competing ramp) locks in the texture
  awareness; `TestSuggestHealSource` and `scripts/spot-verify.mjs` still pass.
- **MVP polish candidates, after field testing:** ~~the auto source picker only
  probes 3 rings × 16 angles (could search smarter on busy textures)~~ done — see
  above. ~~Per-spot opacity keyboard path~~ and ~~"visualize spots" dust view~~ done
  2026-07-16: digits 1-9/0 set the selected spot's opacity while healing, and
  A toggles `SpotVisualizeLayer` (client-side high-pass relief of the shown
  rendition, sensitivity slider in the Retouch group; `spotvis` shot surface).

## Pre-existing (not from the ML work)

- ~~**ui-verify failures.**~~ Debugged and fixed 2026-07-27. The failures that
  actually reproduced on this Linux box were three, and each had a concrete
  cause in the harness rather than the product:
  - `thumbSliderWidth` (`width=0`) — the inline thumbnail slider is *designed*
    to hide on a tight FilterBar (`@max-[1040px]` container query), and the
    300px EditPanel (open by default) plus the left rail pushes the bar past
    that breakpoint. The renderer now closes the EditPanel for the measurement
    (the "roomy bar" precondition) and restores it after.
  - `positionKept` (`focus at -1`) — the check excluded `visibleIds[9]` and
    asserted focus lands on the photo that shifts into slot 9, which needs ≥11
    photos; the dev fixture has 3. Generalized to exclude a photo that still has
    a successor (`min(9, len-2)`), so it's meaningful on any library size.
  - export `fatal` (`mkdir /tmp\marraw-uitest-export: permission denied`) — the
    export destination hardcoded a `\\` separator, a literal filename on POSIX.
    Switched to `/` (Go's `MkdirAll` accepts it on Windows too).
  The renderer also now pins Library grid up front so the early presence checks
  don't depend on the folder's persisted last view. `contrastSteps`,
  `autoButtons`, `cropFitsAngle` and the crop-reset path all pass; ui-verify is
  green twice consecutively. (The old note listed 5 different checks from an
  earlier baseline — the set had drifted.)
- ~~**Focus-prioritized pre-render order**~~ Done 2026-07-14: the background
  `prerenderPass` (and opt-in `fullresPass`) now render outward from the
  client's focused photo instead of front-to-back. New `Library.SetFocus`
  RPC stores `Deps.focusPhotoID`; `scheduleOutwardFromFocus` runs the pool
  workers off a shared remaining-set, each claim picking the uncached photo
  nearest the current focus (re-read per claim, so the order tracks live
  navigation). The frontend fires `setFocus` from `Workspace` whenever
  `uiStore.focusId` changes. Unit-tested by the distance-non-decreasing
  invariant in `jobs_test.go`.
