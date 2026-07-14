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
// RAW render. ok=false means the input is too small to hash meaningfully.
func DHash(img image.Image) (hash uint64, ok bool) {
	const gw, gh = 9, 8
	b := img.Bounds()
	if b.Dx() < gw || b.Dy() < gh {
		return 0, false
	}
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
	return hash, true
}

// HammingDist counts differing bits between two DHash values — the
// perceptual distance (0 = identical grids, 64 = inverted).
func HammingDist(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}
