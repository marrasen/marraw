package edit

import (
	"bytes"
	"encoding/json"
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

func radialMask() Mask {
	return Mask{Type: MaskRadial, CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.2, Feather: 0.5}
}

func TestMaskNeutrality(t *testing.T) {
	if !(&Params{Masks: []Mask{}}).IsNeutral() {
		t.Error("an empty mask list must stay neutral")
	}
	if !(&Params{Masks: []Mask{{Type: "sky-ai"}}}).IsNeutral() {
		t.Error("unknown mask types must be dropped, leaving neutral")
	}
	// A just-created mask with a neutral adjustment is a real edit: it must
	// persist (not be stored as NULL) even though it changes no pixels yet.
	if (&Params{Masks: []Mask{radialMask()}}).IsNeutral() {
		t.Error("a mask with neutral adjust must not be neutral")
	}
}

func TestMaskHashing(t *testing.T) {
	base := &Params{Masks: []Mask{radialMask()}}
	moved := &Params{Masks: []Mask{radialMask()}}
	moved.Masks[0].CX = 0.7
	adjusted := &Params{Masks: []Mask{radialMask()}}
	adjusted.Masks[0].Adjust.ExpEV = 1
	if base.Hash() == moved.Hash() {
		t.Error("mask geometry change must change the hash")
	}
	if base.Hash() == adjusted.Hash() {
		t.Error("mask adjust change must change the hash")
	}
	// The decode-subset hashes must be mask-blind so mask drags reuse the
	// warm decode and linear reference.
	plain := &Params{Contrast: 0.5}
	masked := &Params{Contrast: 0.5, Masks: []Mask{radialMask()}}
	if plain.LibrawInputsHash() != masked.LibrawInputsHash() {
		t.Error("LibrawInputsHash must ignore masks")
	}
	if plain.LinearInputsHash() != masked.LinearInputsHash() {
		t.Error("LinearInputsHash must ignore masks")
	}
	l := masked.librawInputs()
	b, _ := json.Marshal(&l)
	if bytes.Contains(b, []byte("masks")) {
		t.Errorf("librawInputs must omit masks entirely, got %s", b)
	}
}

func TestMaskNormalize(t *testing.T) {
	e := &Params{Masks: []Mask{
		{Type: MaskLinear, X0: -3, Y0: 0.5, X1: 0.5, Y1: 9, CX: 0.5, RX: 0.3, Feather: 0.7,
			Adjust: MaskAdjust{ExpEV: 99, Saturation: -2}},
		{Type: MaskRadial, CX: 0.5, CY: 0.5, RX: 0, RY: 5, Angle: 365, X0: 0.1},
		{Type: MaskBrush, Feather: 0.5, Strokes: []Stroke{
			{Radius: 0.05, Flow: 3, Pts: []float64{0.123456789, 0.2, 0.3}},
			{Radius: 0.05, Pts: []float64{0.4}}, // degenerate: dropped
		}},
		{Type: "unknown"},
	}}
	e.Normalize()
	if len(e.Masks) != 3 {
		t.Fatalf("want unknown-type mask dropped, got %d masks", len(e.Masks))
	}
	lin := e.Masks[0]
	if lin.X0 != -0.5 || lin.Y1 != 1.5 {
		t.Errorf("linear coords not clamped: %+v", lin)
	}
	if lin.CX != 0 || lin.RX != 0 || lin.Feather != 0 {
		t.Errorf("linear mask must zero radial fields: %+v", lin)
	}
	if lin.Adjust.ExpEV != 4 || lin.Adjust.Saturation != -1 {
		t.Errorf("adjust not clamped: %+v", lin.Adjust)
	}
	rad := e.Masks[1]
	if rad.RX != 0.001 || rad.RY != 2 {
		t.Errorf("radii not clamped: %+v", rad)
	}
	if rad.Angle != 5 {
		t.Errorf("angle must wrap into [0,180), got %v", rad.Angle)
	}
	if rad.X0 != 0 {
		t.Errorf("radial mask must zero linear fields: %+v", rad)
	}
	br := e.Masks[2]
	if br.Feather != 0 {
		t.Errorf("brush mask must zero the parametric feather: %+v", br)
	}
	if len(br.Strokes) != 1 {
		t.Fatalf("degenerate stroke must be dropped, got %d", len(br.Strokes))
	}
	s := br.Strokes[0]
	if s.Flow != 1 || len(s.Pts) != 2 || s.Pts[0] != 0.1235 {
		t.Errorf("stroke not clamped/quantized/evened: %+v", s)
	}
}

// TestMaskNormalizeDoesNotAliasCaller pins the copy-on-normalize contract:
// Hash and IsNeutral normalize a shallow copy, which must never mutate the
// caller's mask slices through shared backing arrays.
func TestMaskNormalizeDoesNotAliasCaller(t *testing.T) {
	e := &Params{Masks: []Mask{
		{Type: "unknown"}, // dropped by Normalize — must not shift caller's slice
		{Type: MaskBrush, Strokes: []Stroke{{Radius: 0.05, Flow: 3, Pts: []float64{0.123456789, 0.2}}}},
	}}
	_ = e.Hash()
	_ = e.IsNeutral()
	if e.Masks[0].Type != "unknown" || e.Masks[1].Strokes[0].Pts[0] != 0.123456789 || e.Masks[1].Strokes[0].Flow != 3 {
		t.Errorf("Hash/IsNeutral mutated the receiver's masks: %+v", e.Masks)
	}
}

func TestMasksOmittedFromNoMaskJSON(t *testing.T) {
	// omitempty is load-bearing: mask-free edits must marshal byte-identical
	// to older builds so existing edit hashes stay stable.
	b, _ := json.Marshal(&Params{Contrast: 0.5})
	if bytes.Contains(b, []byte("masks")) {
		t.Errorf("mask-free params must omit the masks key, got %s", b)
	}
}

func healSpot() Spot {
	return Spot{CX: 0.4, CY: 0.4, Radius: 0.02, SX: 0.5, SY: 0.5, Feather: 0.5}
}

func TestSpotNormalize(t *testing.T) {
	e := &Params{Spots: []Spot{
		{Mode: SpotClone, CX: -3, CY: 0.5, Radius: 9, SX: 2, SY: 0.123456789, Feather: 3, Opacity: -1},
		{Mode: "heal", CX: 0.3, CY: 0.3, Radius: 0.01, SX: 0.4, SY: 0.4}, // folds to ""
		{Kind: "stroke", CX: 0.5, CY: 0.5, Radius: 0.02},                 // unknown kind: dropped
		{Mode: "bogus", CX: 0.5, CY: 0.5, Radius: 0.02},                  // unknown mode: dropped
	}}
	e.Normalize()
	if len(e.Spots) != 2 {
		t.Fatalf("want unknown kind+mode dropped, got %d spots", len(e.Spots))
	}
	s := e.Spots[0]
	if s.CX != -0.5 || s.SX != 1.5 {
		t.Errorf("coords not clamped to frame overhang: %+v", s)
	}
	if s.Radius != 0.5 || s.Feather != 1 || s.Opacity != 0 {
		t.Errorf("radius/feather/opacity not clamped: %+v", s)
	}
	if s.SY != 0.1235 {
		t.Errorf("source coord not quantized: %v", s.SY)
	}
	if e.Spots[1].Mode != SpotHeal {
		t.Errorf(`"heal" must fold to the canonical empty mode, got %q`, e.Spots[1].Mode)
	}
}

func TestSpotHashing(t *testing.T) {
	base := &Params{Spots: []Spot{healSpot()}}
	moved := &Params{Spots: []Spot{healSpot()}}
	moved.Spots[0].SX = 0.7
	cloned := &Params{Spots: []Spot{healSpot()}}
	cloned.Spots[0].Mode = SpotClone
	if base.Hash() == moved.Hash() {
		t.Error("moving the source must change the hash")
	}
	if base.Hash() == cloned.Hash() {
		t.Error("changing the mode must change the hash")
	}
	// The decode-subset hashes must be spot-blind so spot edits reuse the warm
	// decode and linear reference (spots are a post-decode pixel transplant).
	plain := &Params{Contrast: 0.5}
	spotted := &Params{Contrast: 0.5, Spots: []Spot{healSpot()}}
	if plain.LibrawInputsHash() != spotted.LibrawInputsHash() {
		t.Error("LibrawInputsHash must ignore spots")
	}
	if plain.LinearInputsHash() != spotted.LinearInputsHash() {
		t.Error("LinearInputsHash must ignore spots")
	}
	l := spotted.librawInputs()
	b, _ := json.Marshal(&l)
	if bytes.Contains(b, []byte("spots")) {
		t.Errorf("librawInputs must omit spots entirely, got %s", b)
	}
}

func TestSpotNeutrality(t *testing.T) {
	if !(&Params{Spots: []Spot{}}).IsNeutral() {
		t.Error("an empty spot list must stay neutral")
	}
	if !(&Params{Spots: []Spot{{Kind: "stroke"}}}).IsNeutral() {
		t.Error("unknown spot kinds must be dropped, leaving neutral")
	}
	if (&Params{Spots: []Spot{healSpot()}}).IsNeutral() {
		t.Error("a real spot must not be neutral")
	}
}

func TestSpotNormalizeDoesNotAliasCaller(t *testing.T) {
	e := &Params{Spots: []Spot{
		{Kind: "stroke"}, // dropped by Normalize — must not shift caller's slice
		{CX: 0.123456789, CY: 0.2, Radius: 0.02, SX: 0.5, SY: 0.5},
	}}
	_ = e.Hash()
	_ = e.IsNeutral()
	if e.Spots[0].Kind != "stroke" || e.Spots[1].CX != 0.123456789 {
		t.Errorf("Hash/IsNeutral mutated the receiver's spots: %+v", e.Spots)
	}
}

func TestSpotsOmittedFromNoSpotJSON(t *testing.T) {
	// omitempty is load-bearing: spot-free edits must marshal byte-identical to
	// older builds so existing edit hashes stay stable.
	b, _ := json.Marshal(&Params{Contrast: 0.5})
	if bytes.Contains(b, []byte("spots")) {
		t.Errorf("spot-free params must omit the spots key, got %s", b)
	}
}
