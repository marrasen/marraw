package lens

import "math"

// Coefficient interpolation and coordinate-system conversion.
//
// Everything in this file is a Go transcription of Lensfun's math, and the
// comments name the upstream function each piece mirrors so the two can be
// diffed when the database format moves. Three coordinate systems are in
// play (lensfun's libs/lensfun/modifier.cpp says it best):
//
//  1. "normalized" — the natural system in units of the lens's real focal
//     length. This is what the renderer works in.
//  2. Hugin's — r = 1 at the middle of the long edge (half the image height
//     in landscape). The poly3 / poly5 / ptlens distortion and the TCA
//     coefficients in the database are in this system.
//  3. The "pa" vignetting system — r = 1 at the image corner.
//
// The rescale* functions below convert (2) and (3) into (1), which is also
// where a calibration measured on a different sensor size gets corrected for
// the format difference.

// fullFrameDiag is the 35mm frame diagonal in millimeters — the reference
// every crop factor in the database is relative to.
var fullFrameDiag = math.Hypot(36, 24)

// Correction is a lens profile resolved for one photo: the coefficients
// interpolated to the frame's focal length and aperture and rescaled into
// normalized coordinates. It says nothing about the render's size, so one
// resolved Correction serves every level of the pyramid; binding it to a
// pixel grid is Warp's job.
type Correction struct {
	// Name is the matched lens for display, e.g. "Sony E 50mm f/1.8 OSS".
	Name string
	// Focal and Aperture are the values the coefficients were resolved for.
	Focal, Aperture float64

	// crop and realFocal place the coefficients in the frame: they are all
	// the normalized coordinate system needs beyond the pixel dimensions,
	// which is why a resolved Correction is independent of the render size
	// and can be cached once per photo.
	crop, realFocal float64

	// Dist is the distortion polynomial, valid when DistModel != "".
	DistModel DistModel
	Dist      [3]float64
	// TCA is the red/blue radial scaling in Lensfun's poly3 layout
	// (vr, vb, cr, cb, br, bb), valid when HasTCA.
	TCA    [6]float64
	HasTCA bool
	// Vig is the "pa" vignetting polynomial (k1, k2, k3), valid when HasVig.
	Vig    [3]float64
	HasVig bool
}

// HasDist reports whether a distortion polynomial was resolved.
func (c *Correction) HasDist() bool { return c != nil && c.DistModel != "" }

// Empty reports whether the correction would change nothing.
func (c *Correction) Empty() bool {
	return c == nil || (!c.HasDist() && !c.HasTCA && !c.HasVig)
}

// Resolve interpolates l's calibration to the given shot, on a body of the
// given crop factor. distance is the subject distance in meters, which only
// vignetting uses. It returns nil when nothing could be resolved.
func (l *Lens) Resolve(crop, focal, aperture, distance float64) *Correction {
	if l == nil || focal <= 0 || crop <= 0 {
		return nil
	}
	if aperture <= 0 {
		aperture = 8 // a sane middle when the body recorded no f-number
	}
	if distance <= 0 {
		distance = 1000
	}

	out := &Correction{Name: l.displayName(), Focal: focal, Aperture: aperture}

	// The real focal length drives the whole normalized system, so the
	// distortion interpolation runs first even when only vignetting is
	// wanted. Lenses whose calibration omits real-focal fall back to the
	// nominal value, which is what lfModifier's constructor does.
	realFocal := focal
	dist, okDist := l.interpolateDist(focal)
	if okDist && dist.RealFocal > 0 {
		realFocal = dist.RealFocal
	}

	out.crop, out.realFocal = crop, realFocal

	if okDist {
		out.DistModel = dist.Model
		out.Dist = rescaleDist(dist, l.Crop, l.Aspect, realFocal)
	}
	if tca, ok := l.interpolateTCA(focal); ok {
		out.TCA = rescaleTCA(tca, l.Crop, l.Aspect, realFocal)
		out.HasTCA = true
	}
	if vig, ok := l.interpolateVig(focal, aperture, distance); ok {
		out.Vig = rescaleVig(vig, l.Crop, realFocal)
		out.HasVig = true
	}
	if out.Empty() {
		return nil
	}
	return out
}

func (l *Lens) displayName() string {
	if l.Maker == "" {
		return l.Model
	}
	return l.Maker + " " + l.Model
}

// huginScaling converts a Hugin-system coefficient into the normalized
// system: the ratio between the real focal length and the millimeter length
// that Hugin's r = 1 corresponds to on the calibration sensor. Mirrors the
// rescale_polynomial_coefficients helpers in mod-coord.cpp / mod-subpix.cpp.
func huginScaling(calibCrop, aspect, realFocal float64) float64 {
	if calibCrop <= 0 {
		calibCrop = 1
	}
	if aspect <= 0 {
		aspect = 1.5
	}
	mm := fullFrameDiag / calibCrop / math.Hypot(aspect, 1) / 2
	return realFocal / mm
}

// rescaleDist mirrors rescale_polynomial_coefficients in mod-coord.cpp. The
// division by powers of d also absorbs the focal-length change that the
// PanoTools "d" term would otherwise introduce, so the renderer can assume
// every transformation preserves the focal length.
func rescaleDist(c DistCalib, calibCrop, aspect, realFocal float64) [3]float64 {
	hs := huginScaling(calibCrop, aspect, realFocal)
	t := c.Terms
	switch c.Model {
	case DistPoly3:
		d := 1 - t[0]
		t[0] *= hs * hs / (d * d * d)
	case DistPoly5:
		t[0] *= hs * hs
		t[1] *= hs * hs * hs * hs
	case DistPTLens:
		d := 1 - t[0] - t[1] - t[2]
		t[0] *= hs * hs * hs / (d * d * d * d)
		t[1] *= hs * hs / (d * d * d)
		t[2] *= hs / (d * d)
	}
	return t
}

// rescaleTCA mirrors rescale_polynomial_coefficients in mod-subpix.cpp. The
// two v terms are dimensionless ratios and need no scaling.
func rescaleTCA(c TCACalib, calibCrop, aspect, realFocal float64) [6]float64 {
	hs := huginScaling(calibCrop, aspect, realFocal)
	t := c.Terms
	t[2] *= hs
	t[3] *= hs
	t[4] *= hs * hs
	t[5] *= hs * hs
	return t
}

// rescaleVig mirrors rescale_polynomial_coefficients in mod-color.cpp.
// Vignetting's r = 1 sits at the image corner, so its reference length is
// the half-diagonal and the aspect ratio does not enter.
func rescaleVig(c VigCalib, calibCrop, realFocal float64) [3]float64 {
	if calibCrop <= 0 {
		calibCrop = 1
	}
	hs := realFocal / (fullFrameDiag / calibCrop / 2)
	t := c.Terms
	t[0] *= hs * hs
	t[1] *= hs * hs * hs * hs
	t[2] *= hs * hs * hs * hs * hs * hs
	return t
}

// spline holds the four nearest calibration points around a target focal
// length, ordered by descending focal length: [1] and [2] bracket the
// target, [0] and [3] are the outer neighbours that shape the tangents.
// Mirrors __insert_spline in lens.cpp.
type spline[T any] struct {
	pts  [4]*T
	dist [4]float64
}

func newSpline[T any]() *spline[T] {
	return &spline[T]{dist: [4]float64{-math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, math.MaxFloat64}}
}

// insert files a calibration point by its signed focal distance
// df = target - point. Negative df (the point sits above the target) fills
// slots 0 and 1; positive df fills 2 and 3.
func (s *spline[T]) insert(df float64, v *T) {
	if df < 0 {
		switch {
		case df > s.dist[1]:
			s.dist[0], s.pts[0] = s.dist[1], s.pts[1]
			s.dist[1], s.pts[1] = df, v
		case df > s.dist[0]:
			s.dist[0], s.pts[0] = df, v
		}
		return
	}
	switch {
	case df < s.dist[2]:
		s.dist[3], s.pts[3] = s.dist[2], s.pts[2]
		s.dist[2], s.pts[2] = df, v
	case df < s.dist[3]:
		s.dist[3], s.pts[3] = df, v
	}
}

// hermite is _lf_interpolate from auxfun.cpp: a cubic Hermite through y2 and
// y3 whose tangents come from the outer neighbours. have1/have4 report
// whether those neighbours exist; without them the tangent degrades to the
// straight secant, which is lensfun's FLT_MAX sentinel path.
func hermite(y1, y2, y3, y4, t float64, have1, have4 bool) float64 {
	tg2, tg3 := y3-y2, y3-y2
	if have1 {
		tg2 = (y3 - y1) * 0.5
	}
	if have4 {
		tg3 = (y4 - y2) * 0.5
	}
	t2 := t * t
	t3 := t2 * t
	return (2*t3-3*t2+1)*y2 +
		(t3-2*t2+t)*tg2 +
		(-2*t3+3*t2)*y3 +
		(t3-t2)*tg3
}

// interpolateDist mirrors lfLens::InterpolateDistortion. Calibration points
// of a model other than the first one encountered are skipped, matching
// upstream's "take into account just the first encountered lens model".
func (l *Lens) interpolateDist(focal float64) (DistCalib, bool) {
	var model DistModel
	sp := newSpline[DistCalib]()
	for i := range l.Dist {
		c := &l.Dist[i]
		if model == "" {
			model = c.Model
		} else if c.Model != model {
			continue
		}
		if df := focal - c.Focal; df == 0 {
			return *c, true
		} else {
			sp.insert(df, c)
		}
	}
	if sp.pts[1] == nil || sp.pts[2] == nil {
		// Off the end of the calibrated range: clamp to the nearest point
		// rather than extrapolate a polynomial that was never measured
		// there.
		for _, i := range [2]int{1, 2} {
			if sp.pts[i] != nil {
				return *sp.pts[i], true
			}
		}
		return DistCalib{}, false
	}

	lo, hi := sp.pts[1], sp.pts[2]
	t := (focal - lo.Focal) / (hi.Focal - lo.Focal)
	out := DistCalib{Focal: focal, Model: model}

	realFocal := func(c *DistCalib) float64 {
		if c == nil {
			return 0
		}
		if c.RealFocal > 0 {
			return c.RealFocal
		}
		return c.Focal
	}
	out.RealFocal = hermite(
		realFocal(sp.pts[0]), realFocal(sp.pts[1]),
		realFocal(sp.pts[2]), realFocal(sp.pts[3]),
		t, sp.pts[0] != nil, sp.pts[3] != nil)

	// Preconditioning: the polynomial terms fall off roughly as 1/f, so
	// they are multiplied by each point's own focal length before
	// interpolating and divided by the target's afterwards. See the
	// "Coefficient interpolation" comment block in lens.cpp.
	for i := range out.Terms {
		out.Terms[i] = hermite(
			term(sp.pts[0], i)*focalOf(sp.pts[0]),
			term(sp.pts[1], i)*focalOf(sp.pts[1]),
			term(sp.pts[2], i)*focalOf(sp.pts[2]),
			term(sp.pts[3], i)*focalOf(sp.pts[3]),
			t, sp.pts[0] != nil, sp.pts[3] != nil) / focal
	}
	return out, true
}

func term(c *DistCalib, i int) float64 {
	if c == nil {
		return 0
	}
	return c.Terms[i]
}

func focalOf(c *DistCalib) float64 {
	if c == nil {
		return 0
	}
	return c.Focal
}

// interpolateTCA mirrors lfLens::InterpolateTCA. The two v terms sit close
// to 1 and stay constant across the zoom range, so unlike the others they
// are interpolated unscaled (__parameter_scales returns 1 for index < 2).
func (l *Lens) interpolateTCA(focal float64) (TCACalib, bool) {
	sp := newSpline[TCACalib]()
	for i := range l.TCA {
		c := &l.TCA[i]
		if df := focal - c.Focal; df == 0 {
			return *c, true
		} else {
			sp.insert(df, c)
		}
	}
	if sp.pts[1] == nil || sp.pts[2] == nil {
		for _, i := range [2]int{1, 2} {
			if sp.pts[i] != nil {
				return *sp.pts[i], true
			}
		}
		return TCACalib{}, false
	}
	lo, hi := sp.pts[1], sp.pts[2]
	t := (focal - lo.Focal) / (hi.Focal - lo.Focal)
	out := TCACalib{Focal: focal}
	for i := range out.Terms {
		scale := func(c *TCACalib) float64 {
			if i < 2 {
				return 1
			}
			if c == nil {
				return 0
			}
			return c.Focal
		}
		div := focal
		if i < 2 {
			div = 1
		}
		out.Terms[i] = hermite(
			tcaTerm(sp.pts[0], i)*scale(sp.pts[0]),
			tcaTerm(sp.pts[1], i)*scale(sp.pts[1]),
			tcaTerm(sp.pts[2], i)*scale(sp.pts[2]),
			tcaTerm(sp.pts[3], i)*scale(sp.pts[3]),
			t, sp.pts[0] != nil, sp.pts[3] != nil) / div
	}
	return out, true
}

func tcaTerm(c *TCACalib, i int) float64 {
	if c == nil {
		return 0
	}
	return c.Terms[i]
}

// interpolateVig mirrors lfLens::InterpolateVignetting: inverse-distance
// weighting with p = 3.5 over the (focal, aperture, distance) cloud, rather
// than a spline — vignetting is measured on a grid, not a curve.
func (l *Lens) interpolateVig(focal, aperture, distance float64) (VigCalib, bool) {
	const power = 3.5
	out := VigCalib{Focal: focal, Aperture: aperture, Distance: distance}
	total := 0.0
	nearest := math.MaxFloat64
	for i := range l.Vig {
		c := &l.Vig[i]
		d := l.vigDist(*c, focal, aperture, distance)
		if d < 0.0001 {
			return *c, true
		}
		nearest = math.Min(nearest, d)
		w := math.Abs(1 / math.Pow(d, power))
		for j := range out.Terms {
			out.Terms[j] += w * c.Terms[j]
		}
		total += w
	}
	// Upstream's bail-out: every measured point is too far away in the
	// (focal, aperture, distance) space for the weighting to mean anything.
	if nearest > 1 || total <= 0 {
		return VigCalib{}, false
	}
	for j := range out.Terms {
		out.Terms[j] /= total
	}
	return out, true
}

// vigDist is __vignetting_dist from lens.cpp: focal is normalized against
// the lens's own zoom range while aperture and distance are mapped onto
// reciprocal axes, which is where their parameters behave linearly.
func (l *Lens) vigDist(c VigCalib, focal, aperture, distance float64) float64 {
	f1 := focal - l.MinFocal
	f2 := c.Focal - l.MinFocal
	if df := l.MaxFocal - l.MinFocal; df != 0 {
		f1 /= df
		f2 /= df
	}
	a1, a2 := 4/aperture, 4/c.Aperture
	d1, d2 := 0.1/distance, 0.1/c.Distance
	return math.Sqrt(sq(f2-f1) + sq(a2-a1) + sq(d2-d1))
}

func sq(v float64) float64 { return v * v }
