package pyramid

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

const tiltTestVer = "depthany2s-test"

// depthRampMap builds a depth map that runs 0 (farthest) at the left edge to
// 255 (nearest) at the right, in the AIMapSet form a render consumes.
func depthRampMap(w, h int) AIMapSet {
	pix := make([]uint8, w*h)
	for y := range h {
		for x := range w {
			pix[y*w+x] = uint8(x * 255 / (w - 1))
		}
	}
	return AIMapSet{
		aiSetKey(edit.AIDepth, tiltTestVer): {Pix: pix, W: w, H: h, Key: "test-ramp"},
	}
}

// noiseRGBA fills a frame with a fine checker so local variance is a usable
// proxy for "is this region still sharp".
func noiseRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(40)
			if (x+y)%2 == 0 {
				v = 210
			}
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
		}
	}
	return img
}

// colVariance is the mean squared deviation of the red channel over a column
// band — high where the checker survives, near zero where it was blurred flat.
func colVariance(img *image.RGBA, x0, x1 int) float64 {
	b := img.Bounds()
	var sum, sum2, n float64
	for y := range b.Dy() {
		for x := x0; x < x1; x++ {
			v := float64(img.Pix[y*img.Stride+x*4])
			sum += v
			sum2 += v * v
			n++
		}
	}
	if n == 0 {
		return 0
	}
	mean := sum / n
	return sum2/n - mean*mean
}

func tiltParams(amount, lo, hi float64) *edit.Params {
	e := &edit.Params{TiltAmount: amount, TiltLo: lo, TiltHi: hi, TiltMapVer: tiltTestVer}
	e.Normalize()
	return e
}

// TestTiltKeepsTheFocusBandSharp is the headline contract: the depth window
// comes through byte-identical while the far end of the ramp goes flat.
func TestTiltKeepsTheFocusBandSharp(t *testing.T) {
	const w, h = 240, 120
	ai := depthRampMap(64, 32)
	// Keep the near end (right side, depth → 1) sharp.
	e := tiltParams(1, 0.9, 1)

	before := noiseRGBA(w, h)
	img := noiseRGBA(w, h)
	suppress := ApplyTilt(img, e, ai)

	// The rightmost columns sit at depth 1, inside the window: untouched.
	for y := range h {
		for x := w - 4; x < w; x++ {
			i := y*img.Stride + x*4
			if img.Pix[i] != before.Pix[i] {
				t.Fatalf("in-focus pixel (%d,%d) changed: %d → %d", x, y, before.Pix[i], img.Pix[i])
			}
		}
	}
	// The leftmost columns sit at depth 0, far outside it: flattened.
	if v := colVariance(img, 0, 16); v > 100 {
		t.Errorf("far region still sharp: variance %.0f (expected the checker gone)", v)
	}
	if v := colVariance(img, w-16, w); v < 3000 {
		t.Errorf("near region lost detail: variance %.0f (expected the checker intact)", v)
	}
	if suppress == nil {
		t.Fatal("a defocusing tilt must return a detail-suppression plane")
	}
	if suppress[0] == 0 {
		t.Error("the defocused corner must suppress the detail pass")
	}
	if suppress[w-1] != 0 {
		t.Errorf("the in-focus corner must not suppress the detail pass, got %d", suppress[w-1])
	}
}

// TestTiltGradesWithDepth is what separates this from a depth mask carrying a
// single blur radius: sharpness must fall off MONOTONICALLY with distance from
// the focus band, not step between two zones.
func TestTiltGradesWithDepth(t *testing.T) {
	const w, h = 320, 80
	img := noiseRGBA(w, h)
	ApplyTilt(img, tiltParams(1, 0.95, 1), depthRampMap(80, 20))

	// Sample bands walking away from the in-focus right edge.
	bands := []float64{}
	for x := w - 40; x >= 0; x -= 40 {
		bands = append(bands, colVariance(img, x, x+40))
	}
	for i := 1; i < len(bands); i++ {
		// Allow a small tolerance: the box-blur levels are an approximation,
		// not an analytic ramp.
		if bands[i] > bands[i-1]*1.05+50 {
			t.Errorf("sharpness must fall off with depth distance, band %d (%.0f) > band %d (%.0f)",
				i, bands[i], i-1, bands[i-1])
		}
	}
	if bands[0] < bands[len(bands)-1]*4 {
		t.Errorf("expected a wide sharpness range across the ramp, got %.0f → %.0f",
			bands[0], bands[len(bands)-1])
	}
}

// TestTiltDoesNotBleedAcrossTheFocusEdge is the anti-halo property the
// weight-normalized gather exists for: a bright in-focus subject must not
// print a ghost of itself into the dark defocused region behind it. Without
// the per-level weighting this is the tell of every cheap fake bokeh.
func TestTiltDoesNotBleedAcrossTheFocusEdge(t *testing.T) {
	const w, h = 200, 60
	// Depth map: the right half is near (in focus), the left half far.
	pix := make([]uint8, 50*15)
	for y := range 15 {
		for x := range 50 {
			if x >= 25 {
				pix[y*50+x] = 255
			}
		}
	}
	ai := AIMapSet{aiSetKey(edit.AIDepth, tiltTestVer): {Pix: pix, W: 50, H: 15, Key: "test-step"}}

	// Pixels: a blazing white subject on the near half, black on the far half.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(0)
			if x >= w/2 {
				v = 255
			}
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
		}
	}
	ApplyTilt(img, tiltParams(1, 0.9, 1), ai)

	// Well inside the far (blurred, black) half the gather has only black
	// neighbours to average, so it must stay black. A naive blur-then-blend
	// would have smeared the white subject a long way into it.
	for y := range h {
		for x := range w/2 - 12 {
			if v := img.Pix[y*img.Stride+x*4]; v > 8 {
				t.Fatalf("subject bled into the defocused region at (%d,%d): %d", x, y, v)
			}
		}
	}
}

// TestTiltNoopsWithoutAMap: a sidecar from another machine, or a map made by a
// different model version, renders without the effect rather than failing.
func TestTiltNoopsWithoutAMap(t *testing.T) {
	for name, ai := range map[string]AIMapSet{
		"no maps at all": nil,
		"wrong version":  {aiSetKey(edit.AIDepth, "other-model"): {Pix: make([]uint8, 64), W: 8, H: 8, Key: "x"}},
		"wrong map kind": {aiSetKey(edit.AISubject, tiltTestVer): {Pix: make([]uint8, 64), W: 8, H: 8, Key: "x"}},
	} {
		before := noiseRGBA(64, 32)
		img := noiseRGBA(64, 32)
		if s := ApplyTilt(img, tiltParams(1, 0.4, 0.6), ai); s != nil {
			t.Errorf("%s: expected no suppression plane", name)
		}
		for i := range img.Pix {
			if img.Pix[i] != before.Pix[i] {
				t.Fatalf("%s: frame must be untouched without a map", name)
			}
		}
	}
}

// TestTiltIsResolutionIndependent: the fixed working buffer means a draft, a
// settle and an export must agree on the effect, or the background visibly
// re-renders as the preview settles. Compared as a downscaled mean per band.
func TestTiltIsResolutionIndependent(t *testing.T) {
	e := tiltParams(0.8, 0.8, 1)
	ai := depthRampMap(128, 64)

	means := func(long int) []float64 {
		w, h := long, long/2
		img := noiseRGBA(w, h)
		ApplyTilt(img, e, ai)
		out := make([]float64, 8)
		for b := range out {
			x0, x1 := b*w/8, (b+1)*w/8
			var sum, n float64
			for y := range h {
				for x := x0; x < x1; x++ {
					sum += float64(img.Pix[y*img.Stride+x*4])
					n++
				}
			}
			out[b] = sum / n
		}
		return out
	}
	draft := means(1024)  // at the working resolution
	settle := means(2048) // above it: downscaled into the same buffer

	for b := range draft {
		if d := math.Abs(draft[b] - settle[b]); d > 6 {
			t.Errorf("band %d differs between 1024 and 2048 renders: %.1f vs %.1f",
				b, draft[b], settle[b])
		}
	}
}

// TestTiltBelowOnePixelIsAFullNoop: a dial too low to reach a single working
// pixel must not claim the render's sharpness through the suppression plane
// (applyMaskFX's `destroyed` tracking, for the same reason).
func TestTiltBelowOnePixelIsAFullNoop(t *testing.T) {
	before := noiseRGBA(200, 100)
	img := noiseRGBA(200, 100)
	// 0.0001 × 0.06 × 200 ≪ 1 px.
	if s := ApplyTilt(img, tiltParams(0.0001, 0.9, 1), depthRampMap(64, 32)); s != nil {
		t.Error("a sub-pixel radius must return no suppression plane")
	}
	for i := range img.Pix {
		if img.Pix[i] != before.Pix[i] {
			t.Fatal("a sub-pixel radius must leave the frame untouched")
		}
	}
}

// BenchmarkTilt1024 is the interactive draft, where the cost lands on every
// frame of a slider drag — BenchmarkMaskFX1024's counterpart. Most of it is the
// blur levels, which is why tiltLevelDims exists.
func BenchmarkTilt1024(b *testing.B) {
	e := tiltParams(1, 0.6, 0.9)
	ai := depthRampMap(1024, 683)
	img := noiseRGBA(1024, 683)
	src := append([]uint8(nil), img.Pix...)
	b.ResetTimer()
	for b.Loop() {
		copy(img.Pix, src)
		ApplyTilt(img, e, ai)
	}
}

// TestMergeSuppression: the mask stage and the tilt stage both suppress, and
// ApplyDetail sees the stronger of the two per pixel.
func TestMergeSuppression(t *testing.T) {
	if got := mergeSuppression(nil, nil); got != nil {
		t.Error("two absent planes must merge to nil")
	}
	b := []uint8{5, 9}
	if got := mergeSuppression(nil, b); &got[0] != &b[0] {
		t.Error("a single plane must pass through without copying")
	}
	a := []uint8{7, 2}
	got := mergeSuppression(a, []uint8{5, 9})
	if got[0] != 7 || got[1] != 9 {
		t.Errorf("expected the per-pixel max, got %v", got)
	}
}
