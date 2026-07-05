package pyramid

import (
	"image"
	"math"
)

// FallbackLookGamma is used when a photo has no calibrated gamma yet
// (e.g. an edit preview requested before any base render finished).
const FallbackLookGamma = 0.72

// ComputeLookGamma calibrates the per-photo tone lift: the gamma that maps
// our RAW render's mean luminance onto the camera JPEG's mean luminance.
// Camera engines (Sony DRO etc.) lift adaptively per scene — a backlit shot
// needs far more than a bright one — so this must be measured per photo,
// not hardcoded. Means are 0..255.
func ComputeLookGamma(rawMean, cameraMean float64) float64 {
	if rawMean <= 1 || rawMean >= 254 || cameraMean <= 1 || cameraMean >= 254 {
		return FallbackLookGamma
	}
	g := math.Log(cameraMean/255) / math.Log(rawMean/255)
	return math.Min(1.1, math.Max(0.5, g))
}

// MeanLuma returns the mean Rec.601 luma (0..255), subsampled for speed.
func MeanLuma(img *image.RGBA) float64 {
	pix := img.Pix
	var sum, n uint64
	// Every 4th pixel is plenty for a scene mean.
	for i := 0; i+3 < len(pix); i += 16 {
		sum += (299*uint64(pix[i]) + 587*uint64(pix[i+1]) + 114*uint64(pix[i+2])) / 1000
		n++
	}
	if n == 0 {
		return 0
	}
	return float64(sum) / float64(n)
}

// ApplyLook warms up LibRaw's flat output to sit close to the camera's own
// JPEG rendering: the calibrated gamma lift, a mild S-curve, and a
// saturation boost. Applied only to RAW-decoded renditions — embedded JPEG
// thumbnails already carry the camera curve.
func ApplyLook(img *image.RGBA, gamma float64) {
	lut := buildLookLUT(gamma)
	pix := img.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		r := int32(lut[pix[i]])
		g := int32(lut[pix[i+1]])
		b := int32(lut[pix[i+2]])
		// Saturation ~1.15 around Rec.601 luma, in integer math.
		luma := (299*r + 587*g + 114*b) / 1000
		pix[i] = clamp8(luma + (r-luma)*115/100)
		pix[i+1] = clamp8(luma + (g-luma)*115/100)
		pix[i+2] = clamp8(luma + (b-luma)*115/100)
	}
}

func clamp8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// buildLookLUT combines the calibrated lift (x^gamma) with a mild S-curve
// (s = 0.1) that restores a little of the contrast the lift flattens.
func buildLookLUT(gamma float64) [256]uint8 {
	var lut [256]uint8
	const s = 0.1
	for i := range lut {
		x := float64(i) / 255
		x = math.Pow(x, gamma)
		y := x + s*x*(1-x)*(2*x-1)*2
		v := int(y*255 + 0.5)
		lut[i] = uint8(max(0, min(255, v)))
	}
	return lut
}
