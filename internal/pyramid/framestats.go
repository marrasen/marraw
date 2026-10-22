package pyramid

import (
	"context"
	"fmt"
	"image"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"weak"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/lens"
)

// Some stages read the whole frame. Dehaze measures the frame's veil, and
// a mask's spatial effects (blur, glow, streaks and the rest) gather over
// the whole frame at a fixed working size (fxPlaneLongEdge). A render no
// larger than that works them out on its own pixels. A larger one takes
// them from a working-size render of its frame (FrameStats), so a window
// of it (RenderWindow) can make the same small render and get the same
// values, without the rest of the frame at full size. Renders that start
// from the full decode — the 1:1 tiles, the windows of them, the export —
// make that small render from the decode (frameProxy), each the same way,
// so their pixels match.

// FrameStats is what a render larger than the working size takes from a
// working-size render of its frame: each mask's effects, worked out there,
// and the veil dehaze takes away.
type FrameStats struct {
	fx   []*maskFXStats // by mask index; nil where a mask runs no effect
	veil int
	// measuring is set while the working-size render fills fx in.
	measuring bool
}

// maskFXStats is one mask's effects worked out at the working size: the
// graded blur's and the bokeh's levels, and the shared buffer the other
// effects ran in, with the light its last passes added.
type maskFXStats struct {
	blur, bokeh []*fxSource // nil where the pass did not run
	shared      *fxSource   // nil when the mask has no effect beyond blur
	delta       *fxDelta    // nil when an effect threw the detail away
}

// needsFrameStats reports whether a render of e reads the whole frame:
// dehaze, or an effect on a mask.
func needsFrameStats(e *edit.Params) bool {
	if e == nil {
		return false
	}
	if e.Dehaze > 0 {
		return true
	}
	for i := range e.Masks {
		m := &e.Masks[i]
		if !m.Disabled && m.Adjust.HasFX() {
			return true
		}
	}
	return false
}

// FrameStatsFor is the FrameStats of a render of e that starts from the
// decode src and folds expEV in after it, as the 1:1 render and the export
// do; nil when e reads nothing over the whole frame. src is only read.
func FrameStatsFor(src *image.RGBA, e *edit.Params, expEV, gamma float64, lensc *lens.Correction, ai AIMapSet, fills FillSet) *FrameStats {
	if !needsFrameStats(e) {
		return nil
	}
	return measureFrame(context.Background(), frameProxy(src, e, expEV, lensc), gamma, e, ai, fills)
}

// frameProxy is the working-size frame a render of e from the decode src
// measures on: the decode shrunk to a size that leaves the crop at the
// working size (shrinkDecode), then the exposure, lens correction and
// geometry the render runs. src is only read.
func frameProxy(src *image.RGBA, e *edit.Params, expEV float64, lensc *lens.Correction) *image.RGBA {
	return proxyFrom(shrinkDecode(src, e), e, expEV, lensc)
}

// proxyMaxEdge bounds the shrunk decode frameProxy works from. A crop
// smaller than a quarter of the frame's long edge would otherwise ask for
// the decode at full size to leave the crop at the working size; past the
// bound, the working-size frame comes out smaller than that, which the
// effects are drawn from just the same.
const proxyMaxEdge = 4 * fxPlaneLongEdge

// shrinkDecode is the decode src shrunk for frameProxy, in a buffer of its
// own.
func shrinkDecode(src *image.RGBA, e *edit.Params) *image.RGBA {
	img := scaleToLongEdge(src, min(previewFrameEdge(src, fxPlaneLongEdge, e), proxyMaxEdge))
	if img == src {
		img = copyRGBA(src, src.Bounds())
	}
	return img
}

// windowKept is what windows of the photo open in the editor measured last
// (windowStats): on every frame of a drag they shrink the same decode, and
// a pan, or a drag of a stage after the measure, asks for the same
// FrameStats again. The decode is held weakly, so this keeps none alive,
// and it goes when the kept decode does (forgetWindowStats).
var windowKept struct {
	sync.Mutex
	src    weak.Pointer[image.RGBA]
	edge   int
	shrunk *image.RGBA
	key    string // windowStatsKey of st
	st     *FrameStats
}

// frameMeasures counts the measures windowStats ran, for tests.
var frameMeasures atomic.Int64

// windowStats is FrameStatsFor for a window: src is a decode nothing
// writes to, and what it measured last is windowKept. It returns nil when ctx is
// done before it is measured.
func windowStats(ctx context.Context, src *image.RGBA, e *edit.Params, expEV, gamma float64, lensc *lens.Correction, ai AIMapSet, fills FillSet) *FrameStats {
	if !needsFrameStats(e) {
		return nil
	}
	ref := weak.Make(src)
	edge := min(previewFrameEdge(src, fxPlaneLongEdge, e), proxyMaxEdge)
	key := windowStatsKey(e, expEV, gamma, lensc, ai, fills)
	windowKept.Lock()
	same := windowKept.shrunk != nil && windowKept.src == ref && windowKept.edge == edge
	shrunk, st := windowKept.shrunk, windowKept.st
	measured := same && st != nil && windowKept.key == key
	windowKept.Unlock()
	if measured {
		return st
	}
	if !same {
		// Outside the lock: a 42 MP shrink must not hold up windows of
		// another photo.
		shrunk = shrinkDecode(src, e)
	}
	if ctx.Err() != nil {
		return nil
	}
	st = measureFrame(ctx, proxyFrom(copyRGBA(shrunk, shrunk.Bounds()), e, expEV, lensc), gamma, e, ai, fills)
	if st == nil {
		return nil
	}
	frameMeasures.Add(1)
	windowKept.Lock()
	windowKept.src, windowKept.edge, windowKept.shrunk, windowKept.key, windowKept.st = ref, edge, shrunk, key, st
	windowKept.Unlock()
	return st
}

// windowStatsKey says what FrameStats depend on: the edit but for the
// stages after the measure (the detail sliders, dehaze's amount), the
// exposure folded in, the look's lift, the lens profile and the AI maps
// and fill patches the edit reads.
func windowStatsKey(e *edit.Params, expEV, gamma float64, lensc *lens.Correction, ai AIMapSet, fills FillSet) string {
	flat := *e
	flat.Clarity, flat.Texture, flat.Sharpen = 0, 0, 0
	if flat.Dehaze > 0 {
		flat.Dehaze = 1
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%v|%v|%p", flat.Hash(), expEV, gamma, lensc)
	for _, k := range slices.Sorted(maps.Keys(ai)) {
		fmt.Fprintf(&b, "|%s=%p", k, ai[k])
	}
	for _, k := range slices.Sorted(maps.Keys(fills)) {
		fmt.Fprintf(&b, "|%s=%p", k, fills[k])
	}
	return b.String()
}

// forgetWindowStats lets what windows measured go, with the decode they
// measured it from.
func forgetWindowStats() {
	windowKept.Lock()
	windowKept.src, windowKept.edge, windowKept.shrunk, windowKept.key, windowKept.st = weak.Pointer[image.RGBA]{}, 0, nil, "", nil
	windowKept.Unlock()
}

// proxyFrom is frameProxy from the shrunk decode work, which it may write
// to.
func proxyFrom(work *image.RGBA, e *edit.Params, expEV float64, lensc *lens.Correction) *image.RGBA {
	ApplyExposureEV(work, expEV, e)
	b := work.Bounds()
	work = ApplyLens(work, LensWarp(lensc, e, b.Dx(), b.Dy()), e)
	return scaleToLongEdge(ApplyGeometry(work, e), fxPlaneLongEdge)
}

// measureFrame renders img, a working-size frame after geometry, up to
// dehaze, and keeps what the whole-frame stages measure on the way. It
// returns nil when ctx is done (checked between the stages).
func measureFrame(ctx context.Context, img *image.RGBA, gamma float64, e *edit.Params, ai AIMapSet, fills FillSet) *FrameStats {
	st := &FrameStats{fx: make([]*maskFXStats, len(e.Masks)), measuring: true}
	ApplyHeal(img, e, ai, fills)
	ApplyLook(img, gamma, e)
	if ctx.Err() != nil {
		return nil
	}
	applyMasksIn(img, wholeRegion(img), e, ai, st)
	if ctx.Err() != nil {
		return nil
	}
	ApplyBW(img, e)
	if e.Dehaze > 0 {
		st.veil = estimateVeil(img)
	}
	st.measuring = false
	return st
}
