package pyramid

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"image"
	"math"
	"sync"

	"github.com/marrasen/marraw/internal/edit"
)

// ApplyMasks applies the edit's local adjustments to a post-geometry render.
// It runs between ApplyLook and ApplyDetail: each mask's tone/color adjustment
// composes over the developed global color (like the HSL mixer), and the
// global detail pass then operates on the final tones. Masks apply in list
// order — a later mask sees the earlier masks' output.
//
// Mask geometry lives in fractional coordinates of the oriented frame (the
// space the crop rectangle lives in), so weights are resolution-independent
// and identical across preview levels, tiles and export; the frame mapping is
// recovered from the params alone (see newMaskFrame). Weights are evaluated
// analytically for the parametric masks — no weight plane, so a full-res
// export allocates nothing extra — and by sampling a fixed-resolution
// coverage plane for brush masks.
func ApplyMasks(img *image.RGBA, e *edit.Params, ai AIMapSet) {
	if !e.HasMasks() {
		return
	}
	bnd := img.Bounds()
	w, h := bnd.Dx(), bnd.Dy()
	if w == 0 || h == 0 {
		return
	}
	f := newMaskFrame(w, h, e)
	var wrow []uint16
	for mi := range e.Masks {
		m := &e.Masks[mi]
		if m.Adjust.IsNeutral() {
			continue
		}
		ev := newMaskEvaluator(m, f, ai, img)
		if ev == nil {
			continue
		}
		if wrow == nil {
			wrow = make([]uint16, w)
		}
		lutR, lutG, lutB := buildMaskLUTs(&m.Adjust)
		satQ := int32(math.Round(100 * (1 + m.Adjust.Saturation)))
		for y := range h {
			x0, x1 := ev.weightRow(y, wrow)
			if x0 >= x1 {
				continue
			}
			row := img.Pix[y*img.Stride : y*img.Stride+w*4]
			for x := x0; x < x1; x++ {
				wq := int32(wrow[x])
				if wq == 0 {
					continue
				}
				i := x * 4
				r0, g0, b0 := int32(row[i]), int32(row[i+1]), int32(row[i+2])
				r := int32(lutR[r0])
				g := int32(lutG[g0])
				b := int32(lutB[b0])
				if satQ != 100 {
					// Saturation around Rec.601 luma, as applyLookSimple.
					luma := (299*r + 587*g + 114*b) / 1000
					r = int32(clamp8(luma + (r-luma)*satQ/100))
					g = int32(clamp8(luma + (g-luma)*satQ/100))
					b = int32(clamp8(luma + (b-luma)*satQ/100))
				}
				// Q8 blend toward the adjusted color; wq is 0..256.
				row[i] = clamp8(r0 + (r-r0)*wq>>8)
				row[i+1] = clamp8(g0 + (g-g0)*wq>>8)
				row[i+2] = clamp8(b0 + (b-b0)*wq>>8)
			}
		}
	}
}

// MaskWeightPlane rasterizes one mask's weight into an outW×outH byte plane
// (255 = full weight) in display space — the develop overlay's hover-tint
// source. A missing/degenerate mask (or an AI mask whose map isn't in ai)
// yields an all-zero plane, never an error.
func MaskWeightPlane(outW, outH int, e *edit.Params, index int, ai AIMapSet) []uint8 {
	plane := make([]uint8, outW*outH)
	if e == nil || index < 0 || index >= len(e.Masks) || outW <= 0 || outH <= 0 {
		return plane
	}
	f := newMaskFrame(outW, outH, e)
	ev := newMaskEvaluator(&e.Masks[index], f, ai, nil)
	if ev == nil {
		return plane
	}
	wrow := make([]uint16, outW)
	for y := 0; y < outH; y++ {
		for i := range wrow {
			wrow[i] = 0
		}
		x0, x1 := ev.weightRow(y, wrow)
		row := plane[y*outW:]
		for x := x0; x < x1 && x < outW; x++ {
			row[x] = uint8(min(255, int(wrow[x])*255/256))
		}
	}
	return plane
}

// maskFrame maps output-buffer pixels back onto the oriented frame — the
// full quarter-rotated/mirrored frame before straighten and crop, whose
// fractions the mask geometry (and the crop rectangle) are stored in. The
// frame size is reconstructed from the crop fractions (out/cropW); the ±1 px
// error against the true pre-crop buffer is invisible under any feather.
// The mapping mirrors the inverse sampling in ApplyGeometry.
type maskFrame struct {
	frameW, frameH float64 // oriented frame size at the output buffer's scale
	ox0, oy0       float64 // crop origin in frame pixels
	fcx, fcy       float64 // frame center (straighten pivot)
	cos, sin       float64 // rotation by -CropAngle (output → frame)
}

func newMaskFrame(outW, outH int, e *edit.Params) maskFrame {
	fw, fh := float64(outW), float64(outH)
	var f maskFrame
	if e.HasCrop() {
		fw = float64(outW) / e.CropW
		fh = float64(outH) / e.CropH
		f.ox0 = e.CropX * fw
		f.oy0 = e.CropY * fh
	}
	f.frameW, f.frameH = fw, fh
	f.fcx, f.fcy = fw/2, fh/2
	rad := -e.CropAngle * math.Pi / 180
	f.cos, f.sin = math.Cos(rad), math.Sin(rad)
	return f
}

// framePoint returns the frame-pixel coordinates of an output pixel center.
// The map is affine: stepping x by one output pixel steps the frame point by
// (cos, sin), which the evaluators use to walk rows incrementally.
func (f *maskFrame) framePoint(x, y float64) (fx, fy float64) {
	px := f.ox0 + x + 0.5 - f.fcx
	py := f.oy0 + y + 0.5 - f.fcy
	return f.fcx + px*f.cos - py*f.sin, f.fcy + px*f.sin + py*f.cos
}

// outputPoint is the inverse of framePoint (frame pixels → output pixel
// coordinates), used to bound a brush mask's strokes in output space.
func (f *maskFrame) outputPoint(fx, fy float64) (x, y float64) {
	dx, dy := fx-f.fcx, fy-f.fcy
	// Transpose of the framePoint rotation.
	px := dx*f.cos + dy*f.sin
	py := -dx*f.sin + dy*f.cos
	return px + f.fcx - f.ox0 - 0.5, py + f.fcy - f.oy0 - 0.5
}

// maskEvaluator fills one output row with Q8 weights (0..256) and returns the
// half-open span of columns that may be non-zero, letting the apply loop skip
// the rest of the row. This is the seam the three mask types share.
type maskEvaluator interface {
	weightRow(y int, w []uint16) (x0, x1 int)
}

// newMaskEvaluator builds the per-type weight source; nil means the mask is
// degenerate (or its AI map is unavailable) and contributes nothing. img is
// the render target — AI masks refine their edges against its luma on
// high-resolution renders (see guided.go); the parametric types ignore it.
func newMaskEvaluator(m *edit.Mask, f maskFrame, ai AIMapSet, img *image.RGBA) maskEvaluator {
	// Explicit nil checks: returning a nil *T directly would wrap it in a
	// non-nil interface.
	switch m.Type {
	case edit.MaskLinear:
		if ev := newLinearEval(m, f); ev != nil {
			return ev
		}
	case edit.MaskRadial:
		if ev := newRadialEval(m, f); ev != nil {
			return ev
		}
	case edit.MaskBrush:
		if ev := newBrushEval(m, f); ev != nil {
			return ev
		}
	case edit.MaskAI:
		if ev := newAIEval(m, f, ai); ev != nil {
			if g := newGuidedEval(ev, img); g != nil {
				return g
			}
			return ev
		}
	}
	return nil
}

// weightLUTSize quantizes the shape parameter (ρ² or t) for the baked
// feather curve, the vignette-gain precedent.
const weightLUTSize = 1024

// smoothstep01 is the classic 3t²-2t³ ease, clamped.
func smoothstep01(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// --- Radial (ellipse) ---

type radialEval struct {
	f       maskFrame
	cx, cy  float64 // center, frame pixels
	ca, sa  float64 // ellipse rotation
	rx, ry  float64 // radii, frame pixels
	wlut    [weightLUTSize]uint16
	outside uint16 // weight beyond ρ ≥ 1 (256 when inverted)
}

func newRadialEval(m *edit.Mask, f maskFrame) *radialEval {
	rx := m.RX * f.frameW
	ry := m.RY * f.frameH
	if rx < 1e-6 || ry < 1e-6 {
		return nil
	}
	rad := m.Angle * math.Pi / 180
	ev := &radialEval{
		f: f, cx: m.CX * f.frameW, cy: m.CY * f.frameH,
		ca: math.Cos(rad), sa: math.Sin(rad), rx: rx, ry: ry,
	}
	// Feather softens from the edge inward: w=1 for ρ ≤ 1-feather, 0 at ρ=1.
	feather := math.Max(m.Feather, 1.0/weightLUTSize)
	for q := range ev.wlut {
		rho := math.Sqrt(float64(q) / (weightLUTSize - 1))
		wgt := smoothstep01((1 - rho) / feather)
		if m.Invert {
			wgt = 1 - wgt
		}
		ev.wlut[q] = uint16(math.Round(256 * wgt))
	}
	if m.Invert {
		ev.outside = 256
	}
	return ev
}

func (ev *radialEval) weightRow(y int, w []uint16) (int, int) {
	width := len(w)
	fx, fy := ev.f.framePoint(0, float64(y))
	dx, dy := fx-ev.cx, fy-ev.cy
	// Normalized ellipse coordinates walk linearly along the row.
	u := (dx*ev.ca + dy*ev.sa) / ev.rx
	v := (-dx*ev.sa + dy*ev.ca) / ev.ry
	du := (ev.f.cos*ev.ca + ev.f.sin*ev.sa) / ev.rx
	dv := (-ev.f.cos*ev.sa + ev.f.sin*ev.ca) / ev.ry

	x0, x1 := 0, width
	if ev.outside == 0 {
		// Cull to the span where ρ² = (u+du·x)² + (v+dv·x)² ≤ 1.
		a := du*du + dv*dv
		b := 2 * (u*du + v*dv)
		c := u*u + v*v - 1
		if a < 1e-18 {
			if c > 0 {
				return 0, 0
			}
		} else {
			disc := b*b - 4*a*c
			if disc <= 0 {
				return 0, 0
			}
			s := math.Sqrt(disc)
			lo := (-b - s) / (2 * a)
			hi := (-b + s) / (2 * a)
			x0 = max(0, int(math.Floor(lo)))
			x1 = min(width, int(math.Ceil(hi))+1)
			if x0 >= x1 {
				return 0, 0
			}
		}
	}
	u += du * float64(x0)
	v += dv * float64(x0)
	for x := x0; x < x1; x++ {
		q := u*u + v*v
		if q >= 1 {
			w[x] = ev.outside
		} else {
			w[x] = ev.wlut[int(q*(weightLUTSize-1))]
		}
		u += du
		v += dv
	}
	return x0, x1
}

// --- Linear (graduated) ---

type linearEval struct {
	f      maskFrame
	ax, ay float64 // point A, frame pixels
	ux, uy float64 // (B-A)/|B-A|² so t = dot(p-A, u)
	invert bool
	wlut   [weightLUTSize]uint16
}

func newLinearEval(m *edit.Mask, f maskFrame) *linearEval {
	ax, ay := m.X0*f.frameW, m.Y0*f.frameH
	bx, by := m.X1*f.frameW, m.Y1*f.frameH
	dx, dy := bx-ax, by-ay
	len2 := dx*dx + dy*dy
	if len2 < 1e-9 {
		return nil
	}
	ev := &linearEval{f: f, ax: ax, ay: ay, ux: dx / len2, uy: dy / len2, invert: m.Invert}
	// Weight 1 at A (t ≤ 0) easing to 0 at B (t ≥ 1); the span is the feather.
	for q := range ev.wlut {
		wgt := 1 - smoothstep01(float64(q)/(weightLUTSize-1))
		if m.Invert {
			wgt = 1 - wgt
		}
		ev.wlut[q] = uint16(math.Round(256 * wgt))
	}
	return ev
}

func (ev *linearEval) weightRow(y int, w []uint16) (int, int) {
	width := len(w)
	fx, fy := ev.f.framePoint(0, float64(y))
	t := (fx-ev.ax)*ev.ux + (fy-ev.ay)*ev.uy
	dt := ev.f.cos*ev.ux + ev.f.sin*ev.uy

	// Cull to where the weight can be non-zero: t < 1 normally (weight fades
	// out at B), t > 0 when inverted.
	x0, x1 := 0, width
	const eps = 1e-12
	if math.Abs(dt) < eps {
		zero := t >= 1
		if ev.invert {
			zero = t <= 0
		}
		if zero {
			return 0, 0
		}
	} else {
		var bound float64
		var keepBelow bool // keep x below the bound when dt > 0
		if !ev.invert {
			bound = (1 - t) / dt
			keepBelow = dt > 0
		} else {
			bound = -t / dt
			keepBelow = dt < 0
		}
		if keepBelow {
			x1 = min(width, int(math.Ceil(bound))+1)
		} else {
			x0 = max(0, int(math.Floor(bound)))
		}
		if x0 >= x1 {
			return 0, 0
		}
	}
	t += dt * float64(x0)
	for x := x0; x < x1; x++ {
		switch {
		case t <= 0:
			w[x] = ev.wlut[0]
		case t >= 1:
			w[x] = ev.wlut[weightLUTSize-1]
		default:
			w[x] = ev.wlut[int(t*(weightLUTSize-1))]
		}
		t += dt
	}
	return x0, x1
}

// --- Brush ---

// brushPlaneLongEdge is the fixed raster resolution for brush coverage, in
// oriented-frame space. Fixed — not the render's resolution — so a 1024
// preview and a full-resolution export sample the exact same plane and agree
// by construction; the feathered stamps keep the upsampling invisible.
const brushPlaneLongEdge = 1024

type brushEval struct {
	f          maskFrame
	plane      []uint8
	pw, ph     int
	invert     bool
	covToW     *[256]uint16 // 0..255 coverage → 0..256 Q8 weight
	xMin, xMax int          // output-space bounds of the strokes (non-inverted culling)
	yMin, yMax int
}

var covToWeight = func() *[256]uint16 {
	var t [256]uint16
	for i := range t {
		t[i] = uint16((i*256 + 127) / 255)
	}
	return &t
}()

func newBrushEval(m *edit.Mask, f maskFrame) *brushEval {
	if len(m.Strokes) == 0 && !m.Invert {
		return nil
	}
	pw, ph := brushPlaneDims(f.frameW, f.frameH)
	ev := &brushEval{
		f: f, plane: brushPlaneFor(m.Strokes, pw, ph), pw: pw, ph: ph,
		invert: m.Invert, covToW: covToWeight,
		xMin: 0, xMax: 1 << 30, yMin: 0, yMax: 1 << 30,
	}
	if !m.Invert {
		ev.strokeBounds(m.Strokes)
	}
	return ev
}

// strokeBounds maps the strokes' frame-space bounding box (plus radius) into
// output pixel bounds so untouched rows and columns are skipped entirely.
func (ev *brushEval) strokeBounds(strokes []edit.Stroke) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	long := math.Max(ev.f.frameW, ev.f.frameH)
	for _, s := range strokes {
		if s.Erase {
			continue // erasing never extends coverage
		}
		r := s.Radius * long
		for i := 0; i+1 < len(s.Pts); i += 2 {
			px := s.Pts[i] * ev.f.frameW
			py := s.Pts[i+1] * ev.f.frameH
			minX = math.Min(minX, px-r)
			maxX = math.Max(maxX, px+r)
			minY = math.Min(minY, py-r)
			maxY = math.Max(maxY, py+r)
		}
	}
	if minX > maxX {
		ev.xMax, ev.yMax = 0, 0 // nothing painted
		return
	}
	ev.setFrameBox(minX, minY, maxX, maxY)
}

// setFrameBox maps a frame-space box into output pixel bounds. The
// frame→output map may rotate, so the box is bounded by its corners.
func (ev *brushEval) setFrameBox(minX, minY, maxX, maxY float64) {
	oxMin, oyMin := math.Inf(1), math.Inf(1)
	oxMax, oyMax := math.Inf(-1), math.Inf(-1)
	for _, c := range [4][2]float64{{minX, minY}, {maxX, minY}, {minX, maxY}, {maxX, maxY}} {
		ox, oy := ev.f.outputPoint(c[0], c[1])
		oxMin, oyMin = math.Min(oxMin, ox), math.Min(oyMin, oy)
		oxMax, oyMax = math.Max(oxMax, ox), math.Max(oyMax, oy)
	}
	ev.xMin = int(math.Floor(oxMin))
	ev.xMax = int(math.Ceil(oxMax)) + 1
	ev.yMin = int(math.Floor(oyMin))
	ev.yMax = int(math.Ceil(oyMax)) + 1
}

// --- AI (model-generated map) ---

// newAIEval builds the evaluator for an AI mask: the mask's parameters are
// folded into a derived coverage plane (cached — see coveragePlane) in
// oriented-frame space, and sampling reuses the brush machinery wholesale,
// so previews and export agree by the same construction as brush masks.
// Returns nil when the referenced map isn't in the set (not yet generated,
// or generated by a different model version): the mask contributes nothing
// rather than failing the render.
func newAIEval(m *edit.Mask, f maskFrame, ai AIMapSet) *brushEval {
	am := ai[aiSetKey(m.AIKind, m.MapVer)]
	if am == nil || am.W == 0 || am.H == 0 {
		return nil
	}
	ev := &brushEval{
		f: f, plane: coveragePlane(am, m), pw: am.W, ph: am.H,
		invert: m.Invert, covToW: covToWeight,
		xMin: 0, xMax: 1 << 30, yMin: 0, yMax: 1 << 30,
	}
	if !m.Invert {
		ev.coverageBounds()
	}
	return ev
}

// coverageBounds is strokeBounds' counterpart for a plane whose coverage is
// already rasterized: bound the non-zero box (plus one plane pixel of
// bilinear support) so untouched rows and columns are skipped.
func (ev *brushEval) coverageBounds() {
	minPX, minPY, maxPX, maxPY := ev.pw, ev.ph, -1, -1
	for y := 0; y < ev.ph; y++ {
		row := ev.plane[y*ev.pw : (y+1)*ev.pw]
		for x, v := range row {
			if v == 0 {
				continue
			}
			if maxPY < 0 {
				minPY = y // first non-zero row
			}
			maxPY = y
			if x < minPX {
				minPX = x
			}
			if x > maxPX {
				maxPX = x
			}
		}
	}
	if maxPX < 0 {
		ev.xMax, ev.yMax = 0, 0 // empty coverage
		return
	}
	sx := ev.f.frameW / float64(ev.pw)
	sy := ev.f.frameH / float64(ev.ph)
	ev.setFrameBox(
		(float64(minPX)-1)*sx, (float64(minPY)-1)*sy,
		(float64(maxPX)+2)*sx, (float64(maxPY)+2)*sy,
	)
}

func (ev *brushEval) weightRow(y int, w []uint16) (int, int) {
	width := len(w)
	x0 := max(0, ev.xMin)
	x1 := min(width, ev.xMax)
	if y < ev.yMin || y >= ev.yMax || x0 >= x1 {
		if !ev.invert {
			return 0, 0
		}
		for x := range w {
			w[x] = 256
		}
		return 0, width
	}
	if ev.invert {
		x0, x1 = 0, width
	}
	// Plane coordinates are affine along the row, like the frame point.
	sx := float64(ev.pw) / ev.f.frameW
	sy := float64(ev.ph) / ev.f.frameH
	fx, fy := ev.f.framePoint(float64(x0), float64(y))
	px := fx*sx - 0.5
	py := fy*sy - 0.5
	dpx := ev.f.cos * sx
	dpy := ev.f.sin * sy
	for x := x0; x < x1; x++ {
		cov := ev.samplePlane(px, py)
		if ev.invert {
			w[x] = 256 - ev.covToW[cov]
		} else {
			w[x] = ev.covToW[cov]
		}
		px += dpx
		py += dpy
	}
	if ev.invert {
		// Outside the plane-sampled span the coverage is zero → weight 256.
		for x := 0; x < x0; x++ {
			w[x] = 256
		}
		for x := x1; x < width; x++ {
			w[x] = 256
		}
		return 0, width
	}
	return x0, x1
}

// samplePlane bilinearly reads the coverage plane; outside reads as zero.
func (ev *brushEval) samplePlane(px, py float64) uint8 {
	x0f, y0f := math.Floor(px), math.Floor(py)
	x0, y0 := int(x0f), int(y0f)
	fx, fy := px-x0f, py-y0f
	var acc float64
	for _, s := range [4]struct {
		x, y int
		wgt  float64
	}{
		{x0, y0, (1 - fx) * (1 - fy)},
		{x0 + 1, y0, fx * (1 - fy)},
		{x0, y0 + 1, (1 - fx) * fy},
		{x0 + 1, y0 + 1, fx * fy},
	} {
		if s.x < 0 || s.x >= ev.pw || s.y < 0 || s.y >= ev.ph {
			continue
		}
		acc += float64(ev.plane[s.y*ev.pw+s.x]) * s.wgt
	}
	return uint8(acc + 0.5)
}

// brushPlaneDims sizes the coverage plane to the frame aspect with the long
// edge fixed at brushPlaneLongEdge, so stroke radii (fractions of the frame
// long edge) are circular in plane pixels.
func brushPlaneDims(frameW, frameH float64) (pw, ph int) {
	if frameW >= frameH {
		pw = brushPlaneLongEdge
		ph = max(1, int(math.Round(brushPlaneLongEdge*frameH/frameW)))
	} else {
		ph = brushPlaneLongEdge
		pw = max(1, int(math.Round(brushPlaneLongEdge*frameW/frameH)))
	}
	return pw, ph
}

// --- Brush plane rasterization + cache ---

// brushCache keeps recently rasterized coverage planes so dragging a mask's
// adjustment sliders (which leaves the strokes untouched) never re-rasterizes.
// Painting changes the key every preview, which is fine — a raster is ~ms.
var brushCache = struct {
	sync.Mutex
	planes map[string][]uint8
	order  []string // LRU, most recent last
}{planes: map[string][]uint8{}}

const brushCacheCap = 8

func brushPlaneFor(strokes []edit.Stroke, pw, ph int) []uint8 {
	key := brushKey(strokes, pw, ph)
	brushCache.Lock()
	if p, ok := brushCache.planes[key]; ok {
		for i, k := range brushCache.order {
			if k == key {
				brushCache.order = append(append(brushCache.order[:i:i], brushCache.order[i+1:]...), key)
				break
			}
		}
		brushCache.Unlock()
		return p
	}
	brushCache.Unlock()

	p := rasterStrokes(strokes, pw, ph)

	brushCache.Lock()
	if _, ok := brushCache.planes[key]; !ok {
		brushCache.planes[key] = p
		brushCache.order = append(brushCache.order, key)
		if len(brushCache.order) > brushCacheCap {
			delete(brushCache.planes, brushCache.order[0])
			brushCache.order = brushCache.order[1:]
		}
	}
	brushCache.Unlock()
	return p
}

func brushKey(strokes []edit.Stroke, pw, ph int) string {
	h := sha256.New()
	var dims [8]byte
	binary.LittleEndian.PutUint32(dims[0:], uint32(pw))
	binary.LittleEndian.PutUint32(dims[4:], uint32(ph))
	h.Write(dims[:])
	b, _ := json.Marshal(strokes)
	h.Write(b)
	return string(h.Sum(nil)[:16])
}

// rasterStrokes renders the stroke list to an 8-bit coverage plane. Each
// stroke stamps feathered circles along its polyline, combined with max so
// overlap inside one stroke doesn't build up; strokes then compose in order —
// paint with the over operator scaled by flow, erase multiplicatively.
func rasterStrokes(strokes []edit.Stroke, pw, ph int) []uint8 {
	plane := make([]uint8, pw*ph)
	if len(strokes) == 0 {
		return plane
	}
	scratch := make([]uint8, pw*ph)
	for _, s := range strokes {
		radPx := s.Radius * brushPlaneLongEdge
		if radPx < 0.5 || len(s.Pts) < 2 {
			continue
		}
		for i := range scratch {
			scratch[i] = 0
		}
		stampStroke(scratch, pw, ph, &s, radPx)
		flowQ := int32(255)
		if s.Flow > 0 {
			flowQ = int32(math.Round(s.Flow * 255))
		}
		if s.Erase {
			for i, c := range scratch {
				if c != 0 {
					plane[i] = uint8(int32(plane[i]) * (255*255 - flowQ*int32(c)) / (255 * 255))
				}
			}
		} else {
			for i, c := range scratch {
				if c != 0 {
					v := int32(plane[i])
					plane[i] = uint8(v + (flowQ*int32(c)*(255-v)+255*255/2)/(255*255))
				}
			}
		}
	}
	return plane
}

// stampStroke max-accumulates feathered stamps along the stroke's polyline
// into cov, spacing them at a quarter radius so the envelope is smooth.
func stampStroke(cov []uint8, pw, ph int, s *edit.Stroke, radPx float64) {
	// Feathered falloff over normalized distance², LUT'd like the weights.
	var lut [weightLUTSize]uint8
	feather := math.Max(s.Feather, 1.0/weightLUTSize)
	for q := range lut {
		d := math.Sqrt(float64(q) / (weightLUTSize - 1))
		lut[q] = uint8(math.Round(255 * smoothstep01((1-d)/feather)))
	}
	stamp := func(cx, cy float64) {
		x0 := max(0, int(math.Floor(cx-radPx)))
		x1 := min(pw-1, int(math.Ceil(cx+radPx)))
		y0 := max(0, int(math.Floor(cy-radPx)))
		y1 := min(ph-1, int(math.Ceil(cy+radPx)))
		inv := 1 / (radPx * radPx)
		for y := y0; y <= y1; y++ {
			dy := float64(y) - cy
			row := cov[y*pw:]
			for x := x0; x <= x1; x++ {
				dx := float64(x) - cx
				q := (dx*dx + dy*dy) * inv
				if q >= 1 {
					continue
				}
				v := lut[int(q*(weightLUTSize-1))]
				if v > row[x] {
					row[x] = v
				}
			}
		}
	}
	step := math.Max(1, radPx/4)
	px, py := s.Pts[0]*float64(pw), s.Pts[1]*float64(ph)
	stamp(px, py)
	for i := 2; i+1 < len(s.Pts); i += 2 {
		qx, qy := s.Pts[i]*float64(pw), s.Pts[i+1]*float64(ph)
		dist := math.Hypot(qx-px, qy-py)
		n := int(math.Ceil(dist / step))
		for k := 1; k <= n; k++ {
			t := float64(k) / float64(n)
			stamp(px+(qx-px)*t, py+(qy-py)*t)
		}
		px, py = qx, qy
	}
}

// --- Per-mask adjustment LUTs ---

// buildMaskLUTs folds the mask's exposure, temp/tint and tone curve into one
// per-channel LUT over the display-encoded input. Exposure and the white
// balance gains act in linear light (undoing previewExposureGamma, the
// applyExposureLUT precedent); the tone shapes are buildLookLUT's, with the
// calibrated gamma lift replaced by identity — the global look already
// applied it. Each channel is forced monotone like the global curve.
func buildMaskLUTs(a *edit.MaskAdjust) (lutR, lutG, lutB [256]uint8) {
	// Temp/tint as per-channel linear gains: ±1 ≈ ±half a stop on R/B (temp)
	// or G (tint) — half the strength of the global WBTemp/WBTint mapping,
	// which suits local touch-ups — renormalized to keep luma steady.
	gr := math.Pow(2, 0.5*a.Temp)
	gb := math.Pow(2, -0.5*a.Temp)
	gg := math.Pow(2, -0.5*a.Tint)
	l := 0.299*gr + 0.587*gg + 0.114*gb
	gr, gg, gb = gr/l, gg/l, gb/l

	k := math.Pow(2, a.ExpEV)
	s := 0.5 * a.Contrast
	wh := 0.25 * a.Whites
	bk := 0.25 * a.Blacks
	ts := 0.3 * a.ToneShadows
	th := 0.3 * a.ToneHighlights

	build := func(gain float64, lut *[256]uint8) {
		prev := 0
		for i := range lut {
			x := math.Pow(float64(i)/255, previewExposureGamma) * k * gain
			y := math.Pow(math.Min(1, x), 1/previewExposureGamma)
			y += s * y * (1 - y) * (2*y - 1) * 2
			y += ts * 6.75 * y * (1 - y) * (1 - y)
			y += th * 6.75 * y * y * (1 - y)
			y += bk * (1 - y) * (1 - y) * (1 - y)
			y += wh * y * y * y
			v := int(y*255 + 0.5)
			v = max(prev, min(255, max(0, v)))
			lut[i] = uint8(v)
			prev = v
		}
	}
	build(gr, &lutR)
	build(gg, &lutG)
	build(gb, &lutB)
	return
}
