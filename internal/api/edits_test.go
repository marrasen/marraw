package api

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

func TestRangeColorWindow(t *testing.T) {
	// A saturated green pick centres a wrap-free hue window on ~1/3 and sets a
	// floor below its saturation.
	lo, hi, sat, err := rangeColorWindow(0, 200, 0)
	if err != nil {
		t.Fatalf("green pick: %v", err)
	}
	if math.Abs(lo-(1.0/3-0.045)) > 1e-9 || math.Abs(hi-(1.0/3+0.045)) > 1e-9 {
		t.Errorf("green window = [%v,%v], want ~[%v,%v]", lo, hi, 1.0/3-0.045, 1.0/3+0.045)
	}
	if sat <= 0 || sat >= 1 {
		t.Errorf("green satMin = %v, want a floor in (0,1)", sat)
	}
	// A red pick (hue 0) wraps: lo near 1, hi near 0 (hi < lo), which Normalize
	// keeps as a circular window.
	lo, hi, _, err = rangeColorWindow(220, 0, 0)
	if err != nil {
		t.Fatalf("red pick: %v", err)
	}
	if lo <= hi {
		t.Errorf("red window should wrap (lo > hi), got [%v,%v]", lo, hi)
	}
	// Grey and near-black picks have no hue and must be refused.
	if _, _, _, err := rangeColorWindow(128, 128, 128); err == nil {
		t.Error("grey pick must be refused")
	}
	if _, _, _, err := rangeColorWindow(3, 5, 2); err == nil {
		t.Error("too-dark pick must be refused")
	}
}

// TestFoldParamsForUnitScales guards the fold's WB ratio against a
// normalization-unit mismatch: a picked custom WBMul is normalized to green=1,
// while the reference cam_mul is in raw units (green ~1024 on many cameras).
// Without normalizing both to green the ratio collapses to ~1/1000 and the
// preview goes black — the regression this test pins down.
func TestFoldParamsForUnitScales(t *testing.T) {
	// Raw-units as-shot WB, green ~1024 (typical Sony cam_mul).
	refMul := [4]float64{2400, 1024, 1500, 1024}
	// A picked custom WB near as-shot, normalized to green=1.
	ep := &edit.Params{
		WBMode: edit.WBCustom,
		WBMul:  [4]float64{2400.0 / 1024, 1, 1500.0 / 1024, 1},
	}
	fp := foldParamsFor(ep, refMul, [4][3]float64{})

	// Same chromaticity as as-shot ⇒ all gains ≈ 1, none crushed toward black.
	for c, k := range fp.K {
		if k < 0.5 || k > 2 {
			t.Errorf("K[%d] = %.4g, want ≈1 (unit mismatch would give ~0.001)", c, k)
		}
	}
	if g := fp.K[1]; math.Abs(g-1) > 1e-9 {
		t.Errorf("green gain = %.6f, want 1 (WB must not change luminance)", g)
	}

	// A warmer custom pick (more red, less blue) must raise red and lower blue
	// relative to green, still without collapsing.
	ep.WBMul = [4]float64{3000.0 / 1024, 1, 1100.0 / 1024, 1}
	fp = foldParamsFor(ep, refMul, [4][3]float64{})
	if !(fp.K[0] > fp.K[1] && fp.K[1] > fp.K[2]) {
		t.Errorf("warm pick gains not ordered R>G>B: %v", fp.K)
	}
}

// TestApproxDecodeExposureReuse: a decode stored at one exposure is reused for
// the same photo when only exposure differs, reporting the baked-in ExpEV so
// the caller can fold the delta; a white-balance change (or a different photo)
// misses.
func TestApproxDecodeExposureReuse(t *testing.T) {
	e := &Edits{}
	rgba := image.NewRGBA(image.Rect(0, 0, 2, 2))

	stored := &edit.Params{ExpEV: 0.5, WBTemp: 10}
	key, noExpKey, expEV := decodeKeys(stored)
	e.storeDecode(7, key, noExpKey, expEV, rgba)

	// Same LibRaw inputs, a different exposure, plus look-stage offsets an auto
	// preset layers on (contrast/vignette are post-decode) — must still reuse
	// and report the baked 0.5, since the no-exposure key ignores all of it.
	want := &edit.Params{ExpEV: 1.8, WBTemp: 10, Contrast: 0.4, Vignette: 0.3, Saturation: 0.2}
	got, baked, ok := e.approxDecode(7, want)
	if !ok {
		t.Fatal("exposure-only change did not reuse the decode")
	}
	if got != rgba {
		t.Error("reused a different rgba than stored")
	}
	if baked != 0.5 {
		t.Errorf("baked ExpEV = %v, want 0.5", baked)
	}

	// A different photo misses.
	if _, _, ok := e.approxDecode(8, want); ok {
		t.Error("reused a decode across photos")
	}

	// A white-balance change (a real LibRaw input) misses.
	wbChanged := &edit.Params{ExpEV: 1.8, WBTemp: 40}
	if _, _, ok := e.approxDecode(7, wbChanged); ok {
		t.Error("reused a decode across a white-balance change")
	}

	// Beyond LibRaw's exp_shift range the decode only carries the clamped
	// stops, so the reported bake must be what the pixels have — not the dial
	// value — or the caller's fold delta would drop the residual.
	hot := &edit.Params{ExpEV: 4.5, WBTemp: 10}
	key, noExpKey, expEV = decodeKeys(hot)
	if expEV != edit.LibrawMaxExpEV {
		t.Errorf("decodeKeys baked EV for +4.5 = %v, want %v", expEV, edit.LibrawMaxExpEV)
	}
	e.storeDecode(7, key, noExpKey, expEV, rgba)
	if _, baked, ok := e.approxDecode(7, &edit.Params{ExpEV: 2, WBTemp: 10}); !ok || baked != edit.LibrawMaxExpEV {
		t.Errorf("reuse of a clamped-bake decode reported %v, want %v", baked, edit.LibrawMaxExpEV)
	}
}
