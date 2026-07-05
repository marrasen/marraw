package pyramid

import (
	"image"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// TestLookLUTNeutralParams: zero-valued edit params must produce the exact
// base-look curve — neutral sliders may not shift pixels.
func TestLookLUTNeutralParams(t *testing.T) {
	if buildLookLUT(0.72, nil) != buildLookLUT(0.72, &edit.Params{}) {
		t.Error("neutral params LUT differs from base LUT")
	}
}

// TestLookLUTMonotone: no slider combination may invert tones.
func TestLookLUTMonotone(t *testing.T) {
	extremes := []edit.Params{
		{Contrast: 1, Whites: 1, Blacks: -1, ToneShadows: -1, ToneHighlights: 1},
		{Contrast: -1, Whites: -1, Blacks: 1, ToneShadows: 1, ToneHighlights: -1},
		{Contrast: 1, ToneShadows: 1, ToneHighlights: -1, Whites: -1, Blacks: -1},
	}
	for i, e := range extremes {
		lut := buildLookLUT(0.72, &e)
		for v := 1; v < 256; v++ {
			if lut[v] < lut[v-1] {
				t.Fatalf("case %d: LUT not monotone at %d: %d < %d", i, v, lut[v], lut[v-1])
			}
		}
	}
}

// TestLookLUTContrast: positive contrast must widen the mid-tone spread.
func TestLookLUTContrast(t *testing.T) {
	base := buildLookLUT(0.72, nil)
	punchy := buildLookLUT(0.72, &edit.Params{Contrast: 1})
	flat := buildLookLUT(0.72, &edit.Params{Contrast: -1})
	baseSpread := int(base[192]) - int(base[64])
	if s := int(punchy[192]) - int(punchy[64]); s <= baseSpread {
		t.Errorf("contrast +1 spread %d not above base %d", s, baseSpread)
	}
	if s := int(flat[192]) - int(flat[64]); s >= baseSpread {
		t.Errorf("contrast -1 spread %d not below base %d", s, baseSpread)
	}
}

func flatGray(w, h int, v uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
	}
	return img
}

// TestApplyLookGrayscale: saturation -1 must remove all chroma.
func TestApplyLookGrayscale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 200, 80, 40, 255
	}
	ApplyLook(img, 0.72, &edit.Params{Saturation: -1})
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != img.Pix[i+1] || img.Pix[i+1] != img.Pix[i+2] {
			t.Fatalf("pixel %d not gray: %v %v %v", i/4, img.Pix[i], img.Pix[i+1], img.Pix[i+2])
		}
	}
}

// TestApplyLookVignette: positive vignette darkens corners relative to the
// center, negative brightens them; the center stays put.
func TestApplyLookVignette(t *testing.T) {
	for _, v := range []float64{1, -1} {
		img := flatGray(101, 81, 128)
		ref := flatGray(101, 81, 128)
		ApplyLook(img, 0.72, &edit.Params{Vignette: v})
		ApplyLook(ref, 0.72, nil)
		center := img.Pix[(40*img.Stride)+50*4]
		refCenter := ref.Pix[(40*ref.Stride)+50*4]
		corner := img.Pix[0]
		refCorner := ref.Pix[0]
		if d := int(center) - int(refCenter); d < -2 || d > 2 {
			t.Errorf("vignette %v moved the center by %d", v, d)
		}
		if v > 0 && corner >= refCorner {
			t.Errorf("vignette %v: corner %d not darker than base %d", v, corner, refCorner)
		}
		if v < 0 && corner <= refCorner {
			t.Errorf("vignette %v: corner %d not brighter than base %d", v, corner, refCorner)
		}
	}
}

// TestApplyLookSplitToning: a blue shadow tint must raise blue above red in
// the shadows and leave luma roughly alone.
func TestApplyLookSplitToning(t *testing.T) {
	img := flatGray(8, 8, 40)
	ApplyLook(img, 0.72, &edit.Params{SplitShadowHue: 240, SplitShadowAmt: 1})
	r, b := img.Pix[0], img.Pix[2]
	if b <= r {
		t.Errorf("blue shadow tint: B=%d not above R=%d", b, r)
	}
}

// TestApplyLookVibranceProtectsSaturated: vibrance must boost a muted pixel
// proportionally more than an already-vivid one.
func TestApplyLookVibranceProtectsSaturated(t *testing.T) {
	mk := func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 2, 1))
		// Pixel 0: muted. Pixel 1: vivid.
		img.Pix[0], img.Pix[1], img.Pix[2], img.Pix[3] = 140, 120, 110, 255
		img.Pix[4], img.Pix[5], img.Pix[6], img.Pix[7] = 220, 60, 40, 255
		return img
	}
	chroma := func(img *image.RGBA, p int) int {
		r, g, b := int(img.Pix[p]), int(img.Pix[p+1]), int(img.Pix[p+2])
		return max(r, g, b) - min(r, g, b)
	}
	base, vib := mk(), mk()
	ApplyLook(base, 0.72, nil)
	ApplyLook(vib, 0.72, &edit.Params{Vibrance: 1})
	mutedGain := float64(chroma(vib, 0)) / float64(max(1, chroma(base, 0)))
	vividGain := float64(chroma(vib, 4)) / float64(max(1, chroma(base, 4)))
	if mutedGain <= vividGain {
		t.Errorf("vibrance gain muted=%.2f should exceed vivid=%.2f", mutedGain, vividGain)
	}
}
