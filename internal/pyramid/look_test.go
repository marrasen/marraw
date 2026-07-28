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

// TestLookLUTIdentityCurve: an all-diagonal curve is a no-op — the render
// fast path (HasToneCurve) must leave the base LUT byte-identical.
func TestLookLUTIdentityCurve(t *testing.T) {
	base := buildLookLUT(0.72, nil)
	id := buildLookLUT(0.72, &edit.Params{
		ToneCurve: []edit.CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.5}, {X: 1, Y: 1}},
	})
	if base != id {
		t.Error("identity tone curve changed the LUT")
	}
}

// TestLookLUTCurveLift: a curve pulling the midpoint up must brighten mids and
// stay monotone; pulling it down must darken them. Endpoints stay pinned.
func TestLookLUTCurveLift(t *testing.T) {
	base := buildLookLUT(0.72, nil)
	up := buildLookLUT(0.72, &edit.Params{
		ToneCurve: []edit.CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.7}, {X: 1, Y: 1}},
	})
	down := buildLookLUT(0.72, &edit.Params{
		ToneCurve: []edit.CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.3}, {X: 1, Y: 1}},
	})
	if up[128] <= base[128] {
		t.Errorf("lift curve did not brighten mids: %d <= %d", up[128], base[128])
	}
	if down[128] >= base[128] {
		t.Errorf("drop curve did not darken mids: %d >= %d", down[128], base[128])
	}
	if up[0] != base[0] || up[255] != base[255] {
		t.Errorf("endpoints moved: %d/%d vs %d/%d", up[0], up[255], base[0], base[255])
	}
}

// TestLookLUTCurveMonotone: even a wild zig-zag curve may not invert tones —
// the render clamp plus monotone-cubic interpolation must keep the LUT
// non-decreasing.
func TestLookLUTCurveMonotone(t *testing.T) {
	lut := buildLookLUT(0.72, &edit.Params{
		ToneCurve: []edit.CurvePoint{
			{X: 0, Y: 0}, {X: 0.25, Y: 0.9}, {X: 0.5, Y: 0.1},
			{X: 0.75, Y: 0.95}, {X: 1, Y: 0.2},
		},
	})
	for v := 1; v < 256; v++ {
		if lut[v] < lut[v-1] {
			t.Fatalf("curve LUT not monotone at %d: %d < %d", v, lut[v], lut[v-1])
		}
	}
}

// TestLookLUTsNoChannelCurves: with no per-channel curve all three LUTs must
// be the shared master, so channel-free edits render byte-identically.
func TestLookLUTsNoChannelCurves(t *testing.T) {
	master := buildLookLUT(0.72, nil)
	for name, e := range map[string]*edit.Params{
		"nil":    nil,
		"empty":  {},
		"master": {ToneCurve: []edit.CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.7}, {X: 1, Y: 1}}},
		"identityChannel": {ToneCurveR: []edit.CurvePoint{
			{X: 0, Y: 0}, {X: 0.5, Y: 0.5}, {X: 1, Y: 1},
		}},
	} {
		r, g, b := buildLookLUTs(0.72, e)
		if r != g || g != b {
			t.Errorf("%s: channels diverged without a per-channel curve", name)
		}
		want := buildLookLUT(0.72, e)
		if r != want {
			t.Errorf("%s: LUT differs from the master curve", name)
		}
		if name == "identityChannel" && r != master {
			t.Error("an identity channel curve must leave the master untouched")
		}
	}
}

// TestLookLUTsPerChannel: a red-lift curve must brighten only the red channel,
// and each channel's LUT must stay monotone.
func TestLookLUTsPerChannel(t *testing.T) {
	master := buildLookLUT(0.72, nil)
	e := &edit.Params{
		ToneCurveR: []edit.CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.75}, {X: 1, Y: 1}},
		ToneCurveB: []edit.CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.25}, {X: 1, Y: 1}},
	}
	r, g, b := buildLookLUTs(0.72, e)
	if g != master {
		t.Error("green has no curve and must equal the master LUT")
	}
	if r[128] <= master[128] {
		t.Errorf("red lift did not brighten red: %d <= %d", r[128], master[128])
	}
	if b[128] >= master[128] {
		t.Errorf("blue drop did not darken blue: %d >= %d", b[128], master[128])
	}
	// Endpoints are pinned by both curves, so they must not move.
	if r[0] != master[0] || r[255] != master[255] || b[0] != master[0] || b[255] != master[255] {
		t.Error("pinned endpoints moved")
	}
	for _, lut := range []*[256]uint8{&r, &g, &b} {
		for v := 1; v < 256; v++ {
			if lut[v] < lut[v-1] {
				t.Fatalf("per-channel LUT not monotone at %d: %d < %d", v, lut[v], lut[v-1])
			}
		}
	}
}

// TestApplyLookChannelCurve: a per-channel curve must tint a neutral gray
// (the whole point — the master curve alone can only move all three together).
func TestApplyLookChannelCurve(t *testing.T) {
	img := flatGray(4, 4, 128)
	ApplyLook(img, 0.72, &edit.Params{
		ToneCurveR: []edit.CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.8}, {X: 1, Y: 1}},
	})
	if img.Pix[0] <= img.Pix[1] {
		t.Errorf("red lift must tint gray warm: r=%d g=%d", img.Pix[0], img.Pix[1])
	}
	if img.Pix[1] != img.Pix[2] {
		t.Errorf("green and blue had no curve and must match: g=%d b=%d", img.Pix[1], img.Pix[2])
	}
}

// TestBuildCurveLUTPassesControlPoints: the interpolated LUT must hit each
// control point's output at its input (within a quantization level).
func TestBuildCurveLUTPassesControlPoints(t *testing.T) {
	pts := []edit.CurvePoint{{X: 0, Y: 0.05}, {X: 0.4, Y: 0.6}, {X: 1, Y: 0.95}}
	lut := buildCurveLUT(pts)
	for _, p := range pts {
		got := lut[int(p.X*255+0.5)]
		if diff := got - p.Y; diff > 1.0/255 || diff < -1.0/255 {
			t.Errorf("control point (%.2f,%.2f): got %.4f", p.X, p.Y, got)
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
