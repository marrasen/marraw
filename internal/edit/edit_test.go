package edit

import (
	"testing"

	"github.com/marrasen/marraw/internal/libraw"
)

func TestNormalizeKelvin(t *testing.T) {
	e := &Params{WBMode: WBKelvin, WBKelvin: 5500, WBTemp: 0.3, WBTint: 0.1}
	e.Normalize()
	if e.WBTemp != 0 {
		t.Errorf("kelvin mode must drop the relative temp, got %v", e.WBTemp)
	}
	if e.WBKelvin != 5500 || e.WBTint != 0.1 {
		t.Errorf("kelvin/tint must survive: %+v", e)
	}

	e = &Params{WBMode: WBCamera, WBKelvin: 5500}
	e.Normalize()
	if e.WBKelvin != 0 {
		t.Errorf("non-kelvin mode must drop wbKelvin, got %v", e.WBKelvin)
	}
}

func TestNormalizeSplitHue(t *testing.T) {
	e := &Params{SplitShadowHue: 220, SplitHighlightHue: 40, SplitHighlightAmt: 0.5}
	e.Normalize()
	if e.SplitShadowHue != 0 {
		t.Errorf("hue without amount must normalize to 0, got %v", e.SplitShadowHue)
	}
	if e.SplitHighlightHue != 40 {
		t.Errorf("hue with amount must survive, got %v", e.SplitHighlightHue)
	}
}

func TestIsNeutralNewFields(t *testing.T) {
	if !(&Params{SplitShadowHue: 120}).IsNeutral() {
		t.Error("a bare split hue (no amount) must stay neutral")
	}
	for name, p := range map[string]Params{
		"contrast": {Contrast: 0.1},
		"vignette": {Vignette: -0.2},
		"demosaic": {Demosaic: DemosaicDHT},
		"kelvin":   {WBMode: WBKelvin, WBKelvin: 5500},
	} {
		if p.IsNeutral() {
			t.Errorf("%s edit must not be neutral", name)
		}
	}
}

func TestLibrawParamsMapping(t *testing.T) {
	e := &Params{
		WBMode:   WBKelvin,
		WBKelvin: 4800,
		WBTint:   0.2,
		Demosaic: DemosaicDHT,
		CARed:    1,
		CABlue:   -0.5,
	}
	p := e.LibrawParams(false)
	if p.WBKelvin != 4800 || p.UseCameraWB {
		t.Errorf("kelvin not mapped: %+v", p)
	}
	if p.WBTint != 0.2 {
		t.Errorf("tint must pass through in kelvin mode, got %v", p.WBTint)
	}
	if p.UserQual != libraw.DemosaicDHT {
		t.Errorf("demosaic not mapped: %v", p.UserQual)
	}
	if p.CARed != 1.002 || p.CABlue != 0.999 {
		t.Errorf("CA mapping wrong: red=%v blue=%v", p.CARed, p.CABlue)
	}

	// Kelvin mode without a temperature falls back to camera WB.
	e = &Params{WBMode: WBKelvin}
	if p := e.LibrawParams(false); !p.UseCameraWB || p.WBKelvin != 0 {
		t.Errorf("kelvin mode without value must keep camera WB: %+v", p)
	}
}

func TestDeltaLookFields(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	e := &Params{Contrast: 0.9, Saturation: -0.9}
	Delta{Contrast: f(0.5), Saturation: f(-0.5), Vibrance: f(0.25)}.Apply(e)
	if e.Contrast != 1 {
		t.Errorf("contrast must clamp at 1, got %v", e.Contrast)
	}
	if e.Saturation != -1 {
		t.Errorf("saturation must clamp at -1, got %v", e.Saturation)
	}
	if e.Vibrance != 0.25 {
		t.Errorf("vibrance delta not applied, got %v", e.Vibrance)
	}
}

func TestCropNormalizeAndDims(t *testing.T) {
	// A full-frame crop normalizes away to neutral.
	e := &Params{CropX: 0, CropY: 0, CropW: 1, CropH: 1}
	e.Normalize()
	if !e.IsNeutral() {
		t.Errorf("full-frame crop should be neutral, got %+v", e)
	}
	// A bare straighten angle is a real (non-neutral) edit that keeps the
	// full dimensions.
	a := &Params{CropAngle: 5}
	if a.IsNeutral() {
		t.Error("straighten angle must not be neutral")
	}
	if w, h := a.OutputDims(4000, 3000); w != 4000 || h != 3000 {
		t.Errorf("straighten-only OutputDims = %dx%d, want 4000x3000", w, h)
	}
	// A real crop reports HasCrop and shrinks the dimensions.
	c := &Params{CropX: 0.1, CropY: 0.1, CropW: 0.5, CropH: 0.5}
	if !c.HasCrop() {
		t.Error("expected HasCrop")
	}
	if w, h := c.OutputDims(4000, 3000); w != 2000 || h != 1500 {
		t.Errorf("crop OutputDims = %dx%d, want 2000x1500", w, h)
	}
}

func TestHashStableAcrossEquivalentStates(t *testing.T) {
	a := &Params{WBMode: WBKelvin, WBKelvin: 5500, WBTemp: 0.5}
	b := &Params{WBMode: WBKelvin, WBKelvin: 5500}
	if a.Hash() != b.Hash() {
		t.Error("normalized-equal states must hash equal")
	}
}
