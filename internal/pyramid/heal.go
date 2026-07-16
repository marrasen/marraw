package pyramid

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
)

// ApplyHeal transplants the edit's retouch spots into a post-geometry render.
// It runs before ApplyLook (see ApplyFinish) so healed pixels develop
// identically to their source — a spot copied in scene space picks up the same
// tone curve, color and detail as everything around it. Spots apply in list
// order: a later spot sees the earlier spots' output.
//
// Spot geometry lives in fractional coordinates of the oriented frame (the
// crop-rectangle space, like masks), recovered from the params alone via
// newMaskFrame, so a spot lands on the same image content across every preview
// level, 1:1 tile and export. A disc is rotation-invariant, so only the two
// centers are mapped through the frame transform; the radius (a fraction of the
// frame long edge) carries straight over because output→frame is a pure
// rotation with no scale.
func ApplyHeal(img *image.RGBA, e *edit.Params) {
	if !e.HasSpots() {
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return
	}
	f := newMaskFrame(w, h, e)
	long := math.Max(f.frameW, f.frameH)
	for i := range e.Spots {
		applyHealSpot(img, w, h, &f, long, &e.Spots[i])
	}
}

// applyHealSpot fills one circular spot from its source patch.
func applyHealSpot(img *image.RGBA, w, h int, f *maskFrame, long float64, s *edit.Spot) {
	// Skip kinds and modes this build doesn't know — the newMaskEvaluator
	// precedent. A sidecar from a newer version renders here without a
	// Normalize pass (editsForHash parses stored JSON as-is), so a future
	// "stroke" spot must be ignored, not misrendered as a circle.
	if s.Kind != "" {
		return
	}
	switch s.Mode {
	case edit.SpotHeal, edit.SpotClone, "heal": // "heal" = un-normalized spelling of the default
	default:
		return
	}
	radPx := s.Radius * long
	if radPx < 0.75 { // sub-pixel: invisible at every render size we generate
		return
	}
	dcx, dcy := f.outputPoint(s.CX*f.frameW, s.CY*f.frameH)
	scx, scy := f.outputPoint(s.SX*f.frameW, s.SY*f.frameH)
	// Keep the sampled source disc on the frame; the ±radPx clamp guarantees
	// the clone reads never fall outside (sampleBilinear would return black).
	// A frame smaller than the disc is degenerate — the sub-pixel guard above
	// already rejects the only sizes where that bites.
	if float64(w) > 2*radPx {
		scx = clampF(scx, radPx, float64(w)-radPx)
	}
	if float64(h) > 2*radPx {
		scy = clampF(scy, radPx, float64(h)-radPx)
	}
	offX, offY := scx-dcx, scy-dcy

	// Snapshot the source region (disc plus the plane-fit annulus) before we
	// write, so an overlapping source and destination never feed back.
	reach := radPx*healAnnulusOuter + 2
	half := int(math.Ceil(reach)) + 1
	srcRect := image.Rect(int(scx)-half, int(scy)-half, int(scx)+half+1, int(scy)+half+1)
	ir := srcRect.Intersect(img.Bounds())
	if ir.Empty() {
		return
	}
	src := copyRGBA(img, srcRect)
	srcW, srcH := src.Bounds().Dx(), src.Bounds().Dy()
	// srcAt samples the snapshot at an output-space coordinate.
	srcAt := func(ox, oy float64) (r, g, b float64, ok bool) {
		lx := ox - float64(ir.Min.X)
		ly := oy - float64(ir.Min.Y)
		if lx < 0 || ly < 0 || lx > float64(srcW-1) || ly > float64(srcH-1) {
			return 0, 0, 0, false
		}
		r8, g8, b8, _ := sampleBilinear(src, lx, ly)
		return float64(r8), float64(g8), float64(b8), true
	}

	// Feathered disc weight, LUT'd over normalized distance² (the stampStroke
	// precedent). 0..256 for a Q8 blend, then scaled by opacity.
	var flut [weightLUTSize]uint16
	feather := math.Max(s.Feather, 1.0/weightLUTSize)
	for q := range flut {
		d := math.Sqrt(float64(q) / (weightLUTSize - 1))
		flut[q] = uint16(math.Round(256 * smoothstep01((1-d)/feather)))
	}
	opQ := int32(256)
	if s.Opacity > 0 {
		opQ = int32(math.Round(s.Opacity * 256))
	}

	// Heal mode fits a low-frequency membrane so a differently-lit source
	// blends into the destination surround; clone copies verbatim.
	var dPlane, sPlane [3]planeFit
	heal := s.Mode != edit.SpotClone
	if heal {
		dPlane = fitAnnulusPlanes(dcx, dcy, radPx, func(x, y float64) (float64, float64, float64, bool) {
			ix, iy := int(x), int(y)
			if ix < 0 || iy < 0 || ix >= w || iy >= h {
				return 0, 0, 0, false
			}
			o := img.PixOffset(ix, iy)
			return float64(img.Pix[o]), float64(img.Pix[o+1]), float64(img.Pix[o+2]), true
		})
		sPlane = fitAnnulusPlanes(scx, scy, radPx, srcAt)
	}

	invR2 := 1 / (radPx * radPx)
	x0 := max(0, int(math.Floor(dcx-radPx)))
	x1 := min(w-1, int(math.Ceil(dcx+radPx)))
	y0 := max(0, int(math.Floor(dcy-radPx)))
	y1 := min(h-1, int(math.Ceil(dcy+radPx)))
	for y := y0; y <= y1; y++ {
		dy := float64(y) - dcy
		row := img.Pix[y*img.Stride:]
		for x := x0; x <= x1; x++ {
			dx := float64(x) - dcx
			d2 := dx*dx + dy*dy
			q := d2 * invR2
			if q >= 1 {
				continue
			}
			wq := int32(flut[int(q*(weightLUTSize-1))]) * opQ >> 8
			if wq == 0 {
				continue
			}
			sr, sg, sb, ok := srcAt(float64(x)+offX, float64(y)+offY)
			if !ok {
				continue
			}
			if heal {
				u, v := dx/radPx, dy/radPx
				sr += dPlane[0].at(u, v) - sPlane[0].at(u, v)
				sg += dPlane[1].at(u, v) - sPlane[1].at(u, v)
				sb += dPlane[2].at(u, v) - sPlane[2].at(u, v)
			}
			i := x * 4
			r0, g0, b0 := int32(row[i]), int32(row[i+1]), int32(row[i+2])
			nr := int32(clamp8(int32(math.Round(sr))))
			ng := int32(clamp8(int32(math.Round(sg))))
			nb := int32(clamp8(int32(math.Round(sb))))
			row[i] = clamp8(r0 + (nr-r0)*wq>>8)
			row[i+1] = clamp8(g0 + (ng-g0)*wq>>8)
			row[i+2] = clamp8(b0 + (nb-b0)*wq>>8)
		}
	}
}

// healAnnulusInner/Outer bound the ring (in spot radii) whose pixels — clean
// surround, outside the blemish by construction — drive the heal-mode plane
// fit on both the destination and the source.
const (
	healAnnulusInner = 1.0
	healAnnulusOuter = 1.35
)

// planeFit is a fitted plane z = a + b·u + c·v in disc-local normalized
// coordinates (u,v = offset/radius). The zero value evaluates to 0 everywhere,
// so an unfitted channel contributes no correction.
type planeFit struct{ a, b, c float64 }

func (p planeFit) at(u, v float64) float64 { return p.a + p.b*u + p.c*v }

// fitAnnulusPlanes least-squares-fits one plane per RGB channel over the
// annulus [healAnnulusInner, healAnnulusOuter]·rad around (cx,cy), sampling
// through get (which reports ok=false for out-of-frame pixels). Coordinates
// are normalized by rad so the normal equations stay well-conditioned across
// render sizes. With too few usable samples it falls back to the constant mean
// (b=c=0); with none it returns the zero fit (no correction — a plain clone).
// Deterministic: samples accumulate in scanline order.
func fitAnnulusPlanes(cx, cy, rad float64, get func(x, y float64) (r, g, b float64, ok bool)) [3]planeFit {
	rIn2 := (healAnnulusInner * rad) * (healAnnulusInner * rad)
	rOut := healAnnulusOuter * rad
	rOut2 := rOut * rOut
	x0 := int(math.Floor(cx - rOut))
	x1 := int(math.Ceil(cx + rOut))
	y0 := int(math.Floor(cy - rOut))
	y1 := int(math.Ceil(cy + rOut))

	var n float64
	var su, sv, suu, suv, svv float64
	var sz, suz, svz [3]float64
	invR := 1 / rad
	for y := y0; y <= y1; y++ {
		dy := float64(y) - cy
		for x := x0; x <= x1; x++ {
			dx := float64(x) - cx
			d2 := dx*dx + dy*dy
			if d2 < rIn2 || d2 > rOut2 {
				continue
			}
			r, g, b, ok := get(float64(x), float64(y))
			if !ok {
				continue
			}
			u, v := dx*invR, dy*invR
			n++
			su += u
			sv += v
			suu += u * u
			suv += u * v
			svv += v * v
			z := [3]float64{r, g, b}
			for c := range 3 {
				sz[c] += z[c]
				suz[c] += u * z[c]
				svz[c] += v * z[c]
			}
		}
	}
	var out [3]planeFit
	if n < 16 { // too sparse for a stable slope
		if n > 0 {
			for c := range 3 {
				out[c] = planeFit{a: sz[c] / n}
			}
		}
		return out
	}
	// Symmetric normal-equations matrix, shared across channels.
	m := mat3{
		n, su, sv,
		su, suu, suv,
		sv, suv, svv,
	}
	inv, ok := m.inverse()
	if !ok {
		for c := range 3 {
			out[c] = planeFit{a: sz[c] / n}
		}
		return out
	}
	for c := range 3 {
		a, b, cc := inv.mulVec(sz[c], suz[c], svz[c])
		out[c] = planeFit{a: a, b: b, c: cc}
	}
	return out
}

// mat3 is a row-major 3×3 matrix used for the plane-fit normal equations.
type mat3 [9]float64

func (m mat3) inverse() (mat3, bool) {
	a, b, c := m[0], m[1], m[2]
	d, e, f := m[3], m[4], m[5]
	g, h, i := m[6], m[7], m[8]
	A := e*i - f*h
	B := -(d*i - f*g)
	C := d*h - e*g
	det := a*A + b*B + c*C
	if math.Abs(det) < 1e-9 {
		return mat3{}, false
	}
	id := 1 / det
	return mat3{
		A * id, (c*h - b*i) * id, (b*f - c*e) * id,
		B * id, (a*i - c*g) * id, (c*d - a*f) * id,
		C * id, (b*g - a*h) * id, (a*e - b*d) * id,
	}, true
}

func (m mat3) mulVec(x, y, z float64) (a, b, c float64) {
	return m[0]*x + m[1]*y + m[2]*z,
		m[3]*x + m[4]*y + m[5]*z,
		m[6]*x + m[7]*y + m[8]*z
}

// SuggestHealSource picks a source center for a retouch spot on a post-geometry
// render, deterministically, and returns it in oriented-frame fractions ready
// to store in Spot.SX/SY. It samples candidate patches on rings around the spot
// and scores each by how closely its surround matches the spot's own surround
// (sum of squared RGB differences on two probe circles), returning the best. A
// stable source belongs in the params — computing it in the pipeline would let
// it drift with render size — so callers run this once at spot creation.
func SuggestHealSource(img *image.RGBA, e *edit.Params, s edit.Spot) (sx, sy float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	f := newMaskFrame(w, h, e)
	long := math.Max(f.frameW, f.frameH)
	radPx := s.Radius * long
	dcx, dcy := f.outputPoint(s.CX*f.frameW, s.CY*f.frameH)

	sample := func(x, y float64) (r, g, b float64) {
		r8, g8, b8, _ := sampleBilinear(img, x, y)
		return float64(r8), float64(g8), float64(b8)
	}
	// Fixed probe offsets on two circles around a center (0.6R, 1.1R).
	const probeAngles = 16
	var probes [probeAngles * 2][2]float64
	pi := 0
	for _, pr := range [2]float64{0.6, 1.1} {
		for a := range probeAngles {
			ang := 2 * math.Pi * float64(a) / probeAngles
			probes[pi] = [2]float64{pr * radPx * math.Cos(ang), pr * radPx * math.Sin(ang)}
			pi++
		}
	}
	// Reference: the spot's own surround.
	var ref [probeAngles * 2][3]float64
	for k, p := range probes {
		r, g, bl := sample(dcx+p[0], dcy+p[1])
		ref[k] = [3]float64{r, g, bl}
	}

	margin := radPx * (healAnnulusOuter + 0.1)
	bestScore := math.Inf(1)
	bestX, bestY := dcx, dcy
	found := false
	// Candidate centers: fixed angles × rising ring distances.
	const candAngles = 16
	for _, dist := range [3]float64{2.2, 3.2, 4.5} {
		rd := dist * radPx
		for a := range candAngles {
			ang := 2 * math.Pi * float64(a) / candAngles
			ccx := dcx + rd*math.Cos(ang)
			ccy := dcy + rd*math.Sin(ang)
			// Keep the candidate patch on the frame and clear of the spot.
			if ccx < margin || ccx > float64(w)-margin || ccy < margin || ccy > float64(h)-margin {
				continue
			}
			if math.Hypot(ccx-dcx, ccy-dcy) < 2*radPx {
				continue
			}
			var score float64
			for k, p := range probes {
				r, g, bl := sample(ccx+p[0], ccy+p[1])
				dr, dg, db := r-ref[k][0], g-ref[k][1], bl-ref[k][2]
				score += dr*dr + dg*dg + db*db
			}
			if score < bestScore {
				bestScore = score
				bestX, bestY = ccx, ccy
				found = true
			}
		}
	}
	if !found {
		// No on-frame candidate (a huge spot, or a tiny frame): offset toward
		// the frame center so the source at least stays inside.
		bestX = clampF(dcx+2.5*radPx, radPx, float64(w)-radPx)
		bestY = dcy
	}
	fx, fy := f.framePoint(bestX, bestY)
	if f.frameW == 0 || f.frameH == 0 {
		return s.SX, s.SY
	}
	return fx / f.frameW, fy / f.frameH
}
