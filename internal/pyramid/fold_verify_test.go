package pyramid

import (
	"os"
	"path/filepath"
	"testing"

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
	exactImg, err := proc.Process(exactP)
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
	linImg, err := proc.Process(linP)
	if err != nil {
		t.Fatal(err)
	}
	lin, err := FromLibrawLinear(linImg)
	if err != nil {
		t.Fatal(err)
	}
	b := lin.Bounds()
	folded := foldScale(lin, b.Dx(), b.Dy(), FoldParams{K: [3]float64{1, 1, 1}, Pwr: 1.0 / 2.222, Ts: 4.5})

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

// TestFoldWBApproximation measures how far the post-demosaic WB fold drifts
// from LibRaw's pre-demosaic WB on a temperature move — the gap the deferred
// settle corrects. It's informational (a generous ceiling), quantifying the
// worst the transient drag frame can look before the accurate render lands.
func TestFoldWBApproximation(t *testing.T) {
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
	const temp = 0.5 // ~half a stop warmer

	// Exact: LibRaw white-balances before demosaic.
	exactP := libraw.DefaultParams()
	exactP.HalfSize = true
	exactP.NoAutoBright = true
	exactP.UseCameraWB = false
	exactP.WBTemp = temp
	exactImg, err := proc.Process(exactP)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := FromLibraw(exactImg)
	if err != nil {
		t.Fatal(err)
	}

	// Fold: neutral linear reference, WB applied post-demosaic as a ratio.
	linP := libraw.DefaultParams()
	linP.HalfSize = true
	linP.NoAutoBright = true
	linP.OutputBPS = 16
	linP.Gamma = [2]float64{1, 1}
	linImg, err := proc.Process(linP)
	if err != nil {
		t.Fatal(err)
	}
	lin, err := FromLibrawLinear(linImg)
	if err != nil {
		t.Fatal(err)
	}
	target := libraw.AdjustWB(refMul, temp, 0)
	var k [3]float64
	for c := range 3 {
		k[c] = target[c] / refMul[c]
	}
	b := lin.Bounds()
	folded := foldScale(lin, b.Dx(), b.Dy(), FoldParams{K: k, Pwr: 1.0 / 2.222, Ts: 4.5})

	var sumAbs, n int64
	var worst int64
	for i := 0; i+3 < len(exact.Pix); i += 16 {
		for c := range 3 {
			d := int64(folded.Pix[i+c]) - int64(exact.Pix[i+c])
			if d < 0 {
				d = -d
			}
			sumAbs += d
			if d > worst {
				worst = d
			}
			n++
		}
	}
	mae := float64(sumAbs) / float64(n)
	t.Logf("WB temp=+%.1f: fold-vs-exact MAE=%.2f worst=%d (this is what the settle corrects)", temp, mae, worst)
	if mae > 10 {
		t.Errorf("WB fold MAE %.2f unexpectedly large — check channel mapping/order", mae)
	}
}
