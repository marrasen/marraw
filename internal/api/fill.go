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
	res, err := e.generateFill(tctx, photo, &params, spot, key, func(done, total int64) {
		task.Progress(int(done>>20), int(total>>20)) // model download, MB units
	})
	task.Err(err)
	return res, err
}

// generateFill renders the spot's context window pre-look, inpaints it and
// caches the patch. The window is cut from the warm half-size decode at the
// edit's LibRaw inputs — the same pixels (decode + residual-exposure fold +
// quarter-rotate/mirror, no straighten/crop) the render paths hand to
// ApplyHeal, so the composite blends seamlessly by construction.
func (e *Edits) generateFill(ctx context.Context, photo store.Photo, params *edit.Params, spot *edit.Spot, key string, onProgress func(done, total int64)) (*FillResult, error) {
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
	wx0, wy0, wx1, wy1 := pyramid.SpotFillWindow(frameW/frameH, spot)
	rect := image.Rect(
		int(wx0*frameW), int(wy0*frameH),
		max(int(wx0*frameW)+1, int(wx1*frameW+0.5)), max(int(wy0*frameH)+1, int(wy1*frameH+0.5)),
	).Add(ob.Min).Intersect(ob)
	if rect.Empty() {
		return nil, fmt.Errorf("fill: spot window off frame")
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
	mask := pyramid.SpotFillMask(rectFrame, window.Bounds().Dx(), window.Bounds().Dy(), frameW, frameH, spot)

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
