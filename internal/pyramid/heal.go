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
		s := &e.Spots[i]
		// Skip kinds and modes this build doesn't know — the newMaskEvaluator
		// precedent. A sidecar from a newer version renders here without a
		// Normalize pass (editsForHash parses stored JSON as-is), so a future
		// "fill" spot must be ignored, not misrendered as a circle.
		switch s.Kind {
		case "":
			applyHealSpot(img, w, h, &f, long, s)
		case "stroke":
			applyHealStroke(img, w, h, &f, long, s)
		}
	}
}

// healModeKnown reports whether the spot's fill mode is one this build renders
// ("heal" is the un-normalized spelling of the default).
func healModeKnown(m edit.SpotMode) bool {
	switch m {
	case edit.SpotHeal, edit.SpotClone, "heal":
		return true
	}
	return false
}

// applyHealSpot fills one circular spot from its source patch.
func applyHealSpot(img *image.RGBA, w, h int, f *maskFrame, long float64, s *edit.Spot) {
	if !healModeKnown(s.Mode) {
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

// applyHealStroke fills one painted (Kind "stroke") spot from its source
// region — the same region translated by the spot's dest→source vector. The
// coverage is rasterized through the brush-mask machinery (a fixed-resolution
// plane in oriented-frame space, so previews, tiles and export agree by
// construction), and heal mode generalizes the circle's annulus plane fit to
// a boundary band: the ring of pixels just outside the painted region drives
// a low-frequency membrane on both destination and source, so a differently-
// lit source blends into the destination surround.
func applyHealStroke(img *image.RGBA, w, h int, f *maskFrame, long float64, s *edit.Spot) {
	if !healModeKnown(s.Mode) || len(s.Strokes) == 0 {
		return
	}
	pw, ph := brushPlaneDims(f.frameW, f.frameH)
	ev := &brushEval{
		f: *f, plane: brushPlaneFor(s.Strokes, pw, ph), pw: pw, ph: ph,
		covToW: covToWeight,
		xMin:   0, xMax: 1 << 30, yMin: 0, yMax: 1 << 30,
	}
	ev.strokeBounds(s.Strokes)
	x0 := max(0, ev.xMin)
	x1 := min(w, ev.xMax)
	y0 := max(0, ev.yMin)
	y1 := min(h, ev.yMax)
	if x0 >= x1 || y0 >= y1 {
		return
	}

	// Dest→source offset in output space: the frame→output map is rigid, so a
	// constant frame-space translation stays a constant output-space one.
	dcx, dcy := f.outputPoint(s.CX*f.frameW, s.CY*f.frameH)
	scx, scy := f.outputPoint(s.SX*f.frameW, s.SY*f.frameH)
	offX, offY := scx-dcx, scy-dcy
	// Keep the source region on the frame (the circle's source-center clamp):
	// the covered box translated by the offset must fit inside the image. The
	// box is clipped to the image, so the clamp range is never empty.
	offX = clampF(offX, -float64(x0), float64(w)-float64(x1))
	offY = clampF(offY, -float64(y0), float64(h)-float64(y1))

	// The boundary band's width tracks the brush size: the circle fits over
	// [1.0, 1.35]·radius, so use 0.35 of the largest stroke radius.
	var radPx float64
	for i := range s.Strokes {
		radPx = math.Max(radPx, s.Strokes[i].Radius*long)
	}
	if radPx < 0.75 { // sub-pixel: invisible at every render size we generate
		return
	}
	bandPx := math.Max(2, 0.35*radPx)

	// Snapshot the source region (covered box plus the band) before we write,
	// so an overlapping source and destination never feed back.
	pad := int(math.Ceil(bandPx)) + 2
	srcRect := image.Rect(
		x0+int(math.Floor(offX))-pad, y0+int(math.Floor(offY))-pad,
		x1+int(math.Ceil(offX))+pad, y1+int(math.Ceil(offY))+pad,
	)
	ir := srcRect.Intersect(img.Bounds())
	if ir.Empty() {
		return
	}
	src := copyRGBA(img, srcRect)
	srcW, srcH := src.Bounds().Dx(), src.Bounds().Dy()
	srcAt := func(ox, oy float64) (r, g, b float64, ok bool) {
		lx := ox - float64(ir.Min.X)
		ly := oy - float64(ir.Min.Y)
		if lx < 0 || ly < 0 || lx > float64(srcW-1) || ly > float64(srcH-1) {
			return 0, 0, 0, false
		}
		r8, g8, b8, _ := sampleBilinear(src, lx, ly)
		return float64(r8), float64(g8), float64(b8), true
	}

	opQ := int32(256)
	if s.Opacity > 0 {
		opQ = int32(math.Round(s.Opacity * 256))
	}

	// Heal mode: fit the destination and source membranes over the boundary
	// band. (u,v) are normalized against the covered box so the normal
	// equations stay well-conditioned at any render size; the same (u,v)
	// parametrize both fits and the per-pixel evaluation, exactly like the
	// circle's dest-relative coordinates.
	heal := s.Mode != edit.SpotClone
	bcx := float64(x0+x1) / 2
	bcy := float64(y0+y1) / 2
	scale := math.Max(1, math.Max(float64(x1-x0), float64(y1-y0))/2)
	var dPlane, sPlane [3]planeFit
	if heal {
		dPlane, sPlane = fitStrokeBandPlanes(img, w, h, f, ev, bandPx, long, bcx, bcy, scale, offX, offY, srcAt)
	}

	invScale := 1 / scale
	wrow := make([]uint16, w)
	for y := y0; y < y1; y++ {
		rx0, rx1 := ev.weightRow(y, wrow)
		if rx0 >= rx1 {
			continue
		}
		v := (float64(y) - bcy) * invScale
		row := img.Pix[y*img.Stride:]
		for x := rx0; x < rx1; x++ {
			wq := int32(wrow[x]) * opQ >> 8
			if wq == 0 {
				continue
			}
			sr, sg, sb, ok := srcAt(float64(x)+offX, float64(y)+offY)
			if !ok {
				continue
			}
			if heal {
				u := (float64(x) - bcx) * invScale
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

// fitStrokeBandPlanes fits the heal membranes for a stroke spot over its
// boundary band: coverage-plane pixels that are clean (zero coverage) but
// within the band's reach of painted ones. Band membership is decided at the
// fixed plane resolution (a separable dilation of the coverage), then each
// band pixel's center is mapped to output space, where the destination fit
// samples the live image and the source fit samples the translated snapshot
// (via srcAt, which rejects out-of-snapshot reads). Deterministic: plane
// pixels scan in row order.
func fitStrokeBandPlanes(
	img *image.RGBA, w, h int, f *maskFrame, ev *brushEval,
	bandPx, long, bcx, bcy, scale, offX, offY float64,
	srcAt func(x, y float64) (r, g, b float64, ok bool),
) (dPlane, sPlane [3]planeFit) {
	// Band reach in plane pixels: the plane's long edge stands in for the
	// frame's, so scale the output-space band by their ratio.
	reach := max(1, int(math.Ceil(bandPx*brushPlaneLongEdge/long)))

	// Bound the scan to the coverage box (plus reach) in plane space.
	px0, py0, px1, py1 := ev.pw, ev.ph, 0, 0
	for y := 0; y < ev.ph; y++ {
		rowc := ev.plane[y*ev.pw : (y+1)*ev.pw]
		for x, c := range rowc {
			if c == 0 {
				continue
			}
			if y < py0 {
				py0 = y
			}
			py1 = y
			if x < px0 {
				px0 = x
			}
			if x > px1 {
				px1 = x
			}
		}
	}
	if px0 > px1 {
		return dPlane, sPlane // empty coverage
	}
	px0 = max(0, px0-reach)
	py0 = max(0, py0-reach)
	px1 = min(ev.pw-1, px1+reach)
	py1 = min(ev.ph-1, py1+reach)
	bw := px1 - px0 + 1
	bh := py1 - py0 + 1

	// Separable dilation of the binary coverage over the scan box: dil[i] > 0
	// means a painted pixel lies within a (2·reach+1)² box.
	dil := make([]uint8, bw*bh)
	tmp := make([]uint8, bw*bh)
	for y := range bh {
		rowc := ev.plane[(py0+y)*ev.pw:]
		out := tmp[y*bw:]
		run := 0 // painted pixels inside the horizontal window
		for x := -reach; x < bw; x++ {
			if x+reach < bw && rowc[px0+x+reach] != 0 {
				run++
			}
			if x-reach-1 >= 0 && rowc[px0+x-reach-1] != 0 {
				run--
			}
			if x >= 0 && run > 0 {
				out[x] = 1
			}
		}
	}
	for x := range bw {
		run := 0
		for y := -reach; y < bh; y++ {
			if y+reach < bh && tmp[(y+reach)*bw+x] != 0 {
				run++
			}
			if y-reach-1 >= 0 && tmp[(y-reach-1)*bw+x] != 0 {
				run--
			}
			if y >= 0 && run > 0 {
				dil[y*bw+x] = 1
			}
		}
	}

	// Accumulate the fits over band pixels (clean but near paint), sampling
	// both images at the plane pixel's output-space center.
	sx := f.frameW / float64(ev.pw)
	sy := f.frameH / float64(ev.ph)
	var dAcc, sAcc planeAccum
	for y := range bh {
		for x := range bw {
			if dil[y*bw+x] == 0 || ev.plane[(py0+y)*ev.pw+px0+x] != 0 {
				continue
			}
			fx := (float64(px0+x) + 0.5) * sx
			fy := (float64(py0+y) + 0.5) * sy
			ox, oy := f.outputPoint(fx, fy)
			if ox < 0 || oy < 0 || ox > float64(w-1) || oy > float64(h-1) {
				continue
			}
			u := (ox - bcx) / scale
			v := (oy - bcy) / scale
			r8, g8, b8, _ := sampleBilinear(img, ox, oy)
			dAcc.add(u, v, float64(r8), float64(g8), float64(b8))
			if sr, sg, sb, ok := srcAt(ox+offX, oy+offY); ok {
				sAcc.add(u, v, sr, sg, sb)
			}
		}
	}
	return dAcc.solve(), sAcc.solve()
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

// planeAccum accumulates least-squares samples for one plane fit per RGB
// channel over normalized (u,v) coordinates. Deterministic as long as samples
// are added in a deterministic order.
type planeAccum struct {
	n             float64
	su, sv        float64
	suu, suv, svv float64
	sz, suz, svz  [3]float64
}

func (p *planeAccum) add(u, v, r, g, b float64) {
	p.n++
	p.su += u
	p.sv += v
	p.suu += u * u
	p.suv += u * v
	p.svv += v * v
	z := [3]float64{r, g, b}
	for c := range 3 {
		p.sz[c] += z[c]
		p.suz[c] += u * z[c]
		p.svz[c] += v * z[c]
	}
}

// solve returns the fitted planes. With too few samples for a stable slope it
// falls back to the constant mean (b=c=0); with none it returns the zero fit
// (no correction — a plain clone).
func (p *planeAccum) solve() [3]planeFit {
	var out [3]planeFit
	if p.n < 16 { // too sparse for a stable slope
		if p.n > 0 {
			for c := range 3 {
				out[c] = planeFit{a: p.sz[c] / p.n}
			}
		}
		return out
	}
	// Symmetric normal-equations matrix, shared across channels.
	m := mat3{
		p.n, p.su, p.sv,
		p.su, p.suu, p.suv,
		p.sv, p.suv, p.svv,
	}
	inv, ok := m.inverse()
	if !ok {
		for c := range 3 {
			out[c] = planeFit{a: p.sz[c] / p.n}
		}
		return out
	}
	for c := range 3 {
		a, b, cc := inv.mulVec(p.sz[c], p.suz[c], p.svz[c])
		out[c] = planeFit{a: a, b: b, c: cc}
	}
	return out
}

// fitAnnulusPlanes least-squares-fits one plane per RGB channel over the
// annulus [healAnnulusInner, healAnnulusOuter]·rad around (cx,cy), sampling
// through get (which reports ok=false for out-of-frame pixels). Coordinates
// are normalized by rad so the normal equations stay well-conditioned across
// render sizes. Deterministic: samples accumulate in scanline order.
func fitAnnulusPlanes(cx, cy, rad float64, get func(x, y float64) (r, g, b float64, ok bool)) [3]planeFit {
	rIn2 := (healAnnulusInner * rad) * (healAnnulusInner * rad)
	rOut := healAnnulusOuter * rad
	rOut2 := rOut * rOut
	x0 := int(math.Floor(cx - rOut))
	x1 := int(math.Ceil(cx + rOut))
	y0 := int(math.Floor(cy - rOut))
	y1 := int(math.Ceil(cy + rOut))

	var acc planeAccum
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
			acc.add(dx*invR, dy*invR, r, g, b)
		}
	}
	return acc.solve()
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

// StrokeSpotCircle reduces a stroke spot's painted region to its enclosing
// circle — center in oriented-frame fractions, radius as a fraction of the
// frame long edge — the shape SuggestHealSource's ring search understands.
// The frame geometry comes from the params (newMaskFrame) at the given render
// size, like every other spot mapping. radius 0 means nothing is painted.
func StrokeSpotCircle(w, h int, e *edit.Params, s *edit.Spot) (cx, cy, radius float64) {
	f := newMaskFrame(w, h, e)
	long := math.Max(f.frameW, f.frameH)
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for i := range s.Strokes {
		st := &s.Strokes[i]
		if st.Erase {
			continue
		}
		r := st.Radius * long
		for p := 0; p+1 < len(st.Pts); p += 2 {
			px := st.Pts[p] * f.frameW
			py := st.Pts[p+1] * f.frameH
			minX = math.Min(minX, px-r)
			maxX = math.Max(maxX, px+r)
			minY = math.Min(minY, py-r)
			maxY = math.Max(maxY, py+r)
		}
	}
	if minX > maxX || f.frameW == 0 || f.frameH == 0 || long == 0 {
		return 0, 0, 0
	}
	cx = (minX + maxX) / 2 / f.frameW
	cy = (minY + maxY) / 2 / f.frameH
	radius = math.Hypot(maxX-minX, maxY-minY) / 2 / long
	return cx, cy, radius
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
