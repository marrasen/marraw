package pyramid

import (
	"image"
	"math"
)

// outputSharpenPreset tunes the unsharp mask for one target medium: blur
// footprint (r, passes), a soft luma deadzone so flat-area noise is not
// amplified, and the USM gain per amount step.
type outputSharpenPreset struct {
	r, passes int
	threshold int32
	gain      map[string]float64
}

// Presets follow the usual output-sharpening ladder: screen matches the
// creative sharpen's footprint (r=1, 1 pass ≈ 0.8px sigma); print targets go
// wider and stronger to survive ink spread, matte widest and strongest.
var outputSharpenPresets = map[string]outputSharpenPreset{
	"screen": {r: 1, passes: 1, threshold: 2, gain: map[string]float64{"low": 0.30, "standard": 0.55, "high": 0.85}},
	"glossy": {r: 1, passes: 2, threshold: 3, gain: map[string]float64{"low": 0.45, "standard": 0.80, "high": 1.20}},
	"matte":  {r: 2, passes: 2, threshold: 4, gain: map[string]float64{"low": 0.60, "standard": 1.00, "high": 1.50}},
}

// ApplyOutputSharpen applies output sharpening after the final resize: an
// unsharp mask on luma tuned per target medium. target "screen"/"matte"/
// "glossy" selects radius and strength; anything else (including "off" and
// "") is a no-op. amount "low"/"high" scales strength; anything else means
// "standard". Mutates img in place.
func ApplyOutputSharpen(img *image.RGBA, target, amount string) {
	p, ok := outputSharpenPresets[target]
	if !ok {
		return
	}
	gain, ok := p.gain[amount]
	if !ok {
		gain = p.gain["standard"]
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 4 || h < 4 {
		return
	}
	gainQ := int32(math.Round(gain * 256))
	luma := lumaPlane(img)
	blur := boxBlurPlane(luma, w, h, p.r, p.passes)

	pix := img.Pix
	for y := range h {
		row := pix[y*img.Stride : y*img.Stride+w*4]
		for x := range w {
			j := y*w + x
			diff := int32(luma[j]) - int32(blur[j])
			// Soft threshold: shave t off every delta instead of gating, so
			// there is no banding right at the cutoff.
			switch {
			case diff > p.threshold:
				diff -= p.threshold
			case diff < -p.threshold:
				diff += p.threshold
			default:
				continue
			}
			d := gainQ * diff >> 8
			if d == 0 {
				continue
			}
			i := x * 4
			row[i] = clamp8(int32(row[i]) + d)
			row[i+1] = clamp8(int32(row[i+1]) + d)
			row[i+2] = clamp8(int32(row[i+2]) + d)
		}
	}
}
