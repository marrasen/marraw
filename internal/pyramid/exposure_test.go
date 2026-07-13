package pyramid

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

func meanLuma(img *image.RGBA) float64 {
	var sum float64
	var n int
	for i := 0; i+3 < len(img.Pix); i += 4 {
		sum += float64(img.Pix[i]) + float64(img.Pix[i+1]) + float64(img.Pix[i+2])
		n += 3
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// TestRenderPreviewExposureDelta: a positive expDeltaEV must brighten the frame
// and a negative one darken it, while 0 leaves the base render untouched.
func TestRenderPreviewExposureDelta(t *testing.T) {
	src := flatGray(64, 48, 128)
	e := &edit.Params{}

	base := meanLuma(RenderPreview(src, 64, 0.72, e, 0, nil))
	up := meanLuma(RenderPreview(src, 64, 0.72, e, 1, nil))
	down := meanLuma(RenderPreview(src, 64, 0.72, e, -1, nil))

	if up <= base {
		t.Errorf("+1 EV mean %.1f not brighter than base %.1f", up, base)
	}
	if down >= base {
		t.Errorf("-1 EV mean %.1f not darker than base %.1f", down, base)
	}

	// expDeltaEV 0 is a no-op: identical to leaving the fold out entirely.
	noFold := flatGray(64, 48, 128)
	ApplyLook(noFold, 0.72, e)
	ApplyDetail(noFold, e)
	if got := meanLuma(RenderPreview(src, 64, 0.72, e, 0, nil)); math.Abs(got-meanLuma(noFold)) > 0.5 {
		t.Errorf("delta 0 render %.2f differs from unfolded %.2f", got, meanLuma(noFold))
	}
}

// TestApplyExposureLUTPhotometric: the fold linearizes with the decode's own
// display gamma (previewExposureGamma), scales by 2^Δ, and re-encodes — so a
// mid-tone at +1 EV lands on encode(2·linear(v)), a ~×1.37 rise, NOT the ~×2.6
// that folding in lookGamma space produced.
func TestApplyExposureLUTPhotometric(t *testing.T) {
	const g = previewExposureGamma
	img := flatGray(4, 4, 100)
	applyExposureLUT(img, 1)

	lin := math.Pow(100.0/255, g) * 2
	if lin > 1 {
		lin = 1
	}
	want := uint8(math.Round(255 * math.Pow(lin, 1/g)))
	if got := img.Pix[0]; got != want {
		t.Errorf("fold(+1EV) of 100 = %d, want %d", got, want)
	}
	// Guard the magnitude: +1 EV on a mid-tone must be a moderate lift, not the
	// near-doubling the old lookGamma-space fold produced.
	if got := img.Pix[0]; got > 160 {
		t.Errorf("fold(+1EV) of 100 = %d, too bright (lookGamma-space regression?)", got)
	}
}

// TestApplyExposureLUTNoOp: delta 0 must not touch pixels.
func TestApplyExposureLUTNoOp(t *testing.T) {
	img := flatGray(4, 4, 137)
	applyExposureLUT(img, 0)
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i] != 137 {
			t.Fatalf("delta 0 changed pixel to %d", img.Pix[i])
		}
	}
}
