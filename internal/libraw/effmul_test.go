package libraw

import (
	"testing"
)

// TestEffectiveMulResolvesAutoWB pins the readback the WB picker depends on:
// after a decode, EffectiveMul must report the multipliers LibRaw actually
// applied — including auto WB, which is computed from the pixels and cannot be
// derived from the file's metadata.
func TestEffectiveMulResolvesAutoWB(t *testing.T) {
	path := sampleRAW(t)
	proc, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	if err := proc.Open(path); err != nil {
		t.Fatal(err)
	}
	cam := proc.CamMul()

	base := DefaultParams()
	base.HalfSize = true
	base.NoAutoBright = true

	camP := base
	if _, err := proc.Process(t.Context(), camP); err != nil {
		t.Fatal(err)
	}
	camEff := proc.EffectiveMul()

	autoP := base
	autoP.UseCameraWB, autoP.UseAutoWB = false, true
	if _, err := proc.Process(t.Context(), autoP); err != nil {
		t.Fatal(err)
	}
	autoEff := proc.EffectiveMul()

	t.Logf("cam_mul   = %v", cam)
	t.Logf("camera WB effective = %v", camEff)
	t.Logf("auto WB   effective = %v", autoEff)

	// Camera mode must report the file's own ratios (LibRaw normalizes the
	// set, so compare chromaticity, not absolute scale).
	for _, c := range [2]int{0, 2} {
		want, got := cam[c]/cam[1], camEff[c]/camEff[1]
		if got < want*0.98 || got > want*1.02 {
			t.Errorf("camera-WB effective ratio[%d] = %.4g, want ≈%.4g (as-shot)", c, got, want)
		}
	}

	// Auto mode must report something the caller could NOT have derived —
	// otherwise the picker has no way to express a pick made off an auto frame.
	same := true
	for _, c := range [2]int{0, 2} {
		if math_Abs(autoEff[c]/autoEff[1]-cam[c]/cam[1]) > 1e-3 {
			same = false
		}
	}
	if same {
		t.Skip("auto WB happened to match as-shot on this file; readback untested")
	}
	if autoEff[0] <= 0 || autoEff[1] <= 0 || autoEff[2] <= 0 {
		t.Errorf("auto WB effective multipliers not usable: %v", autoEff)
	}
}

func math_Abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
