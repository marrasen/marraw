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

// TestApplyExposureEVPhotometric: the fold linearizes with the decode's own
// output encoding (the dcraw gamma the pixels carry), scales by 2^Δ, and
// re-encodes — so a mid-tone at +1 EV lands on encode(2·decode(v)), a
// moderate rise, NOT the ~×2.6 that folding in lookGamma space produced.
func TestApplyExposureEVPhotometric(t *testing.T) {
	img := flatGray(4, 4, 100)
	ApplyExposureEV(img, 1, nil)

	pwr, ts := outputEncoding(nil)
	enc, dec := dcrawGammaEncoder(pwr, ts), dcrawGammaDecoder(pwr, ts)
	lin := dec(100.0/255) * 2
	if lin > 1 {
		lin = 1
	}
	want := uint8(math.Round(255 * enc(lin)))
	if got := img.Pix[0]; got != want {
		t.Errorf("fold(+1EV) of 100 = %d, want %d", got, want)
	}
	// Guard the magnitude: +1 EV on a mid-tone must be a moderate lift, not the
	// near-doubling the old lookGamma-space fold produced.
	if got := img.Pix[0]; got > 160 {
		t.Errorf("fold(+1EV) of 100 = %d, too bright (lookGamma-space regression?)", got)
	}
}

// TestDcrawGammaDecoderInvertsEncoder: the analytic inverse must round-trip
// the encoder across both segments (toe and power) for the default and
// custom gamma/shadow pairs.
func TestDcrawGammaDecoderInvertsEncoder(t *testing.T) {
	for _, c := range [][2]float64{{1 / 2.222, 4.5}, {1 / 1.8, 6}, {1 / 3.0, 2}} {
		enc, dec := dcrawGammaEncoder(c[0], c[1]), dcrawGammaDecoder(c[0], c[1])
		for i := 0; i <= 1000; i++ {
			r := float64(i) / 1000
			if got := dec(enc(r)); math.Abs(got-r) > 1e-9 {
				t.Fatalf("pwr=%.3f ts=%.1f: dec(enc(%.3f)) = %.9f", c[0], c[1], r, got)
			}
		}
	}
}

// TestApplyExposureEVResidual: an accurate render of ExpEV beyond LibRaw's
// exp_shift range applies the residual on top of the baked decode — the same
// frame must come out brighter than the baked stops alone, and the residual
// split must cover the whole dial range.
func TestApplyExposureEVResidual(t *testing.T) {
	e := &edit.Params{ExpEV: 5}
	if e.BakedExpEV() != edit.LibrawMaxExpEV || e.ResidualExpEV() != 5-edit.LibrawMaxExpEV {
		t.Fatalf("split of +5 EV = baked %v residual %v", e.BakedExpEV(), e.ResidualExpEV())
	}
	img := flatGray(4, 4, 100)
	ApplyExposureEV(img, e.ResidualExpEV(), e)
	if img.Pix[0] <= 100 {
		t.Errorf("+%v EV residual did not brighten: %d", e.ResidualExpEV(), img.Pix[0])
	}
}

// TestFoldScalePrecisionAtExtremeEV: foldScale's 16.16 fixed-point gain and
// its truncation of the bilinear sample must stay within one output level of
// exact float math at the exposure dial's extremes (K = 2^±5, where the
// truncation error is amplified the most).
func TestFoldScalePrecisionAtExtremeEV(t *testing.T) {
	const pwr, ts = 1.0 / 2.222, 4.5
	enc := dcrawGammaEncoder(pwr, ts)
	// A horizontal 16-bit ramp; 1:1 scale so bilinear sampling is exact.
	lin := image.NewRGBA64(image.Rect(0, 0, 256, 2))
	for y := range 2 {
		for x := range 256 {
			v := uint16(x * 257)
			o := lin.PixOffset(x, y)
			for c := range 3 {
				lin.Pix[o+c*2], lin.Pix[o+c*2+1] = byte(v>>8), byte(v)
			}
		}
	}
	for _, ev := range []float64{edit.MinExpEV, edit.MaxExpEV} {
		k := math.Exp2(ev)
		got := foldScale(lin, 256, 2, FoldParams{K: [3]float64{k, k, k}, Pwr: pwr, Ts: ts})
		for x := range 256 {
			exact := math.Round(255 * enc(math.Min(1, float64(x*257)/65535*k)))
			if d := math.Abs(float64(got.Pix[got.PixOffset(x, 0)]) - exact); d > 1 {
				t.Fatalf("EV %+.0f: fold of %d = %d, exact %.0f (off by %.0f)",
					ev, x*257, got.Pix[got.PixOffset(x, 0)], exact, d)
			}
		}
	}
}

// TestApplyExposureEVNoOp: delta 0 must not touch pixels.
func TestApplyExposureEVNoOp(t *testing.T) {
	img := flatGray(4, 4, 137)
	ApplyExposureEV(img, 0, nil)
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i] != 137 {
			t.Fatalf("delta 0 changed pixel to %d", img.Pix[i])
		}
	}
}
