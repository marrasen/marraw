package pyramid

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// fillFlat paints the whole image one gray level (opaque).
func fillFlat(img *image.RGBA, v uint8) {
	b := img.Bounds()
	for y := range b.Dy() {
		for x := range b.Dx() {
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
}

// fillDisc paints a filled disc (center/radius in pixels) one gray level.
func fillDisc(img *image.RGBA, cx, cy, r float64, v uint8) {
	for y := int(cy - r); y <= int(cy+r); y++ {
		for x := int(cx - r); x <= int(cx+r); x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy > r*r {
				continue
			}
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
}

func lumaAt(img *image.RGBA, x, y int) int {
	i := img.PixOffset(x, y)
	return int(img.Pix[i])
}

func TestApplyHealNeutralNoOp(t *testing.T) {
	img := gradientImage(64, 48)
	before := clonePix(img)
	ApplyHeal(img, nil, nil, nil)
	ApplyHeal(img, &edit.Params{}, nil, nil)
	for i := range before {
		if img.Pix[i] != before[i] {
			t.Fatalf("neutral ApplyHeal changed pixel %d: %d -> %d", i, before[i], img.Pix[i])
		}
	}
}

// TestApplyHealClone copies the source verbatim into the destination disc.
func TestApplyHealClone(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	fillFlat(img, 100)
	fillDisc(img, 150, 100, 20, 200) // bright source patch
	e := &edit.Params{Spots: []edit.Spot{{
		Mode: edit.SpotClone,
		CX:   0.25, CY: 0.5, Radius: 0.05, // dest at (50,100), radPx=10
		SX: 0.75, SY: 0.5, // source at (150,100)
		Feather: 0.1,
	}}}
	ApplyHeal(img, e, nil, nil)
	if got := lumaAt(img, 50, 100); got < 190 {
		t.Errorf("clone center should copy the bright source (~200), got %d", got)
	}
	// Far outside the disc is untouched.
	if got := lumaAt(img, 50, 160); got != 100 {
		t.Errorf("clone must not touch pixels outside the disc, got %d", got)
	}
}

// TestApplyHealToneMatches covers a blemished destination healed from a
// differently-lit source: the fill should match the destination surround, not
// the source brightness.
func TestApplyHealToneMatches(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	fillFlat(img, 100)               // destination surround
	fillDisc(img, 150, 100, 22, 200) // bright source patch (constant)
	fillDisc(img, 50, 100, 10, 0)    // black blemish to cover
	before := lumaAt(img, 50, 100)
	if before != 0 {
		t.Fatalf("blemish setup wrong, center=%d", before)
	}
	e := &edit.Params{Spots: []edit.Spot{{
		CX: 0.25, CY: 0.5, Radius: 0.05, // heal mode (default)
		SX: 0.75, SY: 0.5,
		Feather: 0.1,
	}}}
	ApplyHeal(img, e, nil, nil)
	got := lumaAt(img, 50, 100)
	if got < 90 || got > 110 {
		t.Errorf("heal should tone-match the ~100 surround, got %d", got)
	}
}

// TestApplyHealSkipsUnknownKindsAndModes pins the forward-compat contract: a
// sidecar written by a newer build renders here WITHOUT a Normalize pass, so
// spot kinds/modes this build doesn't know must be ignored, never misrendered
// as circles (the newMaskEvaluator unknown-type precedent). A fill spot whose
// patch is not generated (or not generatable) shares the guarantee: it
// composites nothing, never garbage.
func TestApplyHealSkipsUnknownKindsAndModes(t *testing.T) {
	img := gradientImage(120, 90)
	before := clonePix(img)
	ApplyHeal(img, &edit.Params{Spots: []edit.Spot{
		{Kind: "polygon", CX: 0.5, CY: 0.5, Radius: 0.2, SX: 0.2, SY: 0.2},
		// A known fill spot with no cached patch in the set: skipped.
		{Mode: edit.SpotFill, CX: 0.5, CY: 0.5, Radius: 0.2},
		// A stroke spot with an unknown MODE must be skipped too.
		{Kind: "stroke", Mode: "bogus", CX: 0.5, CY: 0.5, SX: 0.2, SY: 0.2,
			Strokes: []edit.Stroke{{Radius: 0.05, Pts: []float64{0.4, 0.5, 0.6, 0.5}}}},
	}}, nil, nil)
	for i := range before {
		if img.Pix[i] != before[i] {
			t.Fatalf("unknown kind/mode spot changed pixel %d: %d -> %d", i, before[i], img.Pix[i])
		}
	}
	// The un-normalized "heal" spelling of the default mode still heals.
	ApplyHeal(img, &edit.Params{Spots: []edit.Spot{
		{Mode: "heal", CX: 0.5, CY: 0.5, Radius: 0.1, SX: 0.2, SY: 0.2, Feather: 0.1},
	}}, nil, nil)
	changed := false
	for i := range before {
		if img.Pix[i] != before[i] {
			changed = true
			break
		}
	}
	if !changed {
		t.Error(`mode "heal" must render as the default heal, not be skipped`)
	}
}

// TestApplyHealNearEdge must not panic or read out of bounds when a spot sits
// against the frame edge.
func TestApplyHealNearEdge(t *testing.T) {
	img := gradientImage(120, 90)
	e := &edit.Params{Spots: []edit.Spot{
		{CX: 0.01, CY: 0.01, Radius: 0.08, SX: 0.5, SY: 0.5, Feather: 0.4},
		{CX: 0.99, CY: 0.99, Radius: 0.08, SX: 0.5, SY: 0.5, Mode: edit.SpotClone},
	}}
	ApplyHeal(img, e, nil, nil) // just needs to survive
}

// strokeSpot paints a horizontal bar via one stroke: dest reference at
// (cx,cy), source offset dx/dy to the right/down (frame fractions).
func strokeSpot(cx, cy, dx, dy, rad float64, mode edit.SpotMode) edit.Spot {
	return edit.Spot{
		Kind: "stroke", Mode: mode,
		CX: cx, CY: cy, SX: cx + dx, SY: cy + dy,
		Strokes: []edit.Stroke{{
			Radius: rad, Feather: 0.3,
			Pts: []float64{cx - 0.08, cy, cx + 0.08, cy},
		}},
	}
}

// TestApplyHealStrokeClone copies the translated source region verbatim into
// the painted region.
func TestApplyHealStrokeClone(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	fillFlat(img, 100)
	// Bright band where the translated source lands (dest row 100 → source row 160).
	for y := 140; y < 180; y++ {
		for x := range 200 {
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 200, 200, 200
		}
	}
	e := &edit.Params{Spots: []edit.Spot{strokeSpot(0.5, 0.5, 0, 0.3, 0.04, edit.SpotClone)}}
	ApplyHeal(img, e, nil, nil)
	if got := lumaAt(img, 100, 100); got < 190 {
		t.Errorf("stroke clone center should copy the bright source (~200), got %d", got)
	}
	// Well clear of the painted bar: untouched.
	if got := lumaAt(img, 100, 60); got != 100 {
		t.Errorf("stroke clone must not touch pixels outside the region, got %d", got)
	}
}

// TestApplyHealStrokeToneMatches heals a dark blemish bar from a brighter
// source region: the boundary-band membrane must pull the fill to the
// destination surround, not the source brightness.
func TestApplyHealStrokeToneMatches(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	fillFlat(img, 100)
	for y := 140; y < 180; y++ { // bright source band
		for x := range 200 {
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 200, 200, 200
		}
	}
	for y := 96; y <= 104; y++ { // dark blemish bar under the stroke
		for x := 84; x <= 116; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 0, 0, 0
		}
	}
	e := &edit.Params{Spots: []edit.Spot{strokeSpot(0.5, 0.5, 0, 0.3, 0.04, edit.SpotHeal)}}
	ApplyHeal(img, e, nil, nil)
	got := lumaAt(img, 100, 100)
	if got < 85 || got > 115 {
		t.Errorf("stroke heal should tone-match the ~100 surround, got %d", got)
	}
}

// TestApplyHealStrokeNearEdge must survive strokes hugging (and overhanging)
// the frame edge, where the source offset gets clamped onto the frame.
func TestApplyHealStrokeNearEdge(t *testing.T) {
	img := gradientImage(120, 90)
	e := &edit.Params{Spots: []edit.Spot{
		strokeSpot(0.02, 0.02, 0.5, 0.5, 0.06, edit.SpotHeal),
		strokeSpot(0.98, 0.98, 0.5, 0.5, 0.06, edit.SpotClone),
	}}
	ApplyHeal(img, e, nil, nil) // just needs to survive
}

// TestApplyHealStrokeResolutionStable checks a stroke heals to the same tone
// whether rendered large or downscaled — the fixed-resolution coverage plane
// makes the region identical by construction.
func TestApplyHealStrokeResolutionStable(t *testing.T) {
	mk := func(w, h int) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		fillFlat(img, 120)
		for y := int(0.7 * float64(h)); y < int(0.9*float64(h)); y++ {
			for x := range w {
				i := img.PixOffset(x, y)
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 210, 210, 210
			}
		}
		return img
	}
	e := &edit.Params{Spots: []edit.Spot{strokeSpot(0.5, 0.5, 0, 0.3, 0.04, edit.SpotHeal)}}
	big := mk(400, 300)
	small := mk(200, 150)
	ApplyHeal(big, e, nil, nil)
	ApplyHeal(small, e, nil, nil)
	gb := lumaAt(big, 200, 150)
	gs := lumaAt(small, 100, 75)
	if diff := gb - gs; diff < -12 || diff > 12 {
		t.Errorf("stroke heal tone drifted across resolution: big=%d small=%d", gb, gs)
	}
}

func TestStrokeSpotCircle(t *testing.T) {
	s := strokeSpot(0.5, 0.5, 0, 0.3, 0.04, edit.SpotHeal)
	cx, cy, rad := StrokeSpotCircle(200, 200, &edit.Params{}, &s)
	if math.Abs(cx-0.5) > 0.001 || math.Abs(cy-0.5) > 0.001 {
		t.Errorf("enclosing circle center should sit on the bar: (%v,%v)", cx, cy)
	}
	// Bar half-length 0.08 plus brush radius 0.04 → enclosing radius ≥ 0.12.
	if rad < 0.11 || rad > 0.2 {
		t.Errorf("enclosing radius out of range: %v", rad)
	}
	empty := edit.Spot{Kind: "stroke"}
	if _, _, r := StrokeSpotCircle(200, 200, &edit.Params{}, &empty); r != 0 {
		t.Errorf("empty stroke spot must report radius 0, got %v", r)
	}
}

func TestSuggestHealSource(t *testing.T) {
	img := smoothImage(200, 160)
	e := &edit.Params{}
	spot := edit.Spot{CX: 0.5, CY: 0.5, Radius: 0.05}
	sx1, sy1 := SuggestHealSource(img, e, spot)
	sx2, sy2 := SuggestHealSource(img, e, spot)
	if sx1 != sx2 || sy1 != sy2 {
		t.Errorf("SuggestHealSource must be deterministic: (%v,%v) vs (%v,%v)", sx1, sy1, sx2, sy2)
	}
	// The suggestion must be in-frame and clear of the destination disc.
	if sx1 < 0 || sx1 > 1 || sy1 < 0 || sy1 > 1 {
		t.Errorf("suggested source off-frame: (%v,%v)", sx1, sy1)
	}
	f := newMaskFrame(200, 160, e)
	long := math.Max(f.frameW, f.frameH)
	scx, scy := f.outputPoint(sx1*f.frameW, sy1*f.frameH)
	dcx, dcy := f.outputPoint(spot.CX*f.frameW, spot.CY*f.frameH)
	if d := math.Hypot(scx-dcx, scy-dcy); d < 2*spot.Radius*long {
		t.Errorf("suggested source overlaps the spot: distance %v < %v", d, 2*spot.Radius*long)
	}
}

// stripeRampImage is horizontal fine stripes (texture varies with y, period p)
// laid over a horizontal brightness ramp (tone varies with x). The texture phase
// is a function of y only, so the only seamless donor for a spot sits on the same
// row; a same-column donor has matching absolute tone but the wrong phase. A picker
// that matched mean colour would be pulled off-row by the ramp, so a same-row pick
// proves the search is texture-structure aware and brightness-robust.
func stripeRampImage(w, h, period int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		stripe := 60 * math.Sin(2*math.Pi*float64(y)/float64(period))
		for x := range w {
			ramp := -40 + 80*float64(x)/float64(w)
			v := uint8(clampF(128+stripe+ramp, 0, 255))
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
	return img
}

func TestSuggestHealSourceTexture(t *testing.T) {
	const period = 16
	img := stripeRampImage(240, 240, period)
	e := &edit.Params{}
	spot := edit.Spot{CX: 0.5, CY: 0.5, Radius: 0.05}
	sx, sy := SuggestHealSource(img, e, spot)

	sx2, sy2 := SuggestHealSource(img, e, spot)
	if sx != sx2 || sy != sy2 {
		t.Fatalf("must be deterministic: (%v,%v) vs (%v,%v)", sx, sy, sx2, sy2)
	}
	// The donor must land on the same stripe phase as the spot — within a
	// quarter period in y — despite the competing brightness ramp in x.
	if dy := math.Abs(sy-0.5) * 240; dy > period/4 {
		t.Errorf("donor drifted off the spot's stripe phase: |Δy|=%.1fpx (period %d)", dy, period)
	}
	// It must still move off the spot (a real, distinct source was chosen).
	f := newMaskFrame(240, 240, e)
	long := math.Max(f.frameW, f.frameH)
	scx, scy := f.outputPoint(sx*f.frameW, sy*f.frameH)
	dcx, dcy := f.outputPoint(spot.CX*f.frameW, spot.CY*f.frameH)
	if d := math.Hypot(scx-dcx, scy-dcy); d < 2*spot.Radius*long {
		t.Errorf("suggested source overlaps the spot: distance %v", d)
	}
}

// TestApplyHealResolutionStable checks a spot heals to the same tone whether
// rendered large or downscaled — spot geometry is resolution independent.
func TestApplyHealResolutionStable(t *testing.T) {
	mk := func(w, h int) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		fillFlat(img, 120)
		fillDisc(img, float64(w)*0.75, float64(h)*0.5, float64(w)*0.12, 210)
		fillDisc(img, float64(w)*0.25, float64(h)*0.5, float64(w)*0.05, 20)
		return img
	}
	e := &edit.Params{Spots: []edit.Spot{{
		CX: 0.25, CY: 0.5, Radius: 0.05, SX: 0.75, SY: 0.5, Feather: 0.2,
	}}}
	big := mk(400, 300)
	small := mk(200, 150)
	ApplyHeal(big, e, nil, nil)
	ApplyHeal(small, e, nil, nil)
	gb := lumaAt(big, 100, 150)
	gs := lumaAt(small, 50, 75)
	if diff := gb - gs; diff < -12 || diff > 12 {
		t.Errorf("heal tone drifted across resolution: big=%d small=%d", gb, gs)
	}
}

// TestApplyFillCompositesPatch pins the fill composite: a cached patch keyed
// by the spot's SpotFillKey blends into the disc (feathered, so the center
// carries the patch color) and leaves everything outside the disc untouched.
func TestApplyFillCompositesPatch(t *testing.T) {
	img := gradientImage(120, 90)
	before := clonePix(img)
	e := &edit.Params{Spots: []edit.Spot{
		{Mode: edit.SpotFill, CX: 0.5, CY: 0.5, Radius: 0.1, Feather: 0.2},
	}}
	s := &e.Spots[0]

	// A constant green patch spanning the spot's window at an arbitrary
	// resolution — the composite must resample it, not assume the render size.
	patch := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := 0; i < len(patch.Pix); i += 4 {
		patch.Pix[i], patch.Pix[i+1], patch.Pix[i+2], patch.Pix[i+3] = 10, 200, 30, 255
	}
	fills := FillSet{e.SpotFillKey(s): &FillPatch{Img: patch}}

	ApplyHeal(img, e, nil, fills)

	// Center of the disc: the patch color at full weight.
	co := img.PixOffset(60, 45)
	if img.Pix[co+1] < 150 || img.Pix[co] > 60 {
		t.Errorf("disc center did not take the patch color: rgb=%d,%d,%d",
			img.Pix[co], img.Pix[co+1], img.Pix[co+2])
	}
	// Outside the disc (radius is 0.1 of the 120 long edge = 12px): untouched.
	oo := img.PixOffset(90, 45)
	for c := 0; c < 3; c++ {
		if img.Pix[oo+c] != before[oo+c] {
			t.Fatalf("pixel outside the disc changed: %d -> %d", before[oo+c], img.Pix[oo+c])
		}
	}
	// A disabled fill spot composites nothing.
	img2 := gradientImage(120, 90)
	before2 := clonePix(img2)
	e.Spots[0].Disabled = true
	ApplyHeal(img2, e, nil, fills)
	for i := range before2 {
		if img2.Pix[i] != before2[i] {
			t.Fatalf("disabled fill spot changed pixel %d", i)
		}
	}
}

// TestSpotFillWindowDeterminism pins that generation and composite agree on
// the window: same normalized spot, same fractions, at any aspect.
func TestSpotFillWindowDeterminism(t *testing.T) {
	s := &edit.Spot{Mode: edit.SpotFill, CX: 0.5, CY: 0.5, Radius: 0.05}
	for _, aspect := range []float64{1.5, 1 / 1.5, 1} {
		x0, y0, x1, y1 := SpotFillWindow(aspect, s)
		x0b, y0b, x1b, y1b := SpotFillWindow(aspect, s)
		if x0 != x0b || y0 != y0b || x1 != x1b || y1 != y1b {
			t.Fatalf("window not deterministic at aspect %v", aspect)
		}
		if x1 <= x0 || y1 <= y0 {
			t.Fatalf("degenerate window at aspect %v: %v %v %v %v", aspect, x0, y0, x1, y1)
		}
		// The window must contain the spot region with real context margin.
		if x0 > 0.5-0.05 || x1 < 0.5+0.05 || y0 > 0.5-0.05 || y1 < 0.5+0.05 {
			t.Fatalf("window does not cover the spot at aspect %v: %v %v %v %v", aspect, x0, y0, x1, y1)
		}
	}
}

// --- Mask removals ---

// maskFillFixture builds a Remove mask over a centered label rectangle plus a
// constant-colour patch spanning its window, at a resolution unrelated to the
// render's so the composite has to resample rather than assume.
func maskFillFixture(colour [3]uint8) (*edit.Params, AIMapSet, FillSet) {
	e := &edit.Params{Masks: []edit.Mask{
		{Type: edit.MaskAI, AIKind: edit.AIPerson, MapVer: "rfdetr-1", ClassID: 2, Remove: true, Feather: 0.3},
	}}
	ai := personMap(100, 100, 40, 30, 60, 70)
	patch := image.NewRGBA(image.Rect(0, 0, 48, 64))
	for i := 0; i < len(patch.Pix); i += 4 {
		patch.Pix[i], patch.Pix[i+1], patch.Pix[i+2], patch.Pix[i+3] = colour[0], colour[1], colour[2], 255
	}
	return e, ai, FillSet{e.MaskFillKey(&e.Masks[0]): &FillPatch{Img: patch}}
}

func TestApplyMaskFillCompositesPatch(t *testing.T) {
	e, ai, fills := maskFillFixture([3]uint8{10, 200, 30})
	img := gradientImage(120, 120)
	before := clonePix(img)
	ApplyHeal(img, e, ai, fills)

	// Region center (frame fraction .5,.5 → px 60,60): the patch colour.
	co := img.PixOffset(60, 60)
	if img.Pix[co+1] < 150 || img.Pix[co] > 60 {
		t.Errorf("region center did not take the patch: rgb=%d,%d,%d",
			img.Pix[co], img.Pix[co+1], img.Pix[co+2])
	}
	// Well outside the region (and its feather): untouched.
	oo := img.PixOffset(5, 5)
	for c := 0; c < 3; c++ {
		if img.Pix[oo+c] != before[oo+c] {
			t.Fatalf("pixel far outside the region changed: %d -> %d", before[oo+c], img.Pix[oo+c])
		}
	}
}

func TestApplyMaskFillNoPatchIsNoOp(t *testing.T) {
	// The neutral contract every stage shares: a removal whose patch has not
	// been generated yet (or whose map is missing) must leave the buffer
	// byte-identical, never render a hole.
	e, ai, _ := maskFillFixture([3]uint8{10, 200, 30})
	for _, c := range []struct {
		name  string
		ai    AIMapSet
		fills FillSet
	}{
		{"no patch", ai, nil},
		{"no map", nil, nil},
	} {
		img := gradientImage(120, 120)
		before := clonePix(img)
		ApplyHeal(img, e, c.ai, c.fills)
		for i := range before {
			if img.Pix[i] != before[i] {
				t.Fatalf("%s: changed pixel %d: %d -> %d", c.name, i, before[i], img.Pix[i])
			}
		}
	}
	// A hidden removal composites nothing even with its patch in hand.
	e2, ai2, fills2 := maskFillFixture([3]uint8{10, 200, 30})
	e2.Masks[0].Disabled = true
	img := gradientImage(120, 120)
	before := clonePix(img)
	ApplyHeal(img, e2, ai2, fills2)
	for i := range before {
		if img.Pix[i] != before[i] {
			t.Fatalf("disabled removal changed pixel %d", i)
		}
	}
}

func TestApplyMaskFillAnchoredToContent(t *testing.T) {
	// Masks live in oriented-frame fractions, so a removal must land on the
	// same image content at every render size — the mask_test anchoring
	// contract, now covering the pre-look fill stage.
	e, ai, fills := maskFillFixture([3]uint8{10, 200, 30})
	big := gradientImage(240, 240)
	small := gradientImage(120, 120)
	ApplyHeal(big, e, ai, fills)
	ApplyHeal(small, e, ai, fills)
	for _, p := range []struct{ x, y float64 }{{0.5, 0.5}, {0.45, 0.4}, {0.1, 0.1}} {
		bo := big.PixOffset(int(p.x*240), int(p.y*240))
		so := small.PixOffset(int(p.x*120), int(p.y*120))
		bg := big.Pix[bo+1] > 150 && big.Pix[bo] < 60
		sg := small.Pix[so+1] > 150 && small.Pix[so] < 60
		if bg != sg {
			t.Errorf("fraction %.2f,%.2f: filled=%v at 240px but %v at 120px", p.x, p.y, bg, sg)
		}
	}
}

func TestApplyMaskFillsBeforeSpots(t *testing.T) {
	// Removals composite first so a retouch spot can heal a seam the inpaint
	// left; the reverse order would overwrite the repair.
	e, ai, fills := maskFillFixture([3]uint8{10, 200, 30})
	// A clone spot covering the region center, sourced from a corner.
	e.Spots = []edit.Spot{{Mode: edit.SpotClone, CX: 0.5, CY: 0.5, Radius: 0.08, SX: 0.1, SY: 0.1}}
	img := gradientImage(120, 120)
	ApplyHeal(img, e, ai, fills)
	co := img.PixOffset(60, 60)
	if img.Pix[co+1] > 150 && img.Pix[co] < 60 {
		t.Error("the spot did not run after the removal: center still shows the patch")
	}
}
