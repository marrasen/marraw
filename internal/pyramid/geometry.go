package pyramid

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
)

// ApplyGeometry rotates, crops and straightens a display-space render in
// place-ish: it returns src unchanged for a neutral geometry, a cheap
// sub-image copy for an axis-aligned crop, and a bilinear rotate-sample when
// a straighten angle is set. Fractions are resolution-independent, so this
// runs identically on a half-size level render and a full-resolution decode.
//
// Model: the frame first turns by Rotate quarter turns clockwise, then is
// rotated about its center by CropAngle, then the axis-aligned crop
// rectangle (fractions of the quarter-rotated frame) is taken from the
// result. Output size is edit.OutputDims — the angle never changes it.
// Samples that fall outside the source read as opaque black, which is what
// the crop overlay's angle guides keep the user's rectangle clear of.
func ApplyGeometry(src *image.RGBA, e *edit.Params) *image.RGBA {
	if e == nil {
		return src
	}
	origW, origH := src.Bounds().Dx(), src.Bounds().Dy()
	if code := rotateFlipCode(e.RotateTurns()); code != 0 {
		src = rotateFlip(src, code)
	}
	if !e.HasCrop() && e.CropAngle == 0 {
		return src
	}
	b := src.Bounds()
	fullW, fullH := b.Dx(), b.Dy()

	// Crop rectangle in source pixels (full frame when only straightening).
	// OutputDims takes the pre-rotation dims — it applies the axis swap itself.
	cx0, cy0 := 0.0, 0.0
	cropW, cropH := fullW, fullH
	if e.HasCrop() {
		cx0 = e.CropX * float64(fullW)
		cy0 = e.CropY * float64(fullH)
		cropW, cropH = e.OutputDims(origW, origH)
	}

	// Fast path: a pure axis-aligned crop is a clamped sub-image copy.
	if e.CropAngle == 0 {
		x0 := clampInt(int(math.Round(cx0)), 0, fullW-1)
		y0 := clampInt(int(math.Round(cy0)), 0, fullH-1)
		x1 := clampInt(x0+cropW, x0+1, fullW)
		y1 := clampInt(y0+cropH, y0+1, fullH)
		return copyRGBA(src, image.Rect(b.Min.X+x0, b.Min.Y+y0, b.Min.X+x1, b.Min.Y+y1))
	}

	// Rotate-sample: for each output pixel, find its point in the rotated
	// frame, un-rotate about the frame center to a source coordinate, and
	// bilinearly sample. cos(-θ), sin(-θ) invert the frame rotation.
	rad := e.CropAngle * math.Pi / 180
	cos, sin := math.Cos(-rad), math.Sin(-rad)
	fcx, fcy := float64(fullW)/2, float64(fullH)/2

	dst := image.NewRGBA(image.Rect(0, 0, cropW, cropH))
	for oy := range cropH {
		fy := cy0 + float64(oy) + 0.5 - fcy
		for ox := range cropW {
			fx := cx0 + float64(ox) + 0.5 - fcx
			sx := fcx + fx*cos - fy*sin - 0.5
			sy := fcy + fx*sin + fy*cos - 0.5
			r, g, bl, a := sampleBilinear(src, sx, sy)
			o := dst.PixOffset(ox, oy)
			dst.Pix[o+0] = r
			dst.Pix[o+1] = g
			dst.Pix[o+2] = bl
			dst.Pix[o+3] = a
		}
	}
	return dst
}

// copyRGBA returns a tightly-packed copy of the given rectangle of src so the
// result's stride matches its width (downstream scaling and tiling assume a
// full-width RGBA, not a SubImage view into a larger buffer).
func copyRGBA(src *image.RGBA, r image.Rectangle) *image.RGBA {
	r = r.Intersect(src.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := range r.Dy() {
		so := src.PixOffset(r.Min.X, r.Min.Y+y)
		do := dst.PixOffset(0, y)
		copy(dst.Pix[do:do+r.Dx()*4], src.Pix[so:so+r.Dx()*4])
	}
	return dst
}

// sampleBilinear reads src at a fractional coordinate, returning opaque black
// outside the image so straighten's exposed corners are clean rather than
// smeared edge pixels.
func sampleBilinear(src *image.RGBA, x, y float64) (r, g, b, a uint8) {
	bnd := src.Bounds()
	x0f := math.Floor(x)
	y0f := math.Floor(y)
	x0, y0 := int(x0f), int(y0f)
	fx, fy := x-x0f, y-y0f

	var acc [4]float64
	for _, s := range [4]struct {
		px, py int
		w      float64
	}{
		{x0, y0, (1 - fx) * (1 - fy)},
		{x0 + 1, y0, fx * (1 - fy)},
		{x0, y0 + 1, (1 - fx) * fy},
		{x0 + 1, y0 + 1, fx * fy},
	} {
		if s.px < bnd.Min.X || s.px >= bnd.Max.X || s.py < bnd.Min.Y || s.py >= bnd.Max.Y {
			continue // outside → contributes nothing (black, transparent-free)
		}
		o := src.PixOffset(s.px, s.py)
		acc[0] += float64(src.Pix[o+0]) * s.w
		acc[1] += float64(src.Pix[o+1]) * s.w
		acc[2] += float64(src.Pix[o+2]) * s.w
		acc[3] += float64(src.Pix[o+3]) * s.w
	}
	return uint8(acc[0] + 0.5), uint8(acc[1] + 0.5), uint8(acc[2] + 0.5), uint8(acc[3] + 0.5)
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// rotateFlipCode maps canonical quarter turns clockwise onto the EXIF flip
// codes rotateFlip implements: 1 → 6 (90° CW), 2 → 3 (180°), 3 → 5 (90° CCW).
func rotateFlipCode(turns int) int {
	switch turns {
	case 1:
		return 6
	case 2:
		return 3
	case 3:
		return 5
	}
	return 0
}
