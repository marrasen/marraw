package pyramid

import (
	"image"
	"sync"
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
	return measureFrame(frameProxy(src, e, expEV, lensc), gamma, e, ai, fills)
}

// frameProxy is the working-size frame a render of e from the decode src
// measures on: the decode shrunk to a size that leaves the crop at the
// working size (shrinkDecode), then the exposure, lens correction and
// geometry the render runs. src is only read.
func frameProxy(src *image.RGBA, e *edit.Params, expEV float64, lensc *lens.Correction) *image.RGBA {
	return proxyFrom(shrinkDecode(src, e), e, expEV, lensc)
}

// shrinkDecode is the decode src shrunk for frameProxy, in a buffer of its
// own.
func shrinkDecode(src *image.RGBA, e *edit.Params) *image.RGBA {
	img := scaleToLongEdge(src, previewFrameEdge(src, fxPlaneLongEdge, e))
	if img == src {
		img = copyRGBA(src, src.Bounds())
	}
	return img
}

// shrunk is the last decode a window shrank (shrinkKept). A window of the
// photo open in the editor shrinks the same decode on every frame of a
// drag, and the shrink is most of what measuring the frame costs. The
// decode is held weakly, so this keeps none alive.
var shrunk struct {
	sync.Mutex
	src  weak.Pointer[image.RGBA]
	edge int
	img  *image.RGBA
}

// shrinkKept is shrinkDecode for src, a decode nothing writes to, as a
// window's is: the last one is kept. The result is a copy the caller owns.
func shrinkKept(src *image.RGBA, e *edit.Params) *image.RGBA {
	edge := previewFrameEdge(src, fxPlaneLongEdge, e)
	key := weak.Make(src)
	shrunk.Lock()
	defer shrunk.Unlock()
	if shrunk.img == nil || shrunk.src != key || shrunk.edge != edge {
		shrunk.src, shrunk.edge, shrunk.img = key, edge, shrinkDecode(src, e)
	}
	return copyRGBA(shrunk.img, shrunk.img.Bounds())
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
// dehaze, and keeps what the whole-frame stages measure on the way.
func measureFrame(img *image.RGBA, gamma float64, e *edit.Params, ai AIMapSet, fills FillSet) *FrameStats {
	st := &FrameStats{fx: make([]*maskFXStats, len(e.Masks)), measuring: true}
	ApplyHeal(img, e, ai, fills)
	ApplyLook(img, gamma, e)
	applyMasksIn(img, wholeRegion(img), e, ai, st)
	ApplyBW(img, e)
	if e.Dehaze > 0 {
		st.veil = estimateVeil(img)
	}
	st.measuring = false
	return st
}
