# Tilt shift — depth-graded defocus as a first-class effect

Drafted 2026-08-09, **built the same day**. The depth model already shipped
(Depth Anything V2 Small, `depthany2s-1`) and its map already drove
depth-range masks. This turns the same map into a proper depth-of-field
effect: a global develop control that keeps a chosen depth band sharp and
blurs the rest, with blur radius growing with distance from the band.

Where the build diverged from the plan is recorded inline below, under
**Built:** notes. The two that matter: the coc plane is produced by
synthesizing the AI depth mask the effect describes and running it through
the existing mask evaluator (far more reuse than planned), and the blur
levels compute at per-level resolutions rather than all at 1024.

## Why a separate effect, not "depth mask + Blur"

The assembled route exists today — add a depth AI mask, set the window to
what should stay *blurry*... except it can't be spelled that way: the mask
covers a band and `Blur` defocuses through its coverage at a **single
radius**. That yields two zones with a feathered seam, not graded defocus.
The things a real DoF look needs, and a dedicated stage can have:

1. **Per-pixel radius from a circle-of-confusion ramp.** Blur should be ~0 at
   the window edge and grow with depth distance, saturating at the Amount
   dial. One mask + one radius cannot express the ramp; stacking several
   depth masks at staggered radii could approximate it, but nobody will.
2. **Depth-aware gather weights per blur level.** When computing the blurred
   planes, in-focus pixels must not contribute to out-of-focus gathers, or
   the sharp subject's colour halos into the blurred background. The mask-FX
   weight-normalized gather (`maskfx.go` invariant #1) is the right machine,
   but the weight has to be the coc plane itself, not a mask's coverage.
3. **One control set.** Amount + an in-focus range slider, in the Effects
   group. The mask route needs: add mask → consent → invert your thinking
   (window = what blurs) → set feather → set blur. Nobody discovers that.

Naming note: photographic "tilt shift" is the miniature fake — a *positional*
band of sharpness. With real depth we get true DoF simulation, which
subsumes it (a depth window on a scene shot from above reads exactly like
the miniature effect). UI label stays "Tilt shift"; it's the name people
reach for.

## Parameters

Three floats and a version stamp on `edit.Params` (`internal/edit/edit.go`),
appended after `BW`, all `omitempty` per the hash-stability rule stated at
edit.go:419-427 and pinned by the JSON back-compat tests:

- `TiltAmount float64` `json:"tiltAmount,omitempty" validate:"gte=0,lte=1"` —
  max blur strength; 0 = effect off (the gate).
- `TiltLo, TiltHi float64` `json:"tiltLo,omitempty"` / `tiltHi` — the
  in-focus depth window, 0..1 in the map's normalized inverse-depth space,
  **1 = nearest**, same convention as `Mask.DepthLo/Hi`.
- `TiltMapVer string` `json:"tiltMapVer,omitempty"` — model version stamped
  by the client on enable (the `MapVer` precedent, edit.go:216-225). It
  enters `Params.Hash()` so a model upgrade re-renders, and it tells the
  render which map file to load.

`Normalize()` additions: clamp lo/hi to 0..1, `quant4`, swap if `hi < lo`
(the `normalizeMasks` depth case at edit.go:900-906 is the template), and —
following the split-tone precedent at edit.go:674 — `TiltAmount == 0` zeroes
the other three so a visually-neutral edit hashes neutral. Predicate:
`HasTilt() = TiltAmount > 0 && TiltMapVer != ""`, nil-safe like `IsBW()`.

Falloff (how fast blur ramps outside the window) is a **fixed constant** in
v1, not a dial. A dial can come later but needs bipolar encoding (0 = the
default) to survive `omitempty`; don't spend a slider on it before the fixed
value proves inadequate.

Keep tilt fields **out of** `LibrawInputsHash` / `LinearInputsHash` — they
are hand-listed subsets (edit.go:1096, :1122), so exclusion is automatic,
but add the fields to the exclusion-pinning tests so a slider drag keeps the
warm decode and the scene-linear fold path.

## Render stage

New file `internal/pyramid/tilt.go`, `ApplyTilt(img, e, maps, ...)`, slotted
in `ApplyFinish` between `ApplyBW` and `ApplyDetail` (look.go:109-115), plus
the four other agreement points:

- the inline stage-for-stage mirror in `cache.go:550-572` (with a progress
  tick),
- forced **off** in the two frames that already force `BW` off:
  `wbPickFrame` (edits.go:709) and `developedBaseForMask` (edits.go:869) —
  WB sampling and mask/fill bases must never see gathered pixels.

`RenderPreview`, `RenderPreviewLinear` and export all funnel through
`ApplyFinish`, so they come for free once the `AIMapSet` is populated (next
section).

### Algorithm

Inherits the three mask-FX invariants (maskfx.go:12-104): linear light via
`fxLin`/`fxEnc`, weight-normalized gather, and the fixed 1024 working plane
so draft, settle, 1:1 tile and export compute the identical effect.

1. **Coc plane.** Sample the oriented depth map (via `orientMap` +
   `maskFrame`/`samplePlane`, exactly as `aiEval` does) onto the 1024 work
   grid and derive `c = 1 − band(d)` where `band` is the same smoothstepped
   window as depth-mask coverage (`deriveCoverage`, aimap.go:343-355) with
   the fixed falloff as feather. `c` is the normalized circle of confusion:
   0 in focus, 1 at max defocus.

   **Built:** rather than sampling the map, `ApplyTilt` synthesizes the AI
   depth mask its params describe (`tiltMask`) and runs it through
   `newAIEval` + `weightPlaneFrom`, then inverts the plane in place. The
   whole registration chain — oriented frame, crop, straighten, the guided
   refinement, the derived-coverage LRU — is then literally the code an AI
   depth mask uses, and a hand-added depth mask with the same window shares
   the cached plane. It needed one change to the shared evaluator: a
   `clampEdge` flag on `brushEval`, because the plane's outermost pixel
   centres sit half a plane-pixel inside the frame and a *global* field read
   through it would otherwise fade to fully-defocused in a thin border all
   the way around the photo. Masks keep the old read-outside-as-zero.
2. **Graded blur, K levels.** K≈3 blurred copies of the linear work plane at
   radii `r·{⅓, ⅔, 1}`, `r = TiltAmount · fxBlurFrac · workLong`, using the
   iterated separable box from `fxBlur` — but with gather weight
   `w = min(c_src / c_level, 1)` so in-focus pixels don't bleed into
   defocused layers (the anti-halo property, point 2 above). This is
   `fxSource`'s Σw·c/Σw with the coc plane as the weight plane.

   **Built:** the per-level weight alone was not enough. The depth map
   resolves an object boundary over a few soft pixels while the photo
   resolves it exactly, so the sliver the map calls half-defocused still
   holds the SUBJECT's colour and a plain weighted gather spread it a whole
   radius into the background — the ghost, at ~4% of a white subject 30 px
   in. Fixed with a gather-side erosion (`tiltErode`): the coc a level
   gathers with is the local MINIMUM over 2 px, so a pixel lends its colour
   to a defocus gather only if everything around it is at least that
   defocused. A local minimum rather than fxSource's blur-and-knee because
   the knee is tuned for a 0-or-255 matte and on a smooth depth ramp would
   bias every gather toward its deeper neighbours — a directional smear
   across the whole gradient.
3. **Composite at output resolution.** Per output pixel, sample `c` — plain
   bilinear for ≤1024 drafts, guided-filter refined (`guidedEval`,
   guided.go:38-49) at ≥2048 so the low-res map doesn't read soft or
   misregistered on exports — then lerp between the adjacent blur levels by
   `c` and replace the pixel. Destructive pass, so also emit `c` as a
   detail-suppression plane, merged (max) with the one `ApplyMasks` returns,
   so sharpen/clarity/texture don't re-crisp the defocus.

Known physical omission, accepted for v1: real foreground bokeh spills
*over* in-focus regions behind it; scatter is out of scope. Box-iterated ≈
gaussian bokeh for now — the backlog's "disc bokeh for the mask blur"
(backlog.md:140) upgrade would serve both consumers when it happens.

Cost: K box-blur passes on a fixed 1024 plane — a few ms, same envelope as
the existing FX. Bounded regardless of output size, so guest-reachable
renders stay bounded; no decode-path change, so cull navigation is
untouched.

**Built:** "a few ms" was wrong by an order of magnitude — three levels of
four planes at 1024 measured **50 ms**, on every frame of a slider drag
(a whole mask defocus is 27 ms). The fix is `tiltLevelDims`: fxPlaneLongEdge's
own argument — a defocused region carries no detail finer than its blur
radius — applies per LEVEL, not once for the stage, so each level computes at
whatever resolution puts its radius on ~12 px, capped at the shared working
buffer. The sizes depend only on the effect's reach and the frame's aspect,
never on the render's resolution, so the draft/settle/tile/export agreement
holds. Levels dropped to ~10 ms and the whole stage to 46 ms at 1024
(`BenchmarkTilt1024`). A small dial stays at the working buffer and still
costs the old 50 ms — bounded by what it was, and the case where the effect
is least visible.

At 24 MP the stage is ~750 ms, of which ~475 ms is the guided-filter
refinement — the standard AI-mask edge-refinement cost, paid identically by a
depth mask on an export.

### Map plumbing

`AIMapStore.SetFor` (aimap.go:172) loads only maps referenced by AI
*masks* — extend it to also load `depth@TiltMapVer` when `e.HasTilt()`.
Missing file ⇒ render proceeds without tilt, the same silent-degrade
contract masks have. When generation completes, `Cache.InvalidateEdit`
already fires for photos whose edit hash is non-base (aimasks.go:390-392);
a `HasTilt` edit is non-base by construction — verify that path re-renders
the loupe when the map lands.

No `renderVersion` bump: new gated fields, existing renders byte-identical.

## Client

`npm run gen` after the Go wire change (never hand-edit `client/src/api`).

**UI**: the Effects group (`EditPanel.tsx:672`, today just Vignette) gains a
Tilt shift block: `AmtSlider` for Amount, `EditRangeSlider` (controls.tsx:307,
the depth-mask widget) for the in-focus window, seeded like
`DEPTH_WINDOW_DEFAULT`. Enabling with no map on disk runs the
generate-with-consent flow — factor `addAI`/`runAI` out of
`MasksSection.tsx:119-186` into a shared helper rather than duplicating the
consent dialog logic.

**Built:** the shared helper is `useAIMapGate` (a hook: consent check,
dialog, generate, then a caller-supplied callback), and `MasksSection` was
converted onto it rather than left duplicating the flow. The Effects rows
live in `TiltShiftRows`, which renders a single "Blur by distance" button
while the effect is off — there is nothing to drag until a map exists — and
swaps in the two sliders once it is on. Clearing writes all four params to
neutral so the panel reads the same as what the server stores.

**Registration checklist** (the B&W / 60329a6 template): `NEUTRAL`,
`PARAM_LABELS` in `controlSpecs.ts`; the `effects` key list in `groupChanged`
(EditPanel.tsx:375); `isDefault` absent-or-zero handling for the omitted
fields; `'Tilt shift'` in `ADD_REMOVE_LABELS` (editLabels.ts:12) so undo
reads "Add tilt shift".

**Built:** tilt is deliberately NOT a `ControlId`, so it gets no keyboard
walk, no dial, and no `CONTROL_GROUP`/`dials.ts META` entry. A `ControlId`
promises a control you can step up from zero, and this one cannot: the first
step needs a model run and a consent dialog. AI masks are outside that
machinery for the same reason. All four params share one `PARAM_LABELS`
entry (the split-tone precedent) so switching the effect on is one undo
entry that reads "Add tilt shift" — which also needed `paramIsDefault` to
treat an absent field as neutral, since the server omits all four when off.

**Presets**: `PRESET_FIELDS` is compiler-enforced exhaustive — `tiltAmount`
`'add'`, `tiltLo/Hi/MapVer` `'absolute'`; mirrored in the hand-written
server copy `presetapply.go` under the `effects` section. Caveat commented in
both places: depth is min-max normalized **per photo**, so a copied window
lands on a different physical distance in the target photo. Accepted — it
still lands on "roughly the near/far band you meant". A preset that carries
tilt onto a photo with no depth map renders without the effect until
`esEnsureAIMaps` produces one, which is why that function now asks for the
depth map on `HasTilt` and why `esApplyParamsPreview` (the preset path) now
calls it at all.

**Out of the loop**: `edit.Delta` (batch nudge), `SUGGESTION_KEYS`, XMP
(`crs:` has no equivalent; the param rides the `.marraw.json` sidecar blob
automatically, like masks).

## Verification

- `internal/pyramid/tilt_test.go` — inject a synthetic gradient depth map
  into the store; assert: in-window pixels unchanged within ε, local
  variance drops monotonically with depth distance, no colour bleed across
  a hard sharp/blurred edge (the anti-halo gather), and the fixed-plane
  invariant (1024 vs 2048 renders agree on the effect).
- `internal/edit/edit_test.go` — omitempty back-compat, `IsNeutral` rows,
  normalize (swap/quant/zero-on-amount-0), decode-hash exclusion.
- `scripts/tilt-verify.mjs` — real fixture over the aprot RPC: generate the
  depth map, set params, export, measure near/far local variance.
- `scripts/shot.renderer.js` — `tiltshift` surface with a `__tiltProbe`,
  driving the panel's own controls: enable, assert consent flow, drag the
  window, assert the frame changes and the group dot lights.

**Built:** all four exist and pass. `scripts/tilt-verify.mjs` earned one
lesson worth keeping: **never compare a render against the NEUTRAL
baseline.** An unedited level under 1024 is derived from the camera's
embedded JPEG rather than rendered from the RAW, so it shares no pixels with
anything that went through the develop pipeline — every "the stage changed
the render" check passes whether or not the stage did a thing, and every
"the stage was a no-op" check fails. The script renders a trivially edited
reference and compares against that.

## Status

Built and verified 2026-08-09 on a real RAW fixture: 16/16 backend checks
(`scripts/tilt-verify.mjs`), the `tiltshift` shot surface, Go and client
unit suites. A full-resolution export drops from 1.51 MB to 0.81 MB with the
effect on — the defocus is real at export resolution, not just in the
preview.

## Later, if wanted

A focus *picker* (click the image → new RPC samples the depth map at that
point, centers the window — the `PickWhiteBalance` precedent), a focus-band
overlay while dragging (server tint à la `MaskTintPreview`), a falloff dial
(bipolar-encoded), disc bokeh shared with the mask blur, and the scatter pass
that would let a defocused foreground spill over the sharp subject behind it.
