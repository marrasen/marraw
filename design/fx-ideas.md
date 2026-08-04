# FX ideas

Jotted 2026-08-04, after settling the README positioning: marraw is a fast,
fun RAW editor for a folder of photos, not a DAM. The features it grows are
the ones that make a photo better on screen — effects and on-device ML — not
metadata, catalogs or tethering (README → "What marraw *won't* do").

This is an idea list, not a commitment. Each entry says what it is, roughly
what it costs, and what already exists to build it on.

## Where FX live today

"FX" means the per-mask spatial passes in `internal/pyramid/maskfx.go` — the
ones that *gather* neighbouring pixels, as opposed to the point-operation tone
sliders. Seven today: mosaic, blur, motion blur, zoom blur, prism, glow,
light streaks. All pure Go on CPU, banded across goroutines, computed on a
fixed 1024-long-edge working plane so a draft, a 1:1 tile and a 60 MP export
run the same gathers at the same cost.

Three invariants any new effect inherits (maskfx.go:20-40): weight-normalized
gather with gather-side erosion (so subject pixels can't ghost into the halo),
linear light via `fxLin`/`fxEnc`, and that fixed working resolution. Detail-
destroying passes replace the pixel; detail-preserving ones composite as a
difference against the full-res pixel so they don't throw away resolution the
1024 buffer can't carry.

Adding one is a well-worn path — commit `1783e31` ("Masks: two more FX dials
— glow and prism") is the 9-file template: field on `MaskAdjust`, clamp in
`Normalize`, stage in `applyMaskFX`, characterization test, regenerated client
API, `MASK_FX_ORDER` + specs, a case in `scripts/maskfx-verify.mjs`, docs.
Because every mask type resolves to a uniform weight plane, a new effect works
on AI, brush, linear, radial and range masks the day it lands.

## Per-mask effects

**Halation** — the red-shifted bloom of film: the same highlight extraction
glow already does, but tinted and with a wider radius in R than in B. Additive
delta pass, so it survives at full resolution. Cheapest real payoff on this
list, and it pairs with the look stage rather than fighting it.

**Spin blur** — tangential smear about `maskFXCenter`, the rotational twin of
zoom blur. Same gather machinery, different vector field. Nearly free given
zoom blur exists; the two together cover the whole "motion about a point"
space.

**Starburst** — the streak pass fired at N symmetric angles instead of one, a
cross-screen filter. A loop around existing code plus a count dial. Watch the
cost: N× the streak gather, and streaks already reach 50% of the long edge.

**Soften** — an edge-preserving low-pass for skin, using the guided filter
already in `guided.go` for AI mask edge-snapping. Detail-destroying, so it
feeds `fxDetailSuppression` like blur does. Pairs naturally with the person
mask; the risk is that it lands as "beauty filter" rather than "retouch" —
keep the default gentle.

**Grain** — would be the first *global* effect, and that is the problem: it
belongs in the look stage, not the per-mask FX stage, so it is a different
piece of architecture from everything above. Wanted, but not next.

## Inpainting

The MI-GAN model (28 MB, MIT, `internal/inpaint`) is already downloaded for
fill-mode retouch spots and is the most underused thing in the app — it runs
one forward pass over a crop around the mask and needs no source patch.

**Mask-driven "Remove"** — *in progress.* Any eligible mask gains a Remove
toggle: the region is inpainted from its surround. Person mask + Remove is a
one-click person eraser; brush + Remove removes anything you can paint over.
Soft or frame-sized mask types (linear, radial, depth, range) are excluded —
a gradient has no object-shaped region, and range coverage is computed from
developed pixels, so no stable patch key exists for it.

**Uncrop after straighten** — inpaint the blank wedges a straighten leaves,
instead of shrinking the crop into the frame. Cheaper than it sounds:
`ApplyGeometry` already emits those wedges with alpha < 255, so the coverage
mask is free. What stops it today is that `CropX/Y/W/H` are validated to
[0,1] — an over-frame crop is not representable — plus the client-side
`fitCropToRotation` machinery that actively prevents the rect from touching a
wedge. Parked: Marcus wouldn't use it. Kept here because the note is worth
more than the memory of having considered it.

**Outpaint to aspect** — extend the canvas to a target ratio. Same wedge
machinery as uncrop, but the region is much larger and MI-GAN resolves ~512px
across whatever it covers, so quality falls off exactly where the feature is
most visible. Parked.

## Not on this list

Anything that needs a catalog, a keyword index, IPTC fields, a tethered
camera, or an ICC printer profile. Those are the README's "won't do" section,
and the reason this file exists is to have somewhere better to put the energy.
