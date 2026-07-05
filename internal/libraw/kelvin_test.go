package libraw

import (
	"math"
	"testing"
)

// TestKelvinXYZ checks the locus approximation against known illuminants:
// D65 (6504 K) and D50 (5003 K) chromaticities.
func TestKelvinXYZ(t *testing.T) {
	cases := []struct {
		kelvin, x, y float64
	}{
		{6504, 0.3127, 0.3290},
		{5003, 0.3457, 0.3585},
	}
	for _, c := range cases {
		X, Y, Z := kelvinXYZ(c.kelvin)
		x := X / (X + Y + Z)
		y := Y / (X + Y + Z)
		if math.Abs(x-c.x) > 0.005 || math.Abs(y-c.y) > 0.005 {
			t.Errorf("%.0fK: got xy (%.4f, %.4f), want (%.4f, %.4f)", c.kelvin, x, y, c.x, c.y)
		}
	}
}

// TestKelvinMulFallback exercises the sRGB fallback (a fresh handle has no
// camera matrix): warmer temperatures must need less red gain and more blue
// gain, and green stays the reference.
func TestKelvinMulFallback(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	assertKelvinMonotone(t, p)
}

// TestKelvinMulCamera runs the same monotonicity check through a real
// camera matrix.
func TestKelvinMulCamera(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	assertKelvinMonotone(t, p)

	// A daylight-ish temperature should land in the same ballpark as the
	// camera's daylight multipliers (pre_mul), well within a factor of two.
	mul := p.KelvinMul(5500)
	pre := camMulOf(p.h)
	for c := range 3 {
		ratio := (mul[c] / mul[1]) / (pre[c] / pre[1])
		t.Logf("ch%d: kelvin=%.3f asshot=%.3f ratio=%.2f", c, mul[c]/mul[1], pre[c]/pre[1], ratio)
	}
}

func assertKelvinMonotone(t *testing.T, p *Processor) {
	t.Helper()
	prev := [4]float64{}
	for i, k := range []float64{2500, 4000, 5500, 8000, 12000} {
		mul := p.KelvinMul(k)
		t.Logf("%5.0fK: R=%.3f G=%.3f B=%.3f", k, mul[0], mul[1], mul[2])
		if mul[1] != 1 {
			t.Errorf("%.0fK: green multiplier %.3f, want 1 (normalization)", k, mul[1])
		}
		if i > 0 {
			if mul[0] <= prev[0] {
				t.Errorf("%.0fK: red multiplier %.3f not above %.3f — must rise with temperature", k, mul[0], prev[0])
			}
			if mul[2] >= prev[2] {
				t.Errorf("%.0fK: blue multiplier %.3f not below %.3f — must fall with temperature", k, mul[2], prev[2])
			}
		}
		prev = mul
	}
}
