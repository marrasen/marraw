package pyramid

import (
	"image"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// paintedClassMap builds a synthetic category map: category 3 fills the top
// half, category 7 a square in the bottom-right quadrant, 0 elsewhere.
func paintedClassMap(w, h int) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h/2; y++ {
		for x := 0; x < w; x++ {
			g.Pix[y*g.Stride+x] = 3
		}
	}
	for y := h * 3 / 4; y < h; y++ {
		for x := w * 3 / 4; x < w; x++ {
			g.Pix[y*g.Stride+x] = 7
		}
	}
	return g
}

func testStoreWithMap(t *testing.T, kind edit.AIKind, ver string, m *image.Gray) (*AIMapStore, string) {
	t.Helper()
	s := NewAIMapStore(t.TempDir())
	const photoKey = "abcdef123456"
	if err := s.Save(photoKey, kind, ver, m); err != nil {
		t.Fatal(err)
	}
	return s, photoKey
}

// TestAIMapStoreRoundTrip: Save → SetFor loads the map; a version mismatch or
// unknown kind loads nothing.
func TestAIMapStoreRoundTrip(t *testing.T) {
	s, key := testStoreWithMap(t, edit.AIClass, "m1", paintedClassMap(64, 48))
	e := &edit.Params{Masks: []edit.Mask{{
		Type: edit.MaskAI, AIKind: edit.AIClass, MapVer: "m1", ClassID: 3,
		Adjust: edit.MaskAdjust{ExpEV: 1},
	}}}
	set := s.SetFor(key, e)
	if len(set) != 1 {
		t.Fatalf("SetFor loaded %d maps, want 1", len(set))
	}
	am := set[aiSetKey(edit.AIClass, "m1")]
	if am == nil || am.W != 64 || am.H != 48 {
		t.Fatalf("unexpected map: %+v", am)
	}

	e.Masks[0].MapVer = "m2" // regenerated model: old file must not satisfy it
	if set := s.SetFor(key, e); len(set) != 0 {
		t.Error("version-mismatched map must not load")
	}
	var nilStore *AIMapStore
	if set := nilStore.SetFor(key, e); set != nil {
		t.Error("nil store must yield a nil set")
	}
}

// TestAIMaskAppliesInsideClassOnly: with a class mask, pixels inside the
// category region change and pixels outside stay untouched.
func TestAIMaskAppliesInsideClassOnly(t *testing.T) {
	s, key := testStoreWithMap(t, edit.AIClass, "m1", paintedClassMap(64, 48))
	e := &edit.Params{Masks: []edit.Mask{{
		Type: edit.MaskAI, AIKind: edit.AIClass, MapVer: "m1", ClassID: 3,
		Adjust: edit.MaskAdjust{ExpEV: 1.5},
	}}}

	img := smoothImage(128, 96)
	before := append([]uint8(nil), img.Pix...)
	ApplyMasks(img, e, s.SetFor(key, e))

	changed := func(x, y int) bool {
		i := y*img.Stride + x*4
		return img.Pix[i] != before[i] || img.Pix[i+1] != before[i+1] || img.Pix[i+2] != before[i+2]
	}
	// Deep inside the top-half region (category 3).
	if !changed(64, 20) {
		t.Error("pixel inside the class region did not change")
	}
	// Deep inside the bottom-left quadrant (category 0) and the category-7 box.
	if changed(16, 80) {
		t.Error("pixel outside the class region changed")
	}
	if changed(120, 90) {
		t.Error("pixel in a different category changed")
	}
}

// TestAIMaskMissingMapIsNoOp: an AI mask whose map is absent must contribute
// nothing (and certainly not fail the render).
func TestAIMaskMissingMapIsNoOp(t *testing.T) {
	e := &edit.Params{Masks: []edit.Mask{{
		Type: edit.MaskAI, AIKind: edit.AISubject, MapVer: "m1",
		Adjust: edit.MaskAdjust{ExpEV: 2},
	}}}
	img := smoothImage(64, 48)
	before := append([]uint8(nil), img.Pix...)
	ApplyMasks(img, e, nil)
	for i := range before {
		if img.Pix[i] != before[i] {
			t.Fatal("mask without a map changed pixels")
		}
	}
}

// TestAISubjectThresholdAndInvert: the subject matte thresholds around the
// default 0.5, and Invert flips the kept side.
func TestAISubjectThresholdAndInvert(t *testing.T) {
	// Left half strong subject (220), right half background (30).
	g := image.NewGray(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			v := uint8(30)
			if x < 32 {
				v = 220
			}
			g.Pix[y*g.Stride+x] = v
		}
	}
	s, key := testStoreWithMap(t, edit.AISubject, "m1", g)

	run := func(invert bool) *image.RGBA {
		e := &edit.Params{Masks: []edit.Mask{{
			Type: edit.MaskAI, AIKind: edit.AISubject, MapVer: "m1", Invert: invert,
			Adjust: edit.MaskAdjust{ExpEV: 1.5},
		}}}
		img := smoothImage(128, 96)
		ApplyMasks(img, e, s.SetFor(key, e))
		return img
	}
	ref := smoothImage(128, 96)

	plain := run(false)
	if plain.Pix[40*plain.Stride+30*4] == ref.Pix[40*ref.Stride+30*4] {
		t.Error("subject side did not change")
	}
	if plain.Pix[40*plain.Stride+100*4] != ref.Pix[40*ref.Stride+100*4] {
		t.Error("background side changed without invert")
	}

	inv := run(true)
	if inv.Pix[40*inv.Stride+30*4] != ref.Pix[40*ref.Stride+30*4] {
		t.Error("inverted mask still changed the subject side")
	}
	if inv.Pix[40*inv.Stride+100*4] == ref.Pix[40*ref.Stride+100*4] {
		t.Error("inverted mask did not change the background side")
	}
}

// TestAIDepthWindow: only pixels whose depth falls inside [lo,hi] change.
func TestAIDepthWindow(t *testing.T) {
	// Depth ramp left (far, 0) → right (near, 255).
	g := image.NewGray(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			g.Pix[y*g.Stride+x] = uint8(x * 255 / 63)
		}
	}
	s, key := testStoreWithMap(t, edit.AIDepth, "m1", g)
	e := &edit.Params{Masks: []edit.Mask{{
		Type: edit.MaskAI, AIKind: edit.AIDepth, MapVer: "m1",
		DepthLo: 0.7, DepthHi: 1.0, // keep the near third
		Adjust: edit.MaskAdjust{ExpEV: 1.5},
	}}}
	img := smoothImage(128, 96)
	before := append([]uint8(nil), img.Pix...)
	ApplyMasks(img, e, s.SetFor(key, e))

	near := 120*4 + 40*img.Stride // depth ≈ 0.94
	far := 8*4 + 40*img.Stride    // depth ≈ 0.06
	if img.Pix[near] == before[near] {
		t.Error("near pixel inside the depth window did not change")
	}
	if img.Pix[far] != before[far] {
		t.Error("far pixel outside the depth window changed")
	}
}

// TestAIMaskCrossResolution: AI masks sample a fixed-resolution plane, so two
// render sizes must agree after downscaling — the brush-mask guarantee.
func TestAIMaskCrossResolution(t *testing.T) {
	s, key := testStoreWithMap(t, edit.AIClass, "m1", paintedClassMap(256, 192))
	e := &edit.Params{Masks: []edit.Mask{{
		Type: edit.MaskAI, AIKind: edit.AIClass, MapVer: "m1", ClassID: 3, Feather: 0.5,
		Adjust: edit.MaskAdjust{ExpEV: 1.2, Saturation: 0.4},
	}}}
	ai := s.SetFor(key, e)

	big := smoothImage(512, 384)
	ApplyMasks(big, e, ai)
	bigDown := scaleToLongEdge(big, 256)

	small := smoothImage(256, 192)
	ApplyMasks(small, e, ai)

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
	if mean := float64(sum) / float64(count); mean > 1.5 {
		t.Errorf("cross-resolution mean diff %.2f (worst %d)", mean, worst)
	}
}

// TestAIMaskContentAnchoring: AI-mask weight at a fixed frame point survives
// crop + straighten, like every other mask type.
func TestAIMaskContentAnchoring(t *testing.T) {
	s, key := testStoreWithMap(t, edit.AIClass, "m1", paintedClassMap(200, 160))
	m := &edit.Mask{Type: edit.MaskAI, AIKind: edit.AIClass, MapVer: "m1", ClassID: 3, Feather: 0.3}
	e := &edit.Params{Masks: []edit.Mask{*m}}
	ai := s.SetFor(key, e)

	plain := newMaskFrame(1000, 800, &edit.Params{})
	evPlain := newMaskEvaluator(m, plain, ai)

	crop := &edit.Params{CropX: 0.2, CropY: 0.1, CropW: 0.6, CropH: 0.7, CropAngle: 5}
	outW, outH := crop.OutputDims(1000, 800)
	f := newMaskFrame(outW, outH, crop)
	ev := newMaskEvaluator(m, f, ai)

	for _, out := range [][2]int{{100, 100}, {300, 250}, {50, 400}, {500, 300}} {
		fx, fy := f.framePoint(float64(out[0]), float64(out[1]))
		px, py := int(fx-0.5), int(fy-0.5)
		if px < 0 || px >= 1000 || py < 0 || py >= 800 {
			continue
		}
		got := weightAt(ev, out[0], out[1], outW)
		want := weightAt(evPlain, px, py, 1000)
		if d := int(got) - int(want); d < -8 || d > 8 {
			t.Errorf("AI weight at frame point (%d,%d) changed under crop: %d vs %d", px, py, got, want)
		}
	}
}

// TestAIMaskSurvivesRotation: maps are stored in base orientation and
// rotated at load, so a quarter-rotate edit keeps the mask glued to content.
// Category 3 fills the TOP half of the base map; after a 90° CW display turn
// that content occupies the RIGHT half of the oriented frame.
func TestAIMaskSurvivesRotation(t *testing.T) {
	s, key := testStoreWithMap(t, edit.AIClass, "m1", paintedClassMap(200, 160))
	e := &edit.Params{Rotate: 1, Masks: []edit.Mask{{
		Type: edit.MaskAI, AIKind: edit.AIClass, MapVer: "m1", ClassID: 3,
		Adjust: edit.MaskAdjust{ExpEV: 1.5},
	}}}

	// Oriented frame of a 200×160 base is 160×200 after one turn.
	img := smoothImage(160, 200)
	before := append([]uint8(nil), img.Pix...)
	ApplyMasks(img, e, s.SetFor(key, e))

	changed := func(x, y int) bool {
		i := y*img.Stride + x*4
		return img.Pix[i] != before[i]
	}
	if !changed(130, 100) { // right half = base top half
		t.Error("rotated AI mask lost its content region")
	}
	if changed(30, 100) { // left half = base bottom half
		t.Error("rotated AI mask leaked outside its content region")
	}

	// FlipH mirrors: the region moves to the left half.
	e.FlipH = true
	img2 := smoothImage(160, 200)
	before2 := append([]uint8(nil), img2.Pix...)
	ApplyMasks(img2, e, s.SetFor(key, e))
	i := 100*img2.Stride + 30*4
	if img2.Pix[i] == before2[i] {
		t.Error("flipped AI mask lost its content region")
	}
}

// TestNormalizeAIMasks: canonicalization drops unknown kinds, zeroes
// irrelevant per-kind fields, and orders the depth window.
func TestNormalizeAIMasks(t *testing.T) {
	e := &edit.Params{Masks: []edit.Mask{
		{Type: edit.MaskAI, AIKind: edit.AIDepth, DepthLo: 0.9, DepthHi: 0.2, ClassID: 5, Threshold: 0.7},
		{Type: edit.MaskAI, AIKind: "nonsense"},
		{Type: edit.MaskLinear, X0: 0.1, Y0: 0.1, X1: 0.9, Y1: 0.9, AIKind: edit.AISubject, MapVer: "stale"},
	}}
	e.Normalize()
	if len(e.Masks) != 2 {
		t.Fatalf("normalize kept %d masks, want 2 (unknown AI kind dropped)", len(e.Masks))
	}
	d := e.Masks[0]
	if d.DepthLo != 0.2 || d.DepthHi != 0.9 {
		t.Errorf("depth window not ordered: %v..%v", d.DepthLo, d.DepthHi)
	}
	if d.ClassID != 0 || d.Threshold != 0 {
		t.Error("depth mask kept class/threshold fields")
	}
	l := e.Masks[1]
	if l.AIKind != "" || l.MapVer != "" {
		t.Error("linear mask kept AI fields")
	}
}
