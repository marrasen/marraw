package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"math"
	"os"
	"sync"
	"time"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/aimask"
	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// Edits handles non-destructive editing.
type Edits struct {
	deps *Deps

	// decodeCache holds the most recent half-size RAW decode for the preview
	// path, keyed by photo + LibRaw-input hash. Crop, straighten and the
	// look-stage sliders don't change the decode, so while one of them is
	// dragged this lets every preview skip the ~400 ms demosaic and re-run
	// only the cheap post-decode stages. One entry: the current hot photo and
	// LibRaw state; it is replaced when either changes.
	decodeMu    sync.Mutex
	decodeEntry *decodeCache

	// linEntry holds the most recent scene-linear reference decode for the
	// fold path, keyed by photo + the pre-demosaic inputs only. WB, exposure,
	// brightness and gamma fold onto it post-decode, so dragging any of them
	// reuses this and skips the ~400 ms demosaic entirely. One hot entry,
	// replaced when the photo or a pre-demosaic control changes.
	linMu    sync.Mutex
	linEntry *linCache

	// pickEntry holds the frame the WB picker samples, pinned to the draft as
	// it was when the picker opened. Every click during one picking session
	// reads this same frame, so two nearby picks are comparable and re-picking
	// a spot is idempotent — sampling the LIVE preview instead would fold each
	// pick into the basis of the next, and the only way to compare two spots
	// would be pick, undo, pick. Replaced when the photo or the pinned base
	// changes; one entry, like the decode and linear caches.
	pickMu    sync.Mutex
	pickEntry *pickCache
}

type decodeCache struct {
	photoID  int64
	key      string
	noExpKey string      // LibrawInputsHashNoExp: matches across exposure-only changes
	expEV    float64     // the exposure baked into rgba (BakedExpEV, via LibRaw exp_shift)
	wbCompEV float64     // edit.WBCompEV for this decode's WB — every render folds it in
	rgba     *image.RGBA // never mutated in place once cached
}

type linCache struct {
	photoID int64
	key     string        // LinearInputsHash: pre-demosaic inputs only
	refMul  [4]float64    // as-shot WB the reference was decoded at
	camXYZ  [4][3]float64 // camera matrix, for resolving Kelvin WB in Go
	rgbCam  [3][4]float64 // camera→sRGB matrix, for folding WB where LibRaw applies it
	lin     *image.RGBA64 // scene-linear reference; never mutated once cached
	clipped bool          // a channel is floored where the frame is lit — see refClipped
}

type pickCache struct {
	photoID  int64
	key      string // Hash of the pinned base (masks stripped)
	longEdge int
	mul      [4]float64 // the multipliers the decode actually applied
	// The camera→sRGB matrix and its inverse: a pick is measured in camera
	// channels, because that is where the multipliers it produces are applied.
	// hasM is false for a four-colour sensor or an uninvertible matrix.
	m, minv [3][3]float64
	hasM    bool
	rgba    *image.RGBA // developed, display-oriented; never mutated once cached
}

// GetEditParams returns the stored edit state. An untouched photo returns
// the seeded starting point instead of null once the calibrate pass has
// measured its camera-mimic compensation: the exposure dial then already
// reads the auto-brighten lift (e.g. +1.3 EV), so the first adjustment
// starts from what is on screen instead of dropping the compensation.
func (e *Edits) GetEditParams(ctx context.Context, photoID int64) (*edit.Params, error) {
	aprot.RegisterRefreshTrigger(ctx, editKey(photoID))
	p, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	if !p.EditParams.Valid {
		if seeded := seededParams(p); seeded != nil {
			return seeded, nil
		}
		return nil, nil
	}
	return edit.Parse(p.EditParams.String)
}

// seededParams is the starting edit state of an untouched photo: neutral
// except for the measured base exposure compensation. Nil when unmeasured
// or when the measurement was a no-op (then base-look rendering applies).
func seededParams(p store.Photo) *edit.Params {
	if !p.BaseExpEV.Valid || p.BaseExpEV.Float64 == 0 {
		return nil
	}
	return &edit.Params{ExpEV: p.BaseExpEV.Float64}
}

// previewLongEdge is the full-quality preview size; renders at this size are
// persisted to the pyramid cache so a following commit serves the same
// pixels instantly over /img.
const previewLongEdge = 2048

// PreviewEdit renders a preview of the (unsaved) edit state and returns the
// JPEG itself as a binary Blob riding the WebSocket — no second HTTP round
// trip. longEdge picks the rendition size: 0 or anything >= 2048 is the full
// 2048 cache-backed render; smaller values (the client drags at 1024) render
// entirely in memory — quarter the pixels and no disk round trip, so the
// stream of drag frames stays fast. The photo's unpacked handle is kept hot,
// so repeated calls while dragging a slider skip file reading entirely.
func (e *Edits) PreviewEdit(ctx context.Context, photoID int64, params edit.Params, longEdge int) (*aprot.Blob, error) {
	// Render the normalized state, not the raw wire values: every cache key
	// (Hash, the decode-subset hashes) is computed over normalized params, and
	// saveEdit persists normalized params — so an out-of-range draft value
	// (say a spot radius past the clamp) would otherwise render pixels that
	// get cached under, and later served for, a hash whose committed render
	// disagrees. Normalizing here makes preview and commit pixel-identical.
	params.Normalize()
	if longEdge <= 0 || longEdge >= previewLongEdge {
		path, err := e.ensurePreview(ctx, photoID, params)
		if err != nil {
			return nil, err
		}
		// A superseded settle was cancelled by the client — skip shipping a
		// blob nobody will look at.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return &aprot.Blob{ContentType: "image/jpeg", Data: data}, nil
	}

	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	var ep *edit.Params
	if !params.IsNeutral() {
		ep = &params
	}
	gamma := photo.LookGamma
	if gamma == 0 {
		gamma = pyramid.FallbackLookGamma
	}

	// Fold path: a deterministic edit whose WB is fold-able decodes ONCE to a
	// scene-linear reference; WB, exposure, brightness and gamma then fold in
	// as a cheap per-pixel pass, so dragging any of them never re-demosaics.
	// Auto WB (computed inside dcraw) and the base look (scene-dependent
	// auto-brighten) aren't reproducible by the fold and take the exact path.
	if img, ok, err := e.previewLinear(ctx, photoID, photo, ep, longEdge, gamma); err != nil {
		return nil, err
	} else if ok {
		return jpegBlob(img)
	}

	// Fallback: exact decode, reusing a warm decode that differs only in
	// exposure (the common case right after an auto/preset) and folding the
	// difference in post-decode; a full miss runs the demosaic. The deferred
	// 2048 settle re-decodes exactly for the accurate render. Either way the
	// delta folded in is ep.ExpEV minus what the decode actually baked — for
	// a fresh decode that is ResidualExpEV, the stops beyond LibRaw's
	// exp_shift range.
	var rgba *image.RGBA
	var expDelta, wbComp float64
	if reused, baked, comp, ok := e.approxDecode(photoID, ep); ok {
		rgba, wbComp = reused, comp
		if ep != nil {
			expDelta = ep.ExpEV - baked
		}
	} else {
		rgba, wbComp, err = e.previewDecode(ctx, photoID, photo, ep)
		if err != nil {
			return nil, err
		}
		expDelta = ep.ResidualExpEV()
	}
	// The WB compensation rides the same slot: it is a linear-light scalar
	// exactly like the residual exposure, and without it this frame lands at a
	// different brightness than the folded one it replaces.
	return jpegBlob(pyramid.RenderPreview(rgba, longEdge, gamma, ep, expDelta+wbComp,
		e.deps.Cache.AIMaps.SetFor(photo.CacheKey, ep),
		e.deps.Cache.Fills.SetFor(photo.CacheKey, ep),
		e.deps.Cache.Lenses.For(photo)))
}

// jpegBlob encodes a transient preview frame. The quality is slightly below
// the cached rendition's: the frame is fleeting and a smaller blob keeps the
// WebSocket drag stream snappy.
func jpegBlob(img *image.RGBA) (*aprot.Blob, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
		return nil, err
	}
	return &aprot.Blob{ContentType: "image/jpeg", Data: buf.Bytes()}, nil
}

// ensurePreview guarantees the 2048 rendition of the given (possibly
// unsaved) edit state exists in the cache and returns its path.
func (e *Edits) ensurePreview(ctx context.Context, photoID int64, params edit.Params) (string, error) {
	// Idempotent when the caller (PreviewEdit) already normalized; kept here
	// too so the rendered pixels can never diverge from the hash they are
	// cached under, whoever calls.
	params.Normalize()
	hash := params.Hash()
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return "", err
	}
	path := e.deps.Cache.PathFor(photo.CacheKey, "2048", hash)
	if _, err := os.Stat(path); err == nil {
		return path, nil // already rendered
	}

	// Neutral params must render the exact base look (auto-brighten), not
	// the deterministic edit pipeline — they share the "base" cache slot.
	var ep *edit.Params
	if !params.IsNeutral() {
		ep = &params
	}

	rgba, wbComp, err := e.previewDecode(ctx, photoID, photo, ep)
	if err != nil {
		return "", err
	}
	// A cancelled (superseded) render stops here: the decode above still
	// warmed the cache for its successor, but the JPEG encode and disk write
	// would be pure waste.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	gamma := photo.LookGamma
	if gamma == 0 {
		gamma = pyramid.FallbackLookGamma
	}
	// WritePreview never mutates its input, so handing it the shared cached
	// decode is safe.
	if err := e.deps.Cache.WritePreview(rgba, photo, hash, gamma, ep, wbComp); err != nil {
		return "", err
	}
	return path, nil
}

// previewDecode returns the half-size RAW decode for the given LibRaw-input
// state, reusing the cached decode when only geometry/look changed —
// otherwise it runs the (expensive) demosaic once and caches it for the next
// drag frame.
// The second return is edit.Params.WBCompEV for this decode — the stops that
// take LibRaw's white-balance normalization back out. Every caller must fold it
// in alongside ResidualExpEV (pyramid.ApplyExposureEV / RenderPreview's expDelta)
// or the render comes out at a different brightness than the preview it settles
// behind.
func (e *Edits) previewDecode(ctx context.Context, photoID int64, photo store.Photo, ep *edit.Params) (*image.RGBA, float64, error) {
	return e.decodePreview(ctx, photoID, photo, ep, true)
}

// statsDecode is previewDecode for the metering paths (auto adjustments,
// suggestions): the frame comes back carrying the edit's FULL exposure, with
// edit.Params.ResidualExpEV — the stops beyond LibRaw's exp_shift range —
// folded in the way every accurate render folds it. Metering the raw decode
// instead measured a darker frame than the one on screen and made auto tone
// drop the residual on the floor (a +4.28 EV photo came back +2.65).
//
// The fold is destructive and previewDecode hands back the shared cache
// entry, so a non-zero residual meters a copy; the common case (|ExpEV| ≤ 3)
// costs nothing.
func (e *Edits) statsDecode(ctx context.Context, photoID int64, photo store.Photo, ep *edit.Params) (*image.RGBA, error) {
	rgba, wbComp, err := e.previewDecode(ctx, photoID, photo, ep)
	if err != nil {
		return nil, err
	}
	delta := ep.ResidualExpEV() + wbComp
	if delta == 0 {
		return rgba, nil
	}
	dup := image.NewRGBA(rgba.Rect)
	copy(dup.Pix, rgba.Pix)
	pyramid.ApplyExposureEV(dup, delta, ep)
	return dup, nil
}

// decodePreview is previewDecode with the single-entry cache store optional:
// batch passes (the folder-wide subject scan) read the cache but never write
// it, so a background scan can't evict the interactive editor's warm decode —
// each scan frame would otherwise replace the entry and every look-stage
// slider drag would pay a fresh ~400 ms demosaic while the scan runs.
func (e *Edits) decodePreview(ctx context.Context, photoID int64, photo store.Photo, ep *edit.Params, cache bool) (*image.RGBA, float64, error) {
	libKey, noExpKey, expEV := decodeKeys(ep)
	if rgba, wbComp, ok := e.cachedDecodeComp(photoID, libKey); ok {
		return rgba, wbComp, nil
	}
	proc, release, err := e.deps.Handles.Acquire(photoID, photo.Path())
	if err != nil {
		return nil, 0, err
	}
	if ctx.Err() != nil {
		release()
		return nil, 0, ctx.Err() // superseded while waiting for the handle
	}
	// Read the file's balance BEFORE Process: scale_colors resolves the chosen
	// WB into pre_mul in place, and camMulOf falls back to pre_mul, so after a
	// decode this would read normalized values instead of the as-shot ones.
	wbComp := ep.WBCompEV(proc.CamMul(), proc.CamXYZ())
	img, err := e.processKeepingHandleUsable(ctx, photoID, proc, photo.Path(), ep.LibrawParams(true), release)
	if err != nil {
		return nil, 0, err
	}
	release()
	rgba, err := pyramid.FromLibraw(img)
	if err != nil {
		return nil, 0, err
	}
	if cache {
		e.storeDecode(photoID, libKey, noExpKey, expEV, wbComp, rgba)
	}
	return rgba, wbComp, nil
}

// decodeKeys derives the decode cache keys for a LibRaw-input state: the exact
// key, the exposure-independent key (for approxDecode reuse), and the baked
// exposure — BakedExpEV, not ExpEV, because LibRaw only bakes what exp_shift
// spans and approxDecode's delta must measure from the pixels as decoded. A
// nil/base decode keys as "base" with zero exposure.
func decodeKeys(ep *edit.Params) (key, noExpKey string, expEV float64) {
	if ep == nil {
		return "base", "base", 0
	}
	return ep.LibrawInputsHash(), ep.LibrawInputsHashNoExp(), ep.BakedExpEV()
}

// cachedDecodeComp returns the cached half-size decode for (photoID, key)
// along with the WB compensation it was decoded under, or ok=false.
func (e *Edits) cachedDecodeComp(photoID int64, key string) (rgba *image.RGBA, wbCompEV float64, ok bool) {
	e.decodeMu.Lock()
	defer e.decodeMu.Unlock()
	if e.decodeEntry != nil && e.decodeEntry.photoID == photoID && e.decodeEntry.key == key {
		return e.decodeEntry.rgba, e.decodeEntry.wbCompEV, true
	}
	return nil, 0, false
}

// approxDecode returns a decode reusable for a transient preview of ep: the
// cached one when it matches every LibRaw input except exposure, along with the
// exposure baked into it so the caller can fold the difference in post-decode
// (an exact match reports delta 0) and the WB compensation it was decoded
// under. Miss → ok=false. Only the fast preview path uses this; the accurate
// render keys on the full LibrawInputsHash.
//
// The compensation carries over unconditionally: the no-exposure key still
// covers every WB field and Highlight, so a reusable decode is by construction
// one whose white balance — and therefore its normalization — is identical.
func (e *Edits) approxDecode(photoID int64, ep *edit.Params) (rgba *image.RGBA, bakedExpEV, wbCompEV float64, ok bool) {
	_, noExpKey, _ := decodeKeys(ep)
	e.decodeMu.Lock()
	defer e.decodeMu.Unlock()
	if e.decodeEntry != nil && e.decodeEntry.photoID == photoID && e.decodeEntry.noExpKey == noExpKey {
		return e.decodeEntry.rgba, e.decodeEntry.expEV, e.decodeEntry.wbCompEV, true
	}
	return nil, 0, 0, false
}

// storeDecode replaces the single-entry decode cache.
func (e *Edits) storeDecode(photoID int64, key, noExpKey string, expEV, wbCompEV float64, rgba *image.RGBA) {
	e.decodeMu.Lock()
	defer e.decodeMu.Unlock()
	e.decodeEntry = &decodeCache{photoID: photoID, key: key, noExpKey: noExpKey, expEV: expEV, wbCompEV: wbCompEV, rgba: rgba}
}

// foldable reports whether the fold path can render ep: it needs a
// deterministic edit (the base look's auto-brighten isn't reproducible) whose
// white balance isn't auto (auto WB is computed inside dcraw from the pixels).
func foldable(ep *edit.Params) bool {
	return ep != nil && ep.WBMode != edit.WBAuto
}

// previewLinear renders a transient frame off the scene-linear reference,
// folding WB/exposure/brightness/gamma in Go. Returns ok=false (no error) when
// the edit isn't fold-able, so the caller takes the exact decode path.
func (e *Edits) previewLinear(ctx context.Context, photoID int64, photo store.Photo, ep *edit.Params, longEdge int, gamma float64) (*image.RGBA, bool, error) {
	if !foldable(ep) {
		return nil, false, nil
	}
	entry, err := e.linearMaster(ctx, photoID, photo, ep)
	if err != nil {
		return nil, false, err
	}
	if entry.clipped {
		return nil, false, nil // reference can't carry a WB change; decode exactly
	}
	fp := foldParamsFor(ep, entry.refMul, entry.camXYZ, entry.rgbCam)
	ai := e.deps.Cache.AIMaps.SetFor(photo.CacheKey, ep)
	fills := e.deps.Cache.Fills.SetFor(photo.CacheKey, ep)
	return pyramid.RenderPreviewLinear(entry.lin, longEdge, fp, gamma, ep, ai, fills, e.deps.Cache.Lenses.For(photo)), true, nil
}

// linearMaster returns the cached scene-linear reference for the photo at ep's
// pre-demosaic state, decoding it (one demosaic) and caching it on a miss.
// Because only the pre-demosaic inputs key the cache, WB/exposure/brightness/
// gamma edits all reuse it without decoding.
func (e *Edits) linearMaster(ctx context.Context, photoID int64, photo store.Photo, ep *edit.Params) (*linCache, error) {
	key := ep.LinearInputsHash()
	if c := e.cachedLinear(photoID, key); c != nil {
		return c, nil
	}
	proc, release, err := e.deps.Handles.Acquire(photoID, photo.Path())
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		release()
		return nil, ctx.Err() // superseded while waiting for the handle
	}
	refMul := proc.CamMul()
	camXYZ := proc.CamXYZ()
	rgbCam := proc.RgbCam()
	img, err := e.processKeepingHandleUsable(ctx, photoID, proc, photo.Path(), ep.LinearRefLibrawParams(), release)
	if err != nil {
		return nil, err
	}
	release()
	lin, err := pyramid.FromLibrawLinear(img)
	if err != nil {
		return nil, err
	}
	c := &linCache{photoID: photoID, key: key, refMul: refMul, camXYZ: camXYZ, rgbCam: rgbCam, lin: lin, clipped: refClipped(lin)}
	e.storeLinear(c)
	return c, nil
}

// refClipped reports whether the scene-linear reference has a channel sitting
// on the 16-bit floor in places the frame is plainly lit — the state in which
// it can no longer carry a white-balance change, because the fold only scales
// what is there and no gain brings a zeroed channel back.
//
// LibRaw applies the as-shot multipliers before the colour matrix, and under a
// narrow-band source (blue stage light, say) that combination can drive a
// channel negative and clip it. A blue-lit ILCE-7RM2 frame came back with
// green at 0 across 92% of the reference: the fold rendered it magenta while
// the exact decode of the same edit was correct. Photos like that give up the
// fold path and re-decode per frame — slower to drag, but right.
func refClipped(lin *image.RGBA64) bool {
	b := lin.Bounds()
	const (
		lit   = 1024 // clearly not a shadow
		floor = 8
	)
	var litN, badN int
	for y := b.Min.Y; y < b.Max.Y; y += 8 {
		for x := b.Min.X; x < b.Max.X; x += 8 {
			o := lin.PixOffset(x, y)
			var v [3]uint32
			for c := range 3 {
				v[c] = uint32(lin.Pix[o+2*c])<<8 | uint32(lin.Pix[o+2*c+1])
			}
			hi := max(v[0], v[1], v[2])
			if hi < lit {
				continue // shadow: a floored channel there means nothing
			}
			litN++
			if min(v[0], v[1], v[2]) < floor {
				badN++
			}
		}
	}
	// A handful of saturated specular pixels is normal; a fifth of the lit
	// frame missing a channel is not.
	return litN > 0 && badN*5 > litN
}

// cachedLinear returns the cached linear reference for (photoID, key), or nil.
func (e *Edits) cachedLinear(photoID int64, key string) *linCache {
	e.linMu.Lock()
	defer e.linMu.Unlock()
	if e.linEntry != nil && e.linEntry.photoID == photoID && e.linEntry.key == key {
		return e.linEntry
	}
	return nil
}

// storeLinear replaces the single-entry linear-reference cache.
func (e *Edits) storeLinear(c *linCache) {
	e.linMu.Lock()
	defer e.linMu.Unlock()
	e.linEntry = c
}

// foldParamsFor turns an edit into the raw-stage fold: the white-balance
// change as per-camera-channel gain, exposure and brightness as scalars, the
// matrix pair that moves between the reference's output space and the camera
// channels, and the output-gamma power/toe.
//
// Both multipliers are normalized to green before the ratio: the target may be
// in a different unit scale than the reference — a picked/custom WBMul is
// normalized to green=1, while cam_mul is in raw units (green ~1024 on many
// cameras) — so the raw ratio would be ~1/1000 and paint the frame black. With
// green=1 on both, the ratio is unit-independent and green (luminance) is
// preserved, so WB shifts only tint, not exposure. The exact decode normalizes
// by the minimum multiplier instead, which does move exposure; edit.WBCompEV
// takes that back out (see previewDecode and the tile path), so the settle
// lands on the frame that was dragged.
func foldParamsFor(ep *edit.Params, refMul [4]float64, camXYZ [4][3]float64, rgbCam [3][4]float64) pyramid.FoldParams {
	target := ep.EffectiveWBMul(refMul, camXYZ)
	bright := ep.Bright
	if bright <= 0 {
		bright = 1
	}
	tG := target[1]
	if tG <= 0 {
		tG = 1
	}
	rG := refMul[1]
	if rG <= 0 {
		rG = 1
	}
	var d [3]float64
	for c := range 3 {
		rc := refMul[c] / rG
		if rc <= 0 {
			rc = 1
		}
		d[c] = (target[c] / tG) / rc
	}
	g := ep.Gamma
	if g <= 0 {
		g = 2.222
	}
	s := ep.Shadow
	if s <= 0 {
		s = 4.5
	}
	fp := pyramid.FoldParams{D: d, Exp: math.Exp2(ep.ExpEV), Bright: bright, Pwr: 1 / g, Ts: s}
	// Where the decode clips. LibRaw clips at the white level after dividing
	// the multipliers by their smallest, and the compensation then darkens the
	// result — so in the reference's units the ceiling sits that much lower,
	// and highlights the reference still holds are gone from the settle. A
	// preview that kept them would blow out a region the final render doesn't.
	// Zero compensation (the ordinary case, where green already needs the
	// least gain) leaves this at the plain white level.
	fp.White = 65535 * math.Exp2(ep.WBCompEV(refMul, camXYZ))
	fp.M, fp.Minv, fp.HasMatrix = foldMatrix(rgbCam)
	return fp
}

// foldMatrix turns LibRaw's rgb_cam into the 3×3 pair the fold scales between,
// or reports false when it can't be used: a four-colour sensor (a non-zero
// fourth column — the fold has three channels to work with) or a matrix that
// won't invert. Both fall back to scaling the developed channels directly,
// which is what the fold did everywhere before; the exposure compensation is
// matrix-independent and still applies, so those files lose the hue accuracy
// (and the clip ceiling, which only the matrix loop honours) but not the
// brightness agreement.
func foldMatrix(rgbCam [3][4]float64) (m, minv [3][3]float64, ok bool) {
	for i := range 3 {
		if math.Abs(rgbCam[i][3]) > 1e-6 {
			return m, minv, false
		}
		for j := range 3 {
			m[i][j] = rgbCam[i][j]
		}
	}
	minv, ok = pyramid.Invert3(m)
	if !ok {
		return [3][3]float64{}, [3][3]float64{}, false
	}
	return m, minv, true
}

// wbPickLongEdge is the size of the pinned frame the WB picker samples. It
// matches the full-quality preview: a 7×7 patch then covers a small surface
// rather than half a shirt, and the client shows this same frame under the
// magnifier, where a smaller render would read visibly soft.
const wbPickLongEdge = previewLongEdge

// PickWhiteBalance returns the edit state that neutralizes the surface at the
// given relative coordinates (0..1 in the displayed, cropped frame — the space
// the pinned frame is rendered in, so no remapping is needed): wbMode=custom
// with multipliers that make the picked surface come out grey.
//
// base is the draft as it was when the picker opened, and it — not the live
// draft — is what gets sampled, for the whole picking session. Sampling the
// live frame would mean each pick lands on a frame already carrying the last
// one, so two nearby spots could only be compared by picking, undoing and
// picking again; against a pinned frame every click means the same thing and
// re-picking a spot is idempotent.
func (e *Edits) PickWhiteBalance(ctx context.Context, photoID int64, params, base edit.Params, x, y float64) (*edit.Params, error) {
	if x < 0 || x > 1 || y < 0 || y > 1 {
		return nil, aprot.ErrInvalidParams("pick coordinates must be within 0..1")
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	frame, err := e.wbPickFrame(ctx, photoID, photo, base)
	if err != nil {
		return nil, err
	}
	// Back out the look to approximately linear light: the sample is a
	// developed pixel, so the display curve and the look's saturation boost
	// have to come off before the channels are a ratio of scene light.
	lookGamma := photo.LookGamma
	if lookGamma == 0 {
		lookGamma = pyramid.FallbackLookGamma
	}
	satFactor := math.Max(0.2, 1.15*(1+base.Saturation))
	rl, gl, bl := samplePatchLinear(frame.rgba, x, y, lookGamma, satFactor)
	if rl < 1e-4 || gl < 1e-4 || bl < 1e-4 {
		log.Printf("wb pick: no-signal patch at (%.3f,%.3f) linear rl=%.4g gl=%.4g bl=%.4g on %dx%d pinned frame",
			x, y, rl, gl, bl, frame.rgba.Bounds().Dx(), frame.rgba.Bounds().Dy())
		return nil, aprot.ErrInvalidParams("picked area is too dark — pick a brighter neutral area")
	}

	// The multipliers act on the camera's own channels, before the colour
	// matrix, so the patch has to be measured there too: the sample is a
	// developed pixel, several matrix rows' worth of mixing away from the
	// channels the pick will scale. Taking it back through the inverse asks the
	// right question — "what gain makes THESE channels equal?" — and because
	// the matrix maps neutral to neutral, equal camera channels come out grey.
	// The old post-matrix ratio neutralized the preview and nothing else.
	pr, pg, pb := rl, gl, bl
	if frame.hasM {
		cr := frame.minv[0][0]*rl + frame.minv[0][1]*gl + frame.minv[0][2]*bl
		cg := frame.minv[1][0]*rl + frame.minv[1][1]*gl + frame.minv[1][2]*bl
		cb := frame.minv[2][0]*rl + frame.minv[2][1]*gl + frame.minv[2][2]*bl
		if cr > 1e-6 && cg > 1e-6 && cb > 1e-6 {
			pr, pg, pb = cr, cg, cb
		} else {
			// A deeply saturated patch can land outside the camera's gamut and
			// come back negative. No neutral answer exists there; fall back to
			// the output-space ratio, which at least still points the right way.
			log.Printf("wb pick: patch at (%.3f,%.3f) is out of camera gamut (cam %.4g/%.4g/%.4g); using output-space ratio", x, y, cr, cg, cb)
		}
	}

	// The pinned frame was developed at frame.mul, so neutralizing the patch
	// means scaling those by the patch's own imbalance: m[c] = mul[c]·(g/c).
	// Normalized to green, then held within 3 stops of the frame's own balance.
	eff := frame.mul
	eg := eff[1]
	if eg <= 0 {
		eg = 1
	}
	mul := clampPickedWB([4]float64{
		eff[0] / eg * (pg / pr),
		1,
		eff[2] / eg * (pg / pb),
		1,
	}, eff)

	out := params
	out.WBMode = edit.WBCustom
	out.WBMul = mul
	out.WBTemp, out.WBTint, out.WBKelvin = 0, 0, 0
	return &out, nil
}

// WBPickFrame returns the frame PickWhiteBalance samples for this base, as a
// JPEG — the client shows it under the pipette so the magnifier's readout is
// literally the pixels the pick is computed from. Rendering it here (rather
// than letting the client reuse its live preview) also warms the pin, so the
// first click is as fast as the rest.
func (e *Edits) WBPickFrame(ctx context.Context, photoID int64, base edit.Params) (*aprot.Blob, error) {
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	frame, err := e.wbPickFrame(ctx, photoID, photo, base)
	if err != nil {
		return nil, err
	}
	return jpegBlob(frame.rgba)
}

// wbPickFrame renders (or returns the pinned) frame the WB picker samples: the
// base developed exactly as the loupe develops it, minus the masks.
//
// Masks are stripped for the same reason PickRangeColor strips them — a local
// adjustment's own temp/tint (or an FX mask's glow and prism) would otherwise
// feed straight into the global white balance. It decodes rather than reusing
// the shared preview caches because the multipliers LibRaw actually applied
// have to be read back off the handle: in auto WB mode dcraw derives them from
// the pixels, and without them there is nothing to express the pick relative
// to. That decode is paid once per picking session.
func (e *Edits) wbPickFrame(ctx context.Context, photoID int64, photo store.Photo, base edit.Params) (*pickCache, error) {
	base.Masks = nil
	// The pick has to read color: a BW frame would sample as neutral and
	// every pick would come back as no correction at all. Dropping it here
	// also keeps the satFactor back-out below matching what was rendered.
	base.BW = false
	base.Normalize()
	key := base.Hash()
	if c := e.cachedPick(photoID, key); c != nil {
		return c, nil
	}
	var ep *edit.Params
	if !base.IsNeutral() {
		ep = &base
	}
	proc, release, err := e.deps.Handles.Acquire(photoID, photo.Path())
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		release()
		return nil, ctx.Err() // superseded while waiting for the handle
	}
	// Both read before Process: CamMul because scale_colors overwrites the
	// pre_mul it falls back to, RgbCam because the pick is computed through it.
	wbComp := ep.WBCompEV(proc.CamMul(), proc.CamXYZ())
	rgbCam := proc.RgbCam()
	img, err := e.processKeepingHandleUsable(ctx, photoID, proc, photo.Path(), ep.LibrawParams(true), release)
	if err != nil {
		return nil, err
	}
	mul := proc.EffectiveMul() // resolved WB, auto included — valid until the next Process
	release()
	rgba, err := pyramid.FromLibraw(img)
	if err != nil {
		return nil, err
	}
	gamma := photo.LookGamma
	if gamma == 0 {
		gamma = pyramid.FallbackLookGamma
	}
	m, minv, hasM := foldMatrix(rgbCam)
	c := &pickCache{
		photoID:  photoID,
		key:      key,
		longEdge: wbPickLongEdge,
		mul:      mul,
		m:        m,
		minv:     minv,
		hasM:     hasM,
		// Rendered with the same WB compensation every accurate render carries,
		// so the frame under the magnifier is the one the loupe is showing. It
		// is a scalar on all three channels, so the pick's ratios — and with
		// them the answer — are unchanged either way.
		rgba: pyramid.RenderPreview(rgba, wbPickLongEdge, gamma, ep, ep.ResidualExpEV()+wbComp,
			e.deps.Cache.AIMaps.SetFor(photo.CacheKey, ep),
			e.deps.Cache.Fills.SetFor(photo.CacheKey, ep),
			e.deps.Cache.Lenses.For(photo)),
	}
	e.storePick(c)
	return c, nil
}

// cachedPick returns the pinned pick frame for (photoID, key), or nil.
func (e *Edits) cachedPick(photoID int64, key string) *pickCache {
	e.pickMu.Lock()
	defer e.pickMu.Unlock()
	if e.pickEntry != nil && e.pickEntry.photoID == photoID && e.pickEntry.key == key &&
		e.pickEntry.longEdge == wbPickLongEdge {
		return e.pickEntry
	}
	return nil
}

// storePick replaces the single-entry pinned-frame cache.
func (e *Edits) storePick(c *pickCache) {
	e.pickMu.Lock()
	defer e.pickMu.Unlock()
	e.pickEntry = c
}

// samplePatchLinear averages a small patch of a developed frame around (x,y)
// in approximately linear light: the display look (BT.709 gamma × calibrated
// lift) is inverted with a single combined power, and the look's saturation
// boost (satFactor) is undone around luma. The edit's tone-curve adjustments
// are not inverted — they are monotonic per channel and a WB pick reads the
// ratio between channels, which they barely move.
func samplePatchLinear(img *image.RGBA, x, y, lookGamma, satFactor float64) (r, g, b float64) {
	bnd := img.Bounds()
	cx := bnd.Min.X + int(x*float64(bnd.Dx()-1))
	cy := bnd.Min.Y + int(y*float64(bnd.Dy()-1))
	const rad = 3
	decodePow := 2.222 / lookGamma

	var n float64
	for py := cy - rad; py <= cy+rad; py++ {
		for px := cx - rad; px <= cx+rad; px++ {
			if px < bnd.Min.X || px >= bnd.Max.X || py < bnd.Min.Y || py >= bnd.Max.Y {
				continue
			}
			o := img.PixOffset(px, py)
			fr, fg, fb := float64(img.Pix[o])/255, float64(img.Pix[o+1])/255, float64(img.Pix[o+2])/255
			// Undo the look's saturation boost around Rec.601 luma.
			luma := 0.299*fr + 0.587*fg + 0.114*fb
			fr = luma + (fr-luma)/satFactor
			fg = luma + (fg-luma)/satFactor
			fb = luma + (fb-luma)/satFactor
			r += math.Pow(math.Max(0, fr), decodePow)
			g += math.Pow(math.Max(0, fg), decodePow)
			b += math.Pow(math.Max(0, fb), decodePow)
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return r / n, g / n, b / n
}

// pickWBStops is how far a single pick may move a channel from the white
// balance the sampled frame was developed at, in stops. A pick corrects a
// cast; it does not invent channel gain. Loose on purpose — real picks move
// well under a stop, and the case this exists for moves ten.
const pickWBStops = 3

// clampPickedWB holds a picked white balance within pickWBStops of eff, the
// multipliers the sampled frame was developed at. Both green-normalized.
//
// A spot lit by one narrow-band source can carry almost no signal in a channel
// — on a blue-lit stage shot a patch sampled 350× more green than red — and
// neutralizing that literally asks for a multiplier in the hundreds, which
// crushes the channels actually carrying the light and drops the frame to
// near-black. Bounding against the frame's own white balance rather than an
// absolute range keeps the bound meaningful whatever the light: LibRaw's auto
// WB for that same shot is [1.865, 1, 0.372], nowhere near the blackbody
// locus, and an absolute envelope built from the Kelvin dial would clamp a
// perfectly good pick.
func clampPickedWB(mul, eff [4]float64) [4]float64 {
	const span = 1 << pickWBStops
	eg := eff[1]
	if eg <= 0 {
		eg = 1
	}
	for _, c := range [2]int{0, 2} {
		base := eff[c] / eg
		if base <= 0 {
			continue
		}
		mul[c] = min(max(mul[c], base/span), base*span)
	}
	return mul
}

// developedBaseForMask renders the developed (post-Look) image with masks
// stripped, at the given long edge — the pixels a range mask selects on. It
// mirrors PreviewEdit's non-cached render (fold path when foldable, exact
// decode otherwise) but returns the RGBA rather than a JPEG blob, and it is
// display-oriented (cropped, rotated, mirrored) like the frame the client
// shows, so a range mask's tint and the eyedropper sample both align 1:1 with
// what the user sees.
func (e *Edits) developedBaseForMask(ctx context.Context, photoID int64, photo store.Photo, params edit.Params, longEdge int) (*image.RGBA, error) {
	params.Masks = nil // select against the post-Look image, before any mask
	// ...and before the B&W conversion, which runs after the masks: hue
	// ranges and the eyedropper select on the color the mask stage sees.
	params.BW = false
	var ep *edit.Params
	if !params.IsNeutral() {
		ep = &params
	}
	gamma := photo.LookGamma
	if gamma == 0 {
		gamma = pyramid.FallbackLookGamma
	}
	if img, ok, err := e.previewLinear(ctx, photoID, photo, ep, longEdge, gamma); err != nil {
		return nil, err
	} else if ok {
		return img, nil
	}
	var rgba *image.RGBA
	var expDelta, wbComp float64
	if reused, baked, comp, ok := e.approxDecode(photoID, ep); ok {
		rgba, wbComp = reused, comp
		if ep != nil {
			expDelta = ep.ExpEV - baked
		}
	} else {
		var err error
		rgba, wbComp, err = e.previewDecode(ctx, photoID, photo, ep)
		if err != nil {
			return nil, err
		}
		if ep != nil {
			expDelta = ep.ResidualExpEV()
		}
	}
	return pyramid.RenderPreview(rgba, longEdge, gamma, ep, expDelta+wbComp,
		e.deps.Cache.AIMaps.SetFor(photo.CacheKey, ep),
		e.deps.Cache.Fills.SetFor(photo.CacheKey, ep),
		e.deps.Cache.Lenses.For(photo)), nil
}

// PickRangeColor samples the developed colour at the clicked spot and seeds the
// target range mask's hue window (centred on the picked hue) and saturation
// floor (just below the picked saturation), so the eyedropper selects the
// clicked colour and its near neighbours. Like the WB picker it only PREVIEWS
// — the draft updates, nothing commits until the client applies it. x,y are
// fractions of the displayed (cropped, oriented) frame, which is exactly the
// space developedBaseForMask renders, so no crop/flip/rotate remap is needed.
func (e *Edits) PickRangeColor(ctx context.Context, photoID int64, params edit.Params, x, y float64, maskIndex int) (*edit.Params, error) {
	if x < 0 || x > 1 || y < 0 || y > 1 {
		return nil, aprot.ErrInvalidParams("pick coordinates must be within 0..1")
	}
	if maskIndex < 0 || maskIndex >= len(params.Masks) {
		return nil, aprot.ErrInvalidParams("maskIndex out of range")
	}
	if params.Masks[maskIndex].Type != edit.MaskRange {
		return nil, aprot.ErrInvalidParams("mask is not a range mask")
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	base, err := e.developedBaseForMask(ctx, photoID, photo, params, 1024)
	if err != nil {
		return nil, err
	}
	r, g, b := samplePatchRGBA(base, x, y)
	hueLo, hueHi, satMin, err := rangeColorWindow(r, g, b)
	if err != nil {
		return nil, err
	}
	out := params
	out.Masks = append([]edit.Mask(nil), params.Masks...) // don't mutate caller's backing array
	m := &out.Masks[maskIndex]
	m.RangeHueLo, m.RangeHueHi, m.RangeSatMin = hueLo, hueHi, satMin
	return &out, nil
}

// rangeColorWindow turns a sampled display colour (0..255) into a range mask's
// hue window and saturation floor: a ±0.045 (~16°) hue band centred on the
// pick — wrapping through red, so the pair may come back with hi < lo, which
// Normalize preserves — and a floor a little below the pick's saturation so
// similar colours are kept but greys are excluded. Too-dark or too-grey picks
// (no meaningful hue) are refused with a helpful message.
func rangeColorWindow(r, g, b int) (hueLo, hueHi, satMin float64, err error) {
	maxc, minc := max(r, g, b), min(r, g, b)
	if maxc < 12 {
		return 0, 0, 0, aprot.ErrInvalidParams("picked area is too dark — pick a brighter, more colourful spot")
	}
	delta := maxc - minc
	sat := float64(delta) / float64(maxc)
	if delta == 0 || sat < 0.12 {
		return 0, 0, 0, aprot.ErrInvalidParams("picked area has no colour — pick a more saturated spot")
	}
	var h float64 // 0..6
	switch maxc {
	case r:
		h = float64(g-b) / float64(delta)
	case g:
		h = float64(b-r)/float64(delta) + 2
	default:
		h = float64(r-g)/float64(delta) + 4
	}
	h /= 6
	if h < 0 {
		h += 1
	}
	const tol = 0.045
	return math.Mod(h-tol+1, 1), math.Mod(h+tol, 1), math.Max(0, sat-0.25), nil
}

// samplePatchRGBA averages a small patch of a display-encoded RGBA image around
// the given relative coordinates (0..1), returning 0..255 channels — the
// eyedropper's noise-robust point sample.
func samplePatchRGBA(img *image.RGBA, x, y float64) (r, g, b int) {
	bnd := img.Bounds()
	cx := bnd.Min.X + int(x*float64(bnd.Dx()-1))
	cy := bnd.Min.Y + int(y*float64(bnd.Dy()-1))
	const rad = 2
	var sr, sg, sb, n int
	for py := cy - rad; py <= cy+rad; py++ {
		for px := cx - rad; px <= cx+rad; px++ {
			if px < bnd.Min.X || px >= bnd.Max.X || py < bnd.Min.Y || py >= bnd.Max.Y {
				continue
			}
			o := img.PixOffset(px, py)
			sr += int(img.Pix[o])
			sg += int(img.Pix[o+1])
			sb += int(img.Pix[o+2])
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return sr / n, sg / n, sb / n
}

// SuggestHealSource picks a source patch for a new retouch spot and returns
// the spot with SX/SY filled in (oriented-frame fractions). The chosen source
// is stored in the params so it stays stable and portable — computing it in
// the render pipeline would let it drift with render size. It reuses the hot
// preview decode (spot fields don't touch the LibRaw-input hash), so it is
// typically instant during an editing session.
//
// For a stroke spot the painted region is reduced to its enclosing circle for
// the ring search, and CX/CY are ALSO returned (set to that circle's center):
// the stored dest reference must match the point the source vector was chosen
// against, so the caller persists both.
func (e *Edits) SuggestHealSource(ctx context.Context, photoID int64, params edit.Params, spot edit.Spot) (*edit.Spot, error) {
	switch spot.Kind {
	case "":
		if spot.Radius <= 0 {
			return nil, aprot.ErrInvalidParams("spot radius must be positive")
		}
	case "stroke":
		if len(spot.Strokes) == 0 {
			return nil, aprot.ErrInvalidParams("stroke spot has no strokes")
		}
	default:
		return nil, aprot.ErrInvalidParams("unknown spot kind")
	}
	// Suggest against the normalized state the renders will use (PreviewEdit
	// normalizes too), with the spot clamped like normalizeSpots will clamp it
	// on save — otherwise an out-of-range draft radius would pick a source for
	// a different disc than the one that ends up rendering.
	params.Normalize()
	if spot.Radius > 0.5 {
		spot.Radius = 0.5
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	var ep *edit.Params
	if !params.IsNeutral() {
		ep = &params
	}
	// The warm half-size decode (never mutated), taken to post-geometry space —
	// the same space ApplyHeal fills in — so the source we pick matches the
	// cropped/straightened frame the user is looking at. ApplyGeometry returns
	// the shared decode unchanged for neutral geometry; SuggestHealSource only
	// reads it, so no defensive copy is needed.
	// The WB compensation is dropped here as the residual exposure already is:
	// this searches for a patch that matches another patch in the same frame,
	// and a scalar applied to the whole frame can't change which one wins.
	rgba, _, err := e.previewDecode(ctx, photoID, photo, ep)
	if err != nil {
		return nil, err
	}
	geo := pyramid.ApplyGeometry(
		pyramid.ApplyLens(rgba,
			pyramid.LensWarp(e.deps.Cache.Lenses.For(photo), ep, rgba.Bounds().Dx(), rgba.Bounds().Dy()), ep),
		ep)
	// Pass &params (never nil) for the frame mapping: it carries the crop and
	// rotation that place the fractional coordinates, and newMaskFrame derefs it.
	out := spot
	target := spot
	if spot.Kind == "stroke" {
		b := geo.Bounds()
		cx, cy, rad := pyramid.StrokeSpotCircle(b.Dx(), b.Dy(), &params, &spot)
		if rad <= 0 {
			return nil, aprot.ErrInvalidParams("stroke spot region is empty")
		}
		out.CX, out.CY = cx, cy
		// The search only needs the region's footprint; run it as a circle.
		target = edit.Spot{Mode: spot.Mode, CX: cx, CY: cy, Radius: math.Min(rad, 0.5)}
	}
	out.SX, out.SY = pyramid.SuggestHealSource(geo, &params, target)
	return &out, nil
}

// AutoAdjust computes automatic values for the requested sections ("tone",
// "wb", "color", or "all") from the current decode and returns the caller's
// params with only those sections replaced. Nothing is persisted — the
// client applies the result through the normal SetEditParams path.
func (e *Edits) AutoAdjust(ctx context.Context, photoID int64, params edit.Params, sections []string) (*edit.Params, error) {
	if len(sections) == 0 {
		return nil, aprot.ErrInvalidParams("no auto sections requested")
	}
	var secs []pyramid.AutoSection
	wb := false
	for _, s := range sections {
		switch pyramid.AutoSection(s) {
		case "all":
			secs = pyramid.AutoSectionValues()
			wb = true
		case pyramid.AutoWB:
			wb = true
		case pyramid.AutoTone, pyramid.AutoColor:
			secs = append(secs, pyramid.AutoSection(s))
		default:
			return nil, aprot.ErrInvalidParams("unknown auto section: " + s)
		}
	}

	out := params
	if wb {
		// Selecting LibRaw's auto WB changes the decode itself, so it must
		// land before the statistics pass — tone and color are then measured
		// on the neutralized image and the sections compose.
		out.WBMode = edit.WBAuto
		out.WBMul = [4]float64{}
		out.WBTemp, out.WBTint, out.WBKelvin = 0, 0, 0
	}

	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	// Hot path: when wb wasn't requested the LibRaw inputs are unchanged, so
	// this returns the cached decode from the last preview; when it was, the
	// one half-size demosaic here also pre-warms the cache for the preview
	// the client requests right after.
	rgba, err := e.statsDecode(ctx, photoID, photo, &out)
	if err != nil {
		return nil, err
	}
	gamma := photo.LookGamma
	if gamma == 0 {
		gamma = pyramid.FallbackLookGamma
	}
	// Subject-aware metering: use the AI subject matte when one was already
	// generated for this photo (auto never triggers an inference itself).
	var subject *pyramid.AIMap
	if ver, ok := aimask.MapVerFor(edit.AISubject); ok {
		subject = e.deps.Cache.AIMaps.Load(photo.CacheKey, edit.AISubject, ver)
	}
	pyramid.AutoAdjust(rgba, gamma, &out, secs, subject)
	return &out, nil
}

// SetEditParams persists the edit state (neutral params clear it).
func (e *Edits) SetEditParams(ctx context.Context, photoID int64, params edit.Params) error {
	if err := e.saveEdit(ctx, photoID, &params); err != nil {
		return err
	}
	aprot.TriggerRefresh(ctx, editKey(photoID))
	return nil
}

// ResetEdits clears the edit state of the given photos.
func (e *Edits) ResetEdits(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if err := e.saveEdit(ctx, id, nil); err != nil {
			return err
		}
		aprot.TriggerRefresh(ctx, editKey(id))
	}
	return nil
}

// PasteEditParams applies one edit state to many photos (the copy side is
// client-local: GetEditParams into a clipboard).
func (e *Edits) PasteEditParams(ctx context.Context, ids []int64, params edit.Params) error {
	for i, id := range ids {
		if err := e.saveEdit(ctx, id, &params); err != nil {
			return err
		}
		aprot.TriggerRefresh(ctx, editKey(id))
		aprot.Progress(ctx).Update(i+1, len(ids), "")
	}
	return nil
}

// ApplyBatchEdit applies a relative adjustment to many photos, e.g.
// "+0.5 EV on the current selection".
func (e *Edits) ApplyBatchEdit(ctx context.Context, ids []int64, delta edit.Delta) error {
	for i, id := range ids {
		p, err := e.deps.DB.GetPhoto(ctx, id)
		if err != nil {
			return err
		}
		var params edit.Params
		if p.EditParams.Valid {
			ep, err := edit.Parse(p.EditParams.String)
			if err != nil {
				// Carrying on with neutral params would apply the delta to
				// nothing and then persist that, replacing settings that are
				// unreadable — not gone — with delta-only values. The photo is
				// left exactly as it was, and the batch stops rather than
				// reporting a success it did not deliver.
				return fmt.Errorf("%s: its stored edit could not be read, so the batch stopped here: %w", p.FileName, err)
			}
			params = *ep
		} else if seeded := seededParams(p); seeded != nil {
			// Relative adjustments on untouched photos start from the seeded
			// compensation, not from zero — "+0.5 EV" means half a stop
			// brighter than what is on screen.
			params = *seeded
		}
		delta.Apply(&params)
		if err := e.saveEdit(ctx, id, &params); err != nil {
			return err
		}
		aprot.TriggerRefresh(ctx, editKey(id))
		aprot.Progress(ctx).Update(i+1, len(ids), p.FileName)
	}
	return nil
}

// processKeepingHandleUsable runs one Process and leaves the handle cache in a
// state the next acquire can use. On success the handle is still held: the
// caller releases it once it has taken what it needs from proc (EffectiveMul
// is only valid until the next Process). On failure it is released here.
//
// Passing the real ctx is the point — LibRaw aborts at its next progress
// checkpoint, so a photo the user has already browsed away from stops burning
// its core mid-demosaic instead of blocking the handle for a full decode. What
// that costs is an invariant to restore: a cancelled Process leaves the handle
// recycled, its stream closed and unpacked data freed, so the file is re-Opened
// (metadata only, ~150 ms, paid only on abandonment). If that reopen fails —
// the file was moved or the drive went away — the entry is poisoned and every
// later Process on it would fail, so it is dropped instead.
//
// Same-photo drags never cancel: the client coalesces them to one render in
// flight, so a cancelled ctx here means a photo switch.
//
// This lived as three identical copies, in the preview, linear-master and
// white-balance-pick paths. They agreed, but nothing made them: a fix applied
// to one would have left the others recycling a handle that fails every
// subsequent decode for that photo.
func (e *Edits) processKeepingHandleUsable(
	ctx context.Context,
	photoID int64,
	proc *libraw.Processor,
	path string,
	params libraw.Params,
	release func(),
) (*libraw.Image, error) {
	img, err := proc.Process(ctx, params)
	if err == nil {
		return img, nil
	}
	healthy := true
	if ctx.Err() != nil {
		healthy = proc.Open(path) == nil
		err = ctx.Err()
	}
	release()
	if !healthy {
		e.deps.Handles.Invalidate(photoID)
	}
	return nil, err
}

// saveEdit persists params (nil or neutral clears), pushes the patch to
// folder subscribers, and warms the new grid thumbnail in the background.
func (e *Edits) saveEdit(ctx context.Context, photoID int64, params *edit.Params) error {
	params.Normalize()
	var jsonPtr *string
	hash := edit.BaseHash
	if !params.IsNeutral() {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		s := string(b)
		jsonPtr = &s
		hash = params.Hash()
	}
	if err := e.deps.DB.SetEdit(ctx, photoID, jsonPtr, hash, time.Now().UnixMilli()); err != nil {
		return err
	}

	// Warm the grid thumb for the new state so the grid updates without a
	// scroll-triggered fetch racing the patch, and mirror the new intent to
	// the photo's portable sidecar.
	if p, err := e.deps.DB.GetPhoto(context.WithoutCancel(ctx), photoID); err == nil {
		e.deps.patchFolderPhotos(p.FolderID, []PhotoPatch{editPatch(photoID, hash, params)})
		e.deps.writeSidecarFor(context.WithoutCancel(ctx), p)
		e.deps.warmEdit(p, hash)
	}
	return nil
}

// editPatch is the folder broadcast for one photo's new edit state: the
// content hash plus the aspect-affecting geometry, so natural-layout grids
// can reshape cells without refetching edit params. All three geometry
// fields are always set — a reset must deliver explicit zeros, and nil
// params means exactly that (params are already normalized, so CropW/CropH
// are 0 unless a real crop is present).
func editPatch(photoID int64, hash string, params *edit.Params) PhotoPatch {
	rotate := params.RotateTurns()
	var cropW, cropH float64
	if params != nil {
		cropW, cropH = params.CropW, params.CropH
	}
	return PhotoPatch{ID: photoID, EditHash: &hash, Rotate: &rotate, CropW: &cropW, CropH: &cropH}
}

// warmSlot holds the cancel func of one in-flight post-save warm so a
// superseding warm can be matched by pointer identity (func values are not
// comparable).
type warmSlot struct{ cancel context.CancelFunc }

// warmEdit fires the post-save 512 thumb warm on a cancellable detached
// context and supersedes any warm still running for the same photo. The warm
// is fire-and-forget with no viewport to cancel it, so a plain
// context.Background() decode, once a pool worker claims it, runs its full
// duration uncancellably — a burst of quick-dial commits (or a paste/reset
// sweep) would stack those and delay the visible frame. Cancelling the prior
// warm lets libraw's progress-callback abort the superseded decode mid-flight.
// Priority stays Prefetch so it never outranks the photo the user is viewing.
func (d *Deps) warmEdit(p store.Photo, hash string) {
	ctx, cancel := context.WithCancel(context.Background())
	slot := &warmSlot{cancel: cancel}
	d.warmMu.Lock()
	if d.warmCancels == nil {
		d.warmCancels = map[int64]*warmSlot{}
	}
	if prev := d.warmCancels[p.ID]; prev != nil {
		prev.cancel()
	}
	d.warmCancels[p.ID] = slot
	d.warmMu.Unlock()
	go func() {
		d.Cache.Ensure(ctx, p, "512", hash, decode.PriorityPrefetch)
		cancel()
		d.warmMu.Lock()
		if d.warmCancels[p.ID] == slot { // still ours: clear the slot
			delete(d.warmCancels, p.ID)
		}
		d.warmMu.Unlock()
	}()
}
