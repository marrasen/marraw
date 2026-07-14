package api

import (
	"bytes"
	"context"
	"encoding/json"
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
}

type decodeCache struct {
	photoID  int64
	key      string
	noExpKey string      // LibrawInputsHashNoExp: matches across exposure-only changes
	expEV    float64     // the ExpEV baked into rgba (via LibRaw exp_shift)
	rgba     *image.RGBA // never mutated in place once cached
}

type linCache struct {
	photoID int64
	key     string        // LinearInputsHash: pre-demosaic inputs only
	refMul  [4]float64    // as-shot WB the reference was decoded at
	camXYZ  [4][3]float64 // camera matrix, for resolving Kelvin WB in Go
	lin     *image.RGBA64 // scene-linear reference; never mutated once cached
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
	// 2048 settle re-decodes exactly for the accurate render.
	var rgba *image.RGBA
	var expDelta float64
	if reused, baked, ok := e.approxDecode(photoID, ep); ok {
		rgba = reused
		if ep != nil {
			expDelta = ep.ExpEV - baked
		}
	} else {
		rgba, err = e.previewDecode(ctx, photoID, photo, ep)
		if err != nil {
			return nil, err
		}
	}
	return jpegBlob(pyramid.RenderPreview(rgba, longEdge, gamma, ep, expDelta,
		e.deps.Cache.AIMaps.SetFor(photo.CacheKey, ep)))
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

	rgba, err := e.previewDecode(ctx, photoID, photo, ep)
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
	if err := e.deps.Cache.WritePreview(rgba, photo.CacheKey, hash, gamma, ep); err != nil {
		return "", err
	}
	return path, nil
}

// previewDecode returns the half-size RAW decode for the given LibRaw-input
// state, reusing the cached decode when only geometry/look changed —
// otherwise it runs the (expensive) demosaic once and caches it for the next
// drag frame.
func (e *Edits) previewDecode(ctx context.Context, photoID int64, photo store.Photo, ep *edit.Params) (*image.RGBA, error) {
	libKey, noExpKey, expEV := decodeKeys(ep)
	if rgba := e.cachedDecode(photoID, libKey); rgba != nil {
		return rgba, nil
	}
	proc, release, err := e.deps.Handles.Acquire(photoID, photo.Path())
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		release()
		return nil, ctx.Err() // superseded while waiting for the handle
	}
	// Real ctx: LibRaw aborts at its next progress checkpoint, so a photo
	// the user has already browsed away from stops burning its core mid-
	// demosaic instead of blocking the handle for the full decode. The cancel
	// path must then restore the HandleCache invariant — a cancelled Process
	// leaves the handle recycled (stream closed, unpacked data freed) — by
	// re-Opening the file (metadata-only, ~150 ms, paid only on abandonment).
	// Same-photo drags never cancel: the client coalesces them to one render
	// in flight, so a cancelled ctx here means a photo switch.
	img, err := proc.Process(ctx, ep.LibrawParams(true))
	if err != nil {
		healthy := true
		if ctx.Err() != nil {
			healthy = proc.Open(photo.Path()) == nil
			err = ctx.Err()
		}
		release()
		if !healthy {
			// Reopen failed (file gone/unreadable): drop the poisoned entry so
			// the next acquire starts from a fresh handle instead of a recycled
			// one that fails every Process.
			e.deps.Handles.Invalidate(photoID)
		}
		return nil, err
	}
	release()
	rgba, err := pyramid.FromLibraw(img)
	if err != nil {
		return nil, err
	}
	e.storeDecode(photoID, libKey, noExpKey, expEV, rgba)
	return rgba, nil
}

// decodeKeys derives the decode cache keys for a LibRaw-input state: the exact
// key, the exposure-independent key (for approxDecode reuse), and the baked
// exposure. A nil/base decode keys as "base" with zero exposure.
func decodeKeys(ep *edit.Params) (key, noExpKey string, expEV float64) {
	if ep == nil {
		return "base", "base", 0
	}
	return ep.LibrawInputsHash(), ep.LibrawInputsHashNoExp(), ep.ExpEV
}

// cachedDecode returns the cached half-size decode for (photoID, key), or nil.
func (e *Edits) cachedDecode(photoID int64, key string) *image.RGBA {
	e.decodeMu.Lock()
	defer e.decodeMu.Unlock()
	if e.decodeEntry != nil && e.decodeEntry.photoID == photoID && e.decodeEntry.key == key {
		return e.decodeEntry.rgba
	}
	return nil
}

// approxDecode returns a decode reusable for a transient preview of ep: the
// cached one when it matches every LibRaw input except exposure, along with the
// exposure baked into it so the caller can fold the difference in post-decode
// (an exact match reports delta 0). Miss → ok=false. Only the fast preview path
// uses this; the accurate render keys on the full LibrawInputsHash.
func (e *Edits) approxDecode(photoID int64, ep *edit.Params) (rgba *image.RGBA, bakedExpEV float64, ok bool) {
	_, noExpKey, _ := decodeKeys(ep)
	e.decodeMu.Lock()
	defer e.decodeMu.Unlock()
	if e.decodeEntry != nil && e.decodeEntry.photoID == photoID && e.decodeEntry.noExpKey == noExpKey {
		return e.decodeEntry.rgba, e.decodeEntry.expEV, true
	}
	return nil, 0, false
}

// storeDecode replaces the single-entry decode cache.
func (e *Edits) storeDecode(photoID int64, key, noExpKey string, expEV float64, rgba *image.RGBA) {
	e.decodeMu.Lock()
	defer e.decodeMu.Unlock()
	e.decodeEntry = &decodeCache{photoID: photoID, key: key, noExpKey: noExpKey, expEV: expEV, rgba: rgba}
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
	fp := foldParamsFor(ep, entry.refMul, entry.camXYZ)
	ai := e.deps.Cache.AIMaps.SetFor(photo.CacheKey, ep)
	return pyramid.RenderPreviewLinear(entry.lin, longEdge, fp, gamma, ep, ai), true, nil
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
	// Real ctx with reopen-on-cancel, exactly as previewDecode.
	img, err := proc.Process(ctx, ep.LinearRefLibrawParams())
	if err != nil {
		healthy := true
		if ctx.Err() != nil {
			healthy = proc.Open(photo.Path()) == nil
			err = ctx.Err()
		}
		release()
		if !healthy {
			e.deps.Handles.Invalidate(photoID)
		}
		return nil, err
	}
	release()
	lin, err := pyramid.FromLibrawLinear(img)
	if err != nil {
		return nil, err
	}
	c := &linCache{photoID: photoID, key: key, refMul: refMul, camXYZ: camXYZ, lin: lin}
	e.storeLinear(c)
	return c, nil
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

// foldParamsFor turns an edit into the raw-stage fold: the per-channel linear
// gain (WB ratio from the reference's as-shot WB × 2^EV × brightness) and the
// output-gamma power/toe. Temp/tint fold exactly (the as-shot WB cancels in the
// ratio); custom/Kelvin picks are approximate in output space, which the 2048
// settle corrects.
//
// Both multipliers are normalized to green before the ratio: the target may be
// in a different unit scale than the reference — a picked/custom WBMul is
// normalized to green=1, while cam_mul is in raw units (green ~1024 on many
// cameras) — so the raw ratio would be ~1/1000 and paint the frame black. With
// green=1 on both, the ratio is unit-independent and green (luminance) is
// preserved, so WB shifts only tint, not exposure.
func foldParamsFor(ep *edit.Params, refMul [4]float64, camXYZ [4][3]float64) pyramid.FoldParams {
	target := targetWBMul(ep, refMul, camXYZ)
	exp := math.Exp2(ep.ExpEV)
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
	var k [3]float64
	for c := range 3 {
		rc := refMul[c] / rG
		if rc <= 0 {
			rc = 1
		}
		k[c] = (target[c] / tG) / rc * exp * bright
	}
	g := ep.Gamma
	if g <= 0 {
		g = 2.222
	}
	s := ep.Shadow
	if s <= 0 {
		s = 4.5
	}
	return pyramid.FoldParams{K: k, Pwr: 1 / g, Ts: s}
}

// targetWBMul resolves the effective WB multipliers for ep, mirroring
// edit.LibrawParams + libraw's apply so the folded frame matches the exact
// decode. refMul is the reference's as-shot WB; camXYZ resolves Kelvin.
func targetWBMul(ep *edit.Params, refMul [4]float64, camXYZ [4][3]float64) [4]float64 {
	switch ep.WBMode {
	case edit.WBKelvin:
		if ep.WBKelvin > 0 {
			return libraw.AdjustWB(libraw.KelvinMulFromMatrix(camXYZ, ep.WBKelvin), 0, ep.WBTint)
		}
	case edit.WBCustom:
		base := ep.WBMul
		if base == ([4]float64{}) {
			base = refMul
		}
		return libraw.AdjustWB(base, ep.WBTemp, ep.WBTint)
	default: // camera (as-shot) base, optionally warmed/tinted
		if ep.WBTemp != 0 || ep.WBTint != 0 {
			return libraw.AdjustWB(refMul, ep.WBTemp, ep.WBTint)
		}
	}
	return refMul
}

// PickWhiteBalance returns the edit state that neutralizes the surface at the
// given relative coordinates (0..1 in the displayed, orientation-corrected
// image): wbMode=custom with multipliers computed from the scene-linear camera
// values. Because it reads the demosaiced sensor colour (not the developed
// preview) the result depends only on the pixel — one click lands the neutral
// instead of needing to converge over several, and clicking the same spot
// always yields the same balance regardless of the current draft's WB.
func (e *Edits) PickWhiteBalance(ctx context.Context, photoID int64, params edit.Params, x, y float64) (*edit.Params, error) {
	if x < 0 || x > 1 || y < 0 || y > 1 {
		return nil, aprot.ErrInvalidParams("pick coordinates must be within 0..1")
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	var ep *edit.Params
	if !params.IsNeutral() {
		ep = &params
	}
	// Sample the scene-linear reference (demosaiced, at the camera's as-shot
	// WB, no gamma/look) — the same buffer the fold preview uses — so no
	// display-curve inversion is needed and the sampled colour is the camera's.
	entry, err := e.linearMaster(ctx, photoID, photo, ep)
	if err != nil {
		return nil, err
	}
	// The click is relative to the displayed (rotated, mirrored, possibly
	// cropped) frame while the reference is the full oriented frame, so map
	// through the crop rectangle (fractions of the displayed frame), then
	// undo the mirror, then the quarter turns — the display transform is
	// flip∘rotate, so its inverse runs in that order. A straighten angle is
	// ignored — a WB patch spans enough pixels that a few degrees don't move
	// the sampled colour.
	fx, fy := x, y
	if params.HasCrop() {
		fx = params.CropX + x*params.CropW
		fy = params.CropY + y*params.CropH
	}
	if params.FlipH {
		fx = 1 - fx
	}
	switch params.RotateTurns() {
	case 1: // displayed = oriented turned 90° CW: (x,y) → (1-y, x)
		fx, fy = fy, 1-fx
	case 2:
		fx, fy = 1-fx, 1-fy
	case 3:
		fx, fy = 1-fy, fx
	}
	rl, gl, bl := samplePatchLinearRef(entry.lin, fx, fy)
	if rl < 8 || gl < 8 || bl < 8 { // 16-bit linear; a neutral gray is thousands
		log.Printf("wb pick: too-dark patch at (%.3f,%.3f)->(%.3f,%.3f) linear rl=%.4g gl=%.4g bl=%.4g on %dx%d reference",
			x, y, fx, fy, rl, gl, bl, entry.lin.Bounds().Dx(), entry.lin.Bounds().Dy())
		return nil, aprot.ErrInvalidParams("picked area is too dark — pick a brighter neutral area")
	}

	// Neutralizing custom multipliers, normalized to green. The reference
	// already carries the as-shot WB (cam), so to make the picked surface
	// neutral each channel is scaled by the as-shot ratio times 1/its-value:
	// m[c] = (cam[c]/cam[G]) · (lin[G]/lin[c]).
	cam := entry.refMul
	cg := cam[1]
	if cg <= 0 {
		cg = 1
	}
	mul := [4]float64{
		cam[0] / cg * (gl / rl),
		1,
		cam[2] / cg * (gl / bl),
		1,
	}

	out := params
	out.WBMode = edit.WBCustom
	out.WBMul = mul
	out.WBTemp, out.WBTint, out.WBKelvin = 0, 0, 0
	return &out, nil
}

// samplePatchLinearRef averages a small patch of the scene-linear reference
// around the given relative coordinates. Samples are 16-bit linear (0..65535);
// only their per-channel ratios matter to white balance.
func samplePatchLinearRef(img *image.RGBA64, x, y float64) (r, g, b float64) {
	bnd := img.Bounds()
	cx := bnd.Min.X + int(x*float64(bnd.Dx()-1))
	cy := bnd.Min.Y + int(y*float64(bnd.Dy()-1))
	const rad = 3
	var n float64
	for py := cy - rad; py <= cy+rad; py++ {
		for px := cx - rad; px <= cx+rad; px++ {
			if px < bnd.Min.X || px >= bnd.Max.X || py < bnd.Min.Y || py >= bnd.Max.Y {
				continue
			}
			o := img.PixOffset(px, py)
			r += float64(uint32(img.Pix[o])<<8 | uint32(img.Pix[o+1]))
			g += float64(uint32(img.Pix[o+2])<<8 | uint32(img.Pix[o+3]))
			b += float64(uint32(img.Pix[o+4])<<8 | uint32(img.Pix[o+5]))
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return r / n, g / n, b / n
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
	rgba, err := e.previewDecode(ctx, photoID, photo, &out)
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
			if ep, err := edit.Parse(p.EditParams.String); err == nil {
				params = *ep
			}
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
		h := hash
		e.deps.patchFolderPhotos(p.FolderID, []PhotoPatch{{ID: photoID, EditHash: &h}})
		e.deps.writeSidecarFor(context.WithoutCancel(ctx), p)
		// Prefetch, not Visible: this warm runs fire-and-forget on a detached
		// context (no viewport to cancel it), so it must never outrank the photo
		// the user is actually looking at. Quick-dial edits fire this DURING cull
		// navigation — at Visible priority it competed head-to-head with the next
		// frame's render for a pool worker and helped freeze the browse.
		go e.deps.Cache.Ensure(context.Background(), p, "512", hash, decode.PriorityPrefetch)
	}
	return nil
}
