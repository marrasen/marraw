package pyramid

import (
	"image"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

func solid(r, g, b uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i+3 < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, 0xff
	}
	return img
}

func px(img *image.RGBA) (r, g, b int) {
	return int(img.Pix[0]), int(img.Pix[1]), int(img.Pix[2])
}

func TestHSLNeutralIsUntouched(t *testing.T) {
	// HasHSL gates the pass entirely: a zero mixer must leave ApplyLook's
	// output bit-identical to the pre-mixer pipeline.
	if (&edit.Params{}).HasHSL() {
		t.Fatal("zero mixer reports HasHSL")
	}
	e := &edit.Params{}
	e.HSLSat[5] = -0.5
	if !e.HasHSL() {
		t.Fatal("adjusted mixer does not report HasHSL")
	}
}

func TestHSLSaturationDesaturatesItsBand(t *testing.T) {
	img := solid(200, 40, 40) // red, band 0
	var e edit.Params
	e.HSLSat[0] = -1
	applyHSL(img, &e)
	r, g, b := px(img)
	if r-g > 8 || r-b > 8 {
		t.Errorf("red band sat -1 left color (%d,%d,%d), want near-gray", r, g, b)
	}
}

func TestHSLHueShiftsTowardNeighbor(t *testing.T) {
	img := solid(200, 40, 40) // pure red, hue 0
	var e edit.Params
	e.HSLHue[0] = 1 // +30° → orange: green channel rises
	applyHSL(img, &e)
	r, g, b := px(img)
	if g < 100 || b > 60 || r < 150 {
		t.Errorf("red hue +1 gave (%d,%d,%d), want orange (high R, mid G, low B)", r, g, b)
	}
}

func TestHSLLuminanceDarkens(t *testing.T) {
	img := solid(200, 40, 40)
	var e edit.Params
	e.HSLLum[0] = -1
	applyHSL(img, &e)
	r, _, _ := px(img)
	if r > 120 {
		t.Errorf("red lum -1 left R=%d, want a strong darkening from 200", r)
	}
}

func TestHSLBandIsolation(t *testing.T) {
	// A green pixel (hue 120, exactly the green band center) must not move
	// under red-band edits.
	img := solid(40, 200, 40)
	var e edit.Params
	e.HSLHue[0] = 1
	e.HSLSat[0] = -1
	e.HSLLum[0] = -1
	applyHSL(img, &e)
	r, g, b := px(img)
	if r != 40 || g != 200 || b != 40 {
		t.Errorf("green pixel moved to (%d,%d,%d) under red-band edits", r, g, b)
	}
}

func TestHSLNeutralPixelGated(t *testing.T) {
	img := solid(128, 128, 128)
	var e edit.Params
	for i := range e.HSLHue {
		e.HSLHue[i], e.HSLSat[i], e.HSLLum[i] = 1, 1, 1
	}
	applyHSL(img, &e)
	r, g, b := px(img)
	if r != 128 || g != 128 || b != 128 {
		t.Errorf("gray pixel moved to (%d,%d,%d); the chroma gate failed", r, g, b)
	}
}

func TestHSLBlendsBetweenBands(t *testing.T) {
	// Hue 15° sits halfway between red (0°) and orange (30°): saturating
	// red -1 and orange 0 must desaturate it only halfway.
	img := solid(200, 90, 40) // hue ≈ 18.75° — between red and orange
	full := solid(200, 90, 40)
	var e edit.Params
	e.HSLSat[0] = -1
	applyHSL(img, &e)
	e.HSLSat[1] = -1 // both neighbors now fully desaturate
	applyHSL(full, &e)
	rHalf, gHalf, _ := px(img)
	rFull, gFull, _ := px(full)
	if rFull-gFull > 8 {
		t.Errorf("both-band sat -1 left (%d,%d), want near-gray", rFull, gFull)
	}
	if rHalf-gHalf <= rFull-gFull+8 {
		t.Errorf("half-weighted pixel lost as much chroma (%d) as the fully weighted one (%d)", rHalf-gHalf, rFull-gFull)
	}
}
