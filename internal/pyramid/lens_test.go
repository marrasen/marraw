package pyramid

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/lens"
)

// fixtureCorrection is the profile of the dev fixture's body — a fixed-lens
// compact with a strong barrel at the wide end, which makes it a good stress
// case for the resample.
func fixtureCorrection(t *testing.T) *lens.Correction {
	t.Helper()
	c := lens.Resolve("Panasonic", "DC-LX100M2", "", 10.9, 1.7)
	if c == nil {
		t.Fatal("lens.Resolve = nil; the embedded database should know this body")
	}
	return c
}

// gridImage draws a white grid on black. Straight lines are what distortion
// bends, so they are also what shows the correction moved anything.
func gridImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(20)
			if x%32 == 0 || y%32 == 0 {
				v = 230
			}
			o := img.PixOffset(x, y)
			img.Pix[o+0], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = v, v, v, 255
		}
	}
	return img
}

func flatImage(w, h int, v uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+3 < len(img.Pix); i += 4 {
		img.Pix[i+0], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
	}
	return img
}

func TestApplyLensWithoutAWarpIsUntouched(t *testing.T) {
	src := gridImage(160, 120)
	if got := ApplyLens(src, nil, nil); got != src {
		t.Error("ApplyLens with a nil warp should return the source unchanged")
	}
}

// TestApplyLensKeepsTheFrameSize is what lets the correction sit in front of
// the geometry stage without disturbing anything downstream: the crop
// rectangle, masks and spots are all fractions of the frame, so the frame
// must stay the same shape.
func TestApplyLensKeepsTheFrameSize(t *testing.T) {
	const w, h = 320, 240
	c := fixtureCorrection(t)
	src := gridImage(w, h)
	out := ApplyLens(src, LensWarp(c, &edit.Params{}, w, h), &edit.Params{})
	if b := out.Bounds(); b.Dx() != w || b.Dy() != h {
		t.Errorf("output is %dx%d, want %dx%d", b.Dx(), b.Dy(), w, h)
	}
}

// TestApplyLensLeavesNoBlackBorder is the visible symptom of a broken
// auto-scale: a correction that samples past the frame draws dark wedges
// along the edges. The source is uniformly bright, so any dark output pixel
// came from outside.
func TestApplyLensLeavesNoBlackBorder(t *testing.T) {
	const w, h = 320, 240
	c := fixtureCorrection(t)
	e := &edit.Params{}
	// Vignetting off: its whole job is to brighten the corners, which would
	// mask exactly the darkening this test looks for.
	e.LensVignetting = -1
	out := ApplyLens(flatImage(w, h, 200), LensWarp(c, e, w, h), e)
	for y := range h {
		for x := range w {
			if x > 0 && x < w-1 && y > 0 && y < h-1 {
				continue // border only
			}
			if v := out.Pix[out.PixOffset(x, y)]; v < 150 {
				t.Fatalf("border pixel (%d,%d) = %d, want ~200 (no black edge)", x, y, v)
			}
		}
	}
}

// TestApplyLensMovesPixels checks the stage actually does something — a
// correction that silently no-ops would pass every structural test above.
func TestApplyLensMovesPixels(t *testing.T) {
	const w, h = 320, 240
	c := fixtureCorrection(t)
	e := &edit.Params{}
	src := gridImage(w, h)
	out := ApplyLens(src, LensWarp(c, e, w, h), e)
	diff := 0
	for i := 0; i+3 < len(src.Pix); i += 4 {
		if src.Pix[i] != out.Pix[i] {
			diff++
		}
	}
	if frac := float64(diff) / float64(w*h); frac < 0.05 {
		t.Errorf("only %.1f%% of pixels changed; the correction is barely doing anything", frac*100)
	}
}

// TestApplyLensBrightensTheCorners pins the vignetting correction's
// direction through the real render stage, including the linear-light
// round trip.
func TestApplyLensBrightensTheCorners(t *testing.T) {
	const w, h = 320, 240
	c := fixtureCorrection(t)
	// Vignetting only: the geometry would move the sampled content and
	// muddy a straight before/after comparison of one pixel.
	e := &edit.Params{LensDistortion: -1, LensCA: -1}
	warp := LensWarp(c, e, w, h)
	if warp == nil {
		t.Fatal("LensWarp = nil")
	}
	if warp.Geometric() {
		t.Fatal("expected a vignetting-only warp")
	}
	out := ApplyLens(flatImage(w, h, 120), warp, e)
	corner := out.Pix[out.PixOffset(0, 0)]
	centre := out.Pix[out.PixOffset(w/2, h/2)]
	if corner <= centre {
		t.Errorf("corner %d should be brighter than the centre %d after devignetting", corner, centre)
	}
	if centre != 120 {
		t.Errorf("centre = %d, want 120 — the gain is 1 on the optical axis", centre)
	}
}

// TestApplyLensVignettingUsesLinearLight guards the correction's magnitude.
// Applying the gain to gamma-encoded values instead of linear light would
// roughly double the correction in stops, which looks plausible on screen
// and is wrong.
func TestApplyLensVignettingUsesLinearLight(t *testing.T) {
	const w, h = 320, 240
	c := fixtureCorrection(t)
	e := &edit.Params{LensDistortion: -1, LensCA: -1}
	warp := LensWarp(c, e, w, h)
	out := ApplyLens(flatImage(w, h, 120), warp, e)

	// Reproduce the expected corner value independently, through the same
	// encoding the decode carries.
	pwr, ts := outputEncoding(e)
	dec := dcrawGammaDecoder(pwr, ts)
	enc := dcrawGammaEncoder(pwr, ts)
	linear := dec(120.0/255) * warp.VigGain(0.5, 0.5)
	want := math.Round(255 * enc(math.Min(1, linear)))
	if got := float64(out.Pix[out.PixOffset(0, 0)]); math.Abs(got-want) > 1 {
		t.Errorf("corner = %v, want %v (gain applied in linear light)", got, want)
	}
}

// TestLensWarpRespectsTheOffSwitch keeps "Off" absolute: no profile, however
// good, may touch a frame the photographer switched the correction off for.
func TestLensWarpRespectsTheOffSwitch(t *testing.T) {
	c := fixtureCorrection(t)
	if w := LensWarp(c, &edit.Params{LensMode: edit.LensOff}, 320, 240); w != nil {
		t.Error("LensWarp with the correction off should be nil")
	}
}

// TestLensWarpDefaultsToCorrecting is the product decision written down: an
// edit that says nothing about lenses — every edit made before this feature
// existed — gets the profile's full correction.
func TestLensWarpDefaultsToCorrecting(t *testing.T) {
	c := fixtureCorrection(t)
	for _, e := range []*edit.Params{nil, {}} {
		if w := LensWarp(c, e, 320, 240); w == nil {
			t.Errorf("LensWarp(%v) = nil, want the profile applied by default", e)
		}
	}
}
