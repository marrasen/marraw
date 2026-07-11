package pyramid

// The HSL color mixer: per-band hue shift, chroma scale, and luminance scale
// over eight fixed hue bands, applied at the end of the look stage. Each
// pixel's hue picks the two neighboring bands with a triangular falloff, so
// adjustments blend smoothly across the wheel; a chroma gate keeps
// near-neutral pixels untouched (a gray sky should not pick up the blue
// band, and noise chroma should not be amplified).

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
)

// HSLBandCenters are the band hues in degrees: red, orange, yellow, green,
// aqua, blue, purple, magenta. Mirrored by MIXER_BANDS in the client's
// EditPanel — the chip order is this order.
var HSLBandCenters = [8]float64{0, 30, 60, 120, 180, 240, 280, 320}

// hslMaxHueShift is the hue rotation at slider ±1, in degrees.
const hslMaxHueShift = 30

// applyHSL applies the mixer in place. Callers gate on edit.HasHSL so a
// neutral mixer costs nothing and existing renders stay bit-identical.
func applyHSL(img *image.RGBA, e *edit.Params) {
	// The per-hue band blend depends only on the hue, so fold the eight
	// bands into per-degree tables once per image.
	var dhT, dsT, dlT [360]float64
	for h := range dhT {
		i0 := 7
		for k := range HSLBandCenters {
			if float64(h) >= HSLBandCenters[k] {
				i0 = k
			}
		}
		i1 := (i0 + 1) % 8
		span := 360 + HSLBandCenters[0] - HSLBandCenters[7] // the wrap segment
		if i1 != 0 {
			span = HSLBandCenters[i1] - HSLBandCenters[i0]
		}
		t := (float64(h) - HSLBandCenters[i0]) / span
		dhT[h] = ((1-t)*e.HSLHue[i0] + t*e.HSLHue[i1]) * hslMaxHueShift
		dsT[h] = (1-t)*e.HSLSat[i0] + t*e.HSLSat[i1]
		dlT[h] = (1-t)*e.HSLLum[i0] + t*e.HSLLum[i1]
	}

	pix := img.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		r := float64(pix[i])
		g := float64(pix[i+1])
		b := float64(pix[i+2])
		mx := math.Max(r, math.Max(g, b))
		mn := math.Min(r, math.Min(g, b))
		c := mx - mn
		if c < 4 {
			continue // effectively neutral: no band membership
		}

		var h float64
		switch mx {
		case r:
			h = 60 * ((g - b) / c)
		case g:
			h = 60 * ((b-r)/c + 2)
		default:
			h = 60 * ((r-g)/c + 4)
		}
		if h < 0 {
			h += 360
		}
		hi := min(int(h), 359)
		dh, ds, dl := dhT[hi], dsT[hi], dlT[hi]
		if dh == 0 && ds == 0 && dl == 0 {
			continue
		}
		// Chroma gate: fades the adjustment in over c 2..24 so near-neutrals
		// stay put instead of flipping hue on sensor noise. Short on purpose —
		// real photos average chroma well under 20, and a longer ramp made the
		// mixer feel weak on exactly the ordinary colors it should grab.
		if gate := (c - 2) / 22; gate < 1 {
			dh *= gate
			ds *= gate
			dl *= gate
		}

		v := mx / 255
		s := c / mx
		h = math.Mod(h+dh+360, 360)
		s = math.Min(1, math.Max(0, s*(1+ds)))
		v = math.Min(1, math.Max(0, v*(1+0.6*dl)))

		// HSV → RGB.
		cc := v * s
		x := cc * (1 - math.Abs(math.Mod(h/60, 2)-1))
		m := v - cc
		var rr, gg, bb float64
		switch {
		case h < 60:
			rr, gg, bb = cc, x, 0
		case h < 120:
			rr, gg, bb = x, cc, 0
		case h < 180:
			rr, gg, bb = 0, cc, x
		case h < 240:
			rr, gg, bb = 0, x, cc
		case h < 300:
			rr, gg, bb = x, 0, cc
		default:
			rr, gg, bb = cc, 0, x
		}
		pix[i] = uint8((rr+m)*255 + 0.5)
		pix[i+1] = uint8((gg+m)*255 + 0.5)
		pix[i+2] = uint8((bb+m)*255 + 0.5)
	}
}
