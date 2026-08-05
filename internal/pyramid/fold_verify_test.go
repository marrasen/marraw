package pyramid

import (
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
)

// sampleRAW mirrors libraw's test helper: a RAW to decode, or skip.
func sampleRAW(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("MARRAW_TEST_RAW_DIR")
	if dir == "" {
		dir = `D:\Photos\2026-04-18 Velox Valor Trollhättan`
	}
	for _, pat := range []string{"*.ARW", "*.arw", "*.CR2", "*.CR3", "*.NEF", "*.DNG"} {
		if m, _ := filepath.Glob(filepath.Join(dir, pat)); len(m) > 0 {
			return m[0]
		}
	}
	t.Skipf("no RAW files found in %s (set MARRAW_TEST_RAW_DIR)", dir)
	return ""
}

// TestFoldMatchesExactDecode is the core correctness proof for the fold path:
// a scene-linear reference decode folded at neutral settings (unit gain, the
// default BT.709 gamma) must reproduce LibRaw's own 8-bit decode of the same
// file. If it does, dragging WB/exposure off the reference won't "pop" when the
// deferred settle re-decodes exactly. It exercises both the dcraw gamma-curve
// reproduction and the 16-bit-linear → 8-bit fold.
func TestFoldMatchesExactDecode(t *testing.T) {
	path := sampleRAW(t)
	proc, err := libraw.New()
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	if err := proc.Open(path); err != nil {
		t.Fatal(err)
	}

	// Exact: LibRaw's deterministic 8-bit decode at camera WB, default gamma.
	exactP := libraw.DefaultParams()
	exactP.HalfSize = true
	exactP.NoAutoBright = true
	exactImg, err := proc.Process(t.Context(), exactP)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := FromLibraw(exactImg)
	if err != nil {
		t.Fatal(err)
	}

	// Fold: the scene-linear reference, folded at neutral settings.
	linP := exactP
	linP.OutputBPS = 16
	linP.Gamma = [2]float64{1, 1}
	linImg, err := proc.Process(t.Context(), linP)
	if err != nil {
		t.Fatal(err)
	}
	lin, err := FromLibrawLinear(linImg)
	if err != nil {
		t.Fatal(err)
	}
	b := lin.Bounds()
	folded := foldScale(lin, b.Dx(), b.Dy(), FoldParams{D: [3]float64{1, 1, 1}, Exp: 1, Bright: 1, Pwr: 1.0 / 2.222, Ts: 4.5})

	if folded.Bounds() != exact.Bounds() {
		t.Fatalf("size mismatch: fold %v exact %v", folded.Bounds(), exact.Bounds())
	}

	// Per-channel mean and worst-case deviation over a subsample.
	var sumFold, sumExact, sumAbs, sumSq int64
	var worst, n int64
	for i := 0; i+3 < len(exact.Pix); i += 16 { // every 4th pixel
		for c := range 3 {
			f := int64(folded.Pix[i+c])
			e := int64(exact.Pix[i+c])
			d := f - e
			if d < 0 {
				d = -d
			}
			sumFold += f
			sumExact += e
			sumAbs += d
			sumSq += d * d
			if d > worst {
				worst = d
			}
			n++
		}
	}
	if n == 0 {
		t.Fatal("no pixels sampled")
	}
	meanFold := float64(sumFold) / float64(n)
	meanExact := float64(sumExact) / float64(n)
	mae := float64(sumAbs) / float64(n)
	rmse := (float64(sumSq) / float64(n))
	t.Logf("meanFold=%.2f meanExact=%.2f MAE=%.3f RMSE^2=%.2f worst=%d over %d samples",
		meanFold, meanExact, mae, rmse, worst, n)

	// The fold reconstructs the exact decode up to 16→8-bit rounding and the
	// gamma-curve reproduction; a couple of levels of average error is expected,
	// a large mean shift or MAE is not.
	if diff := meanFold - meanExact; diff < -1.5 || diff > 1.5 {
		t.Errorf("mean brightness drifted by %.2f levels", diff)
	}
	if mae > 2.0 {
		t.Errorf("mean absolute error %.3f too high — gamma or fold mismatch", mae)
	}
}

// TestFoldMatchesExactDecodeCustomWB is the proof that a white-balance drag
// settles onto the frame it was dragged at. The preview folds WB onto the
// linear reference; the settle, the tiles and every export re-decode through
// LibRaw. Those two agreeing is not a nicety — when they diverged, a pick that
// looked right turned deep magenta the moment Done was pressed.
//
// Each case decodes exactly at a set of multipliers, applies the compensation
// every accurate render applies (edit.Params.WBCompEV, which takes LibRaw's
// min-normalization back out so WB carries no brightness), and compares that
// against the fold of the same edit. The multipliers are deliberately wild —
// the loose pick clamp reaches this far, and this is exactly where scaling
// after the colour matrix instead of before it falls apart.
func TestFoldMatchesExactDecodeCustomWB(t *testing.T) {
	path := sampleRAW(t)
	proc, err := libraw.New()
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	if err := proc.Open(path); err != nil {
		t.Fatal(err)
	}
	refMul := proc.CamMul()
	rgbCam := proc.RgbCam()
	t.Logf("cam_mul = %v", refMul)
	t.Logf("rgb_cam = %v", rgbCam)

	// The camera→sRGB matrix, and its inverse, as foldParamsFor builds them.
	var m [3][3]float64
	for i := range 3 {
		if math.Abs(rgbCam[i][3]) > 1e-6 {
			t.Skip("four-colour sensor: the matrix fold does not apply")
		}
		for j := range 3 {
			m[i][j] = rgbCam[i][j]
		}
	}
	minv, ok := Invert3(m)
	if !ok {
		t.Skip("camera matrix is not invertible on this file")
	}

	// The linear reference the fold works off, decoded at the as-shot balance.
	// Highlight recovery is a pre-demosaic input, so it is part of the
	// reference's cache key (edit.linearInputs) and both sides of a comparison
	// must carry the same one — a reference decoded at a different setting is
	// a different set of pixels, not a different edit of the same ones.
	reference := func(highlight int) *image.RGBA64 {
		t.Helper()
		linP := libraw.DefaultParams()
		linP.HalfSize = true
		linP.NoAutoBright = true
		linP.OutputBPS = 16
		linP.Gamma = [2]float64{1, 1}
		linP.Highlight = highlight
		linImg, err := proc.Process(t.Context(), linP)
		if err != nil {
			t.Fatal(err)
		}
		lin, err := FromLibrawLinear(linImg)
		if err != nil {
			t.Fatal(err)
		}
		return lin
	}

	cases := []struct {
		name      string
		mul       [4]float64
		highlight int
	}{
		// A hard cool move: the case that rendered magenta. Blue is boosted far
		// past green, so the matrix's negative cross-terms bite hardest.
		{"blue boost", [4]float64{1.1, 1, 2.8, 1}, 0},
		// The pick from the blue-stage photo in the report — green is not the
		// smallest multiplier, so LibRaw's normalization moves brightness ~1.4 EV.
		{"warm pick", [4]float64{1.98, 1, 0.389, 1}, 0},
		// Highlight recovery flips the normalization to the max multiplier.
		{"blue boost, highlight recovery", [4]float64{1.1, 1, 2.8, 1}, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Exact: LibRaw applies the multipliers to the CFA, pre-matrix.
			exactP := libraw.DefaultParams()
			exactP.HalfSize = true
			exactP.NoAutoBright = true
			exactP.UseCameraWB = false
			exactP.UserMul = tc.mul
			exactP.Highlight = tc.highlight
			exactImg, err := proc.Process(t.Context(), exactP)
			if err != nil {
				t.Fatal(err)
			}
			exact, err := FromLibraw(exactImg)
			if err != nil {
				t.Fatal(err)
			}
			// What every accurate render does on top of that decode.
			comp := libraw.WBExpCompEV(tc.mul, refMul, tc.highlight)
			ApplyExposureEV(exact, comp, &edit.Params{Highlight: tc.highlight})

			// Fold: the same edit, off the reference for this decode state.
			lin := reference(tc.highlight)
			var d [3]float64
			for c := range 3 {
				d[c] = (tc.mul[c] / tc.mul[1]) / (refMul[c] / refMul[1])
			}
			b := lin.Bounds()
			folded := foldScale(lin, b.Dx(), b.Dy(), FoldParams{
				D: d, Exp: 1, Bright: 1, White: 65535 * math.Exp2(comp),
				M: m, Minv: minv, HasMatrix: true,
				Pwr: 1.0 / 2.222, Ts: 4.5,
			})
			if folded.Bounds() != exact.Bounds() {
				t.Fatalf("size mismatch: fold %v exact %v", folded.Bounds(), exact.Bounds())
			}

			// Per-channel means matter most: a channel drifting on its own is a
			// hue shift, which is what the user sees as "it went magenta".
			var sumFold, sumExact, sumAbs [3]int64
			var worst, n int64
			for i := 0; i+3 < len(exact.Pix); i += 16 { // every 4th pixel
				for c := range 3 {
					f, e := int64(folded.Pix[i+c]), int64(exact.Pix[i+c])
					dd := f - e
					if dd < 0 {
						dd = -dd
					}
					sumFold[c] += f
					sumExact[c] += e
					sumAbs[c] += dd
					worst = max(worst, dd)
				}
				n++
			}
			if n == 0 {
				t.Fatal("no pixels sampled")
			}
			var mae float64
			for c := range 3 {
				mf := float64(sumFold[c]) / float64(n)
				me := float64(sumExact[c]) / float64(n)
				ch := float64(sumAbs[c]) / float64(n)
				mae += ch / 3
				t.Logf("channel %d: fold mean %.2f exact mean %.2f (drift %+.2f) MAE %.2f",
					c, mf, me, mf-me, ch)
				// The old post-matrix fold missed these by dozens of levels.
				if diff := mf - me; diff < -6 || diff > 6 {
					t.Errorf("channel %d mean drifted %+.2f levels — the fold is not reproducing the decode", c, diff)
				}
			}
			t.Logf("comp=%+.3f EV MAE=%.2f worst=%d over %d pixels", comp, mae, worst, n)
			// What is left is the demosaic-order difference: LibRaw scales the
			// CFA before interpolating, the fold scales after.
			if mae > 12 {
				t.Errorf("mean absolute error %.2f too high for demosaic-order residue alone", mae)
			}
		})
	}
}
