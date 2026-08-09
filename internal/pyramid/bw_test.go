package pyramid

import (
	"image"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// solidRGBA builds a flat patch of one color.
func solidRGBA(w, h int, r, g, b uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, 255
	}
	return img
}

// TestBWConvertsToGray: the whole frame comes out neutral through the
// canonical stage order, not just through ApplyBW on its own.
func TestBWConvertsToGray(t *testing.T) {
	img := solidRGBA(4, 4, 200, 80, 40)
	ApplyFinish(img, 0.72, &edit.Params{BW: true}, nil, nil)
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != img.Pix[i+1] || img.Pix[i+1] != img.Pix[i+2] {
			t.Fatalf("pixel %d not gray: %v %v %v", i/4, img.Pix[i], img.Pix[i+1], img.Pix[i+2])
		}
	}
}

// TestBWSurvivesTheFullLookPath: vignette forces applyLookFull, which must
// still hand a collapsible image to ApplyBW.
func TestBWSurvivesTheFullLookPath(t *testing.T) {
	img := solidRGBA(41, 31, 200, 80, 40)
	ApplyFinish(img, 0.72, &edit.Params{BW: true, Vignette: 0.5, Vibrance: 1}, nil, nil)
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != img.Pix[i+1] || img.Pix[i+1] != img.Pix[i+2] {
			t.Fatalf("pixel %d not gray: %v %v %v", i/4, img.Pix[i], img.Pix[i+1], img.Pix[i+2])
		}
	}
}

// TestBWHueWeighting is the point of the feature: pulling one band down must
// darken pixels of that hue while leaving another hue where it was — the
// colored-filter behavior a flat desaturation can't give.
func TestBWHueWeighting(t *testing.T) {
	gray := func(p edit.Params, r, g, b uint8) int {
		img := solidRGBA(2, 2, r, g, b)
		ApplyFinish(img, 0.72, &p, nil, nil)
		return int(img.Pix[0])
	}
	var darkRed edit.Params
	darkRed.BW = true
	darkRed.HSLLum[0] = -1 // red band down

	plainRed := gray(edit.Params{BW: true}, 200, 60, 60)
	plainBlue := gray(edit.Params{BW: true}, 60, 60, 200)
	filtRed := gray(darkRed, 200, 60, 60)
	filtBlue := gray(darkRed, 60, 60, 200)

	if filtRed >= plainRed {
		t.Errorf("red band -1: red patch %d not darker than the neutral mix %d", filtRed, plainRed)
	}
	if filtBlue != plainBlue {
		t.Errorf("red band must not move a blue patch: %d vs %d", filtBlue, plainBlue)
	}
	// And the whole point: two hues that the neutral mix renders close
	// together must separate under the filter.
	var liftRed edit.Params
	liftRed.BW = true
	liftRed.HSLLum[0] = 1
	if gray(liftRed, 200, 60, 60) <= plainRed {
		t.Errorf("red band +1 must brighten the red patch")
	}
}

// TestBWNeutralPixelsIgnoreTheMixer: a gray pixel has no hue to weight, so
// the bands must leave it exactly on its luma (noise must not swing a sky).
func TestBWNeutralPixelsIgnoreTheMixer(t *testing.T) {
	plain := solidRGBA(2, 2, 128, 128, 128)
	mixed := solidRGBA(2, 2, 128, 128, 128)
	var p edit.Params
	p.BW = true
	for i := range p.HSLLum {
		p.HSLLum[i] = 1
	}
	ApplyFinish(plain, 0.72, &edit.Params{BW: true}, nil, nil)
	ApplyFinish(mixed, 0.72, &p, nil, nil)
	if plain.Pix[0] != mixed.Pix[0] {
		t.Errorf("neutral pixel moved with the mixer: %d vs %d", mixed.Pix[0], plain.Pix[0])
	}
}

// TestBWSplitToningTintsTheGray: sepia is B&W plus a warm tint, so split
// toning must survive the collapse rather than being erased by it.
func TestBWSplitToningTintsTheGray(t *testing.T) {
	img := solidRGBA(4, 4, 150, 120, 100)
	ApplyFinish(img, 0.72, &edit.Params{
		BW: true, SplitHighlightHue: 40, SplitHighlightAmt: 1,
		SplitShadowHue: 40, SplitShadowAmt: 1,
	}, nil, nil)
	r, g, b := img.Pix[0], img.Pix[1], img.Pix[2]
	if !(r > g && g > b) {
		t.Errorf("warm tint on gray: want R>G>B, got %d %d %d", r, g, b)
	}
}

// TestBWLeavesLookInColor: the collapse happens after the masks, so the look
// stage must still hand color downstream — that is what keeps hue-range masks
// and per-mask temp/tint meaningful under B&W.
func TestBWLeavesLookInColor(t *testing.T) {
	img := solidRGBA(2, 2, 200, 80, 40)
	ApplyLook(img, 0.72, &edit.Params{BW: true})
	if img.Pix[0] == img.Pix[1] && img.Pix[1] == img.Pix[2] {
		t.Errorf("ApplyLook must not pre-collapse under BW, got %v %v %v", img.Pix[0], img.Pix[1], img.Pix[2])
	}
}

// TestBWIgnoresInertColorControls: saturation, vibrance and the mixer's hue
// and sat bands have no effect on the converted result, so a photo carrying
// old color settings converts the same as a clean one.
func TestBWIgnoresInertColorControls(t *testing.T) {
	base := solidRGBA(8, 8, 190, 90, 60)
	loud := solidRGBA(8, 8, 190, 90, 60)
	var p edit.Params
	p.BW = true
	p.Saturation, p.Vibrance = 1, -1
	for i := range p.HSLHue {
		p.HSLHue[i], p.HSLSat[i] = 1, -1
	}
	ApplyFinish(base, 0.72, &edit.Params{BW: true}, nil, nil)
	ApplyFinish(loud, 0.72, &p, nil, nil)
	if base.Pix[0] != loud.Pix[0] {
		t.Errorf("inert color controls changed the conversion: %d vs %d", loud.Pix[0], base.Pix[0])
	}
}

// TestBWNoOpWhenOff guards the gate: a color edit must be untouched by the
// new stage, byte for byte.
func TestBWNoOpWhenOff(t *testing.T) {
	img := solidRGBA(4, 4, 200, 80, 40)
	before := append([]uint8(nil), img.Pix...)
	ApplyBW(img, &edit.Params{Saturation: -1})
	ApplyBW(img, nil)
	for i := range before {
		if before[i] != img.Pix[i] {
			t.Fatalf("ApplyBW touched a color edit at byte %d", i)
		}
	}
}
