package pyramid

import (
	"image"
	"math"
)

// AI-mask edge refinement. A 1024-long-edge coverage plane bilinearly
// upsampled onto a 24 MP export reads soft and slightly misregistered around
// fine structure (hair, branches). The fix is a fast guided filter (He et
// al.): compute guided-filter coefficients (a, b) against a subsampled luma
// of the render target, then per full-res pixel evaluate
//
//	w = a·luma + b
//
// so the mask's transition snaps to the image's own edges. Cost is O(N_low)
// for the coefficient field plus one multiply-add per output pixel — a few
// ms on an export, which is why it only engages on renders at or above
// guidedMinLongEdge (interactive 1024 drafts keep the pure sampling path;
// the 2048 settle and everything derived from full-res renders refine, so
// what the user zooms into matches what exports).
const (
	guidedMinLongEdge = 2048
	guidedRadius      = 8    // low-res pixels; wide enough to bridge model-vs-image misregistration
	guidedEps         = 2e-3 // luma variance floor: smaller snaps harder to edges
)

// guidedEval wraps an AI mask's plane-sampling evaluator with the refinement.
type guidedEval struct {
	base   *brushEval
	img    *image.RGBA
	s      int // subsample factor between output and coefficient grid
	lw, lh int
	a, b   []float32 // mean GF coefficients on the low-res grid; weight in 0..1
}

// newGuidedEval returns nil when refinement shouldn't engage (output too
// small relative to the map, or too small absolutely).
func newGuidedEval(base *brushEval, img *image.RGBA) *guidedEval {
	if img == nil {
		return nil
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	// The block size follows the whole output, so a window (whose origin
	// RenderWindow puts on the block grid) refines on the grid the whole
	// render does.
	outLong := max(base.f.outW, base.f.outH)
	mapLong := max(base.pw, base.ph)
	if outLong < guidedMinLongEdge || outLong < 2*mapLong {
		return nil
	}
	s := (outLong + mapLong - 1) / mapLong
	lw, lh := (w+s-1)/s, (h+s-1)/s

	// Low-res guidance: block-averaged luma in 0..1.
	lowI := make([]float32, lw*lh)
	for ly := 0; ly < lh; ly++ {
		for lx := 0; lx < lw; lx++ {
			var sum, n int
			for y := ly * s; y < min((ly+1)*s, h); y++ {
				row := img.Pix[y*img.Stride:]
				for x := lx * s; x < min((lx+1)*s, w); x++ {
					i := x * 4
					sum += (299*int(row[i]) + 587*int(row[i+1]) + 114*int(row[i+2])) / 1000
					n++
				}
			}
			if n > 0 {
				lowI[ly*lw+lx] = float32(sum) / (255 * float32(n))
			}
		}
	}

	// Low-res coarse weights in 0..1, sampled through the ordinary evaluator
	// at block centers.
	lowW := make([]float32, lw*lh)
	wrow := make([]uint16, w)
	for ly := 0; ly < lh; ly++ {
		y := min(ly*s+s/2, h-1)
		for i := range wrow {
			wrow[i] = 0
		}
		x0, x1 := base.weightRow(y, wrow)
		for lx := 0; lx < lw; lx++ {
			x := min(lx*s+s/2, w-1)
			if x >= x0 && x < x1 {
				lowW[ly*lw+lx] = float32(wrow[x]) / 256
			}
		}
	}

	// Guided filter: a = cov(I,W)/(var(I)+eps), b = meanW − a·meanI, then the
	// coefficients are themselves box-averaged (the standard final smoothing).
	meanI := boxBlurF32(lowI, lw, lh, guidedRadius)
	meanW := boxBlurF32(lowW, lw, lh, guidedRadius)
	ii := make([]float32, lw*lh)
	iw := make([]float32, lw*lh)
	for i := range lowI {
		ii[i] = lowI[i] * lowI[i]
		iw[i] = lowI[i] * lowW[i]
	}
	corrII := boxBlurF32(ii, lw, lh, guidedRadius)
	corrIW := boxBlurF32(iw, lw, lh, guidedRadius)
	a := make([]float32, lw*lh)
	bb := make([]float32, lw*lh)
	for i := range a {
		varI := corrII[i] - meanI[i]*meanI[i]
		covIW := corrIW[i] - meanI[i]*meanW[i]
		a[i] = covIW / (varI + guidedEps)
		bb[i] = meanW[i] - a[i]*meanI[i]
	}
	// The final coefficient smoothing uses a tighter radius than the
	// statistics window: full-radius averaging dilutes the edge response the
	// statistics just found (the mask stops snapping), while a small blur
	// still suppresses blocking from the per-window estimates.
	return &guidedEval{
		base: base, img: img, s: s, lw: lw, lh: lh,
		a: boxBlurF32(a, lw, lh, guidedRadius/4),
		b: boxBlurF32(bb, lw, lh, guidedRadius/4),
	}
}

func (g *guidedEval) weightRow(y int, wrow []uint16) (int, int) {
	width := len(wrow)
	// The refinement's support extends one GF radius beyond the coarse
	// bounds; pad the culling box accordingly.
	pad := (guidedRadius + 1) * g.s
	x0 := max(0, g.base.xMin-pad)
	x1 := min(width, g.base.xMax+pad)
	if g.base.invert {
		x0, x1 = 0, width
	} else if y < g.base.yMin-pad || y >= g.base.yMax+pad || x0 >= x1 {
		return 0, 0
	}

	// Bilinear taps into the coefficient grid, at (y - s/2) / s blocks: in
	// integers, then the fraction, so a buffer whose origin sits on the
	// block grid (a window, RenderWindow) gets the same taps as the whole
	// frame — float32 coordinates rounded differently by where they start.
	s2 := 2 * g.s
	ny := 2*y - g.s
	ly0 := ny / s2
	ty := float32(ny-ly0*s2) / float32(s2)
	if ly0 < 0 {
		ly0, ty = 0, 0
	}
	ly1 := min(ly0+1, g.lh-1)
	if ly0 >= g.lh {
		ly0, ly1, ty = g.lh-1, g.lh-1, 0
	}

	row := g.img.Pix[y*g.img.Stride:]
	for x := x0; x < x1; x++ {
		nx := 2*x - g.s
		lx0 := nx / s2
		tx := float32(nx-lx0*s2) / float32(s2)
		if lx0 < 0 {
			lx0, tx = 0, 0
		}
		lx1 := min(lx0+1, g.lw-1)
		if lx0 >= g.lw {
			lx0, lx1, tx = g.lw-1, g.lw-1, 0
		}
		w00 := (1 - tx) * (1 - ty)
		w10 := tx * (1 - ty)
		w01 := (1 - tx) * ty
		w11 := tx * ty
		av := g.a[ly0*g.lw+lx0]*w00 + g.a[ly0*g.lw+lx1]*w10 + g.a[ly1*g.lw+lx0]*w01 + g.a[ly1*g.lw+lx1]*w11
		bv := g.b[ly0*g.lw+lx0]*w00 + g.b[ly0*g.lw+lx1]*w10 + g.b[ly1*g.lw+lx0]*w01 + g.b[ly1*g.lw+lx1]*w11

		i := x * 4
		luma := float32(299*int(row[i])+587*int(row[i+1])+114*int(row[i+2])) / (1000 * 255)
		// lowW came through base.weightRow, which already applied Invert —
		// the coefficients describe the final weight field, no flip here.
		wf := av*luma + bv
		q := int32(math.Round(float64(wf) * 256))
		if q < 0 {
			q = 0
		}
		if q > 256 {
			q = 256
		}
		wrow[x] = uint16(q)
	}
	return x0, x1
}

// centroid delegates to the coverage plane the refinement wraps: the guided
// filter snaps the mask's edge to the image's, which moves no meaningful mass.
func (g *guidedEval) centroid() (float64, float64, bool) { return g.base.centroid() }

// boxBlurF32 returns a box-averaged copy (edge-clamped running sums per
// axis), the float sibling of boxBlurU8.
func boxBlurF32(p []float32, w, h, radius int) []float32 {
	if radius < 1 || w == 0 || h == 0 {
		out := make([]float32, len(p))
		copy(out, p)
		return out
	}
	tmp := make([]float32, len(p))
	out := make([]float32, len(p))
	// The running sums are float64: a float32 sum drifts by where it
	// started, and a window (RenderWindow) starts its rows and columns
	// elsewhere than the whole frame does.
	n := float64(2*radius + 1)
	for y := 0; y < h; y++ {
		row := p[y*w : (y+1)*w]
		orow := tmp[y*w : (y+1)*w]
		var sum float64
		for x := -radius; x <= radius; x++ {
			sum += float64(row[min(max(x, 0), w-1)])
		}
		for x := 0; x < w; x++ {
			orow[x] = float32(sum / n)
			sum += float64(row[min(x+radius+1, w-1)]) - float64(row[max(x-radius, 0)])
		}
	}
	for x := 0; x < w; x++ {
		var sum float64
		for y := -radius; y <= radius; y++ {
			sum += float64(tmp[min(max(y, 0), h-1)*w+x])
		}
		for y := 0; y < h; y++ {
			out[y*w+x] = float32(sum / n)
			sum += float64(tmp[min(y+radius+1, h-1)*w+x]) - float64(tmp[max(y-radius, 0)*w+x])
		}
	}
	return out
}
