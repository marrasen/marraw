package edit

import (
	"bytes"
	"encoding/json"
	"reflect"
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
		"contrast":  {Contrast: 0.1},
		"vignette":  {Vignette: -0.2},
		"demosaic":  {Demosaic: DemosaicDHT},
		"kelvin":    {WBMode: WBKelvin, WBKelvin: 5500},
		"toneCurve":  {ToneCurve: []CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.7}, {X: 1, Y: 1}}},
		"toneCurveR": {ToneCurveR: []CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.7}, {X: 1, Y: 1}}},
		"toneCurveG": {ToneCurveG: []CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.7}, {X: 1, Y: 1}}},
		"toneCurveB": {ToneCurveB: []CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.7}, {X: 1, Y: 1}}},
	} {
		if p.IsNeutral() {
			t.Errorf("%s edit must not be neutral", name)
		}
	}
	// A purely diagonal curve is identity and must stay neutral, master or
	// per-channel.
	diag := []CurvePoint{{X: 0, Y: 0}, {X: 0.4, Y: 0.4}, {X: 1, Y: 1}}
	for name, p := range map[string]Params{
		"master": {ToneCurve: diag},
		"red":    {ToneCurveR: diag},
		"green":  {ToneCurveG: diag},
		"blue":   {ToneCurveB: diag},
	} {
		if !p.IsNeutral() {
			t.Errorf("an all-diagonal %s curve must stay neutral", name)
		}
	}
}

// TestToneCurveChannelsNormalize: every channel curve normalizes independently
// with the master's rules, and HasChannelCurves only counts the channels.
func TestToneCurveChannelsNormalize(t *testing.T) {
	bend := []CurvePoint{{X: 1.5, Y: 1}, {X: 0, Y: 0}, {X: 0.5, Y: 0.812345}}
	e := &Params{ToneCurveR: bend, ToneCurveG: append([]CurvePoint(nil), bend...)}
	e.Normalize()
	want := []CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.8123}, {X: 1, Y: 1}}
	for name, got := range map[string][]CurvePoint{"R": e.ToneCurveR, "G": e.ToneCurveG} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v want %+v", name, got, want)
		}
	}
	if e.ToneCurveB != nil || e.ToneCurve != nil {
		t.Error("untouched curves must stay nil")
	}
	if !e.HasChannelCurves() {
		t.Error("a bent channel curve must report HasChannelCurves")
	}
	if e.HasToneCurve() {
		t.Error("channel curves must not report as a master curve")
	}
	// The master alone is not a channel curve.
	m := &Params{ToneCurve: []CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.7}, {X: 1, Y: 1}}}
	if m.HasChannelCurves() || !m.HasToneCurve() {
		t.Error("master/channel predicates crossed")
	}
	var nilP *Params
	if nilP.HasChannelCurves() || nilP.HasToneCurve() {
		t.Error("nil params must have no curves")
	}
}

func TestToneCurveNormalize(t *testing.T) {
	// Out-of-order, out-of-range points: sorted, clamped to the unit square,
	// quantized, and (being off-diagonal) kept.
	e := &Params{ToneCurve: []CurvePoint{
		{X: 1.4, Y: 2}, {X: -0.5, Y: -1}, {X: 0.5, Y: 0.812345},
	}}
	e.Normalize()
	want := []CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.8123}, {X: 1, Y: 1}}
	if !reflect.DeepEqual(e.ToneCurve, want) {
		t.Fatalf("normalize: got %+v want %+v", e.ToneCurve, want)
	}
	// Duplicate input X: the later point wins, collapsing to one entry.
	e = &Params{ToneCurve: []CurvePoint{{X: 0.5, Y: 0.2}, {X: 0.5, Y: 0.9}, {X: 1, Y: 1}}}
	e.Normalize()
	if len(e.ToneCurve) != 2 || e.ToneCurve[0] != (CurvePoint{X: 0.5, Y: 0.9}) {
		t.Fatalf("duplicate X must collapse to last: %+v", e.ToneCurve)
	}
	// Identity and single-point curves fold to nil.
	for _, in := range [][]CurvePoint{
		{{X: 0, Y: 0}, {X: 1, Y: 1}},
		{{X: 0.5, Y: 0.5}},
		{},
	} {
		e = &Params{ToneCurve: in}
		e.Normalize()
		if e.ToneCurve != nil {
			t.Errorf("identity/degenerate curve %+v must fold to nil, got %+v", in, e.ToneCurve)
		}
	}
}

// TestToneCurveNormalizeDoesNotAliasCaller: Normalize builds a fresh slice, so
// hashing (which normalizes a shallow copy) must not mutate the caller's curve.
func TestToneCurveNormalizeDoesNotAliasCaller(t *testing.T) {
	orig := []CurvePoint{{X: 1, Y: 1}, {X: 0, Y: 0}, {X: 0.5, Y: 0.7}}
	e := &Params{ToneCurve: orig}
	_ = e.Hash()
	if orig[0] != (CurvePoint{X: 1, Y: 1}) {
		t.Errorf("Hash mutated the caller's curve: %+v", orig)
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

// TestExposureBakedResidualSplit: the dial spans MinExpEV..MaxExpEV but LibRaw
// only bakes LibrawMinExpEV..LibrawMaxExpEV; the split must cover the whole
// range with baked+residual == ExpEV and residual zero inside the baked range.
func TestExposureBakedResidualSplit(t *testing.T) {
	cases := []struct{ ev, baked, residual float64 }{
		{0, 0, 0},
		{2.5, 2.5, 0},
		{-1.5, -1.5, 0},
		{4, 3, 1},
		{-3, -2, -1},
		{MaxExpEV, LibrawMaxExpEV, MaxExpEV - LibrawMaxExpEV},
		{MinExpEV, LibrawMinExpEV, MinExpEV - LibrawMinExpEV},
	}
	for _, c := range cases {
		e := &Params{ExpEV: c.ev}
		if e.BakedExpEV() != c.baked || e.ResidualExpEV() != c.residual {
			t.Errorf("ExpEV %v: baked %v residual %v, want %v/%v",
				c.ev, e.BakedExpEV(), e.ResidualExpEV(), c.baked, c.residual)
		}
	}
	var nilP *Params
	if nilP.BakedExpEV() != 0 || nilP.ResidualExpEV() != 0 {
		t.Error("nil params must split as 0/0")
	}
}

// TestLibrawParamsExpShiftClamped: the exp_shift handed to LibRaw stays within
// its hard 0.25..8 range even at the dial extremes — the stops beyond render
// via ResidualExpEV, never by asking LibRaw for an out-of-range shift.
func TestLibrawParamsExpShiftClamped(t *testing.T) {
	if got := (&Params{ExpEV: MaxExpEV}).LibrawParams(true).ExpShift; got != 8 {
		t.Errorf("ExpShift at +%d EV = %v, want 8", MaxExpEV, got)
	}
	if got := (&Params{ExpEV: MinExpEV}).LibrawParams(true).ExpShift; got != 0.25 {
		t.Errorf("ExpShift at %d EV = %v, want 0.25", MinExpEV, got)
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

func TestNormalizePersonMask(t *testing.T) {
	e := &Params{Masks: []Mask{
		{Type: MaskAI, AIKind: AIPerson, MapVer: "rfdetrseg-1", ClassID: 3,
			DepthLo: 0.4, DepthHi: 0.9, Threshold: 0.7, Feather: 0.2},
		{Type: MaskAI, AIKind: AIPerson, MapVer: "rfdetrseg-1", ClassID: 0}, // background: clamps to 1
		{Type: MaskAI, AIKind: "hologram", MapVer: "x"},                     // unknown kind: dropped
	}}
	e.Normalize()
	if len(e.Masks) != 2 {
		t.Fatalf("kept %d masks, want 2 (unknown AI kind dropped)", len(e.Masks))
	}
	m := e.Masks[0]
	if m.ClassID != 3 || m.DepthLo != 0 || m.DepthHi != 0 || m.Threshold != 0 {
		t.Errorf("person mask not normalized: %+v", m)
	}
	if m.Feather != 0.2 {
		t.Errorf("feather = %v, want 0.2 kept", m.Feather)
	}
	if e.Masks[1].ClassID != 1 {
		t.Errorf("background instance ID must clamp to 1, got %d", e.Masks[1].ClassID)
	}
}

// TestMaskFXFields covers the spatial-FX half of MaskAdjust: the clamps, the
// inert-angle rule, and the two gates the render stage keys off.
func TestMaskFXFields(t *testing.T) {
	e := &Params{Masks: []Mask{{
		Type: MaskRadial, CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.3,
		Adjust: MaskAdjust{Blur: 3, MotionBlur: -1, ZoomBlur: 0.5, Streaks: 9, Mosaic: -0.2, FXAngle: 200},
	}}}
	e.Normalize()
	a := e.Masks[0].Adjust
	if a.Blur != 1 || a.MotionBlur != 0 || a.ZoomBlur != 0.5 || a.Streaks != 1 || a.Mosaic != 0 {
		t.Errorf("FX amounts not clamped to 0..1: %+v", a)
	}
	// Streaks is live, so the angle survives — wrapped into 0..180.
	if a.FXAngle != 20 {
		t.Errorf("FXAngle = %v, want 20 (200 mod 180)", a.FXAngle)
	}

	// An FX-only mask is a real edit, and an FX-free one is still tone-only.
	if (&MaskAdjust{Blur: 0.5}).IsNeutral() {
		t.Error("a blur-only adjustment must not be neutral")
	}
	if (&MaskAdjust{Blur: 0.5}).HasTone() {
		t.Error("a blur-only adjustment has no tone")
	}
	if !(&MaskAdjust{ExpEV: 1}).HasTone() || (&MaskAdjust{ExpEV: 1}).HasFX() {
		t.Error("an exposure-only adjustment is tone, not FX")
	}
	for _, a := range []MaskAdjust{{Blur: 1}, {MotionBlur: 1}, {ZoomBlur: 1}, {Streaks: 1}, {Mosaic: 1}} {
		if !a.HasFX() {
			t.Errorf("%+v must report HasFX", a)
		}
	}
}

// TestMaskFXInertAngle: an angle nothing reads must not fork the hash, or a
// stray drag on the direction slider would invalidate every cached rendition
// and make a neutral mask look adjusted.
func TestMaskFXInertAngle(t *testing.T) {
	with := func(a MaskAdjust) *Params {
		m := radialMask()
		m.Adjust = a
		return &Params{Masks: []Mask{m}}
	}
	plain := with(MaskAdjust{})
	angled := with(MaskAdjust{FXAngle: 90})
	if plain.Hash() != angled.Hash() {
		t.Error("an inert FXAngle must not change the hash")
	}
	if !angled.IsNeutral() != !plain.IsNeutral() {
		t.Error("an inert FXAngle must not change neutrality")
	}
	// With a smear live, the angle is load-bearing and must hash.
	if with(MaskAdjust{Streaks: 0.5}).Hash() == with(MaskAdjust{Streaks: 0.5, FXAngle: 90}).Hash() {
		t.Error("a live FXAngle must change the hash")
	}

	// FX rides inside MaskAdjust, so the decode-subset hashes stay mask-blind:
	// dragging Blur must never force a re-demosaic.
	base := &Params{Contrast: 0.5, Masks: []Mask{radialMask()}}
	fx := &Params{Contrast: 0.5, Masks: []Mask{radialMask()}}
	fx.Masks[0].Adjust.Blur = 0.6
	if base.LibrawInputsHash() != fx.LibrawInputsHash() {
		t.Error("LibrawInputsHash must ignore mask FX")
	}
	if base.LinearInputsHash() != fx.LinearInputsHash() {
		t.Error("LinearInputsHash must ignore mask FX")
	}
}

// TestMaskFXJSONBackCompat: the FX fields are appended and omitempty, so a
// pre-FX edit still serializes byte-for-byte as it always did.
func TestMaskFXJSONBackCompat(t *testing.T) {
	e := &Params{Masks: []Mask{radialMask()}}
	e.Normalize()
	b, _ := json.Marshal(e)
	for _, k := range []string{"blur", "motionBlur", "zoomBlur", "streaks", "mosaic", "fxAngle"} {
		if bytes.Contains(b, []byte(`"`+k+`"`)) {
			t.Errorf("unset %s must be omitted, got %s", k, b)
		}
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
func TestRangeMaskNormalize(t *testing.T) {
	e := &Params{Masks: []Mask{
		// Luma window given inverted (hi < lo) and out of range: reordered and
		// clamped. Hue window given inverted: NOT reordered (circular). Stray
		// geometry/AI fields must be zeroed.
		{Type: MaskRange, RangeLumaHi: 0.2, RangeLumaLo: 0.8, RangeHueLo: 0.9, RangeHueHi: 0.1,
			RangeSatMin: 2, Feather: 0.5, X0: 0.3, CX: 0.4, AIKind: AISubject, MapVer: "x",
			Adjust: MaskAdjust{ExpEV: 1}},
	}}
	e.Normalize()
	if len(e.Masks) != 1 {
		t.Fatalf("want the range mask kept, got %d", len(e.Masks))
	}
	m := e.Masks[0]
	if m.RangeLumaLo != 0.2 || m.RangeLumaHi != 0.8 {
		t.Errorf("luma window must be reordered ascending, got lo=%v hi=%v", m.RangeLumaLo, m.RangeLumaHi)
	}
	if m.RangeHueLo != 0.9 || m.RangeHueHi != 0.1 {
		t.Errorf("hue window must NOT be reordered (wraps), got lo=%v hi=%v", m.RangeHueLo, m.RangeHueHi)
	}
	if m.RangeSatMin != 1 {
		t.Errorf("satMin must clamp to 1, got %v", m.RangeSatMin)
	}
	if m.X0 != 0 || m.CX != 0 || m.AIKind != "" || m.MapVer != "" {
		t.Errorf("range mask must zero geometry/AI fields: %+v", m)
	}
}

func TestRangeMaskFieldsOmittedFromNonRangeJSON(t *testing.T) {
	// omitempty on the range fields is load-bearing: a non-range mask must
	// marshal byte-identical to older builds so existing edit hashes stay stable.
	b, _ := json.Marshal(&Params{Masks: []Mask{{Type: MaskLinear, X1: 1, Adjust: MaskAdjust{ExpEV: 1}}}})
	if bytes.Contains(b, []byte("range")) {
		t.Errorf("non-range mask must omit all range keys, got %s", b)
	}
}

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
		{Kind: "polygon", CX: 0.5, CY: 0.5, Radius: 0.02},                // unknown kind: dropped
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

func TestSpotNormalizeStrokeKind(t *testing.T) {
	e := &Params{Spots: []Spot{
		{Kind: "stroke", CX: 0.5, CY: 0.5, Radius: 0.3, Feather: 0.7, SX: 0.7, SY: 0.5, Strokes: []Stroke{
			{Radius: 0.02, Feather: 0.5, Pts: []float64{0.4123456789, 0.5, 0.45, 0.5, 0.99}}, // odd tail dropped
			{Radius: 0.02, Pts: []float64{0.1}}, // degenerate: dropped
		}},
		{Kind: "stroke", CX: 0.5, CY: 0.5, SX: 0.7, SY: 0.5}, // nothing painted: dropped
	}}
	e.Normalize()
	if len(e.Spots) != 1 {
		t.Fatalf("want the empty stroke spot dropped, got %d spots", len(e.Spots))
	}
	s := e.Spots[0]
	if s.Kind != "stroke" {
		t.Fatalf("stroke kind must survive Normalize, got %q", s.Kind)
	}
	if s.Radius != 0 || s.Feather != 0 {
		t.Errorf("stroke spots carry radius/feather per-stroke; spot fields must zero: %+v", s)
	}
	if len(s.Strokes) != 1 {
		t.Fatalf("want 1 usable stroke, got %d", len(s.Strokes))
	}
	if got := s.Strokes[0].Pts; len(got) != 4 || got[0] != 0.4123 {
		t.Errorf("stroke pts not quantized/trimmed: %v", got)
	}
	// A stroke spot with no usable geometry is neutral once normalized.
	if !(&Params{Spots: []Spot{{Kind: "stroke", CX: 0.5, CY: 0.5}}}).IsNeutral() {
		t.Error("a stroke spot with nothing painted must normalize to neutral")
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

func TestSpotNormalizeFill(t *testing.T) {
	e := &Params{Spots: []Spot{
		// A circle fill: kept, and the meaningless source reference zeroes
		// so equivalent fills hash identically.
		{Mode: SpotFill, CX: 0.5, CY: 0.5, Radius: 0.02, SX: 0.62, SY: 0.55, Feather: 0.5},
		// Fill composes with the brush region too.
		{Kind: "stroke", Mode: SpotFill, SX: 0.3, SY: 0.3,
			Strokes: []Stroke{{Radius: 0.02, Pts: []float64{0.4, 0.4, 0.45, 0.4}}}},
	}}
	e.Normalize()
	if len(e.Spots) != 2 {
		t.Fatalf("fill spots must survive Normalize, got %d", len(e.Spots))
	}
	for i, s := range e.Spots {
		if s.Mode != SpotFill {
			t.Errorf("spot %d: mode = %q, want fill", i, s.Mode)
		}
		if s.SX != 0 || s.SY != 0 {
			t.Errorf("spot %d: fill source reference must zero, got (%v,%v)", i, s.SX, s.SY)
		}
	}
	if (&Params{Spots: []Spot{{Mode: SpotFill, CX: 0.5, CY: 0.5, Radius: 0.02}}}).IsNeutral() {
		t.Error("a fill spot must not be neutral")
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
