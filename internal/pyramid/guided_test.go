package pyramid

import (
	"image"
	"image/color"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// TestGuidedRefinementSnapsToImageEdge: on a high-resolution render, an AI
// mask whose 200 px map boundary is misregistered ~60 output px from the
// image's real luminance edge must snap toward that edge, while plain
// bilinear sampling stays at the map boundary.
func TestGuidedRefinementSnapsToImageEdge(t *testing.T) {
	const outW, outH = 2400, 1600
	// Bright subject left of x=1200, dark background right of it.
	img := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			v := uint8(40)
			if x < 1200 {
				v = 200
			}
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	// Category-1 region covers map x < 95 → coarse boundary at output x≈1140,
	// 60 px short of the true edge at 1200.
	m200 := image.NewGray(image.Rect(0, 0, 200, 133))
	for y := 0; y < 133; y++ {
		for x := 0; x < 95; x++ {
			m200.Pix[y*m200.Stride+x] = 1
		}
	}
	s, key := testStoreWithMap(t, edit.AIClass, "m1", m200)
	mask := &edit.Mask{Type: edit.MaskAI, AIKind: edit.AIClass, MapVer: "m1", ClassID: 1,
		Adjust: edit.MaskAdjust{ExpEV: 1}}
	e := &edit.Params{Masks: []edit.Mask{*mask}}
	ai := s.SetFor(key, e)
	f := newMaskFrame(outW, outH, e)

	ev := newMaskEvaluator(mask, f, ai, img)
	if _, ok := ev.(*guidedEval); !ok {
		t.Fatalf("expected guided refinement to engage at %d px, got %T", outW, ev)
	}
	plain := newAIEval(mask, f, ai) // unrefined baseline

	const y = 800
	// Between the coarse boundary (1140) and the true edge (1200): bilinear
	// has already faded out; the refined weight must hold on (bright side).
	refined := weightAt(ev, 1170, y, outW)
	coarse := weightAt(plain, 1170, y, outW)
	if refined < 128 {
		t.Errorf("refined weight on the bright side of the image edge = %d, want >= 128", refined)
	}
	if coarse > 64 {
		t.Errorf("unrefined baseline unexpectedly high at 1170: %d", coarse)
	}
	// Past the true edge (dark side): the refinement must let go.
	if w := weightAt(ev, 1240, y, outW); w > 96 {
		t.Errorf("refined weight past the image edge = %d, want <= 96", w)
	}
	// Deep inside / far outside stay saturated / empty.
	if w := weightAt(ev, 400, y, outW); w < 200 {
		t.Errorf("deep inside the region = %d, want >= 200", w)
	}
	if w := weightAt(ev, 2000, y, outW); w > 16 {
		t.Errorf("far outside the region = %d, want <= 16", w)
	}
}

// TestGuidedSkipsSmallRenders: previews below the threshold keep the pure
// sampling path (interactive latency), even with a map that would qualify.
func TestGuidedSkipsSmallRenders(t *testing.T) {
	s, key := testStoreWithMap(t, edit.AIClass, "m1", paintedClassMap(200, 160))
	mask := &edit.Mask{Type: edit.MaskAI, AIKind: edit.AIClass, MapVer: "m1", ClassID: 3,
		Adjust: edit.MaskAdjust{ExpEV: 1}}
	e := &edit.Params{Masks: []edit.Mask{*mask}}
	ai := s.SetFor(key, e)
	img := image.NewRGBA(image.Rect(0, 0, 1024, 819))
	f := newMaskFrame(1024, 819, e)
	if ev := newMaskEvaluator(mask, f, ai, img); ev != nil {
		if _, guided := ev.(*guidedEval); guided {
			t.Error("guided refinement must not engage on a 1024 px render")
		}
	}
}
