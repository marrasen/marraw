package pyramid

import (
	"image"

	xdraw "golang.org/x/image/draw"
)

// sharpnessLongEdge normalizes the measurement basis: scores are only
// comparable across photos (and cameras) when every thumb is judged at the
// same scale.
const sharpnessLongEdge = 512

// SharpnessScore measures focus as the variance of the luma Laplacian at a
// fixed 512 px basis — the classic blur detector. Soft or motion-blurred
// frames land well below sharp ones; the grid badge draws under
// SharpnessSoft. The embedded camera JPEG is the intended input (cheap, no
// demosaic, and already carries the camera's own sharpening consistently).
func SharpnessScore(img image.Image) float64 {
	b := img.Bounds()
	if b.Dx() < 3 || b.Dy() < 3 {
		return 0
	}
	w, h := b.Dx(), b.Dy()
	if long := max(w, h); long > sharpnessLongEdge {
		w, h = w*sharpnessLongEdge/long, h*sharpnessLongEdge/long
	}
	scaled := image.NewRGBA(image.Rect(0, 0, max(3, w), max(3, h)))
	xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, b, xdraw.Src, nil)
	w, h = scaled.Rect.Dx(), scaled.Rect.Dy()

	luma := make([]int32, w*h)
	for y := 0; y < h; y++ {
		row := scaled.Pix[y*scaled.Stride:]
		for x := 0; x < w; x++ {
			i := x * 4
			luma[y*w+x] = (299*int32(row[i]) + 587*int32(row[i+1]) + 114*int32(row[i+2])) / 1000
		}
	}

	// 4-neighbor Laplacian; accumulate mean and mean-of-squares in one pass.
	var sum, sumSq float64
	n := 0
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			i := y*w + x
			lap := float64(luma[i-w] + luma[i+w] + luma[i-1] + luma[i+1] - 4*luma[i])
			sum += lap
			sumSq += lap * lap
			n++
		}
	}
	if n == 0 {
		return 0
	}
	mean := sum / float64(n)
	return sumSq/float64(n) - mean*mean
}

// Scores are scene-dependent (a low-texture portrait scores far below an
// action scene at the same focus quality), so there is no absolute "soft"
// constant here: the client badges frames well below their own shoot's
// median — the within-shoot comparison culling actually needs. Real camera
// thumbs measured on a 91-frame A7R II shoot: min 97, median ~3000.
