package pyramid

import (
	"image"
	"reflect"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

func suggestIDs(cands []Candidate) []string {
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	return ids
}

func hasID(cands []Candidate, id string) bool {
	for _, c := range cands {
		if c.ID == id {
			return true
		}
	}
	return false
}

// suggestOn measures img with GatherSceneStats and runs SuggestLooks — the
// same path the RPC takes.
func suggestOn(t *testing.T, img *image.RGBA, profile SceneProfile, base edit.Params) []Candidate {
	t.Helper()
	return SuggestLooks(GatherSceneStats(img, testGamma, nil), profile, base)
}

func TestSuggestAlwaysOnTrio(t *testing.T) {
	// A colorful, well-exposed scene with no profile: no scene gate fires
	// (chroma well above the mono thresholds, median on target).
	img := colorImage(t, 140, 100, 80, 500)
	cands := suggestOn(t, img, SceneProfile{}, edit.Params{})
	if len(cands) < 3 || len(cands) > 5 {
		t.Fatalf("candidate count = %d, want 3..5 (%v)", len(cands), suggestIDs(cands))
	}
	for i, want := range []string{"balanced", "punchy", "airy"} {
		if cands[i].ID != want {
			t.Errorf("candidate %d = %q, want %q (%v)", i, cands[i].ID, want, suggestIDs(cands))
		}
	}
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c.ID] {
			t.Errorf("duplicate candidate id %q", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestSuggestBalancedIsAutoBase(t *testing.T) {
	img := colorImage(t, 140, 100, 80, 500)
	base := edit.Params{ExpEV: 0.4, CropW: 0.5, CropH: 0.5}
	want := base
	AutoAdjust(img, testGamma, &want, []AutoSection{AutoTone, AutoColor}, nil)
	cands := suggestOn(t, img, SceneProfile{}, base)
	if !reflect.DeepEqual(cands[0].Params, want) {
		t.Errorf("balanced != auto tone+colour:\n got %+v\nwant %+v", cands[0].Params, want)
	}
}

func TestSuggestSkyGate(t *testing.T) {
	img := colorImage(t, 140, 100, 80, 500)
	if cands := suggestOn(t, img, SceneProfile{HasClassMap: true, Sky: 0.3}, edit.Params{}); !hasID(cands, "sky") {
		t.Errorf("sky 30%%: no sky candidate (%v)", suggestIDs(cands))
	}
	if cands := suggestOn(t, img, SceneProfile{HasClassMap: true, Sky: 0.2}, edit.Params{}); hasID(cands, "sky") {
		t.Errorf("sky 20%%: sky candidate below gate (%v)", suggestIDs(cands))
	}
}

func TestSuggestPortraitGate(t *testing.T) {
	img := colorImage(t, 140, 100, 80, 500)
	if cands := suggestOn(t, img, SceneProfile{HasClassMap: true, People: 0.1}, edit.Params{}); !hasID(cands, "portrait") {
		t.Errorf("people 10%%: no portrait candidate (%v)", suggestIDs(cands))
	}
	if cands := suggestOn(t, img, SceneProfile{HasClassMap: true}, edit.Params{}); hasID(cands, "portrait") {
		t.Errorf("no people, no matte: portrait candidate (%v)", suggestIDs(cands))
	}

	// A usable subject matte (3–70% coverage) gates portrait without any
	// class map — the matte path works fully degraded.
	matte := &AIMap{Pix: make([]uint8, 100*50), W: 100, H: 50}
	for i := range 100 * 10 {
		matte.Pix[i] = 255
	}
	stats := GatherSceneStats(img, testGamma, matte)
	if cands := SuggestLooks(stats, SceneProfile{}, edit.Params{}); !hasID(cands, "portrait") {
		t.Errorf("usable matte: no portrait candidate (%v)", suggestIDs(cands))
	}
}

func TestSuggestVividGate(t *testing.T) {
	img := colorImage(t, 140, 100, 80, 500)
	nature := SceneProfile{HasClassMap: true, Foliage: 0.3, Water: 0.1, Mountains: 0.1}
	if cands := suggestOn(t, img, nature, edit.Params{}); !hasID(cands, "vivid") {
		t.Errorf("nature scene: no vivid candidate (%v)", suggestIDs(cands))
	}
	withPeople := nature
	withPeople.People = 0.1
	if cands := suggestOn(t, img, withPeople, edit.Params{}); hasID(cands, "vivid") {
		t.Errorf("people present: vivid candidate (%v)", suggestIDs(cands))
	}
}

func TestSuggestMonoOnGrayScene(t *testing.T) {
	// wellExposed is pure gray: chroma mean ~0 → the B&W recipe fires even
	// with no class map (histogram-only gate).
	cands := suggestOn(t, wellExposed(t), SceneProfile{}, edit.Params{})
	if !hasID(cands, "mono") {
		t.Errorf("gray scene: no mono candidate (%v)", suggestIDs(cands))
	}
	for _, c := range cands {
		if c.ID == "mono" && (c.Params.Saturation != -1 || c.Params.Vibrance != -1) {
			t.Errorf("mono candidate not desaturated: sat %v vib %v", c.Params.Saturation, c.Params.Vibrance)
		}
	}
}

func TestSuggestLowkeyOnDarkScene(t *testing.T) {
	dark := grayImage(t, []block{{15, 300}, {30, 400}, {50, 300}})
	cands := suggestOn(t, dark, SceneProfile{}, edit.Params{})
	if !hasID(cands, "lowkey") {
		t.Errorf("dark scene: no lowkey candidate (%v)", suggestIDs(cands))
	}
	bright := grayImage(t, []block{{180, 300}, {200, 400}, {250, 300}})
	if cands := suggestOn(t, bright, SceneProfile{}, edit.Params{}); hasID(cands, "lowkey") {
		t.Errorf("bright scene: lowkey candidate (%v)", suggestIDs(cands))
	}
}

func TestSuggestDegradedStaysUseful(t *testing.T) {
	// No class map, no matte: category recipes must never fire, and the
	// gallery still holds at least the three always-on looks.
	for _, img := range []*image.RGBA{
		colorImage(t, 140, 100, 80, 500),
		wellExposed(t),
		grayImage(t, []block{{15, 300}, {30, 400}, {50, 300}}),
	} {
		cands := suggestOn(t, img, SceneProfile{}, edit.Params{})
		if len(cands) < 3 {
			t.Errorf("degraded: only %d candidates (%v)", len(cands), suggestIDs(cands))
		}
		for _, id := range []string{"sky", "portrait", "vivid"} {
			if hasID(cands, id) {
				t.Errorf("degraded: category recipe %q fired (%v)", id, suggestIDs(cands))
			}
		}
	}
}

func TestSuggestGatedCap(t *testing.T) {
	// Every category gate open at once: the gallery still caps at 5.
	img := wellExposed(t) // mono fires too — four gated recipes compete
	profile := SceneProfile{HasClassMap: true, Sky: 0.5, People: 0.2, Foliage: 0.5}
	cands := suggestOn(t, img, profile, edit.Params{})
	if len(cands) > 5 {
		t.Errorf("candidate count = %d, want <= 5 (%v)", len(cands), suggestIDs(cands))
	}
	// Sky (0.5) and portrait (0.6, capped people score) outrank mono (0.8*1
	// = 0.8 on this gray scene)... rank by score, largest first: assert the
	// two best-scoring gates won the slots.
	if !hasID(cands, "mono") || !hasID(cands, "portrait") {
		t.Errorf("expected the two best-scoring gated recipes (%v)", suggestIDs(cands))
	}
}

// TestSuggestWithinValidatorRanges mirrors TestAutoWithinValidatorRanges:
// no scene/profile combination may push a candidate past the edit.Params
// validator limits.
func TestSuggestWithinValidatorRanges(t *testing.T) {
	scenes := []*image.RGBA{
		grayImage(t, []block{{1, 990}, {255, 10}}),
		grayImage(t, []block{{255, 990}, {1, 10}}),
		colorImage(t, 255, 0, 0, 500),
		wellExposed(t),
		grayImage(t, []block{{10, 1000}}),
	}
	profiles := []SceneProfile{
		{},
		{HasClassMap: true, Sky: 0.9, People: 0.5, Foliage: 0.9},
		{HasClassMap: true, Foliage: 0.5, Water: 0.3, Mountains: 0.2},
	}
	for i, img := range scenes {
		for _, profile := range profiles {
			for _, ev := range []float64{edit.MinExpEV, 0, edit.MaxExpEV} {
				for _, c := range suggestOn(t, img, profile, edit.Params{ExpEV: ev}) {
					p := c.Params
					if p.ExpEV < edit.MinExpEV || p.ExpEV > edit.MaxExpEV {
						t.Errorf("scene %d %s: ExpEV %v out of range", i, c.ID, p.ExpEV)
					}
					for name, v := range map[string]float64{
						"Contrast": p.Contrast, "Whites": p.Whites, "Blacks": p.Blacks,
						"ToneShadows": p.ToneShadows, "ToneHighlights": p.ToneHighlights,
						"Vibrance": p.Vibrance, "Saturation": p.Saturation,
						"Clarity": p.Clarity, "Dehaze": p.Dehaze, "Vignette": p.Vignette,
					} {
						if v < -1 || v > 1 {
							t.Errorf("scene %d %s: %s = %v out of range", i, c.ID, name, v)
						}
					}
					for name, v := range map[string]float64{
						"SplitShadowAmt": p.SplitShadowAmt, "SplitHighlightAmt": p.SplitHighlightAmt,
					} {
						if v < 0 || v > 1 {
							t.Errorf("scene %d %s: %s = %v out of range", i, c.ID, name, v)
						}
					}
					for name, v := range map[string]float64{
						"SplitShadowHue": p.SplitShadowHue, "SplitHighlightHue": p.SplitHighlightHue,
					} {
						if v < 0 || v >= 360 {
							t.Errorf("scene %d %s: %s = %v out of range", i, c.ID, name, v)
						}
					}
				}
			}
		}
	}
}

// TestSuggestPreservesBase: geometry, white balance, masks, spots and the
// decode-detail fields pass through byte-identical on every candidate.
func TestSuggestPreservesBase(t *testing.T) {
	base := edit.Params{
		WBMode: edit.WBKelvin, WBKelvin: 5600, WBTint: 0.2, WBMul: [4]float64{2, 1, 1.5, 0},
		Rotate: 1, FlipH: true,
		CropX: 0.1, CropY: 0.1, CropW: 0.5, CropH: 0.5, CropAngle: 3,
		Sharpen: 0.5, NRThreshold: 100, MedPasses: 1,
		Masks: []edit.Mask{{Type: edit.MaskLinear, X0: 0.1, Y0: 0.1, X1: 0.9, Y1: 0.9}},
		Spots: []edit.Spot{{Mode: edit.SpotHeal, CX: 0.5, CY: 0.5, Radius: 0.02}},
	}
	img := colorImage(t, 140, 100, 80, 500)
	profile := SceneProfile{HasClassMap: true, Sky: 0.5, People: 0.2}
	for _, c := range suggestOn(t, img, profile, base) {
		got := c.Params
		// Neutralize the fields recipes may write; everything left must be
		// bit-identical to base.
		got.ExpEV = base.ExpEV
		got.Contrast, got.Whites, got.Blacks, got.ToneShadows, got.ToneHighlights = base.Contrast, base.Whites, base.Blacks, base.ToneShadows, base.ToneHighlights
		got.Vibrance, got.Saturation = base.Vibrance, base.Saturation
		got.Clarity, got.Dehaze = base.Clarity, base.Dehaze
		got.SplitShadowHue, got.SplitShadowAmt = base.SplitShadowHue, base.SplitShadowAmt
		got.SplitHighlightHue, got.SplitHighlightAmt = base.SplitHighlightHue, base.SplitHighlightAmt
		got.Vignette = base.Vignette
		if !reflect.DeepEqual(got, base) {
			t.Errorf("%s touched fields outside the look:\n got %+v\nwant %+v", c.ID, got, base)
		}
	}
}
