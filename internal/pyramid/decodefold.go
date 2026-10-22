package pyramid

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
)

// Fold is what a window folds into the kept decode it renders from
// (KeptForWindow) to reach the edit it shows: the exposure the decode
// lacks, with its white-balance compensation, and — when the edit's white
// balance is not the decode's — the change of balance, a matrix on linear
// light. Folding into 8-bit pixels lands near the exact decode, not on it,
// as the drafts' fold does; the 1:1 tiles of the edit take its place.
type Fold struct {
	ExpEV float64
	// WB, when set, takes the decode's linear output to the edit's white
	// balance. Nil keeps the decode's own.
	WB *[3][3]float64
}

// ApplyFold folds fold into img, a decode of edits' LibRaw inputs but for
// those fold covers, in place. Without a change of white balance it is
// exactly ApplyExposureEV.
func ApplyFold(img *image.RGBA, fold Fold, edits *edit.Params) {
	if fold.WB == nil {
		ApplyExposureEV(img, fold.ExpEV, edits)
		return
	}
	// The exposure scales linear light too, so it rides in the matrix.
	var k [3][3]float32
	scale := math.Exp2(fold.ExpEV)
	for i := range 3 {
		for j := range 3 {
			k[i][j] = float32(fold.WB[i][j] * scale)
		}
	}
	pwr, ts := outputEncoding(edits)
	dec := dcrawGammaDecoder(pwr, ts)
	enc := dcrawGammaEncoder(pwr, ts)
	var lin [256]float32
	for i := range lin {
		lin[i] = float32(dec(float64(i) / 255))
	}
	// Fine enough that a level of the toe (linear 1/1147 at the default
	// slope) spans many steps.
	const steps = 1 << 14
	var out [steps + 1]uint8
	for i := range out {
		out[i] = uint8(math.Round(255 * enc(float64(i)/steps)))
	}
	encode := func(v float32) uint8 {
		return out[int(min(max(v, 0), 1)*steps+0.5)]
	}
	pixBands(img, func(pix []uint8) {
		for i := 0; i+3 < len(pix); i += 4 {
			r, g, b := lin[pix[i]], lin[pix[i+1]], lin[pix[i+2]]
			pix[i] = encode(k[0][0]*r + k[0][1]*g + k[0][2]*b)
			pix[i+1] = encode(k[1][0]*r + k[1][1]*g + k[1][2]*b)
			pix[i+2] = encode(k[2][0]*r + k[2][1]*g + k[2][2]*b)
		}
	})
}

// wbFoldMatrix is the change of white balance from the multipliers from to
// to, on the decode's linear output: LibRaw balances the camera channels
// before rgbCam takes them to the output, so the change is rgbCam · the
// ratio of the two balances · its inverse. The balances are taken
// relative to green, as the exposure compensation (edit.Params.WBCompEV)
// keeps a change of balance from changing the brightness. A camera with a
// fourth colour, or a matrix that will not invert, scales the output
// channels instead. ok is false when a multiplier is missing.
func wbFoldMatrix(from, to [4]float64, rgbCam [3][4]float64) (m [3][3]float64, ok bool) {
	var d [3]float64
	for c := range 3 {
		if from[c] <= 0 || to[c] <= 0 || from[1] <= 0 || to[1] <= 0 {
			return m, false
		}
		d[c] = (to[c] / to[1]) / (from[c] / from[1])
	}
	var cam [3][3]float64
	four := false
	for i := range 3 {
		four = four || math.Abs(rgbCam[i][3]) > 1e-6
		for j := range 3 {
			cam[i][j] = rgbCam[i][j]
		}
	}
	inv, invertible := Invert3(cam)
	if four || !invertible {
		for c := range 3 {
			m[c][c] = d[c]
		}
		return m, true
	}
	for i := range 3 {
		for j := range 3 {
			for k := range 3 {
				m[i][j] += cam[i][k] * d[k] * inv[k][j]
			}
		}
	}
	return m, true
}

// decodeClipped reports whether the 8-bit decode img has a channel at zero
// in a fifth or more of its lit pixels. Under one narrow colour of light
// (blue stage light, say) LibRaw's colour matrix drives a channel below
// zero and the output floors it there: no change of balance brings it
// back, so a fold of one would show colours the exact decode does not
// (the api's refClipped, for the drafts' reference).
func decodeClipped(img *image.RGBA) bool {
	b := img.Bounds()
	const lit = 48 // clearly not a shadow, in display levels
	var litN, badN int
	for y := b.Min.Y; y < b.Max.Y; y += 8 {
		for x := b.Min.X; x < b.Max.X; x += 8 {
			o := img.PixOffset(x, y)
			r, g, bl := img.Pix[o], img.Pix[o+1], img.Pix[o+2]
			if max(r, g, bl) < lit {
				continue
			}
			litN++
			if min(r, g, bl) == 0 {
				badN++
			}
		}
	}
	return litN > 0 && badN*5 > litN
}
