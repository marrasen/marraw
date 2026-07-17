package pyramid

import (
	"bytes"
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// smoothImage is a soft two-axis gradient — no hard edges, so downscale
// comparisons across resolutions stay within resample tolerance.
func smoothImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			i := img.PixOffset(x, y)
			img.Pix[i] = uint8(30 + 180*x/w)
			img.Pix[i+1] = uint8(40 + 150*y/h)
			img.Pix[i+2] = uint8(60 + 100*(x+y)/(w+h))
			img.Pix[i+3] = 0xff
		}
	}
	return img
}

func TestApplyMasksNeutralNoOp(t *testing.T) {
	img := gradientImage(64, 48)
	before := clonePix(img)
	ApplyMasks(img, nil, nil)
	ApplyMasks(img, &edit.Params{}, nil)
	// A mask whose adjustment is neutral must change nothing either.
	ApplyMasks(img, &edit.Params{Masks: []edit.Mask{
		{Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.4, RY: 0.4},
	}}, nil)
	for i := range before {
		if img.Pix[i] != before[i] {
			t.Fatalf("neutral ApplyMasks changed pixel %d: %d -> %d", i, before[i], img.Pix[i])
		}
	}
}

// weightAt evaluates one output pixel's Q8 weight through the evaluator seam.
func weightAt(ev maskEvaluator, x, y, width int) uint16 {
	row := make([]uint16, width)
	x0, x1 := ev.weightRow(y, row)
	if x < x0 || x >= x1 {
		return 0
	}
	return row[x]
}

func TestRadialWeightProperties(t *testing.T) {
	const w, h = 200, 100
	m := &edit.Mask{Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.25, RY: 0.4, Feather: 0.5}
	f := newMaskFrame(w, h, &edit.Params{})
	ev := newMaskEvaluator(m, f, nil, nil)
	if got := weightAt(ev, w/2, h/2, w); got != 256 {
		t.Errorf("center weight = %d, want 256", got)
	}
	if got := weightAt(ev, 0, 0, w); got != 0 {
		t.Errorf("far-corner weight = %d, want 0", got)
	}
	// Weight must fall monotonically along the +x axis from the center.
	prev := uint16(256)
	for x := w / 2; x < w; x++ {
		got := weightAt(ev, x, h/2, w)
		if got > prev {
			t.Fatalf("weight rose from %d to %d at x=%d", prev, got, x)
		}
		prev = got
	}
	// Invert flips: sum of the two evaluations is 256 everywhere.
	inv := *m
	inv.Invert = true
	evInv := newMaskEvaluator(&inv, f, nil, nil)
	for _, p := range [][2]int{{w / 2, h / 2}, {w/2 + 30, h / 2}, {5, 5}, {w - 1, h - 1}} {
		a := weightAt(ev, p[0], p[1], w)
		b := weightAt(evInv, p[0], p[1], w)
		if a+b != 256 {
			t.Errorf("weight + inverted weight = %d at %v, want 256", a+b, p)
		}
	}
}

func TestRadialRotationEquivariance(t *testing.T) {
	// An ellipse rotated 90° with swapped radii is the same shape: weights
	// at the same point must match.
	const w, h = 100, 100
	f := newMaskFrame(w, h, &edit.Params{})
	a := newMaskEvaluator(&edit.Mask{Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.4, RY: 0.15, Feather: 0.4}, f, nil, nil)
	b := newMaskEvaluator(&edit.Mask{Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.15, RY: 0.4, Angle: 90, Feather: 0.4}, f, nil, nil)
	for _, p := range [][2]int{{50, 50}, {70, 50}, {50, 60}, {85, 50}, {30, 40}} {
		wa := weightAt(a, p[0], p[1], w)
		wb := weightAt(b, p[0], p[1], w)
		if d := int(wa) - int(wb); d < -2 || d > 2 {
			t.Errorf("rotated ellipse weight mismatch at %v: %d vs %d", p, wa, wb)
		}
	}
}

func TestLinearWeightProperties(t *testing.T) {
	const w, h = 100, 100
	// Top-to-bottom gradient: full at y=0.25, zero at y=0.75.
	m := &edit.Mask{Type: edit.MaskLinear, X0: 0.5, Y0: 0.25, X1: 0.5, Y1: 0.75}
	f := newMaskFrame(w, h, &edit.Params{})
	ev := newMaskEvaluator(m, f, nil, nil)
	if got := weightAt(ev, 50, 10, w); got != 256 {
		t.Errorf("A-side weight = %d, want 256", got)
	}
	if got := weightAt(ev, 50, 90, w); got != 0 {
		t.Errorf("B-side weight = %d, want 0", got)
	}
	if got := weightAt(ev, 50, 50, w); got < 120 || got > 136 {
		t.Errorf("midpoint weight = %d, want ~128", got)
	}
	inv := *m
	inv.Invert = true
	evInv := newMaskEvaluator(&inv, f, nil, nil)
	for _, p := range [][2]int{{50, 10}, {50, 50}, {50, 90}, {10, 30}} {
		a := weightAt(ev, p[0], p[1], w)
		b := weightAt(evInv, p[0], p[1], w)
		if a+b != 256 {
			t.Errorf("weight + inverted weight = %d at %v, want 256", a+b, p)
		}
	}
	// A degenerate (zero-span) gradient contributes nothing.
	if ev := newMaskEvaluator(&edit.Mask{Type: edit.MaskLinear, X0: 0.5, Y0: 0.5, X1: 0.5, Y1: 0.5}, f, nil, nil); ev != nil {
		t.Error("degenerate linear mask must yield a nil evaluator")
	}
}

// TestMaskExposureMatchesExposureLUT: a mask covering the whole frame at full
// weight with only ExpEV set must reproduce the mask stage's linear-light
// exposure model (scale by 2^Δ under previewExposureGamma — the pure power,
// NOT the dcraw toe curve ApplyExposureEV uses on raw decode output; masks run
// in the look stage where that encoding no longer holds) within ±1 level.
func TestMaskExposureMatchesExposureLUT(t *testing.T) {
	ref := smoothImage(80, 60)
	var lut [256]uint8
	for i := range lut {
		x := math.Min(1, math.Pow(float64(i)/255, previewExposureGamma)*2)
		lut[i] = uint8(math.Round(255 * math.Pow(x, 1/previewExposureGamma)))
	}
	for i := 0; i+3 < len(ref.Pix); i += 4 {
		ref.Pix[i], ref.Pix[i+1], ref.Pix[i+2] = lut[ref.Pix[i]], lut[ref.Pix[i+1]], lut[ref.Pix[i+2]]
	}

	img := smoothImage(80, 60)
	ApplyMasks(img, &edit.Params{Masks: []edit.Mask{{
		Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 5, RY: 5, // covers the frame, no feather reach
		Adjust: edit.MaskAdjust{ExpEV: 1},
	}}}, nil)
	for i := range ref.Pix {
		d := int(ref.Pix[i]) - int(img.Pix[i])
		if d < -1 || d > 1 {
			t.Fatalf("masked exposure diverges from applyExposureLUT at %d: %d vs %d",
				i, ref.Pix[i], img.Pix[i])
		}
	}
}

// TestMaskContentAnchoring: the weight at a fixed *frame* point must survive
// recropping and straightening — masks stay glued to image content.
func TestMaskContentAnchoring(t *testing.T) {
	m := &edit.Mask{Type: edit.MaskRadial, CX: 0.4, CY: 0.45, RX: 0.2, RY: 0.15, Feather: 0.6}

	// Uncropped 1000×800 frame: output pixels are frame pixels.
	plain := newMaskFrame(1000, 800, &edit.Params{})
	evPlain := newMaskEvaluator(m, plain, nil, nil)

	// Cropped + straightened view of the same frame.
	crop := &edit.Params{CropX: 0.2, CropY: 0.1, CropW: 0.6, CropH: 0.7, CropAngle: 5}
	outW, outH := crop.OutputDims(1000, 800)
	f := newMaskFrame(outW, outH, crop)
	ev := newMaskEvaluator(m, f, nil, nil)

	for _, out := range [][2]int{{100, 100}, {300, 250}, {50, 400}, {500, 300}} {
		// Where does this cropped-view pixel land in the frame?
		fx, fy := f.framePoint(float64(out[0]), float64(out[1]))
		px, py := int(math.Round(fx-0.5)), int(math.Round(fy-0.5))
		if px < 0 || px >= 1000 || py < 0 || py >= 800 {
			continue
		}
		got := weightAt(ev, out[0], out[1], outW)
		want := weightAt(evPlain, px, py, 1000)
		// Sub-pixel rounding across the two paths allows a few Q8 counts.
		if d := int(got) - int(want); d < -6 || d > 6 {
			t.Errorf("weight at frame point (%d,%d) changed under crop: %d vs %d", px, py, got, want)
		}
	}
}

// TestMaskCrossResolution: the same masked edit rendered at two resolutions
// must agree after downscaling — weights are defined in fractional frame
// coordinates, so only resample error remains.
func TestMaskCrossResolution(t *testing.T) {
	e := &edit.Params{Masks: []edit.Mask{
		{Type: edit.MaskRadial, CX: 0.45, CY: 0.5, RX: 0.3, RY: 0.25, Angle: 20, Feather: 0.5,
			Adjust: edit.MaskAdjust{ExpEV: 1.2, Contrast: 0.4, Temp: 0.5, Saturation: 0.4}},
		{Type: edit.MaskLinear, X0: 0.5, Y0: 0.2, X1: 0.5, Y1: 0.8,
			Adjust: edit.MaskAdjust{ExpEV: -0.8, Tint: -0.3}},
	}}

	big := smoothImage(512, 384)
	ApplyMasks(big, e, nil)
	bigDown := scaleToLongEdge(big, 256)

	small := smoothImage(256, 192)
	ApplyMasks(small, e, nil)

	var sum, count, worst int
	for i := range small.Pix {
		if i%4 == 3 {
			continue
		}
		d := int(small.Pix[i]) - int(bigDown.Pix[i])
		if d < 0 {
			d = -d
		}
		sum += d
		count++
		if d > worst {
			worst = d
		}
	}
	mean := float64(sum) / float64(count)
	if mean > 1.5 {
		t.Errorf("cross-resolution mean delta %.2f, want ≤ 1.5", mean)
	}
	if worst > 24 {
		t.Errorf("cross-resolution worst delta %d, want ≤ 24", worst)
	}
}

func TestBrushPlaneDeterministicAndCoverage(t *testing.T) {
	strokes := []edit.Stroke{
		{Radius: 0.08, Feather: 0.5, Pts: []float64{0.2, 0.3, 0.5, 0.5, 0.7, 0.4}},
		{Radius: 0.05, Erase: true, Pts: []float64{0.5, 0.5}},
	}
	a := rasterStrokes(strokes, 1024, 768)
	b := rasterStrokes(strokes, 1024, 768)
	if !bytes.Equal(a, b) {
		t.Fatal("same strokes must rasterize to identical planes")
	}
	// Full coverage at a painted point (away from the erase).
	at := func(p []uint8, xf, yf float64) uint8 { return p[int(yf*768)*1024+int(xf*1024)] }
	if got := at(a, 0.2, 0.3); got != 255 {
		t.Errorf("stamp center coverage = %d, want 255", got)
	}
	if got := at(a, 0.9, 0.9); got != 0 {
		t.Errorf("unpainted coverage = %d, want 0", got)
	}
	// The full-flow eraser must clear its center completely.
	if got := at(a, 0.5, 0.5); got != 0 {
		t.Errorf("erased coverage = %d, want 0", got)
	}
	// Flow scales coverage down.
	half := rasterStrokes([]edit.Stroke{{Radius: 0.08, Flow: 0.5, Pts: []float64{0.2, 0.3}}}, 1024, 768)
	if got := at(half, 0.2, 0.3); got < 120 || got > 135 {
		t.Errorf("half-flow coverage = %d, want ~128", got)
	}
}

func TestBrushMaskAppliesInsideStrokeOnly(t *testing.T) {
	img := smoothImage(128, 96)
	before := clonePix(img)
	ApplyMasks(img, &edit.Params{Masks: []edit.Mask{{
		Type:    edit.MaskBrush,
		Strokes: []edit.Stroke{{Radius: 0.1, Feather: 0.3, Pts: []float64{0.25, 0.25}}},
		Adjust:  edit.MaskAdjust{ExpEV: 2},
	}}}, nil)
	center := img.PixOffset(32, 24)
	if img.Pix[center] == before[center] {
		t.Error("brush center must be adjusted")
	}
	far := img.PixOffset(110, 80)
	for c := range 3 {
		if img.Pix[far+c] != before[far+c] {
			t.Errorf("pixel far outside the stroke changed: channel %d %d -> %d",
				c, before[far+c], img.Pix[far+c])
		}
	}
}

func TestMaskLUTNeutralIsIdentityish(t *testing.T) {
	// A neutral adjust never reaches buildMaskLUTs in production (ApplyMasks
	// skips it), but the LUT math should still be ~identity as a sanity floor.
	lutR, lutG, lutB := buildMaskLUTs(&edit.MaskAdjust{})
	for i := range 256 {
		for _, l := range [][256]uint8{lutR, lutG, lutB} {
			d := int(l[i]) - i
			if d < -1 || d > 1 {
				t.Fatalf("neutral mask LUT diverges at %d: %d", i, l[i])
			}
		}
	}
}

// remapMaskCW applies the display-space quarter-turn (CW) remap rule the
// client uses in crop.ts (rotateCropPatch): points map (x,y)→(1−y,x), radial
// radii swap with no aspect factor and the tilt angle keeps, brush points map
// while radii (fractions of the invariant long edge) keep.
func remapMaskCW(m edit.Mask) edit.Mask {
	mapPt := func(x, y float64) (float64, float64) { return 1 - y, x }
	switch m.Type {
	case edit.MaskLinear:
		m.X0, m.Y0 = mapPt(m.X0, m.Y0)
		m.X1, m.Y1 = mapPt(m.X1, m.Y1)
	case edit.MaskRadial:
		m.CX, m.CY = mapPt(m.CX, m.CY)
		m.RX, m.RY = m.RY, m.RX
	case edit.MaskBrush:
		strokes := make([]edit.Stroke, len(m.Strokes))
		for i, s := range m.Strokes {
			pts := make([]float64, len(s.Pts))
			for j := 0; j+1 < len(s.Pts); j += 2 {
				pts[j], pts[j+1] = mapPt(s.Pts[j], s.Pts[j+1])
			}
			s.Pts = pts
			strokes[i] = s
		}
		m.Strokes = strokes
	}
	return m
}

// remapMaskFlipH applies the client's mirror remap rule (flipCropPatch axis
// h): points map (x,y)→(1−x,y), the radial tilt negates, radii keep.
func remapMaskFlipH(m edit.Mask) edit.Mask {
	mapPt := func(x, y float64) (float64, float64) { return 1 - x, y }
	switch m.Type {
	case edit.MaskLinear:
		m.X0, m.Y0 = mapPt(m.X0, m.Y0)
		m.X1, m.Y1 = mapPt(m.X1, m.Y1)
	case edit.MaskRadial:
		m.CX, m.CY = mapPt(m.CX, m.CY)
		m.Angle = math.Mod(math.Mod(-m.Angle, 180)+180, 180)
	case edit.MaskBrush:
		strokes := make([]edit.Stroke, len(m.Strokes))
		for i, s := range m.Strokes {
			pts := make([]float64, len(s.Pts))
			for j := 0; j+1 < len(s.Pts); j += 2 {
				pts[j], pts[j+1] = mapPt(s.Pts[j], s.Pts[j+1])
			}
			s.Pts = pts
			strokes[i] = s
		}
		m.Strokes = strokes
	}
	return m
}

// TestMaskRemapRule: the geometry remap the client applies on rotate/flip
// (crop.ts remapMasks) must keep every mask glued to image content — the
// weight at a fixed content pixel is unchanged when the mask is remapped and
// evaluated in the transformed frame. Radial includes a tilted ellipse in a
// non-square frame: the case where a wrong aspect treatment would show.
func TestMaskRemapRule(t *testing.T) {
	const W, H = 800, 600
	masks := []edit.Mask{
		{Type: edit.MaskLinear, X0: 0.2, Y0: 0.3, X1: 0.7, Y1: 0.8},
		{Type: edit.MaskRadial, CX: 0.4, CY: 0.55, RX: 0.3, RY: 0.15, Angle: 35, Feather: 0.5},
		{Type: edit.MaskBrush, Strokes: []edit.Stroke{{
			Radius: 0.08, Feather: 0.5, Pts: []float64{0.25, 0.3, 0.6, 0.45, 0.7, 0.75},
		}}},
	}
	samples := [][2]int{{160, 180}, {320, 330}, {480, 270}, {560, 450}, {200, 500}}

	for _, m := range masks {
		base := newMaskEvaluator(&m, newMaskFrame(W, H, &edit.Params{}), nil, nil)
		if base == nil {
			t.Fatalf("%s: nil base evaluator", m.Type)
		}

		// CW quarter turn: frame becomes H×W, content pixel (x,y) → (H−1−y, x).
		cw := remapMaskCW(m)
		cwEv := newMaskEvaluator(&cw, newMaskFrame(H, W, &edit.Params{}), nil, nil)
		for _, s := range samples {
			want := weightAt(base, s[0], s[1], W)
			got := weightAt(cwEv, H-1-s[1], s[0], H)
			if d := int(got) - int(want); d < -10 || d > 10 {
				t.Errorf("%s: CW remap weight at content (%d,%d): %d, want %d", m.Type, s[0], s[1], got, want)
			}
		}

		// Horizontal mirror: same frame dims, content pixel (x,y) → (W−1−x, y).
		fl := remapMaskFlipH(m)
		flEv := newMaskEvaluator(&fl, newMaskFrame(W, H, &edit.Params{}), nil, nil)
		for _, s := range samples {
			want := weightAt(base, s[0], s[1], W)
			got := weightAt(flEv, W-1-s[0], s[1], W)
			if d := int(got) - int(want); d < -10 || d > 10 {
				t.Errorf("%s: flip remap weight at content (%d,%d): %d, want %d", m.Type, s[0], s[1], got, want)
			}
		}
	}
}
