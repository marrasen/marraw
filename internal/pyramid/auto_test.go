package pyramid

import (
	"image"
	"math"
	"reflect"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

const testGamma = 0.72

// invLUT finds the input byte whose baseline-look output is closest to the
// wanted display value, so tests can author histograms in display space —
// the domain the auto targets are defined in.
func invLUT(lut *[256]uint8, want int) uint8 {
	best, bestD := 0, 1<<30
	for i := range 256 {
		d := int(lut[i]) - want
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	return uint8(best)
}

type block struct {
	display int // wanted display-space gray value
	count   int // number of sampled pixels
}

// grayImage builds an image whose subsampled (every 4th pixel) luma
// histogram contains exactly the given display-value counts: each sample is
// written as a run of 4 identical pixels, so the stats loop picks one per run.
func grayImage(t *testing.T, blocks []block) *image.RGBA {
	t.Helper()
	lut := buildLookLUT(testGamma, nil)
	n := 0
	for _, b := range blocks {
		n += b.count
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, n))
	i := 0
	for _, b := range blocks {
		v := invLUT(&lut, b.display)
		for range b.count * 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
			i += 4
		}
	}
	return img
}

// colorImage builds an image of one repeated color, authored per channel in
// display space.
func colorImage(t *testing.T, r, g, b, samples int) *image.RGBA {
	t.Helper()
	lut := buildLookLUT(testGamma, nil)
	rv, gv, bv := invLUT(&lut, r), invLUT(&lut, g), invLUT(&lut, b)
	img := image.NewRGBA(image.Rect(0, 0, 4, samples))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = rv, gv, bv, 255
	}
	return img
}

// wellExposed authors a histogram sitting exactly on the auto-tone targets:
// median 107, endpoints at the 0.02/0.97 percentile targets, interquartile
// spread 0.34. Auto tone on it must be a near-no-op.
func wellExposed(t *testing.T) *image.RGBA {
	return grayImage(t, []block{
		{5, 6},     // 0.5th percentile ≈ 0.02
		{86, 250},  // q25
		{107, 246}, // median on target
		{173, 250}, // q75 → spread (173-86)/255 ≈ 0.34
		{200, 243},
		{247, 5}, // 99.5th percentile ≈ 0.97
	})
}

func TestAutoToneDarkLiftsExposure(t *testing.T) {
	img := grayImage(t, []block{{20, 300}, {40, 400}, {60, 300}})
	var p edit.Params
	AutoAdjust(img, testGamma, &p, []AutoSection{AutoTone}, nil)
	if p.ExpEV <= 0.5 {
		t.Errorf("dark scene: ExpEV = %v, want a clear positive lift", p.ExpEV)
	}
}

func TestAutoToneBrightPullsExposure(t *testing.T) {
	img := grayImage(t, []block{{180, 300}, {200, 400}, {220, 300}})
	var p edit.Params
	AutoAdjust(img, testGamma, &p, []AutoSection{AutoTone}, nil)
	if p.ExpEV >= -0.5 {
		t.Errorf("bright scene: ExpEV = %v, want a clear negative pull", p.ExpEV)
	}
}

func TestAutoToneFlatScene(t *testing.T) {
	// Low-contrast midtones: endpoints far from black/white, narrow spread.
	img := grayImage(t, []block{{90, 300}, {107, 400}, {130, 300}})
	var p edit.Params
	AutoAdjust(img, testGamma, &p, []AutoSection{AutoTone}, nil)
	if p.Blacks >= 0 {
		t.Errorf("flat scene: Blacks = %v, want negative (deepen)", p.Blacks)
	}
	if p.Whites <= 0 {
		t.Errorf("flat scene: Whites = %v, want positive (extend)", p.Whites)
	}
	if p.Contrast <= 0 {
		t.Errorf("flat scene: Contrast = %v, want positive", p.Contrast)
	}
}

func TestAutoToneWellExposedIsNoOp(t *testing.T) {
	img := wellExposed(t)
	var p edit.Params
	AutoAdjust(img, testGamma, &p, []AutoSection{AutoTone}, nil)
	for name, v := range map[string]float64{
		"ExpEV": p.ExpEV, "Contrast": p.Contrast, "Whites": p.Whites,
		"Blacks": p.Blacks, "ToneShadows": p.ToneShadows, "ToneHighlights": p.ToneHighlights,
	} {
		if math.Abs(v) > 0.05 {
			t.Errorf("well-exposed scene: %s = %v, want ~0", name, v)
		}
	}

	// Idempotent: the exposure is already on target, so a second pass must
	// not drift the result.
	q := p
	AutoAdjust(img, testGamma, &q, []AutoSection{AutoTone}, nil)
	if math.Abs(q.ExpEV-p.ExpEV) > 0.05 || q.Contrast != p.Contrast || q.Blacks != p.Blacks {
		t.Errorf("second pass drifted: %+v -> %+v", p, q)
	}
}

func TestAutoToneDegenerateScenes(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  *image.RGBA
	}{
		{"black", grayImage(t, []block{{0, 1000}})},
		{"empty", image.NewRGBA(image.Rect(0, 0, 0, 0))},
	} {
		p := edit.Params{ExpEV: 0.7}
		AutoAdjust(tc.img, testGamma, &p, []AutoSection{AutoTone}, nil)
		if p.ExpEV != 0.7 || p.Contrast != 0 || p.Whites != 0 || p.Blacks != 0 {
			t.Errorf("%s scene not neutral: %+v", tc.name, p)
		}
	}
}

func TestAutoToneFlatGrayCardStillCorrectsExposure(t *testing.T) {
	img := grayImage(t, []block{{40, 1000}}) // single-tone, but dark
	var p edit.Params
	AutoAdjust(img, testGamma, &p, []AutoSection{AutoTone}, nil)
	if p.ExpEV <= 0.5 {
		t.Errorf("gray card: ExpEV = %v, want positive", p.ExpEV)
	}
	if p.Contrast != 0 || p.Whites != 0 || p.Blacks != 0 {
		t.Errorf("gray card: histogram stretch on a single tone: %+v", p)
	}
}

func TestAutoColorMutedGetsVibrance(t *testing.T) {
	img := colorImage(t, 120, 110, 100, 500) // chroma ≈ 23 after base boost
	var p edit.Params
	AutoAdjust(img, testGamma, &p, []AutoSection{AutoColor}, nil)
	if p.Vibrance <= 0 {
		t.Errorf("muted scene: Vibrance = %v, want positive", p.Vibrance)
	}
	if p.Saturation != 0 {
		t.Errorf("muted scene: Saturation = %v, want 0", p.Saturation)
	}
}

func TestAutoColorGarishGetsReinedIn(t *testing.T) {
	img := colorImage(t, 230, 40, 10, 500) // chroma ≈ 253 after base boost
	var p edit.Params
	AutoAdjust(img, testGamma, &p, []AutoSection{AutoColor}, nil)
	if p.Vibrance >= 0 {
		t.Errorf("garish scene: Vibrance = %v, want negative", p.Vibrance)
	}
	if p.Saturation >= 0 {
		t.Errorf("garish scene: Saturation = %v, want negative", p.Saturation)
	}
}

func TestAutoColorGrayscaleStaysNeutral(t *testing.T) {
	img := wellExposed(t)
	p := edit.Params{Vibrance: 0.3, Saturation: 0.2}
	AutoAdjust(img, testGamma, &p, []AutoSection{AutoColor}, nil)
	if p.Vibrance != 0 || p.Saturation != 0 {
		t.Errorf("grayscale scene: vibrance/saturation %v/%v, want 0/0", p.Vibrance, p.Saturation)
	}
}

// TestAutoSectionIsolation: each section replaces exactly its own fields and
// leaves every other parameter bit-identical.
func TestAutoSectionIsolation(t *testing.T) {
	img := colorImage(t, 140, 100, 80, 500)
	seed := edit.Params{
		ExpEV: 0.4, Contrast: 0.9, Whites: -0.9, Blacks: 0.9, ToneShadows: 0.9, ToneHighlights: -0.9,
		Vibrance: 0.9, Saturation: -0.9,
		WBMode: edit.WBKelvin, WBKelvin: 5600, WBTint: 0.2,
		CropX: 0.1, CropY: 0.1, CropW: 0.5, CropH: 0.5, CropAngle: 3,
		Texture: 0.5, Clarity: 0.5, Dehaze: 0.5, Sharpen: 0.5,
		SplitShadowHue: 240, SplitShadowAmt: 0.5, Vignette: 0.3,
	}

	tone := seed
	AutoAdjust(img, testGamma, &tone, []AutoSection{AutoTone}, nil)
	if tone.Vibrance != seed.Vibrance || tone.Saturation != seed.Saturation {
		t.Errorf("tone auto touched color fields: %+v", tone)
	}
	check := tone
	check.ExpEV, check.Contrast, check.Whites, check.Blacks, check.ToneShadows, check.ToneHighlights =
		seed.ExpEV, seed.Contrast, seed.Whites, seed.Blacks, seed.ToneShadows, seed.ToneHighlights
	if !reflect.DeepEqual(check, seed) {
		t.Errorf("tone auto touched fields outside its section:\n got %+v\nwant %+v", check, seed)
	}

	color := seed
	AutoAdjust(img, testGamma, &color, []AutoSection{AutoColor}, nil)
	check = color
	check.Vibrance, check.Saturation = seed.Vibrance, seed.Saturation
	if !reflect.DeepEqual(check, seed) {
		t.Errorf("color auto touched fields outside its section:\n got %+v\nwant %+v", check, seed)
	}
}

// TestAutoWithinValidatorRanges: extreme scenes may never push a value past
// the edit.Params validator limits.
func TestAutoWithinValidatorRanges(t *testing.T) {
	scenes := []*image.RGBA{
		grayImage(t, []block{{1, 990}, {255, 10}}),
		grayImage(t, []block{{255, 990}, {1, 10}}),
		colorImage(t, 255, 0, 0, 500),
		wellExposed(t),
	}
	for i, img := range scenes {
		for _, ev := range []float64{-2, 0, 3} {
			p := edit.Params{ExpEV: ev}
			AutoAdjust(img, testGamma, &p, []AutoSection{AutoTone, AutoColor}, nil)
			if p.ExpEV < -2 || p.ExpEV > 3 {
				t.Errorf("scene %d: ExpEV %v out of range", i, p.ExpEV)
			}
			for name, v := range map[string]float64{
				"Contrast": p.Contrast, "Whites": p.Whites, "Blacks": p.Blacks,
				"ToneShadows": p.ToneShadows, "ToneHighlights": p.ToneHighlights,
				"Vibrance": p.Vibrance, "Saturation": p.Saturation,
			} {
				if v < -1 || v > 1 {
					t.Errorf("scene %d: %s = %v out of range", i, name, v)
				}
			}
		}
	}
}

// TestAutoToneSubjectAwareMetering: a dark subject on a bright field lifts
// exposure further when the subject matte weights the metering — and a
// sliver of a matte (sub-3% coverage) changes nothing.
func TestAutoToneSubjectAwareMetering(t *testing.T) {
	lut := buildLookLUT(testGamma, nil)
	dark, bright := invLUT(&lut, 35), invLUT(&lut, 150)
	const w, h = 200, 100
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := bright
			if x < w/4 {
				v = dark // backlit subject: left quarter
			}
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
		}
	}
	matte := &AIMap{Pix: make([]uint8, 100*50), W: 100, H: 50}
	for y := 0; y < 50; y++ {
		for x := 0; x < 25; x++ {
			matte.Pix[y*100+x] = 255
		}
	}

	var global, subj edit.Params
	AutoAdjust(img, testGamma, &global, []AutoSection{AutoTone}, nil)
	AutoAdjust(img, testGamma, &subj, []AutoSection{AutoTone}, matte)
	if subj.ExpEV <= global.ExpEV+0.2 {
		t.Errorf("subject metering ExpEV = %v vs global %v, want clearly higher", subj.ExpEV, global.ExpEV)
	}

	sliver := &AIMap{Pix: make([]uint8, 100*50), W: 100, H: 50}
	for x := 0; x < 2; x++ { // ~2% of the frame
		sliver.Pix[x] = 255
	}
	var tiny edit.Params
	AutoAdjust(img, testGamma, &tiny, []AutoSection{AutoTone}, sliver)
	if tiny.ExpEV != global.ExpEV {
		t.Errorf("sliver matte moved ExpEV: %v vs %v", tiny.ExpEV, global.ExpEV)
	}
}
