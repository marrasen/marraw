package pyramid

import (
	"image"
	"math"
	"testing"
)

func testMatrix() [3][3]float64 {
	// Rows sum to 1, as LibRaw normalizes rgb_cam: a neutral camera pixel must
	// come out a neutral output pixel, which is what makes "equal camera
	// channels" mean "grey" for the WB picker.
	return [3][3]float64{
		{1.74, -0.79, 0.05},
		{-0.19, 1.51, -0.32},
		{0.03, -0.54, 1.51},
	}
}

func TestInvert3(t *testing.T) {
	m := testMatrix()
	inv, ok := Invert3(m)
	if !ok {
		t.Fatal("a well-conditioned matrix must invert")
	}
	for i := range 3 {
		for j := range 3 {
			var v float64
			for k := range 3 {
				v += m[i][k] * inv[k][j]
			}
			want := 0.0
			if i == j {
				want = 1
			}
			if math.Abs(v-want) > 1e-9 {
				t.Errorf("(M·M⁻¹)[%d][%d] = %.12g, want %g", i, j, v, want)
			}
		}
	}
	// A singular matrix is refused rather than producing garbage gains.
	if _, ok := Invert3([3][3]float64{{1, 2, 3}, {2, 4, 6}, {1, 1, 1}}); ok {
		t.Error("a singular matrix must not report an inverse")
	}
}

// ramp builds a 16-bit linear test image with three differently-sloped
// channels, so a matrix round trip has real colour to preserve.
func ramp(w, h int) *image.RGBA64 {
	lin := image.NewRGBA64(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			o := lin.PixOffset(x, y)
			vals := [3]uint16{
				uint16(x * 60000 / max(1, w-1)),
				uint16(30000 + y*20000/max(1, h-1)),
				uint16(45000 - x*40000/max(1, w-1)),
			}
			for c, v := range vals {
				lin.Pix[o+c*2], lin.Pix[o+c*2+1] = byte(v>>8), byte(v)
			}
		}
	}
	return lin
}

// camRamp is ramp pushed through m, so every pixel is a real camera reading
// the fold can take back apart: an arbitrary output-space image would invert
// to camera values outside 0..white, where the fold's clip (which is there to
// reproduce LibRaw's) legitimately bites and the two paths must differ.
func camRamp(w, h int, m [3][3]float64) *image.RGBA64 {
	src := ramp(w, h)
	// Scaled so even the largest row sum stays inside the white level.
	var rowMax float64
	for i := range 3 {
		rowMax = math.Max(rowMax, math.Abs(m[i][0])+math.Abs(m[i][1])+math.Abs(m[i][2]))
	}
	lin := image.NewRGBA64(src.Rect)
	for y := range h {
		for x := range w {
			o := src.PixOffset(x, y)
			var cam [3]float64
			for c := range 3 {
				cam[c] = float64(uint32(src.Pix[o+c*2])<<8|uint32(src.Pix[o+c*2+1])) / rowMax
			}
			for i := range 3 {
				v := m[i][0]*cam[0] + m[i][1]*cam[1] + m[i][2]*cam[2]
				u := uint16(math.Min(65535, math.Max(0, v)))
				lin.Pix[o+i*2], lin.Pix[o+i*2+1] = byte(u>>8), byte(u)
			}
		}
	}
	return lin
}

// TestFoldMatrixIdentityWBMatchesScalar: with no white-balance change the
// matrix round trip is mathematically a no-op, so an exposure or brightness
// drag must produce exactly the pixels the cheap scalar path produces — this
// is what lets the hot path stay on the fixed-point loop.
func TestFoldMatrixIdentityWBMatchesScalar(t *testing.T) {
	m := testMatrix()
	minv, _ := Invert3(m)
	lin := camRamp(64, 32, m)
	base := FoldParams{D: [3]float64{1, 1, 1}, Exp: 1.7, Bright: 1.2, Pwr: 1.0 / 2.222, Ts: 4.5}

	scalar := foldScale(lin, 64, 32, base)
	withM := base
	withM.M, withM.Minv, withM.HasMatrix = m, minv, true
	// flat() sends this through the scalar path; force the matrix loop to prove
	// the two agree rather than just that the shortcut was taken.
	forced := foldScaleMatrix(lin, 64, 32, withM)

	if got := foldScale(lin, 64, 32, withM); !sameBytes(got.Pix, scalar.Pix) {
		t.Error("identity WB with a matrix took a different path than the scalar fold")
	}
	var worst int
	for i := range scalar.Pix {
		if d := int(scalar.Pix[i]) - int(forced.Pix[i]); d > worst || -d > worst {
			worst = max(d, -d)
		}
	}
	if worst > 2 {
		t.Errorf("matrix loop differs from the scalar loop by %d levels at identity WB", worst)
	}
}

// TestFoldMatrixIdentityMatrixMatchesPerChannel: with M = I there is no colour
// mixing, so scaling in "camera space" is scaling the output channels — the
// matrix path must then reproduce the per-channel fold the no-matrix fallback
// still uses for four-colour sensors.
func TestFoldMatrixIdentityMatrixMatchesPerChannel(t *testing.T) {
	lin := ramp(64, 32)
	id := [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	fp := FoldParams{
		D: [3]float64{1.6, 1, 0.5}, Exp: 1, Bright: 1,
		M: id, Minv: id, HasMatrix: true, Pwr: 1.0 / 2.222, Ts: 4.5,
	}
	perChannel := fp
	perChannel.HasMatrix = false

	got := foldScaleMatrix(lin, 64, 32, fp)
	want := foldScale(lin, 64, 32, perChannel)
	var worst int
	for i := range want.Pix {
		if d := int(got.Pix[i]) - int(want.Pix[i]); d > worst || -d > worst {
			worst = max(d, -d)
		}
	}
	if worst > 1 {
		t.Errorf("identity matrix differs from the per-channel fold by %d levels", worst)
	}
}

// TestFoldMatrixNeutralStaysNeutral: because rgb_cam maps a neutral camera
// pixel to a neutral output pixel, a white-balance gain applied through it
// must leave a grey ramp grey — a fold that tinted greys would be putting the
// scaling in the wrong space.
func TestFoldMatrixNeutralStaysNeutral(t *testing.T) {
	m := testMatrix()
	minv, _ := Invert3(m)
	// A grey frame in OUTPUT space is grey in camera space too (M·1 = 1 up to
	// the row sums), so build it in camera space and push it through M.
	lin := image.NewRGBA64(image.Rect(0, 0, 32, 4))
	for y := range 4 {
		for x := range 32 {
			cam := float64(x) * 2000
			var out [3]float64
			for i := range 3 {
				out[i] = (m[i][0] + m[i][1] + m[i][2]) * cam
			}
			o := lin.PixOffset(x, y)
			for c := range 3 {
				v := uint16(math.Min(65535, math.Max(0, out[c])))
				lin.Pix[o+c*2], lin.Pix[o+c*2+1] = byte(v>>8), byte(v)
			}
		}
	}
	// Scaling all three camera channels equally is not a WB change at all, so
	// the greys must survive intact.
	fp := FoldParams{
		D: [3]float64{1, 1, 1}, Exp: 1.5, Bright: 1,
		M: m, Minv: minv, HasMatrix: true, Pwr: 1.0 / 2.222, Ts: 4.5,
	}
	got := foldScaleMatrix(lin, 32, 4, fp)
	for x := range 32 {
		i := got.PixOffset(x, 0)
		r, g, b := int(got.Pix[i]), int(got.Pix[i+1]), int(got.Pix[i+2])
		if abs(r-g) > 2 || abs(b-g) > 2 {
			t.Errorf("x=%d: grey went off-neutral: %d/%d/%d", x, r, g, b)
		}
	}
}

func sameBytes(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func BenchmarkFoldScaleScalar(b *testing.B) {
	lin := ramp(1024, 683)
	fp := FoldParams{D: [3]float64{1, 1, 1}, Exp: 1.3, Bright: 1, Pwr: 1.0 / 2.222, Ts: 4.5}
	b.ResetTimer()
	for b.Loop() {
		foldScale(lin, 1024, 683, fp)
	}
}

func BenchmarkFoldScaleMatrix(b *testing.B) {
	lin := ramp(1024, 683)
	m := testMatrix()
	minv, _ := Invert3(m)
	fp := FoldParams{
		D: [3]float64{1.6, 1, 0.5}, Exp: 1.3, Bright: 1,
		M: m, Minv: minv, HasMatrix: true, Pwr: 1.0 / 2.222, Ts: 4.5,
	}
	b.ResetTimer()
	for b.Loop() {
		foldScale(lin, 1024, 683, fp)
	}
}
