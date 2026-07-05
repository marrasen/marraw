package libraw

/*
#include <libraw/libraw.h>
*/
import "C"

// KelvinMul returns the white-balance multipliers that neutralize a gray
// surface lit at the given color temperature, using the camera's XYZ matrix
// (falling back to sRGB primaries for files without one). Valid after Open.
func (p *Processor) KelvinMul(kelvin float64) [4]float64 {
	return kelvinMulOf(p.h, kelvin)
}

// kelvinMulOf computes multipliers from the illuminant's XYZ through the
// camera matrix: cam_xyz maps XYZ to camera-space response, so the response
// to the illuminant is cam_xyz·XYZ(T) and the neutralizing multiplier is its
// reciprocal, normalized to G=1.
func kelvinMulOf(h *C.libraw_data_t, kelvin float64) [4]float64 {
	x, y, z := kelvinXYZ(kelvin)

	var cam [4][3]float64
	hasCam := false
	for c := range 4 {
		for j := range 3 {
			cam[c][j] = float64(h.color.cam_xyz[c][j])
			if cam[c][j] != 0 {
				hasCam = true
			}
		}
	}
	if !hasCam {
		// XYZ → linear sRGB as a camera-agnostic approximation.
		cam = [4][3]float64{
			{3.2404542, -1.5371385, -0.4985314},
			{-0.9692660, 1.8760108, 0.0415560},
			{0.0556434, -0.2040259, 1.0572252},
		}
	}

	var mul [4]float64
	for c := range 4 {
		resp := cam[c][0]*x + cam[c][1]*y + cam[c][2]*z
		if resp > 1e-6 {
			mul[c] = 1 / resp
		}
	}
	if mul[1] <= 0 {
		return [4]float64{1, 1, 1, 1}
	}
	g := mul[1]
	for c := range 4 {
		mul[c] /= g
	}
	return mul
}

// kelvinXYZ returns the XYZ (Y=1) of a temperature on the Planckian locus
// below 4000 K and the CIE daylight locus above — the standard photographic
// convention. Approximations: Kim et al. (Planckian) and CIE D-illuminant
// polynomials, valid 1667–25000 K; input is clamped to that range.
func kelvinXYZ(kelvin float64) (x, y, z float64) {
	t := clamp(kelvin, 1667, 25000)
	inv := 1e3 / t
	inv2 := inv * inv
	inv3 := inv2 * inv

	var cx float64
	switch {
	case t < 4000:
		cx = -0.2661239*inv3 - 0.2343589*inv2 + 0.8776956*inv + 0.179910
	case t <= 7000:
		cx = -4.6070*inv3 + 2.9678*inv2 + 0.09911*inv + 0.244063
	default:
		cx = -2.0064*inv3 + 1.9018*inv2 + 0.24748*inv + 0.237040
	}

	var cy float64
	if t < 4000 {
		// Kim et al. Planckian-locus y(x) splines.
		if t < 2222 {
			cy = -1.1063814*cx*cx*cx - 1.34811020*cx*cx + 2.18555832*cx - 0.20219683
		} else {
			cy = -0.9549476*cx*cx*cx - 1.37418593*cx*cx + 2.09137015*cx - 0.16748867
		}
	} else {
		// CIE daylight locus.
		cy = -3.000*cx*cx + 2.870*cx - 0.275
	}

	return cx / cy, 1, (1 - cx - cy) / cy
}
