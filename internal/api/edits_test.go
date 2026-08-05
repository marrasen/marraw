package api

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// TestClampPickedWB pins the guard that keeps a pick on a narrow-band-lit spot
// — where a channel carries almost no signal — from asking for a multiplier in
// the hundreds and dropping the frame to near-black. The bound is relative to
// the white balance the sampled frame was developed at, because a real white
// balance need not sit anywhere near the blackbody locus: LibRaw's auto WB for
// the blue-lit shot this came from is [1.865, 1, 0.372].
func TestClampPickedWB(t *testing.T) {
	auto := [4]float64{5.011, 2.687, 1, 2.687} // as EffectiveMul reports it
	green := [3]float64{auto[0] / auto[1], 1, auto[2] / auto[1]}

	// The pick that photo actually produces — a small correction — must pass
	// through untouched. An absolute Kelvin-derived envelope clamped this.
	ordinary := [4]float64{1.982, 1, 0.389, 1}
	if got := clampPickedWB(ordinary, auto); got != ordinary {
		t.Errorf("ordinary pick was clamped: %v -> %v (frame WB %v)", ordinary, got, green)
	}

	// A pick off a spot with no red: ×350 red, and blue crushed.
	got := clampPickedWB([4]float64{349.5, 1, 0.02, 1}, auto)
	if got[1] != 1 {
		t.Errorf("green must stay 1, got %v", got[1])
	}
	const span = 1 << pickWBStops
	for _, c := range [2]int{0, 2} {
		lo, hi := green[c]/span, green[c]*span
		if got[c] < lo-1e-9 || got[c] > hi+1e-9 {
			t.Errorf("channel %d = %.4g, outside %d stops of the frame's WB [%.4g, %.4g]",
				c, got[c], pickWBStops, lo, hi)
		}
	}
	// Clamped to the bound, not to something arbitrary in the middle.
	if got[0] != green[0]*span {
		t.Errorf("red should sit on the bound %.4g, got %.4g", green[0]*span, got[0])
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
	// A plausible camera→sRGB matrix (rows sum to 1: neutral stays neutral).
	rgbCam := [3][4]float64{
		{1.74, -0.79, 0.05, 0},
		{-0.19, 1.51, -0.32, 0},
		{0.03, -0.54, 1.51, 0},
	}
	// A picked custom WB near as-shot, normalized to green=1.
	ep := &edit.Params{
		WBMode: edit.WBCustom,
		WBMul:  [4]float64{2400.0 / 1024, 1, 1500.0 / 1024, 1},
	}
	fp := foldParamsFor(ep, refMul, [4][3]float64{}, rgbCam)

	// Same chromaticity as as-shot ⇒ all gains ≈ 1, none crushed toward black.
	for c, d := range fp.D {
		if d < 0.5 || d > 2 {
			t.Errorf("D[%d] = %.4g, want ≈1 (unit mismatch would give ~0.001)", c, d)
		}
	}
	if g := fp.D[1]; math.Abs(g-1) > 1e-9 {
		t.Errorf("green gain = %.6f, want 1 (WB must not change luminance)", g)
	}
	if !fp.HasMatrix {
		t.Error("a three-colour invertible matrix must be usable by the fold")
	}

	// A warmer custom pick (more red, less blue) must raise red and lower blue
	// relative to green, still without collapsing.
	ep.WBMul = [4]float64{3000.0 / 1024, 1, 1100.0 / 1024, 1}
	fp = foldParamsFor(ep, refMul, [4][3]float64{}, rgbCam)
	if !(fp.D[0] > fp.D[1] && fp.D[1] > fp.D[2]) {
		t.Errorf("warm pick gains not ordered R>G>B: %v", fp.D)
	}

	// A four-colour sensor (non-zero fourth column) and an unusable matrix both
	// fall back to scaling the developed channels, as the fold always did.
	four := rgbCam
	four[1][3] = 0.4
	if fp := foldParamsFor(ep, refMul, [4][3]float64{}, four); fp.HasMatrix {
		t.Error("a four-colour sensor must not take the matrix path")
	}
	if fp := foldParamsFor(ep, refMul, [4][3]float64{}, [3][4]float64{}); fp.HasMatrix {
		t.Error("an all-zero matrix must not take the matrix path")
	}
}

// TestPickWBNeutralizesInCameraSpace: the multipliers a pick produces are
// applied to the camera's channels before the colour matrix, so they have to
// be derived there. Feeding the formula a patch that is a known imbalance in
// camera space must hand back the multipliers that undo it — which the
// post-matrix ratio the picker used before did not.
func TestPickWBNeutralizesInCameraSpace(t *testing.T) {
	rgbCam := [3][4]float64{
		{1.74, -0.79, 0.05, 0},
		{-0.19, 1.51, -0.32, 0},
		{0.03, -0.54, 1.51, 0},
	}
	m, minv, ok := foldMatrix(rgbCam)
	if !ok {
		t.Fatal("test matrix should be usable")
	}
	// A grey surface the decode rendered off-balance: in camera channels the
	// patch reads 1.4/1.0/0.6, so a correct pick multiplies by 1/1.4, 1, 1/0.6.
	cam := [3]float64{1.4, 1.0, 0.6}
	var patch [3]float64
	for i := range 3 {
		for j := range 3 {
			patch[i] += m[i][j] * cam[j]
		}
	}
	eff := [4]float64{2.0, 1, 0.5, 1} // whatever the frame was developed at

	// The formula under test, as PickWhiteBalance runs it.
	var p [3]float64
	for i := range 3 {
		for j := range 3 {
			p[i] += minv[i][j] * patch[j]
		}
	}
	got := [3]float64{eff[0] / eff[1] * (p[1] / p[0]), 1, eff[2] / eff[1] * (p[1] / p[2])}
	want := [3]float64{eff[0] / eff[1] / 1.4, 1, eff[2] / eff[1] / 0.6}
	for c := range 3 {
		if math.Abs(got[c]-want[c]) > 1e-9 {
			t.Errorf("mul[%d] = %.6g, want %.6g", c, got[c], want[c])
		}
	}

	// And the result really does neutralize: scaling the camera-space patch by
	// the pick (relative to what the frame carried) equalizes the channels.
	var out [3]float64
	for c := range 3 {
		out[c] = cam[c] * got[c] / (eff[c] / eff[1])
	}
	if math.Abs(out[0]-out[1]) > 1e-9 || math.Abs(out[2]-out[1]) > 1e-9 {
		t.Errorf("picked WB does not neutralize the patch: %v", out)
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
	e.storeDecode(7, key, noExpKey, expEV, 0, rgba)

	// Same LibRaw inputs, a different exposure, plus look-stage offsets an auto
	// preset layers on (contrast/vignette are post-decode) — must still reuse
	// and report the baked 0.5, since the no-exposure key ignores all of it.
	want := &edit.Params{ExpEV: 1.8, WBTemp: 10, Contrast: 0.4, Vignette: 0.3, Saturation: 0.2}
	got, baked, _, ok := e.approxDecode(7, want)
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
	if _, _, _, ok := e.approxDecode(8, want); ok {
		t.Error("reused a decode across photos")
	}

	// A white-balance change (a real LibRaw input) misses.
	wbChanged := &edit.Params{ExpEV: 1.8, WBTemp: 40}
	if _, _, _, ok := e.approxDecode(7, wbChanged); ok {
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
	e.storeDecode(7, key, noExpKey, expEV, 0, rgba)
	if _, baked, _, ok := e.approxDecode(7, &edit.Params{ExpEV: 2, WBTemp: 10}); !ok || baked != edit.LibrawMaxExpEV {
		t.Errorf("reuse of a clamped-bake decode reported %v, want %v", baked, edit.LibrawMaxExpEV)
	}
}
