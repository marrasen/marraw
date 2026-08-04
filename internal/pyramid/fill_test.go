package pyramid

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// personMap is a label plane with instance 2 filling a centered rectangle —
// the shape a person-instance model hands us, at map resolution.
func personMap(w, h int, x0, y0, x1, y1 int) AIMapSet {
	pix := make([]uint8, w*h)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			pix[y*w+x] = 2
		}
	}
	return AIMapSet{aiSetKey(edit.AIPerson, "rfdetr-1"): &AIMap{Pix: pix, W: w, H: h, Key: "p|rfdetr-1"}}
}

func personRemoveMask() *edit.Mask {
	return &edit.Mask{Type: edit.MaskAI, AIKind: edit.AIPerson, MapVer: "rfdetr-1", ClassID: 2, Remove: true}
}

func TestMaskFillRegionFromAIMap(t *testing.T) {
	ai := personMap(100, 100, 40, 30, 60, 70)
	r, ok := MaskFillRegion(personRemoveMask(), 100, 100, ai)
	if !ok {
		t.Fatal("region must derive from a present map")
	}
	// The label rectangle is 20x40 of 100x100 = 8% of the frame, grown by the
	// dilation margin on every side.
	if r.Area < 0.08 || r.Area > 0.14 {
		t.Errorf("area %.3f outside the expected dilated range", r.Area)
	}
	// Bounds must bracket the label rectangle, and only by the dilation.
	if r.X0 > 0.40 || r.X1 < 0.60 || r.Y0 > 0.30 || r.Y1 < 0.70 {
		t.Errorf("bounds %.2f,%.2f..%.2f,%.2f do not contain the label", r.X0, r.Y0, r.X1, r.Y1)
	}
	if r.X0 < 0.33 || r.X1 > 0.67 || r.Y0 < 0.23 || r.Y1 > 0.77 {
		t.Errorf("bounds %.2f,%.2f..%.2f,%.2f grew far beyond the dilation", r.X0, r.Y0, r.X1, r.Y1)
	}
	// The plane is binary: the model's mask has no partial coverage to honour.
	for i, v := range r.Plane {
		if v != 0 && v != 255 {
			t.Fatalf("plane[%d] = %d, want 0 or 255", i, v)
		}
	}
	// Dilation must grow the region, never shrink it: every labelled pixel
	// stays inside.
	if r.Plane[50*r.W+50] == 0 {
		t.Error("a labelled pixel fell out of the region")
	}
}

func TestMaskFillRegionIgnoresFeather(t *testing.T) {
	// Feather softens the composite edge only — baking it into the region
	// would make every feather tweak cost an inference (edit.MaskFillKey).
	ai := personMap(100, 100, 40, 30, 60, 70)
	soft := personRemoveMask()
	soft.Feather = 0.9
	a, _ := MaskFillRegion(personRemoveMask(), 100, 100, ai)
	b, ok := MaskFillRegion(soft, 100, 100, ai)
	if !ok {
		t.Fatal("feathered mask must still derive a region")
	}
	if a.Area != b.Area || a.X0 != b.X0 || a.X1 != b.X1 {
		t.Errorf("feather changed the region: %.4f/%.2f vs %.4f/%.2f", a.Area, a.X0, b.Area, b.X0)
	}
}

func TestMaskFillRegionRefusals(t *testing.T) {
	ai := personMap(100, 100, 40, 30, 60, 70)
	// No map on disk yet: the caller must be told, not handed an empty region.
	if _, ok := MaskFillRegion(personRemoveMask(), 100, 100, nil); ok {
		t.Error("a missing map must not derive a region")
	}
	// An ineligible type must never reach the model.
	if _, ok := MaskFillRegion(&edit.Mask{Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.3, Remove: true}, 100, 100, ai); ok {
		t.Error("a radial mask must not derive a region")
	}
	// A label nothing matches leaves nothing to inpaint.
	empty := personRemoveMask()
	empty.ClassID = 7
	if _, ok := MaskFillRegion(empty, 100, 100, ai); ok {
		t.Error("an empty region must report not-ok")
	}
}

func TestMaskFillRegionBrushAspect(t *testing.T) {
	m := &edit.Mask{Type: edit.MaskBrush, Remove: true,
		Strokes: []edit.Stroke{{Radius: 0.05, Pts: []float64{0.5, 0.5}}}}
	r, ok := MaskFillRegion(m, 300, 200, nil)
	if !ok {
		t.Fatal("a painted brush must derive a region without any map")
	}
	// The stamp is centered and small; the plane is shaped by the frame aspect.
	if r.W <= r.H {
		t.Errorf("plane %dx%d does not follow the 3:2 frame", r.W, r.H)
	}
	if r.Area > 0.05 {
		t.Errorf("a single small stamp covered %.3f of the frame", r.Area)
	}
	if r.X0 > 0.5 || r.X1 < 0.5 || r.Y0 > 0.5 || r.Y1 < 0.5 {
		t.Errorf("bounds %.2f,%.2f..%.2f,%.2f miss the stamp center", r.X0, r.Y0, r.X1, r.Y1)
	}
}

func TestMaskFillWindowPadsAndClamps(t *testing.T) {
	ai := personMap(100, 100, 40, 30, 60, 70)
	r, _ := MaskFillRegion(personRemoveMask(), 100, 100, ai)
	x0, y0, x1, y1 := MaskFillWindow(1, r)
	if x0 >= r.X0 || x1 <= r.X1 || y0 >= r.Y0 || y1 <= r.Y1 {
		t.Errorf("window %.2f,%.2f..%.2f,%.2f does not contain the region", x0, y0, x1, y1)
	}
	if x0 < 0 || y0 < 0 || x1 > 1 || y1 > 1 {
		t.Errorf("window %.2f,%.2f..%.2f,%.2f left the frame", x0, y0, x1, y1)
	}
	// A frame-filling region must not blow the window past the frame.
	big := &MaskRegion{W: 4, H: 4, X0: 0, Y0: 0, X1: 1, Y1: 1, Area: 1}
	bx0, by0, bx1, by1 := MaskFillWindow(1, big)
	if bx0 != 0 || by0 != 0 || bx1 != 1 || by1 != 1 {
		t.Errorf("full-frame window = %.2f,%.2f..%.2f,%.2f, want the whole frame", bx0, by0, bx1, by1)
	}
}

func TestMaskFillMaskMarksTheRegion(t *testing.T) {
	ai := personMap(100, 100, 40, 30, 60, 70)
	r, _ := MaskFillRegion(personRemoveMask(), 100, 100, ai)
	// A window covering the whole frame keeps the mapping trivial to reason
	// about: mask pixel (x,y) is frame fraction (x/64, y/64).
	g := MaskFillMask(image.Rect(0, 0, 100, 100), 64, 64, 100, 100, r)
	if g.Pix[32*g.Stride+32] != 0 {
		t.Error("the region center must be marked for inpainting (0)")
	}
	if g.Pix[2*g.Stride+2] != 255 {
		t.Error("a corner far outside the region must be kept (255)")
	}
	// Nil region: nothing to repaint, so the model is asked to change nothing.
	n := MaskFillMask(image.Rect(0, 0, 100, 100), 8, 8, 100, 100, nil)
	for i, v := range n.Pix {
		if v != 255 {
			t.Fatalf("nil region marked pixel %d for inpainting", i)
		}
	}
}

func TestSpotFillWindowUnchangedByRefactor(t *testing.T) {
	// The window rule is a wire contract between generation and composite: a
	// spot's cached patches all assume the pre-refactor rect. This reimplements
	// the original inline formula and requires the shared helper to agree.
	original := func(aspect float64, s *edit.Spot) (x0, y0, x1, y1 float64) {
		fw, fh := 1.0, 1.0/aspect
		if aspect < 1 {
			fw, fh = aspect, 1.0
		}
		minX, maxX := s.CX*fw-s.Radius, s.CX*fw+s.Radius
		minY, maxY := s.CY*fh-s.Radius, s.CY*fh+s.Radius
		span := math.Max(maxX-minX, maxY-minY)
		pad := math.Max(span, 0.05)
		return clampF((minX-pad)/fw, 0, 1), clampF((minY-pad)/fh, 0, 1),
			clampF((maxX+pad)/fw, 0, 1), clampF((maxY+pad)/fh, 0, 1)
	}
	for _, aspect := range []float64{1.5, 1.0, 0.75} {
		for _, s := range []edit.Spot{
			{CX: 0.5, CY: 0.5, Radius: 0.02},
			{CX: 0.05, CY: 0.05, Radius: 0.02},
			{CX: 0.95, CY: 0.9, Radius: 0.05},
			{CX: 0.5, CY: 0.5, Radius: 0.1},
		} {
			wx0, wy0, wx1, wy1 := original(aspect, &s)
			x0, y0, x1, y1 := SpotFillWindow(aspect, &s)
			if !nearly(x0, wx0) || !nearly(y0, wy0) || !nearly(x1, wx1) || !nearly(y1, wy1) {
				t.Errorf("aspect %.2f spot %+v: got %.4f,%.4f..%.4f,%.4f want %.4f,%.4f..%.4f,%.4f",
					aspect, s, x0, y0, x1, y1, wx0, wy0, wx1, wy1)
			}
		}
	}
}

func nearly(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}
