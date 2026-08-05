package libraw

import (
	"math"
	"testing"
)

// TestScaleNorm pins the divisor to what dcraw's scale_colors actually uses:
// the smallest multiplier normally, the largest under highlight recovery, and
// the fourth channel standing in as green when it is unset.
func TestScaleNorm(t *testing.T) {
	cases := []struct {
		name      string
		mul       [4]float64
		highlight int
		want      float64
	}{
		{"green is smallest", [4]float64{2.4, 1, 1.5, 1}, 0, 1},
		{"blue is smallest", [4]float64{1.98, 1, 0.389, 1}, 0, 0.389},
		{"red is smallest", [4]float64{0.7, 1, 2.8, 1}, 0, 0.7},
		{"highlight takes the max", [4]float64{1.1, 1, 2.8, 1}, 2, 2.8},
		{"unset fourth reads as green", [4]float64{2.4, 1.2, 1.5, 0}, 0, 1.2},
		{"fourth can be the smallest", [4]float64{2.4, 1.2, 1.5, 0.8}, 0, 0.8},
		{"degenerate green", [4]float64{2.4, 0, 1.5, 0}, 0, 1},
	}
	for _, tc := range cases {
		if got := ScaleNorm(tc.mul, tc.highlight); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("%s: ScaleNorm(%v, %d) = %v, want %v", tc.name, tc.mul, tc.highlight, got, tc.want)
		}
	}
}

// TestWBExpCompEV pins the correction that keeps a white-balance change from
// moving exposure. The interactive fold normalizes to green; LibRaw normalizes
// to the smallest multiplier, so the exact decode of a pick whose green is not
// the smallest comes out brighter — 1.4 stops on the photo this came from,
// which is the jump the user saw when Done was pressed.
func TestWBExpCompEV(t *testing.T) {
	cam := [4]float64{2.4, 1, 1.5, 1} // as-shot, green already the smallest

	// The same balance the frame was decoded at needs no correction, whatever
	// the normalization — this is what keeps camera-WB edits byte-identical.
	if got := WBExpCompEV(cam, cam, 0); got != 0 {
		t.Errorf("target == cam must not move exposure, got %+v EV", got)
	}
	if got := WBExpCompEV(cam, cam, 2); got != 0 {
		t.Errorf("target == cam under highlight recovery, got %+v EV", got)
	}

	// The reported pick: green stays 1 but blue drops to 0.389, so LibRaw
	// divides everything by 0.389 and the decode lands 1/0.389 brighter.
	pick := [4]float64{1.98, 1, 0.389, 1}
	if got, want := WBExpCompEV(pick, cam, 0), math.Log2(0.389); math.Abs(got-want) > 1e-9 {
		t.Errorf("warm pick comp = %+.4f EV, want %+.4f", got, want)
	}

	// Highlight recovery normalizes by the max instead.
	blue := [4]float64{1.1, 1, 2.8, 1}
	if got, want := WBExpCompEV(blue, cam, 2), math.Log2((1/2.4)*(2.8/1)); math.Abs(got-want) > 1e-9 {
		t.Errorf("blue boost comp under highlight recovery = %+.4f EV, want %+.4f", got, want)
	}

	// A degenerate set can't be reasoned about — leave the render alone.
	if got := WBExpCompEV([4]float64{1, 0, 1, 0}, cam, 0); got != 0 {
		t.Errorf("degenerate target must compensate nothing, got %+v EV", got)
	}
}

// TestEffectiveMulMatchesScaleNorm pins the Go-side normalization against the
// vendored LibRaw: after a decode at known multipliers, the readback must be
// those multipliers divided by exactly what ScaleNorm predicted. If LibRaw
// ever changes how it normalizes, this fails rather than silently shifting
// every white-balanced render's brightness.
func TestEffectiveMulMatchesScaleNorm(t *testing.T) {
	path := sampleRAW(t)
	proc, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	if err := proc.Open(path); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		mul       [4]float64
		highlight int
	}{
		{"green smallest", [4]float64{2.4, 1, 1.5, 1}, 0},
		{"blue smallest", [4]float64{1.98, 1, 0.389, 1}, 0},
		{"highlight recovery", [4]float64{1.1, 1, 2.8, 1}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultParams()
			p.HalfSize = true
			p.NoAutoBright = true
			p.UseCameraWB = false
			p.UserMul = tc.mul
			p.Highlight = tc.highlight
			if _, err := proc.Process(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			eff := proc.EffectiveMul()
			norm := ScaleNorm(tc.mul, tc.highlight)
			for c := range 3 {
				want := tc.mul[c] / norm
				if math.Abs(eff[c]-want) > 1e-4*math.Max(1, want) {
					t.Errorf("EffectiveMul[%d] = %.6g, want %.6g (mul %v / norm %.6g)",
						c, eff[c], want, tc.mul, norm)
				}
			}
		})
	}
}
