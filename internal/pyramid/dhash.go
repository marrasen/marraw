package pyramid

import (
	"image"
	"math/bits"

	xdraw "golang.org/x/image/draw"
)

// DHash computes the classic 64-bit difference hash of img: luma on a 9×8
// grid, one bit per horizontally adjacent pair (left brighter than right).
// It survives exposure drift and camera-JPEG re-encodes, which is exactly
// what separates a burst re-frame from a new composition. The embedded
// camera JPEG is the intended input, same as SharpnessScore — never a
// RAW render. The bilinear scaler resamples any decodable thumb onto the
// 9×8 grid, so DHash is total: it hashes every input rather than refusing
// tiny ones, which lets the calibrate pass reach a terminal phash state for
// every photo (an ok=false path left sub-grid thumbs perpetually re-worked).
func DHash(img image.Image) (hash uint64) {
	const gw, gh = 9, 8
	b := img.Bounds()
	scaled := image.NewRGBA(image.Rect(0, 0, gw, gh))
	xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, b, xdraw.Src, nil)

	var luma [gw * gh]int32
	for y := 0; y < gh; y++ {
		row := scaled.Pix[y*scaled.Stride:]
		for x := 0; x < gw; x++ {
			i := x * 4
			luma[y*gw+x] = (299*int32(row[i]) + 587*int32(row[i+1]) + 114*int32(row[i+2])) / 1000
		}
	}
	for y := 0; y < gh; y++ {
		for x := 0; x < gw-1; x++ {
			hash <<= 1
			if luma[y*gw+x] > luma[y*gw+x+1] {
				hash |= 1
			}
		}
	}
	return hash
}

// HammingDist counts differing bits between two DHash values — the
// perceptual distance (0 = identical grids, 64 = inverted).
func HammingDist(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}
