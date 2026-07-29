package lens

import "math"

// Warp is a Correction bound to one frame size and one set of user
// strengths: the auto-scale is solved once here so the per-pixel work is a
// polynomial and nothing else.
//
// The renderer walks the OUTPUT (corrected) frame and asks where each pixel
// came from, so every mapping in this file runs in the "forward" direction —
// undistorted coordinate in, distorted source coordinate out. That is
// lensfun's ModifyCoord_Dist_* / ModifyCoord_TCA_* direction, which is pure
// polynomial evaluation; the iterative Newton solves in its ModifyCoord_UnDist_*
// counterparts are only needed when going the other way.
type Warp struct {
	// halfW, halfH are the frame's half-dimensions in pixels; normScale
	// converts a pixel offset from the centre into normalized coordinates.
	halfW, halfH       float64
	normScale, unScale float64
	distAmt, tcaAmt    float64
	vigAmt             float64
	model              DistModel
	dist               [3]float64
	tca                [6]float64
	vig                [3]float64
	hasTCA, hasVig     bool
	scale              float64 // auto-scale, divides the output coordinate
	distOff            bool
}

// Strengths scales each component of a correction. 1 is the profile's own
// measurement; 0 disables that component; values above 1 overcorrect, which
// is occasionally what a photographer wants from a vignette.
type Strengths struct {
	Distortion, Vignetting, TCA float64
}

// Warp binds a correction to a frame of width×height pixels. It returns nil
// when the strengths leave nothing to do.
//
// The dimensions must be those of the FULL frame the lens projected, before
// any crop: distortion is radial about the optical centre, so a cropped
// frame has to be corrected before it is cut.
func (c *Correction) Warp(width, height int, s Strengths) *Warp {
	if c == nil || width < 2 || height < 2 {
		return nil
	}
	// lfModifier's constructor: the sensor size is quoted for the outer rim
	// of the pixel array, so the diagonal is taken over the full width and
	// height even though coordinates are measured at pixel centres.
	normScale := fullFrameDiag / c.crop / math.Hypot(float64(width), float64(height)) / c.realFocal
	w := &Warp{
		halfW:     float64(width) / 2,
		halfH:     float64(height) / 2,
		normScale: normScale,
		unScale:   1 / normScale,
		distAmt:   math.Max(0, s.Distortion),
		tcaAmt:    math.Max(0, s.TCA),
		vigAmt:    math.Max(0, s.Vignetting),
		scale:     1,
	}
	if c.HasDist() && w.distAmt > 0 {
		w.model, w.dist = c.DistModel, c.Dist
	}
	if c.HasTCA && w.tcaAmt > 0 {
		w.tca, w.hasTCA = c.TCA, true
	}
	if c.HasVig && w.vigAmt > 0 {
		w.vig, w.hasVig = c.Vig, true
	}
	w.distOff = w.model == ""
	if w.distOff && !w.hasTCA && !w.hasVig {
		return nil
	}
	w.scale = w.autoScale()
	return w
}

// Geometric reports whether the warp moves pixels at all. A vignetting-only
// correction does not, which lets the renderer skip the resample entirely
// and apply a gain in place.
func (w *Warp) Geometric() bool { return w != nil && (!w.distOff || w.hasTCA) }

// HasVignetting reports whether a brightness gain is part of this warp.
func (w *Warp) HasVignetting() bool { return w != nil && w.hasVig }

// Source maps an output pixel centre — in pixels, origin at the frame's
// top-left — to the source coordinates its red, green and blue components
// should be sampled from. Without a TCA correction all three are equal.
func (w *Warp) Source(ox, oy float64) (rx, ry, gx, gy, bx, by float64) {
	// Into normalized coordinates about the frame centre, shrinking by the
	// auto-scale so the corrected frame stays full.
	x := (ox - w.halfW) * w.normScale / w.scale
	y := (oy - w.halfH) * w.normScale / w.scale

	dx, dy := w.distort(x, y)
	gx, gy = dx*w.unScale+w.halfW, dy*w.unScale+w.halfH
	if !w.hasTCA {
		return gx, gy, gx, gy, gx, gy
	}
	rdx, rdy, bdx, bdy := w.tcaShift(dx, dy)
	return rdx*w.unScale + w.halfW, rdy*w.unScale + w.halfH,
		gx, gy,
		bdx*w.unScale + w.halfW, bdy*w.unScale + w.halfH
}

// distort evaluates the distortion polynomial in normalized coordinates.
// The strength blends between the identity and the full correction rather
// than scaling the coefficients, so half strength really is half the pixel
// displacement at every radius.
func (w *Warp) distort(x, y float64) (float64, float64) {
	if w.distOff {
		return x, y
	}
	var f float64
	switch w.model {
	case DistPoly3:
		// Rd = Ru * (1 + k1*Ru²)
		f = w.dist[0]*(x*x+y*y) + 1
	case DistPoly5:
		// Rd = Ru * (1 + k1*Ru² + k2*Ru⁴)
		ru2 := x*x + y*y
		f = 1 + w.dist[0]*ru2 + w.dist[1]*ru2*ru2
	case DistPTLens:
		// Rd = Ru * (a*Ru³ + b*Ru² + c*Ru + 1)
		ru2 := x*x + y*y
		ru := math.Sqrt(ru2)
		f = w.dist[0]*ru2*ru + w.dist[1]*ru2 + w.dist[2]*ru + 1
	default:
		return x, y
	}
	f = 1 + (f-1)*w.distAmt
	return x * f, y * f
}

// tcaShift returns the red and blue sampling coordinates for a point that
// green samples at (x, y). Lensfun's poly3 layout is vr, vb, cr, cb, br, bb,
// giving Rd = Ru * (b*Ru² + c*Ru + v) per channel; the linear model is the
// same expression with c = b = 0.
func (w *Warp) tcaShift(x, y float64) (rx, ry, bx, by float64) {
	ru2 := x*x + y*y
	ru := math.Sqrt(ru2)
	fr := w.tca[4]*ru2 + w.tca[2]*ru + w.tca[0]
	fb := w.tca[5]*ru2 + w.tca[3]*ru + w.tca[1]
	fr = 1 + (fr-1)*w.tcaAmt
	fb = 1 + (fb-1)*w.tcaAmt
	return x * fr, y * fr, x * fb, y * fb
}

// VigGain is the multiplier that undoes the lens's fall-off at a source
// pixel, ≥ 1 everywhere the lens darkens the frame. It must be applied to
// LINEAR light — the fall-off is a transmission ratio, so applying it to
// gamma-encoded values would over-brighten the corners.
//
// The "pa" model gives the fall-off itself as c = 1 + k1r² + k2r⁴ + k3r⁶
// (lensfun's ModifyColor_Vignetting_PA); correcting divides by it.
func (w *Warp) VigGain(px, py float64) float64 {
	if !w.hasVig {
		return 1
	}
	x := (px - w.halfW) * w.normScale
	y := (py - w.halfH) * w.normScale
	r2 := x*x + y*y
	r4 := r2 * r2
	c := 1 + w.vig[0]*r2 + w.vig[1]*r4 + w.vig[2]*r4*r2
	if c <= 0.05 {
		return 1 // a degenerate fit would blow the corners out; leave them
	}
	gain := 1 / c
	if w.vigAmt != 1 {
		gain = math.Pow(gain, w.vigAmt)
	}
	return gain
}

// autoScale finds the zoom that keeps the corrected frame free of black
// borders, mirroring lfModifier::GetAutoScale: for each of the four corners
// and four edge midpoints, find the undistorted radius whose distorted
// position lands exactly on the source frame's boundary, and take the
// largest ratio between the point's own radius and that one.
//
// Upstream drives this with Newton's method; a bisection is used here
// instead. The residual is monotone in the radius over the range that
// matters, and bisection cannot diverge on the ultra-wide fits where
// upstream has to give up after 50 iterations.
func (w *Warp) autoScale() float64 {
	if !w.Geometric() {
		return 1
	}
	hw := w.halfW * w.normScale
	hh := w.halfH * w.normScale
	corner := math.Atan2(hh, hw)
	angles := [8]float64{
		0, corner, math.Pi / 2, math.Pi - corner,
		math.Pi, math.Pi + corner, 3 * math.Pi / 2, 2*math.Pi - corner,
	}
	dists := [8]float64{
		hw, math.Hypot(hw, hh), hh, math.Hypot(hw, hh),
		hw, math.Hypot(hw, hh), hh, math.Hypot(hw, hh),
	}

	scale, solved := 0.01, false
	for i, a := range angles {
		ca, sa := math.Cos(a), math.Sin(a)
		edge := w.edgeRadius(ca, sa, hw, hh, dists[i])
		if edge <= 0 {
			continue
		}
		solved = true
		if s := dists[i] / edge; s > scale {
			scale = s
		}
	}
	if !solved {
		// No direction converged — an extreme fit the search window can't
		// bracket. Correct without zooming rather than with a made-up
		// factor; the renderer's clamped sampling keeps the edges sane.
		return 1
	}
	// A permille of headroom, since the boundary between two sampled
	// directions can bulge slightly further in than either of them.
	scale *= 1.001
	if w.hasTCA {
		// The red and blue planes are sampled slightly further out than
		// green, and must not fall off the edge either.
		scale *= 1.001
	}
	return scale
}

// edgeRadius returns the undistorted radius along (ca, sa) whose distorted
// image sits on the source rectangle's boundary, or 0 if no such radius is
// found within a sane search window.
func (w *Warp) edgeRadius(ca, sa, hw, hh, seed float64) float64 {
	// outside grows through 0 exactly at the boundary.
	outside := func(ru float64) float64 {
		x, y := w.distort(ca*ru, sa*ru)
		if w.hasTCA {
			// Take whichever channel reaches furthest out.
			rx, ry, bx, by := w.tcaShift(x, y)
			return math.Max(rect(x, y, hw, hh),
				math.Max(rect(rx, ry, hw, hh), rect(bx, by, hw, hh)))
		}
		return rect(x, y, hw, hh)
	}
	lo := 0.0
	hi := seed
	for range 40 { // grow until the point is outside the frame
		if outside(hi) > 0 {
			break
		}
		lo = hi
		hi *= 1.25
		if hi > seed*1000 {
			return 0
		}
	}
	if outside(hi) <= 0 {
		return 0
	}
	for range 60 {
		mid := (lo + hi) / 2
		if outside(mid) > 0 {
			hi = mid
		} else {
			lo = mid
		}
	}
	return (lo + hi) / 2
}

// rect is the signed excursion of a point beyond a half-width/half-height
// rectangle: negative inside, zero on the boundary, positive outside.
func rect(x, y, hw, hh float64) float64 {
	return math.Max(math.Abs(x)/hw, math.Abs(y)/hh) - 1
}

// HasTCA reports whether a lateral-CA correction is part of this warp, which
// is what decides whether the renderer has to resample red and blue at
// coordinates of their own.
func (w *Warp) HasTCA() bool { return w != nil && w.hasTCA }
