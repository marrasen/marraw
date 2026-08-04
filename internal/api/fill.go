package api

import (
	"context"
	"fmt"
	"image"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"
	xdraw "golang.org/x/image/draw"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/inpaint"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// fillGenLongEdge caps the fill patch's generation resolution: the spot
// window renders at its half-size-decode native resolution up to this long
// edge. Dust-sized spots come out effectively 1:1; only very large fills
// upsample at export, the accepted trade for a warm-decode generation.
const fillGenLongEdge = 1024

// fillSem serializes inpaint generation across the daemon — see
// generateFillPatch. Buffered at one: the work is a warm decode plus a forward
// pass, and running several at once buys nothing while costing the decode
// handles browsing needs.
var fillSem = make(chan struct{}, 1)

// Sentinels the client matches to explain a refused removal, the
// aiModelNotDownloadedMsg precedent (matched as substrings — never reword one
// without updating client/src/lib).
const (
	maskFillNoRegionMsg = "mask has no region to remove"
	maskFillTooLargeMsg = "mask region is too large to remove"
)

// FillResult reports the generated (or already cached) fill patch's version
// tag. Generated mirrors AIMapResult.Generated: true only when pixels were
// (re)computed, so the client busts its thumbnail cache exactly then.
type FillResult struct {
	FillVer   string `json:"fillVer"`
	Generated bool   `json:"generated"`
}

// FillModelStatus reports the download state of the inpainting model — what
// the client's consent dialog shows before the first content-aware fill.
func (e *Edits) FillModelStatus(ctx context.Context) (*AIModelInfo, error) {
	if e.deps.Infer == nil {
		return nil, fmt.Errorf("fill: inference is not configured")
	}
	spec := inpaint.Spec()
	return &AIModelInfo{Downloaded: e.deps.Infer.HasModel(spec), Bytes: spec.Bytes}, nil
}

// GenerateFill ensures the ML inpaint patch for one fill-mode spot exists and
// returns its version tag. Idempotent and cheap when the patch is already
// cached — the client calls it on every commit of an edit with fill spots,
// and only a changed input (spot geometry, decode settings, orientation —
// edit.SpotFillKey) actually re-runs the model. The model download happens
// ONLY with allowDownload (the consent-dialog contract, the GenerateAIMap
// precedent); without consent a missing model fails with the shared sentinel
// so the client can ask.
//
// params is the client's draft (not necessarily committed): the patch must
// match what the preview shows. It is normalized first so the key and the
// stored geometry agree with what a commit would persist.
func (e *Edits) GenerateFill(ctx context.Context, photoID int64, params edit.Params, spotIndex int, allowDownload bool) (*FillResult, error) {
	params.Normalize()
	if spotIndex < 0 || spotIndex >= len(params.Spots) {
		return nil, fmt.Errorf("fill: spot index %d out of range", spotIndex)
	}
	spot := &params.Spots[spotIndex]
	if spot.Mode != edit.SpotFill {
		return nil, fmt.Errorf("fill: spot %d is not a fill spot", spotIndex)
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	fills := e.deps.Cache.Fills
	if fills == nil || e.deps.Infer == nil {
		return nil, fmt.Errorf("fill: inference is not configured")
	}
	key := params.SpotFillKey(spot)
	// Fast path: a cached patch returns without opening a task, so the
	// on-commit calls are free while nothing changed.
	if fills.Has(photo.CacheKey, key) {
		return &FillResult{FillVer: inpaint.FillVer()}, nil
	}
	if !allowDownload && !e.deps.Infer.HasModel(inpaint.Spec()) {
		return nil, fmt.Errorf("fill: %s", aiModelNotDownloadedMsg)
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, "Fill: "+photo.FileName, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "fill"})
	res, err := e.generateFillPatch(tctx, photo, &params, key,
		func(frameW, frameH float64) (float64, float64, float64, float64) {
			return pyramid.SpotFillWindow(frameW/frameH, spot)
		},
		func(rectFrame image.Rectangle, mw, mh int, frameW, frameH float64) *image.Gray {
			return pyramid.SpotFillMask(rectFrame, mw, mh, frameW, frameH, spot)
		},
		func(done, total int64) {
			task.Progress(int(done>>20), int(total>>20)) // model download, MB units
		})
	task.Err(err)
	return res, err
}

// GenerateMaskFill ensures the ML inpaint patch for one Remove mask exists and
// returns its version tag — GenerateFill's counterpart for a mask's region.
// Idempotent and cheap when the patch is cached, so the client may call it on
// every commit; only a changed region (strokes, AI kind/instance/threshold,
// decode settings, orientation — edit.MaskFillKey) re-runs the model.
//
// The mask's AI map must already exist: generation reads the stored map rather
// than running detection, so the client generates maps first (the same order
// the panel already uses to show the mask at all).
func (e *Edits) GenerateMaskFill(ctx context.Context, photoID int64, params edit.Params, maskIndex int, allowDownload bool) (*FillResult, error) {
	params.Normalize()
	if maskIndex < 0 || maskIndex >= len(params.Masks) {
		return nil, fmt.Errorf("fill: mask index %d out of range", maskIndex)
	}
	mask := &params.Masks[maskIndex]
	if !mask.Remove {
		return nil, fmt.Errorf("fill: mask %d is not a removal", maskIndex)
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	fills := e.deps.Cache.Fills
	if fills == nil || e.deps.Infer == nil {
		return nil, fmt.Errorf("fill: inference is not configured")
	}
	key := params.MaskFillKey(mask)
	if fills.Has(photo.CacheKey, key) {
		return &FillResult{FillVer: inpaint.FillVer()}, nil
	}
	if !allowDownload && !e.deps.Infer.HasModel(inpaint.Spec()) {
		return nil, fmt.Errorf("fill: %s", aiModelNotDownloadedMsg)
	}
	// The region comes from the mask's parameters and its stored map, never
	// from rendered pixels, so it is decided here — before any decode — and the
	// composite re-derives the identical region from the same inputs. The
	// catalog's oriented dimensions stand in for the decode's frame: only their
	// ratio matters, and it matches to within a pixel of rounding.
	fw, fh := orientedFrameDims(photo, &params)
	ai := e.deps.Cache.AIMaps.SetFor(photo.CacheKey, &params)
	region, ok := pyramid.MaskFillRegion(mask, fw, fh, ai)
	if !ok {
		return nil, fmt.Errorf("fill: %s", maskFillNoRegionMsg)
	}
	if region.Area > pyramid.MaskFillMaxArea {
		return nil, fmt.Errorf("fill: %s", maskFillTooLargeMsg)
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, "Remove: "+photo.FileName, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "fill"})
	res, err := e.generateFillPatch(tctx, photo, &params, key,
		func(frameW, frameH float64) (float64, float64, float64, float64) {
			// Re-derive at the real frame aspect: the brush plane and the map
			// are aspect-shaped, so the region's bounds depend on it.
			r, ok := pyramid.MaskFillRegion(mask, frameW, frameH, ai)
			if !ok {
				return 0, 0, 0, 0
			}
			region = r
			return pyramid.MaskFillWindow(frameW/frameH, r)
		},
		func(rectFrame image.Rectangle, mw, mh int, frameW, frameH float64) *image.Gray {
			return pyramid.MaskFillMask(rectFrame, mw, mh, frameW, frameH, region)
		},
		func(done, total int64) {
			task.Progress(int(done>>20), int(total>>20)) // model download, MB units
		})
	task.Err(err)
	return res, err
}

// orientedFrameDims is the uncropped frame a mask's coordinates live in: the
// catalog's dimensions turned upright by the EXIF orientation, then by the
// edit's own quarter turns (the aimasks displayDims twin, without the crop).
func orientedFrameDims(photo store.Photo, params *edit.Params) (w, h float64) {
	dw, dh := photo.Width, photo.Height
	if photo.Orientation == 5 || photo.Orientation == 6 {
		dw, dh = dh, dw
	}
	if params.RotateTurns()%2 != 0 {
		dw, dh = dh, dw
	}
	return float64(max(1, dw)), float64(max(1, dh))
}

// generateFillPatch renders a region's context window pre-look, inpaints it
// and caches the patch — the shared core of spot fills and mask removals. The
// window is cut from the warm half-size decode at the edit's LibRaw inputs —
// the same pixels (decode + residual-exposure fold + quarter-rotate/mirror, no
// straighten/crop) the render paths hand to ApplyHeal, so the composite blends
// seamlessly by construction.
//
// windowFor and maskFor are the caller's region: the first returns the context
// window in oriented-frame fractions once the frame size is known, the second
// rasterizes what the model must repaint inside it (255 keep / 0 inpaint).
func (e *Edits) generateFillPatch(ctx context.Context, photo store.Photo, params *edit.Params, key string,
	windowFor func(frameW, frameH float64) (x0, y0, x1, y1 float64),
	maskFor func(rectFrame image.Rectangle, mw, mh int, frameW, frameH float64) *image.Gray,
	onProgress func(done, total int64)) (*FillResult, error) {
	// One inference at a time across the whole daemon. A generation pins a
	// LibRaw handle for its warm decode, and the handle cache is small: without
	// this a burst of fills would hold every slot and cull navigation would
	// queue behind them, which is the one thing browsing may never do.
	select {
	case fillSem <- struct{}{}:
		defer func() { <-fillSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	decode, err := e.previewDecode(ctx, photo.ID, photo, params)
	if err != nil {
		return nil, err
	}
	// Lens correction first, for the same reason the render paths run it
	// first: the patch is composited by ApplyHeal onto an already-corrected
	// frame, so it has to be inpainted from corrected pixels or the fill
	// would be cut from a slightly different geometry than it lands on.
	corrected := pyramid.ApplyLens(decode,
		pyramid.LensWarp(e.deps.Cache.Lenses.For(photo), params, decode.Bounds().Dx(), decode.Bounds().Dy()),
		params)
	// Orient only — the window lives in oriented-frame fractions, before
	// straighten and crop. ApplyGeometry may return the shared cached decode
	// unchanged (no rotate, no flip), so nothing below may mutate `oriented`;
	// the window crop always copies.
	geo := edit.Params{Rotate: params.RotateTurns(), FlipH: params.FlipH}
	oriented := pyramid.ApplyGeometry(corrected, &geo)
	ob := oriented.Bounds()
	frameW, frameH := float64(ob.Dx()), float64(ob.Dy())
	if frameW == 0 || frameH == 0 {
		return nil, fmt.Errorf("fill: empty decode")
	}
	wx0, wy0, wx1, wy1 := windowFor(frameW, frameH)
	rect := image.Rect(
		int(wx0*frameW), int(wy0*frameH),
		max(int(wx0*frameW)+1, int(wx1*frameW+0.5)), max(int(wy0*frameH)+1, int(wy1*frameH+0.5)),
	).Add(ob.Min).Intersect(ob)
	if rect.Empty() {
		return nil, fmt.Errorf("fill: region window off frame")
	}
	// Window copy at generation resolution (never mutating the shared decode).
	mw, mh := rect.Dx(), rect.Dy()
	if long := max(mw, mh); long > fillGenLongEdge {
		mw, mh = mw*fillGenLongEdge/long, mh*fillGenLongEdge/long
	}
	window := image.NewRGBA(image.Rect(0, 0, max(1, mw), max(1, mh)))
	xdraw.CatmullRom.Scale(window, window.Bounds(), oriented, rect, xdraw.Src, nil)
	// The stops LibRaw's exp_shift couldn't bake — the render paths' post-
	// decode fold, applied here to the window copy (pointwise, so window-then-
	// fold equals fold-then-window).
	pyramid.ApplyExposureEV(window, params.ResidualExpEV(), params)

	rectFrame := rect.Sub(ob.Min)
	mask := maskFor(rectFrame, window.Bounds().Dx(), window.Bounds().Dy(), frameW, frameH)

	downloaded := false
	out, err := inpaint.Generate(ctx, e.deps.Infer, window, mask, func(done, total int64) {
		downloaded = true
		if onProgress != nil {
			onProgress(done, total)
		}
	})
	if err != nil {
		return nil, err
	}
	if err := e.deps.Cache.Fills.Save(photo.CacheKey, key, out); err != nil {
		return nil, err
	}
	// A fresh patch changes pixels for the SAVED edit without changing its
	// hash (the generateAIMap precedent): drop the stale cached renditions.
	if photo.EditHash != edit.BaseHash {
		e.deps.Cache.InvalidateEdit(photo.CacheKey, photo.EditHash)
	}
	if downloaded {
		aprot.TriggerRefresh(ctx, modelsInfoKey) // Settings' model list is live
	}
	return &FillResult{FillVer: inpaint.FillVer(), Generated: true}, nil
}
