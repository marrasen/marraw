package pyramid

// Black & white conversion. A flat desaturation throws away the one thing
// that makes a BW frame read: two different colors of the same brightness
// collapse to the same gray. So instead of dropping chroma, we spend it —
// each pixel's gray is pushed by the HSLLum band its hue falls in, scaled by
// how saturated the pixel was. That is the digital form of screwing a colored
// filter onto a BW camera: pull the red band down and a blue sky goes dark
// behind white clouds; lift the green and foliage separates from the sky.
//
// The stage runs after ApplyMasks, not inside ApplyLook, so every local
// adjustment still sees color: hue-range masks can still select "the red
// jacket", and a mask's temp/tint or prism acts on the color that then
// collapses — the filter-in-front-of-the-film order. Split toning moves here
// too, tinting the gray rather than the color underneath, which is what makes
// sepia a tint of the conversion instead of a wash over the original.

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
)

// bwLumStrength is the gray push at band slider ±1 for a fully saturated
// pixel, as a fraction of full scale. Matches the 0.6 value factor applyHSL
// gives the same slider in color mode, so a band means the same thing in
// both treatments.
const bwLumStrength = 0.6

// ApplyBW collapses a developed color render to hue-weighted monochrome and
// lays the split tone over the result. In place; a no-op unless the edit is
// in BW mode. Callers run it between ApplyMasks and ApplyDetail — the detail
// pass adds its contrast as a luma delta across all three channels, so it
// preserves both neutral grays and the tint.
func ApplyBW(img *image.RGBA, e *edit.Params) {
	if !e.IsBW() {
		return
	}
	// Per-degree gray weights, ×256 for the integer pixel loop. The push is
	// proportional to chroma, so near-neutral pixels stay on their luma and
	// sensor noise can't swing a flat sky — no separate chroma gate needed.
	lumT := hueBandTable(&e.HSLLum)
	var wQ [360]int32
	for h := range lumT {
		wQ[h] = int32(math.Round(bwLumStrength * lumT[h] * 256))
	}

	sR, sG, sB := tintDir(e.SplitShadowHue, e.SplitShadowAmt)
	hR, hG, hB := tintDir(e.SplitHighlightHue, e.SplitHighlightAmt)
	split := e.SplitShadowAmt != 0 || e.SplitHighlightAmt != 0

	pix := img.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		r := int32(pix[i])
		g := int32(pix[i+1])
		b := int32(pix[i+2])
		gray := (299*r + 587*g + 114*b) / 1000

		mx := max(r, g, b)
		c := mx - min(r, g, b)
		if c != 0 {
			// Hue in degrees, integer six-sector form; the sub-degree error
			// is worth less than the float round trip here.
			var h int32
			switch mx {
			case r:
				h = 60 * (g - b) / c
			case g:
				h = 60*(b-r)/c + 120
			default:
				h = 60*(r-g)/c + 240
			}
			if h < 0 {
				h += 360
			}
			gray = clamp8i(gray + wQ[min(h, 359)]*c>>8)
		}

		if split {
			// Clamp before weighting: an out-of-range gray squared into the
			// shadow/highlight weights would tint by the wrong mix.
			ws := (255 - gray) * (255 - gray) >> 8
			wh := gray * gray >> 8
			pix[i] = clamp8(gray + (sR*ws+hR*wh)>>8)
			pix[i+1] = clamp8(gray + (sG*ws+hG*wh)>>8)
			pix[i+2] = clamp8(gray + (sB*ws+hB*wh)>>8)
			continue
		}
		v := uint8(gray)
		pix[i], pix[i+1], pix[i+2] = v, v, v
	}
}

// clamp8i is clamp8 without the narrowing, for values that stay in the
// integer loop before the split tone reads them back.
func clamp8i(v int32) int32 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
