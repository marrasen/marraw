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
	luma, w, h := lumaAtBasis(img)
	if luma == nil {
		return 0
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

// SubjectSharpnessScore is SharpnessScore restricted to the AI subject matte:
// the Laplacian variance weighted by matte coverage, so a tack-sharp
// background cannot hide a soft subject. img is the embedded thumb in its
// stored (sensor) frame; matte is the stored subject map, which lives in the
// base display frame, so flip — the photo's LibRaw orientation code — is
// undone on the matte to bring the two into the same frame. ok=false means
// the score is meaningless (degenerate input, a frame the matte barely
// covers, or a matte whose aspect cannot be reconciled with the thumb) and
// the caller should fall back to the global score.
func SubjectSharpnessScore(img image.Image, matte *AIMap, flip int) (float64, bool) {
	luma, w, h := lumaAtBasis(img)
	if luma == nil || matte == nil || matte.W < 1 || matte.H < 1 {
		return 0, false
	}
	m := matte
	// Undo the display transform (rotateFlip's codes): 3 applied 180°,
	// 5 applied 90° CCW, 6 applied 90° CW; orientMap turns CW.
	switch flip {
	case 3:
		m = orientMap(m, 2, false)
	case 5:
		m = orientMap(m, 1, false)
	case 6:
		m = orientMap(m, 3, false)
	}
	// A camera that pre-rotates its embedded thumb would leave the frames
	// transposed even after the undo — better no score than a mis-placed one.
	if w != h && m.W != m.H && (w > h) != (m.W > m.H) {
		return 0, false
	}

	var sumW, sumWL, sumWL2 float64
	for y := 1; y < h-1; y++ {
		my := y * m.H / h
		mrow := m.Pix[my*m.W:]
		for x := 1; x < w-1; x++ {
			wt := float64(mrow[x*m.W/w]) / 255
			if wt == 0 {
				continue
			}
			i := y*w + x
			lap := float64(luma[i-w] + luma[i+w] + luma[i-1] + luma[i+1] - 4*luma[i])
			sumW += wt
			sumWL += wt * lap
			sumWL2 += wt * lap * lap
		}
	}
	// Require the subject to cover a meaningful part of the frame: ISNet
	// min-max normalizes its output, so a subjectless frame still yields
	// stray bright pixels that would otherwise score on noise.
	interior := float64((w - 2) * (h - 2))
	if interior <= 0 || sumW < 0.02*interior {
		return 0, false
	}
	mean := sumWL / sumW
	return sumWL2/sumW - mean*mean, true
}

// lumaAtBasis scales img to the fixed measurement basis and returns its luma
// plane; nil when the input is too small to measure.
func lumaAtBasis(img image.Image) (luma []int32, w, h int) {
	b := img.Bounds()
	if b.Dx() < 3 || b.Dy() < 3 {
		return nil, 0, 0
	}
	w, h = b.Dx(), b.Dy()
	if long := max(w, h); long > sharpnessLongEdge {
		w, h = w*sharpnessLongEdge/long, h*sharpnessLongEdge/long
	}
	scaled := image.NewRGBA(image.Rect(0, 0, max(3, w), max(3, h)))
	xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, b, xdraw.Src, nil)
	w, h = scaled.Rect.Dx(), scaled.Rect.Dy()

	luma = make([]int32, w*h)
	for y := 0; y < h; y++ {
		row := scaled.Pix[y*scaled.Stride:]
		for x := 0; x < w; x++ {
			i := x * 4
			luma[y*w+x] = (299*int32(row[i]) + 587*int32(row[i+1]) + 114*int32(row[i+2])) / 1000
		}
	}
	return luma, w, h
}

// Scores are scene-dependent (a low-texture portrait scores far below an
// action scene at the same focus quality), so there is no absolute "soft"
// constant here: the client badges frames well below their own shoot's
// median — the within-shoot comparison culling actually needs. Real camera
// thumbs measured on a 91-frame A7R II shoot: min 97, median ~3000.
