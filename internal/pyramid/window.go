package pyramid

import (
	"context"
	"image"
	"math"
	"slices"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/lens"
)

// region places a render buffer inside the full output of a render: the
// buffer's pixel (0, 0) is the output's pixel off, and the output is
// outW×outH. The stages that depend on where a pixel sits in the frame —
// the look's vignette, mask and spot positions, the clarity radius — read
// it, so a buffer that is only part of the output (a window, see
// RenderWindow) renders the same pixels the whole output has there.
type region struct {
	off        image.Point
	outW, outH int
}

// wholeRegion is the region of a buffer that is the whole output.
func wholeRegion(img *image.RGBA) region {
	b := img.Bounds()
	return region{outW: b.Dx(), outH: b.Dy()}
}

// Windowable reports whether RenderWindow can render e. Dehaze and the
// spatial effects on masks (blur, glow, streaks and the rest) measure or
// gather over the whole frame, so a window of them needs the whole render.
func Windowable(e *edit.Params) bool {
	if e == nil {
		return true
	}
	if e.Dehaze != 0 {
		return false
	}
	for i := range e.Masks {
		m := &e.Masks[i]
		if !m.Disabled && m.Adjust.HasFX() {
			return false
		}
	}
	return true
}

// OutputSize is the size of the full render of e from a decode of
// decW×decH: the frame after the quarter turns, crop and straighten.
func OutputSize(decW, decH int, e *edit.Params) (w, h int) {
	g, _ := windowGeom(decW, decH, e)
	return g.outW, g.outH
}

// windowGeom is the crop geometry of e over a decode of decW×decH, and
// the quarter-turn code (rotateFlipCode) that comes before it.
func windowGeom(decW, decH int, e *edit.Params) (cropGeom, int) {
	code := 0
	if e != nil {
		code = rotateFlipCode(e.RotateTurns())
	}
	tw, th := decW, decH
	if code == 5 || code == 6 {
		tw, th = decH, decW
	}
	if e == nil || (!e.HasCrop() && e.CropAngle == 0) {
		return cropGeom{fullW: tw, fullH: th, outW: tw, outH: th}, code
	}
	return newCropGeom(tw, th, decW, decH, e), code
}

// RenderWindow renders the part win of the full-resolution render of e —
// the pixels the 1:1 tiles hold there — from src, the full decode that
// render starts from. src is only read. expEV is the exposure the render
// folds in after the decode (its ResidualExpEV and WBCompEV). It returns
// nil when win misses the output, e is not Windowable, or ctx is done
// (checked between the stages).
//
// It works backwards from win: the margin the detail stage and the AI
// masks' edge refinement reach over, then the spots that land in that
// area and the pixels they copy from, then back through the crop and
// straighten, the quarter turns and the lens correction to the part of the
// decode all of that comes from. Then it renders forward, each stage on
// only the part the stages after it need. Every stage is the one the full
// render runs, told where its buffer sits in the frame, so the result
// matches the full render's pixels there, exactly.
func RenderWindow(ctx context.Context, src *image.RGBA, win image.Rectangle, gamma float64, e *edit.Params, expEV float64, lensc *lens.Correction, ai AIMapSet, fills FillSet) *image.RGBA {
	if !Windowable(e) {
		return nil
	}
	sb := src.Bounds()
	decW, decH := sb.Dx(), sb.Dy()
	g, code := windowGeom(decW, decH, e)
	output := image.Rect(0, 0, g.outW, g.outH)
	win = win.Intersect(output)
	if win.Empty() {
		return nil
	}
	whole := region{outW: g.outW, outH: g.outH}

	// What the look, masks and detail run on: the window plus the reach
	// of the detail blurs and of the AI masks' refinement, whose block
	// grid it starts on.
	pad, align := maskReach(whole, e, ai)
	pad += detailMargin(whole, e)
	need := win.Inset(-pad)
	if align > 1 {
		need.Min = image.Pt(need.Min.X/align*align, need.Min.Y/align*align)
	}
	need = need.Intersect(output)
	// What the geometry renders: that, plus what the spots read. Each part
	// renders on its own, into one buffer around them all: a spot's source
	// can sit far from the window, and the frame between them is not read.
	// That buffer starts at the frame's origin, so the spots work in the
	// whole render's own coordinates: their sums round as its do.
	parts := healParts(whole, e, need)
	healed := parts[0]
	for _, p := range parts[1:] {
		healed = healed.Union(p)
	}
	if len(parts) > 1 {
		healed.Min = image.Point{}
	}
	warp := LensWarp(lensc, e, decW, decH)
	part := func(out image.Rectangle) *image.RGBA {
		return renderGeometry(src, out, g, code, warp, e, expEV)
	}
	img := part(need)
	if ctx.Err() != nil {
		return nil
	}
	if len(parts) > 1 {
		all := image.NewRGBA(image.Rect(0, 0, healed.Dx(), healed.Dy()))
		pasteRGBA(all, img, need.Min.Sub(healed.Min))
		for i, p := range parts[1:] {
			if !slices.ContainsFunc(parts[:i+1], p.In) {
				pasteRGBA(all, part(p), p.Min.Sub(healed.Min))
			}
		}
		img = all
	}
	if ctx.Err() != nil {
		return nil
	}

	applyHealIn(img, region{off: healed.Min, outW: g.outW, outH: g.outH}, e, ai, fills)
	if len(parts) > 1 {
		img = copyRGBA(img, need.Sub(healed.Min))
	}
	rg := region{off: need.Min, outW: g.outW, outH: g.outH}
	applyLookIn(img, rg, gamma, e)
	suppress := applyMasksIn(img, rg, e, ai)
	ApplyBW(img, e)
	if ctx.Err() != nil {
		return nil
	}
	applyDetailIn(img, rg, e, suppress)
	return copyRGBA(img, win.Sub(need.Min))
}

// renderGeometry renders the part out of the output up to the spots: the
// decode with the exposure folded in, lens-corrected, turned, cropped and
// straightened. It reads only the part of src out comes from.
func renderGeometry(src *image.RGBA, out image.Rectangle, g cropGeom, code int, warp *lens.Warp, e *edit.Params, expEV float64) *image.RGBA {
	sb := src.Bounds()
	turned := g.source(out)
	if turned.Empty() {
		// All of out is outside the straightened frame: black.
		return image.NewRGBA(image.Rect(0, 0, out.Dx(), out.Dy()))
	}
	lensed := unturnRect(turned, code, e != nil && e.FlipH, sb.Dx(), sb.Dy())
	read := lensed
	if warp.Geometric() {
		read = lensSource(warp, lensed, sb)
	}
	img := copyRGBAAt(src, read)
	ApplyExposureEV(img, expEV, e)
	switch {
	case warp.Geometric():
		img = lensInto(img, warp, e, lensed)
	case warp != nil:
		applyVignette(img, warp, e)
	}
	if code != 0 || (e != nil && e.FlipH) {
		// The turns work from (0, 0); the part they make sits at turned
		// in the turned frame.
		img.Rect = image.Rect(0, 0, img.Rect.Dx(), img.Rect.Dy())
		img = turnFrame(img, e)
		img.Rect = turned
	}
	return geometryInto(img, g, out)
}

// pasteRGBA copies src, which starts at (0, 0), into dst at at.
func pasteRGBA(dst, src *image.RGBA, at image.Point) {
	w := src.Bounds().Dx() * 4
	for y := range src.Bounds().Dy() {
		o := dst.PixOffset(at.X, at.Y+y)
		copy(dst.Pix[o:o+w], src.Pix[y*src.Stride:y*src.Stride+w])
	}
}

// copyRGBAAt is copyRGBA keeping r's coordinates: the copy's bounds are r.
func copyRGBAAt(src *image.RGBA, r image.Rectangle) *image.RGBA {
	img := copyRGBA(src, r)
	img.Rect = r.Intersect(src.Bounds())
	return img
}

// maskReach is how far the masks of e reach around a pixel, and the grid
// a buffer must start on for them: an AI mask's edge refinement averages
// blocks of the output and blurs the result over guidedRadius blocks, then
// over a quarter of that, and reads it back bilinearly.
func maskReach(rg region, e *edit.Params, ai AIMapSet) (pad, align int) {
	align = 1
	if e == nil {
		return 0, 1
	}
	f := newMaskFrameIn(rg, e)
	probe := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for i := range e.Masks {
		m := &e.Masks[i]
		if m.Type != edit.MaskAI || m.Disabled || m.Adjust.IsNeutral() {
			continue
		}
		base := newAIEval(m, f, ai)
		if base == nil {
			continue
		}
		g := newGuidedEval(base, probe)
		if g == nil {
			continue
		}
		// Summed, not the largest: a later AI mask refines against the
		// image the earlier ones changed, each right only so far in.
		pad += (guidedRadius + guidedRadius/4 + 2) * g.s
		align = lcm(align, g.s)
	}
	return pad, align
}

func lcm(a, b int) int {
	x, y := a, b
	for y != 0 {
		x, y = y, x%y
	}
	return a / x * b
}

// healParts is need, a part of the output, and the parts the spots that
// land in it read: the destination's surround the heal fit samples, and
// the source patch. It walks the spots last to first, as a spot reads what
// the spots before it wrote. Fill spots and Remove masks read no other
// pixels.
func healParts(rg region, e *edit.Params, need image.Rectangle) []image.Rectangle {
	parts := []image.Rectangle{need}
	if !e.HasSpots() {
		return parts
	}
	output := image.Rect(0, 0, rg.outW, rg.outH)
	f := newMaskFrameIn(rg, e)
	long := math.Max(f.frameW, f.frameH)
	for i := len(e.Spots) - 1; i >= 0; i-- {
		s := &e.Spots[i]
		if s.Disabled || s.Mode == edit.SpotFill || !healModeKnown(s.Mode) {
			continue
		}
		var dest, src image.Rectangle
		switch s.Kind {
		case "":
			dest, src = healSpotReach(&f, long, s)
		case "stroke":
			dest, src = healStrokeReach(&f, long, s)
		default:
			continue
		}
		if slices.ContainsFunc(parts, dest.Overlaps) {
			for _, p := range [2]image.Rectangle{dest.Intersect(output), src.Intersect(output)} {
				if !p.Empty() {
					parts = append(parts, p)
				}
			}
		}
	}
	return parts
}

// healSpotReach is what applyHealSpot writes and reads around (dest) and
// copies from (src), with a pixel to spare.
func healSpotReach(f *maskFrame, long float64, s *edit.Spot) (dest, src image.Rectangle) {
	radPx := s.Radius * long
	if radPx < 0.75 {
		return
	}
	dcx, dcy := f.outputPoint(s.CX*f.frameW, s.CY*f.frameH)
	scx, scy := f.outputPoint(s.SX*f.frameW, s.SY*f.frameH)
	if float64(f.outW) > 2*radPx {
		scx = clampF(scx, radPx, float64(f.outW)-radPx)
	}
	if float64(f.outH) > 2*radPx {
		scy = clampF(scy, radPx, float64(f.outH)-radPx)
	}
	reach := int(math.Ceil(radPx*healAnnulusOuter)) + 3
	dest = image.Rect(int(math.Floor(dcx))-reach, int(math.Floor(dcy))-reach,
		int(math.Ceil(dcx))+reach+1, int(math.Ceil(dcy))+reach+1)
	src = image.Rect(int(scx)-reach, int(scy)-reach, int(scx)+reach+1, int(scy)+reach+1)
	return dest, src
}

// healStrokeReach is healSpotReach for a painted spot (applyHealStroke):
// the painted box with the band around it, and the same at the source.
func healStrokeReach(f *maskFrame, long float64, s *edit.Spot) (dest, src image.Rectangle) {
	if len(s.Strokes) == 0 {
		return
	}
	ev := &brushEval{f: *f, xMin: 0, xMax: 1 << 30, yMin: 0, yMax: 1 << 30}
	ev.strokeBounds(s.Strokes)
	x0, x1 := max(0, ev.xMin), min(f.outW, ev.xMax)
	y0, y1 := max(0, ev.yMin), min(f.outH, ev.yMax)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	dcx, dcy := f.outputPoint(s.CX*f.frameW, s.CY*f.frameH)
	scx, scy := f.outputPoint(s.SX*f.frameW, s.SY*f.frameH)
	offX := clampF(scx-dcx, -float64(x0), float64(f.outW)-float64(x1))
	offY := clampF(scy-dcy, -float64(y0), float64(f.outH)-float64(y1))
	var radPx float64
	for i := range s.Strokes {
		radPx = math.Max(radPx, s.Strokes[i].Radius*long)
	}
	// The heal fit samples a band around the painted area, measured on the
	// brush plane (fitStrokeBandPlanes): reach plane pixels out, each
	// long/brushPlaneLongEdge output pixels wide — on a large frame much
	// more than the band's own width.
	bandPx := math.Max(2, 0.35*radPx)
	reach := max(1, int(math.Ceil(bandPx*brushPlaneLongEdge/long)))
	pad := int(math.Ceil(math.Max(bandPx, float64(reach+1)*long/brushPlaneLongEdge))) + 4
	dest = image.Rect(x0, y0, x1, y1).Inset(-pad)
	src = image.Rect(x0+int(math.Floor(offX)), y0+int(math.Floor(offY)),
		x1+int(math.Ceil(offX)), y1+int(math.Ceil(offY))).Inset(-pad)
	return dest, src
}

// unturnRect maps r, a part of the frame after the quarter turn code and
// the mirror, back onto the decode's frame (decW×decH) — where the pixels
// turnFrame moves into r come from.
func unturnRect(r image.Rectangle, code int, flip bool, decW, decH int) image.Rectangle {
	tw := decW
	if code == 5 || code == 6 {
		tw = decH
	}
	if flip {
		r = image.Rect(tw-r.Max.X, r.Min.Y, tw-r.Min.X, r.Max.Y)
	}
	switch code {
	case 3: // 180°: (x, y) → (w-1-x, h-1-y)
		return image.Rect(decW-r.Max.X, decH-r.Max.Y, decW-r.Min.X, decH-r.Min.Y)
	case 5: // 90° CCW: (x, y) → (y, w-1-x)
		return image.Rect(decW-r.Max.Y, r.Min.X, decW-r.Min.Y, r.Max.X)
	case 6: // 90° CW: (x, y) → (h-1-y, x)
		return image.Rect(r.Min.Y, decH-r.Max.X, r.Max.Y, decH-r.Min.X)
	}
	return r
}

// lensSource is the part of the uncorrected frame (frame) lensInto reads
// for out. The warp is smooth, so its samples over out are bounded by
// those along out's edges and on a coarse grid inside, with room to spare
// for the bilinear taps.
func lensSource(w *lens.Warp, out, frame image.Rectangle) image.Rectangle {
	if out.Empty() {
		return image.Rectangle{}
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	add := func(x, y float64) {
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	at := func(x, y int) {
		rx, ry, gx, gy, bx, by := w.Source(float64(x)+0.5, float64(y)+0.5)
		add(gx, gy)
		if w.HasTCA() {
			add(rx, ry)
			add(bx, by)
		}
	}
	const step = 32
	for y := out.Min.Y; y < out.Max.Y; y++ {
		if y == out.Min.Y || y == out.Max.Y-1 || (y-out.Min.Y)%step == 0 {
			for x := out.Min.X; x < out.Max.X; x++ {
				at(x, y)
			}
			continue
		}
		at(out.Min.X, y)
		at(out.Max.X-1, y)
	}
	const spare = 4
	return image.Rect(int(math.Floor(minX))-spare, int(math.Floor(minY))-spare,
		int(math.Ceil(maxX))+spare+1, int(math.Ceil(maxY))+spare+1).Intersect(frame)
}
