package pyramid

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
)

// Fold is what a window folds into the kept decode it renders from
// (KeptForWindow) to reach the edit it shows: the exposure the decode
// lacks, with its white-balance compensation, and — when the edit's white
// balance is not the decode's — the change of balance. Folding into 8-bit
// pixels lands near the exact decode, not on it, as the drafts' fold does;
// the 1:1 tiles of the edit take its place.
type Fold struct {
	ExpEV float64
	// WB, when set, takes the decode to the edit's white balance. Nil
	// keeps the decode's own.
	WB *WBFold
}

// WBFold is a change of white balance on a decode's linear output. LibRaw
// balances the camera channels, clips them, and then RGBCam takes them to
// the output: so the fold takes the output back to the camera channels
// (Inv), scales them (D), clips them where the decode of the new balance
// would (Ceil), and takes them out again (RGBCam).
type WBFold struct {
	RGBCam, Inv [3][3]float64
	D           [3]float64
	Ceil        float64
}

// ApplyFold folds fold into img, a decode of edits' LibRaw inputs but for
// those fold covers, in place. Without a change of white balance it is
// exactly ApplyExposureEV.
func ApplyFold(img *image.RGBA, fold Fold, edits *edit.Params) {
	if fold.WB == nil {
		ApplyExposureEV(img, fold.ExpEV, edits)
		return
	}
	wb := fold.WB
	// The exposure scales linear light too, so it rides in the matrix out.
	var inv, out [3][3]float32
	var d [3]float32
	scale := math.Exp2(fold.ExpEV)
	for i := range 3 {
		d[i] = float32(wb.D[i])
		for j := range 3 {
			inv[i][j] = float32(wb.Inv[i][j])
			out[i][j] = float32(wb.RGBCam[i][j] * scale)
		}
	}
	ceil := float32(wb.Ceil)
	// Where a camera channel of the decode sits at its white, give or take
	// the 8-bit rounding.
	const clipped = 0.995
	const blownFrom = 180
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
	var table [steps + 1]uint8
	for i := range table {
		table[i] = uint8(math.Round(255 * enc(float64(i)/steps)))
	}
	encode := func(v float32) uint8 {
		return table[int(min(max(v, 0), 1)*steps+0.5)]
	}
	// A pixel the decode clipped in the output came from a saturated
	// sensor: the output clip took the colour that would have told its
	// camera channels apart, and the exact decode of any balance shows it
	// near white. blown says how near, by how close its darkest channel
	// came to the clip too.
	var blown [256]float32
	for v := blownFrom; v < 256; v++ {
		blown[v] = float32(v-blownFrom) / float32(255-blownFrom)
	}
	// camera rebalances one camera channel v of the decode by d. A channel
	// the decode clipped held anything from its white up, so it stays at
	// the white of the new balance: a blown sky stays white, as the exact
	// decode leaves it.
	camera := func(v, d float32) float32 {
		if v >= clipped {
			return ceil
		}
		return min(d*v, ceil)
	}
	pixBands(img, func(pix []uint8) {
		for i := 0; i+3 < len(pix); i += 4 {
			r, g, b := lin[pix[i]], lin[pix[i+1]], lin[pix[i+2]]
			c0 := camera(inv[0][0]*r+inv[0][1]*g+inv[0][2]*b, d[0])
			c1 := camera(inv[1][0]*r+inv[1][1]*g+inv[1][2]*b, d[1])
			c2 := camera(inv[2][0]*r+inv[2][1]*g+inv[2][2]*b, d[2])
			if t := blown[min(pix[i], pix[i+1], pix[i+2])]; t > 0 && max(pix[i], pix[i+1], pix[i+2]) >= 254 {
				c0 += (ceil - c0) * t
				c1 += (ceil - c1) * t
				c2 += (ceil - c2) * t
			}
			pix[i] = encode(out[0][0]*c0 + out[0][1]*c1 + out[0][2]*c2)
			pix[i+1] = encode(out[1][0]*c0 + out[1][1]*c1 + out[1][2]*c2)
			pix[i+2] = encode(out[2][0]*c0 + out[2][1]*c1 + out[2][2]*c2)
		}
	})
}

// newWBFold is the change of white balance from the multipliers from to
// to, for a decode with highlight mode highlight. The balances are taken
// relative to green, as the exposure compensation (edit.Params.WBCompEV)
// keeps a change of balance from changing the brightness; LibRaw clips the
// channels at its white after dividing the multipliers by their smallest
// (libraw.ScaleNorm), which in those terms sits at Ceil. A camera with a
// fourth colour, or a matrix that will not invert, scales the output
// channels instead. ok is false when a multiplier is missing.
func newWBFold(from, to [4]float64, rgbCam [3][4]float64, highlight int) (f WBFold, ok bool) {
	for c := range 3 {
		if from[c] <= 0 || to[c] <= 0 {
			return f, false
		}
		f.D[c] = (to[c] / to[1]) / (from[c] / from[1])
	}
	f.Ceil = (from[1] / libraw.ScaleNorm(from, highlight)) / (to[1] / libraw.ScaleNorm(to, highlight))
	four := false
	for i := range 3 {
		four = four || math.Abs(rgbCam[i][3]) > 1e-6
		for j := range 3 {
			f.RGBCam[i][j] = rgbCam[i][j]
		}
	}
	inv, invertible := Invert3(f.RGBCam)
	if four || !invertible {
		f.RGBCam = [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
		inv = f.RGBCam
	}
	f.Inv = inv
	return f, true
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
