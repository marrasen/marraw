package pyramid

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
)

// ApplyDetail runs the spatial detail ops — dehaze, then clarity/texture/
// sharpen — on a display-space render, after ApplyLook. Like the look stage
// it mutates img in place and is a no-op for a nil or neutral edit.
//
// Dehaze is a global veil subtraction (per-image estimate, LUT-applied).
// The three local-contrast ops are unsharp masks on luma at different radii:
// sharpen and texture use fixed radii in output pixels (so 1:1 tiles show
// the true result and the fit preview an indication, like every raw editor),
// while clarity's radius scales with the rendition so its midtone
// local-contrast look is resolution-independent.
func ApplyDetail(img *image.RGBA, e *edit.Params) {
	if e == nil || (e.Dehaze == 0 && e.Clarity == 0 && e.Texture == 0 && e.Sharpen == 0) {
		return
	}
	if e.Dehaze != 0 {
		applyDehaze(img, e.Dehaze)
	}
	if e.Clarity == 0 && e.Texture == 0 && e.Sharpen == 0 {
		return
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 4 || h < 4 {
		return
	}
	luma := lumaPlane(img)

	// Q8 fixed-point weights. The scales are tuned so ±100 on the slider is
	// strong but not broken: sharpen full ≈ +150% edge contrast at r=1,
	// texture ±90% at r=3, clarity ±70% midtone-weighted at a large radius.
	sharpQ := int32(math.Round(e.Sharpen * 1.5 * 256))
	texQ := int32(math.Round(e.Texture * 0.9 * 256))
	clarQ := int32(math.Round(e.Clarity * 0.7 * 256))

	var sharpB, texB, clarB []uint8
	if sharpQ != 0 {
		sharpB = boxBlurPlane(luma, w, h, 1, 1)
	}
	if texQ != 0 {
		texB = boxBlurPlane(luma, w, h, 3, 2)
	}
	if clarQ != 0 {
		r := max(8, max(w, h)/50)
		clarB = boxBlurPlane(luma, w, h, r, 2)
	}

	pix := img.Pix
	for y := range h {
		row := pix[y*img.Stride : y*img.Stride+w*4]
		for x := range w {
			j := y*w + x
			l := int32(luma[j])
			var d int32
			if sharpQ != 0 {
				d += sharpQ * (l - int32(sharpB[j]))
			}
			if texQ != 0 {
				d += texQ * (l - int32(texB[j]))
			}
			if clarQ != 0 {
				// Midtone weight 4·l·(255−l)/255 peaks at middle gray and
				// fades at the ends, protecting shadows and highlights.
				m := 4 * l * (255 - l) / 255
				d += clarQ * (l - int32(clarB[j])) * m / 255
			}
			if d == 0 {
				continue
			}
			d >>= 8
			i := x * 4
			row[i] = clamp8(int32(row[i]) + d)
			row[i+1] = clamp8(int32(row[i+1]) + d)
			row[i+2] = clamp8(int32(row[i+2]) + d)
		}
	}
}

// lumaPlane extracts the Rec.601 luma of a zero-origin RGBA image as a
// tightly packed plane.
func lumaPlane(img *image.RGBA) []uint8 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]uint8, w*h)
	pix := img.Pix
	for y := range h {
		row := pix[y*img.Stride : y*img.Stride+w*4]
		for x := range w {
			i := x * 4
			out[y*w+x] = uint8((299*int32(row[i]) + 587*int32(row[i+1]) + 114*int32(row[i+2])) / 1000)
		}
	}
	return out
}

// boxBlurPlane blurs a plane with an r-radius box filter, iterated `passes`
// times (two passes ≈ a triangular kernel, close enough to gaussian for
// unsharp masking). Separable running-sum implementation: O(pixels) per pass
// regardless of radius. Edges clamp.
func boxBlurPlane(src []uint8, w, h, r, passes int) []uint8 {
	tmp := make([]uint8, w*h)
	in := src
	for range passes {
		out := make([]uint8, w*h)
		boxBlurH(in, tmp, w, h, r)
		boxBlurV(tmp, out, w, h, r)
		in = out
	}
	return in
}

func boxBlurH(src, dst []uint8, w, h, r int) {
	if r >= w {
		r = w - 1
	}
	norm := int32(2*r + 1)
	for y := range h {
		row := src[y*w : y*w+w]
		out := dst[y*w : y*w+w]
		var sum int32
		for x := -r; x <= r; x++ {
			sum += int32(row[clampInt(x, 0, w-1)])
		}
		for x := range w {
			out[x] = uint8((sum + norm/2) / norm)
			sum += int32(row[min(x+r+1, w-1)]) - int32(row[max(x-r, 0)])
		}
	}
}

func boxBlurV(src, dst []uint8, w, h, r int) {
	if r >= h {
		r = h - 1
	}
	norm := int32(2*r + 1)
	sums := make([]int32, w)
	for y := -r; y <= r; y++ {
		row := src[clampInt(y, 0, h-1)*w:]
		for x := range w {
			sums[x] += int32(row[x])
		}
	}
	for y := range h {
		out := dst[y*w : y*w+w]
		for x := range w {
			out[x] = uint8((sums[x] + norm/2) / norm)
		}
		add := src[min(y+r+1, h-1)*w:]
		sub := src[max(y-r, 0)*w:]
		for x := range w {
			sums[x] += int32(add[x]) - int32(sub[x])
		}
	}
}

// applyDehaze estimates the atmospheric veil as a low percentile of the
// min-channel (the dark-channel prior collapsed to one global value) and
// stretches it out: out = (in − a·V) · 255/(255 − a·V). Negative amounts add
// a veil instead, lifting everything toward a light gray. Both directions
// are global, so they compile to one LUT applied per channel.
func applyDehaze(img *image.RGBA, amount float64) {
	var lut [256]uint8
	if amount > 0 {
		v := amount * float64(estimateVeil(img))
		v = math.Min(v, 200)
		scale := 255 / (255 - v)
		for i := range lut {
			lut[i] = clamp8(int32(math.Round((float64(i) - v) * scale)))
		}
	} else {
		f := -amount * 0.5
		for i := range lut {
			lut[i] = clamp8(int32(math.Round(float64(i) + f*(235-float64(i)))))
		}
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	pix := img.Pix
	for y := range h {
		row := pix[y*img.Stride : y*img.Stride+w*4]
		for i := 0; i+3 < len(row); i += 4 {
			row[i] = lut[row[i]]
			row[i+1] = lut[row[i+1]]
			row[i+2] = lut[row[i+2]]
		}
	}
}

// estimateVeil returns the ~1st percentile of the subsampled min-channel —
// how far the darkest tones sit above true black. The floor keeps the slider
// doing something visible even on an image with clean blacks.
func estimateVeil(img *image.RGBA) int {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var hist [256]int
	n := 0
	pix := img.Pix
	for y := 0; y < h; y += 4 {
		row := pix[y*img.Stride : y*img.Stride+w*4]
		for i := 0; i+3 < len(row); i += 16 {
			m := min(row[i], row[i+1], row[i+2])
			hist[m]++
			n++
		}
	}
	if n == 0 {
		return 24
	}
	target := n / 100
	acc := 0
	for v := range hist {
		acc += hist[v]
		if acc > target {
			return max(24, v)
		}
	}
	return 24
}
