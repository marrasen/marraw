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
// Model: the frame first turns by Rotate quarter turns clockwise and is
// mirrored about the vertical axis when FlipH is set, then is rotated about
// its center by CropAngle, then the axis-aligned crop rectangle (fractions
// of the quarter-rotated, mirrored frame) is taken from the result. Output
// size is edit.OutputDims — neither the mirror nor the angle changes it.
// Samples that fall outside the source read as opaque black, which is what
// the crop overlay's angle guides keep the user's rectangle clear of. src
// must start at (0, 0): geometryInto reads a source that starts elsewhere
// as that part of the frame.
func ApplyGeometry(src *image.RGBA, e *edit.Params) *image.RGBA {
	if e == nil {
		return src
	}
	origW, origH := src.Bounds().Dx(), src.Bounds().Dy()
	src = turnFrame(src, e)
	if !e.HasCrop() && e.CropAngle == 0 {
		return src
	}
	g := newCropGeom(src.Bounds().Dx(), src.Bounds().Dy(), origW, origH, e)
	return geometryInto(src, g, image.Rect(0, 0, g.outW, g.outH))
}

// turnFrame applies the edit's quarter turns and mirror.
func turnFrame(src *image.RGBA, e *edit.Params) *image.RGBA {
	if code := rotateFlipCode(e.RotateTurns()); code != 0 {
		src = rotateFlip(src, code)
	}
	if e.FlipH {
		src = flipHorizontal(src)
	}
	return src
}

// cropGeom is where the output sits in the turned frame (fullW×fullH):
// the crop rectangle, straightened by angle about the frame centre.
type cropGeom struct {
	fullW, fullH int
	angle        float64 // degrees; 0 is a plain crop
	cx0, cy0     float64 // crop origin in frame pixels
	x0, y0       int     // the same, rounded, for a plain crop
	outW, outH   int     // output size
}

func newCropGeom(fullW, fullH, origW, origH int, e *edit.Params) cropGeom {
	g := cropGeom{fullW: fullW, fullH: fullH, angle: e.CropAngle, outW: fullW, outH: fullH}
	// Crop rectangle in source pixels (full frame when only straightening).
	// OutputDims takes the pre-rotation dims — it applies the axis swap itself.
	if e.HasCrop() {
		g.cx0 = e.CropX * float64(fullW)
		g.cy0 = e.CropY * float64(fullH)
		g.outW, g.outH = e.OutputDims(origW, origH)
	}
	if g.angle == 0 {
		// A plain crop is a clamped sub-image copy.
		g.x0 = clampInt(int(math.Round(g.cx0)), 0, fullW-1)
		g.y0 = clampInt(int(math.Round(g.cy0)), 0, fullH-1)
		g.outW = clampInt(g.x0+g.outW, g.x0+1, fullW) - g.x0
		g.outH = clampInt(g.y0+g.outH, g.y0+1, fullH) - g.y0
	}
	return g
}

// geometryInto renders the part out of the output (in output pixels) from
// src: the turned frame, or a part of it in the frame's own coordinates
// holding every pixel out samples (cropGeom.source). The result starts at
// (0, 0).
func geometryInto(src *image.RGBA, g cropGeom, out image.Rectangle) *image.RGBA {
	if g.angle == 0 {
		return copyRGBA(src, out.Add(image.Pt(g.x0, g.y0)))
	}
	// Rotate-sample: for each output pixel, find its point in the rotated
	// frame, un-rotate about the frame center to a source coordinate, and
	// bilinearly sample. cos(-θ), sin(-θ) invert the frame rotation.
	cos, sin, fcx, fcy := g.rotation()
	dst := image.NewRGBA(image.Rect(0, 0, out.Dx(), out.Dy()))
	bands(out.Dy(), func(y0, y1 int) {
		for ly := y0; ly < y1; ly++ {
			fy := g.cy0 + float64(out.Min.Y+ly) + 0.5 - fcy
			for lx := range out.Dx() {
				fx := g.cx0 + float64(out.Min.X+lx) + 0.5 - fcx
				sx := fcx + fx*cos - fy*sin - 0.5
				sy := fcy + fx*sin + fy*cos - 0.5
				r, gr, bl, a := sampleBilinear(src, sx, sy)
				o := dst.PixOffset(lx, ly)
				dst.Pix[o+0] = r
				dst.Pix[o+1] = gr
				dst.Pix[o+2] = bl
				dst.Pix[o+3] = a
			}
		}
	})
	return dst
}

func (g cropGeom) rotation() (cos, sin, fcx, fcy float64) {
	rad := g.angle * math.Pi / 180
	return math.Cos(-rad), math.Sin(-rad), float64(g.fullW) / 2, float64(g.fullH) / 2
}

// source is the part of the turned frame geometryInto reads for out.
func (g cropGeom) source(out image.Rectangle) image.Rectangle {
	frame := image.Rect(0, 0, g.fullW, g.fullH)
	if g.angle == 0 {
		return out.Add(image.Pt(g.x0, g.y0)).Intersect(frame)
	}
	// The map is affine, so the corner pixels bound the samples; the
	// bilinear taps reach one pixel past them.
	cos, sin, fcx, fcy := g.rotation()
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range [4]image.Point{out.Min, {out.Max.X - 1, out.Min.Y}, {out.Min.X, out.Max.Y - 1}, out.Max.Sub(image.Pt(1, 1))} {
		fx := g.cx0 + float64(p.X) + 0.5 - fcx
		fy := g.cy0 + float64(p.Y) + 0.5 - fcy
		sx := fcx + fx*cos - fy*sin - 0.5
		sy := fcy + fx*sin + fy*cos - 0.5
		minX, maxX = math.Min(minX, sx), math.Max(maxX, sx)
		minY, maxY = math.Min(minY, sy), math.Max(maxY, sy)
	}
	return image.Rect(int(math.Floor(minX))-1, int(math.Floor(minY))-1,
		int(math.Ceil(maxX))+2, int(math.Ceil(maxY))+2).Intersect(frame)
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

	if x0 >= bnd.Min.X && y0 >= bnd.Min.Y && x0+1 < bnd.Max.X && y0+1 < bnd.Max.Y {
		// All four taps inside: the same sums in the same order as the loop
		// below, read straight from the pixel slice.
		w00, w10 := (1-fx)*(1-fy), fx*(1-fy)
		w01, w11 := (1-fx)*fy, fx*fy
		p := src.Pix
		o00 := src.PixOffset(x0, y0)
		o01 := o00 + src.Stride
		var out [4]uint8
		for c := range out {
			acc := float64(p[o00+c]) * w00
			acc += float64(p[o00+4+c]) * w10
			acc += float64(p[o01+c]) * w01
			acc += float64(p[o01+4+c]) * w11
			out[c] = uint8(acc + 0.5)
		}
		return out[0], out[1], out[2], out[3]
	}

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

// flipHorizontal mirrors the image about its vertical axis.
func flipHorizontal(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	bands(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := range w {
				dst.SetRGBA(w-1-x, y, src.RGBAAt(b.Min.X+x, b.Min.Y+y))
			}
		}
	})
	return dst
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
