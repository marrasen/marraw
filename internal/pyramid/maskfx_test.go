package pyramid

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// fxImage is a smooth gradient with a bright disc, so a defocus has something
// to spread and a streak pass has something above the highlight knee.
func fxImage(w, h int) *image.RGBA {
	img := smoothImage(w, h)
	cx, cy := float64(w)*0.3, float64(h)*0.4
	r := float64(min(w, h)) * 0.08
	for y := range h {
		for x := range w {
			if math.Hypot(float64(x)-cx, float64(y)-cy) > r {
				continue
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 250, 245, 235
		}
	}
	return img
}

// checkerImage is a hard 16 px checker — maximal high-frequency content, and
// the worst case for averaging in the wrong colour space.
func checkerImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			i := img.PixOffset(x, y)
			v := uint8(20)
			if (x/16+y/16)%2 == 0 {
				v = 240
			}
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
	return img
}

func fullMask(a edit.MaskAdjust) *edit.Params {
	return &edit.Params{Masks: []edit.Mask{
		{Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 1.5, RY: 1.5, Adjust: a},
	}}
}

// TestMaskFXNeutralNoOp is the no-regression contract: an edit that asks for no
// spatial effect must leave the buffer byte-identical, so every pre-FX render
// still produces the pixels it always did.
func TestMaskFXNeutralNoOp(t *testing.T) {
	img := fxImage(96, 72)
	before := clonePix(img)
	// FX all zero, tone all zero: skipped as neutral.
	ApplyMasks(img, fullMask(edit.MaskAdjust{}), nil)
	// An inert angle is not an effect either.
	ApplyMasks(img, fullMask(edit.MaskAdjust{FXAngle: 90}), nil)
	for i := range before {
		if img.Pix[i] != before[i] {
			t.Fatalf("neutral FX changed pixel %d: %d -> %d", i, before[i], img.Pix[i])
		}
	}
}

// TestMaskFXBlurSoftens: a defocus must reduce local variation, and must do so
// only where the mask covers.
func TestMaskFXBlurSoftens(t *testing.T) {
	// A checker, not the gradient: blurring a linear ramp leaves its slope
	// alone, so a gradient would measure as "not blurred" however hard it was.
	plain := checkerImage(256, 192)
	blurred := checkerImage(256, 192)
	ApplyMasks(blurred, fullMask(edit.MaskAdjust{Blur: 0.6}), nil)

	v0 := localVariation(plain)
	v1 := localVariation(blurred)
	if v1 >= v0*0.2 {
		t.Errorf("blur barely softened: variation %.2f -> %.2f", v0, v1)
	}

	// A mask covering only the left half must leave the right half untouched.
	half := fxImage(256, 192)
	ref := clonePix(half)
	ApplyMasks(half, &edit.Params{Masks: []edit.Mask{{
		Type: edit.MaskLinear, X0: 0.45, Y0: 0.5, X1: 0.55, Y1: 0.5,
		Adjust: edit.MaskAdjust{Blur: 0.6},
	}}}, nil)
	for y := range 192 {
		for x := 200; x < 256; x++ {
			i := half.PixOffset(x, y)
			if half.Pix[i] != ref[i] {
				t.Fatalf("blur leaked outside the mask at (%d,%d): %d -> %d", x, y, ref[i], half.Pix[i])
			}
		}
	}
}

// localVariation is the mean absolute horizontal neighbour difference — a
// cheap stand-in for "how much detail is left".
func localVariation(img *image.RGBA) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var sum, n float64
	for y := range h {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 1; x < w; x++ {
			sum += math.Abs(float64(row[x*4]) - float64(row[(x-1)*4]))
			n++
		}
	}
	return sum / n
}

// TestMaskFXResolutionIndependence: the reaches are fractions of the frame's
// long edge, so the same FX rendered at two sizes must agree once downscaled.
// This is what catches a radius accidentally expressed in output pixels.
func TestMaskFXResolutionIndependence(t *testing.T) {
	e := fullMask(edit.MaskAdjust{Blur: 0.5, Mosaic: 0.3})

	big := fxImage(512, 384)
	ApplyMasks(big, e, nil)
	bigDown := scaleToLongEdge(big, 256)

	small := fxImage(256, 192)
	ApplyMasks(small, e, nil)

	var sum, count int
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
	}
	if mean := float64(sum) / float64(count); mean > 4 {
		t.Errorf("cross-resolution FX mean delta %.2f, want ≤ 4", mean)
	}
}

// TestMaskFXNoSubjectBleed is the quality contract the weight-normalized
// gather exists for: a saturated subject excluded by the mask must not smear
// into the blurred surround, and must itself come through untouched.
func TestMaskFXNoSubjectBleed(t *testing.T) {
	const w, h = 200, 200
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	// A red square in the middle; everything else stays black.
	const lo, hi = 80, 120
	for y := lo; y < hi; y++ {
		for x := lo; x < hi; x++ {
			img.Pix[img.PixOffset(x, y)] = 255
		}
	}
	before := clonePix(img)

	// An inverted radial mask over the square: the mask IS the surround.
	e := &edit.Params{Masks: []edit.Mask{{
		Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.16, RY: 0.16, Invert: true,
		Adjust: edit.MaskAdjust{Blur: 1},
	}}}
	ApplyMasks(img, e, nil)

	// The square's interior is at weight 0, so it must be bit-identical.
	for y := lo + 4; y < hi-4; y++ {
		for x := lo + 4; x < hi-4; x++ {
			i := img.PixOffset(x, y)
			if img.Pix[i] != before[i] {
				t.Fatalf("subject pixel (%d,%d) changed: %d -> %d", x, y, before[i], img.Pix[i])
			}
		}
	}
	// Far from the square the surround is pure black and must stay black: any
	// red out here is the subject bleeding through the gather.
	var worst uint8
	for y := range h {
		for x := range w {
			if math.Hypot(float64(x)-100, float64(y)-100) < 60 {
				continue
			}
			if v := img.Pix[img.PixOffset(x, y)]; v > worst {
				worst = v
			}
		}
	}
	if worst > 6 {
		t.Errorf("subject bled %d levels into the blurred surround, want ≤ 6", worst)
	}
}

// TestMaskFXInvertSymmetry: a mask and its inverse must affect disjoint
// regions — where one is at full weight the other must be the identity.
func TestMaskFXInvertSymmetry(t *testing.T) {
	base := fxImage(192, 144)
	a := fxImage(192, 144)
	b := fxImage(192, 144)
	mk := func(inv bool) *edit.Params {
		return &edit.Params{Masks: []edit.Mask{{
			Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.25, RY: 0.25, Invert: inv,
			Adjust: edit.MaskAdjust{Blur: 0.8},
		}}}
	}
	ApplyMasks(a, mk(false), nil)
	ApplyMasks(b, mk(true), nil)

	f := newMaskFrame(192, 144, &edit.Params{})
	ev := newMaskEvaluator(&mk(false).Masks[0], f, nil, nil)
	for y := 0; y < 144; y += 3 {
		for x := 0; x < 192; x += 3 {
			i := base.PixOffset(x, y)
			switch weightAt(ev, x, y, 192) {
			case 256: // fully inside: the inverted render must be untouched
				if b.Pix[i] != base.Pix[i] {
					t.Fatalf("inverted mask touched a full-weight pixel (%d,%d)", x, y)
				}
			case 0: // fully outside: the plain render must be untouched
				if a.Pix[i] != base.Pix[i] {
					t.Fatalf("mask touched a zero-weight pixel (%d,%d)", x, y)
				}
			}
		}
	}
}

// TestMaskFXCenterStability: the zoom-blur centre is a pure function of the
// mask's params — the same frame fractions at every render size, and under
// Invert, so a tile and an export streak from the same point.
func TestMaskFXCenterStability(t *testing.T) {
	strokes := []edit.Stroke{{Radius: 0.1, Feather: 0.4, Pts: []float64{0.25, 0.7, 0.35, 0.75}}}
	var want [2]float64
	for i, dim := range [][2]int{{256, 192}, {1024, 768}, {2048, 1536}} {
		for _, inv := range []bool{false, true} {
			m := &edit.Mask{Type: edit.MaskBrush, Strokes: strokes, Invert: inv}
			f := newMaskFrame(dim[0], dim[1], &edit.Params{})
			ev := newMaskEvaluator(m, f, nil, nil)
			x, y := maskFXCenter(m, ev)
			if i == 0 && !inv {
				want = [2]float64{x, y}
				if x > 0.5 || y < 0.5 {
					t.Fatalf("centroid (%.3f,%.3f) is not near the painted strokes", x, y)
				}
				continue
			}
			if math.Abs(x-want[0]) > 1e-9 || math.Abs(y-want[1]) > 1e-9 {
				t.Errorf("centre at %dx%d invert=%v = (%.6f,%.6f), want (%.6f,%.6f)",
					dim[0], dim[1], inv, x, y, want[0], want[1])
			}
		}
	}

	// A radial mask answers from the handle already on screen.
	rm := &edit.Mask{Type: edit.MaskRadial, CX: 0.3, CY: 0.8, RX: 0.2, RY: 0.2}
	f := newMaskFrame(256, 192, &edit.Params{})
	if x, y := maskFXCenter(rm, newMaskEvaluator(rm, f, nil, nil)); x != 0.3 || y != 0.8 {
		t.Errorf("radial centre = (%v,%v), want (0.3,0.8)", x, y)
	}
}

// TestMaskFXStreaksAreDirectional: the smear must follow FXAngle, expressed in
// oriented-frame degrees. A dot streaked at 0° spreads in x, at 90° in y.
func TestMaskFXStreaksAreDirectional(t *testing.T) {
	spread := func(angle float64) (sx, sy float64) {
		const w, h = 256, 256
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 0xff
		}
		for y := 124; y < 132; y++ {
			for x := 124; x < 132; x++ {
				i := img.PixOffset(x, y)
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 255, 255, 255
			}
		}
		ApplyMasks(img, fullMask(edit.MaskAdjust{Streaks: 0.6, FXAngle: angle}), nil)
		// Energy 40 px away along each axis.
		for d := 20; d < 50; d++ {
			sx += float64(img.Pix[img.PixOffset(128+d, 128)])
			sy += float64(img.Pix[img.PixOffset(128, 128+d)])
		}
		return sx, sy
	}
	if x, y := spread(0); x < y*3 {
		t.Errorf("0° streak: x-energy %.0f should dominate y-energy %.0f", x, y)
	}
	if x, y := spread(90); y < x*3 {
		t.Errorf("90° streak: y-energy %.0f should dominate x-energy %.0f", y, x)
	}
}

// TestMaskFXPreservesLinearEnergy: a defocus redistributes light, it does not
// destroy it. Mean LINEAR intensity must survive the round trip — this fails
// outright if the gather is done in display space, which is exactly the bug
// that makes streaks and bokeh read as dull grey.
func TestMaskFXPreservesLinearEnergy(t *testing.T) {
	img := checkerImage(192, 192)
	before := meanLinear(img)
	ApplyMasks(img, fullMask(edit.MaskAdjust{Blur: 0.5}), nil)
	after := meanLinear(img)
	if rel := math.Abs(after-before) / before; rel > 0.06 {
		t.Errorf("linear energy moved %.1f%% (%.0f -> %.0f), want ≤ 6%%", rel*100, before, after)
	}
}

func meanLinear(img *image.RGBA) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var sum float64
	for y := range h {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := range w {
			sum += float64(fxLin[row[x*4]])
		}
	}
	return sum / float64(w*h)
}

// TestMaskFXMosaicBlocks: the pixelate is flat inside a block and the block
// grid lands on the same frame fractions at two render sizes.
func TestMaskFXMosaicBlocks(t *testing.T) {
	// Every value change along a row must sit on the grid — which is anchored
	// and spaced in frame fractions, so the same multiples of `step` at any
	// render size. Comparing the two renders' edge LISTS directly would be
	// flaky (neighbouring blocks can happen to average to the same level and
	// drop an edge); pinning both to the predicted grid is the real property.
	const step = 0.5 * fxMosaicFrac
	check := func(w, h int) {
		img := fxImage(w, h)
		ApplyMasks(img, fullMask(edit.MaskAdjust{Mosaic: 0.5}), nil)
		y := h / 2
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		n := 0
		for x := 1; x < w; x++ {
			if row[x*4] == row[(x-1)*4] {
				continue
			}
			n++
			f := float64(x) / float64(max(w, h))
			if off := math.Abs(f/step - math.Round(f/step)); off*step > 0.006 {
				t.Errorf("%dx%d: block edge at fraction %.4f is off the %.3f grid", w, h, f, step)
			}
		}
		if n < 10 {
			t.Errorf("%dx%d: only %d block edges, want the row broken into blocks", w, h, n)
		}
	}
	check(512, 384)
	check(1024, 768)
}

// TestMaskFXGlowIsIsotropic: the bloom must spread a highlight evenly in every
// direction — that is the whole difference from the streak pass, which shares
// its extraction.
func TestMaskFXGlowIsIsotropic(t *testing.T) {
	const w, h = 256, 256
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	for y := 120; y < 136; y++ {
		for x := 120; x < 136; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 255, 255, 255
		}
	}
	ApplyMasks(img, fullMask(edit.MaskAdjust{Glow: 0.8}), nil)

	// Energy at the same distance along each axis must match.
	arm := func(dx, dy int) float64 {
		var sum float64
		for d := 14; d < 34; d++ {
			sum += float64(img.Pix[img.PixOffset(128+dx*d, 128+dy*d)])
		}
		return sum
	}
	arms := []float64{arm(1, 0), arm(-1, 0), arm(0, 1), arm(0, -1)}
	lo, hi := arms[0], arms[0]
	for _, v := range arms {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if lo <= 0 {
		t.Fatalf("glow reached no arm at all: %v", arms)
	}
	if hi > lo*1.25 {
		t.Errorf("glow is not isotropic: arm energies %v", arms)
	}
	// And it must actually add light, not just redistribute it.
	plain := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(plain.Pix, img.Pix)
	if arm(1, 0) <= 0 {
		t.Error("glow added no light beside the highlight")
	}
}

// TestMaskFXPrismSplitsChannels: red and blue must move in opposite radial
// directions, and green must not move at all — it carries the luminance, which
// is why the effect reads as a fringe rather than as a blur.
func TestMaskFXPrismSplitsChannels(t *testing.T) {
	const w, h = 256, 256
	mk := func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w {
				i := img.PixOffset(x, y)
				// A neutral grey field with one bright vertical bar well off
				// centre, so the radial displacement is large there.
				v := uint8(60)
				if x >= 200 && x < 210 {
					v = 240
				}
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
			}
		}
		return img
	}
	green := func(img *image.RGBA) []uint8 {
		out := make([]uint8, w)
		for x := range w {
			out[x] = img.Pix[img.PixOffset(x, 128)+1]
		}
		return out
	}
	base := mk()
	pos := mk()
	neg := mk()
	// A centred radial mask covering the frame, so the effect centre is (0.5,0.5).
	ApplyMasks(pos, fullMask(edit.MaskAdjust{Prism: 1}), nil)
	ApplyMasks(neg, fullMask(edit.MaskAdjust{Prism: -1}), nil)

	if !bytesEqual(green(base), green(pos)) {
		t.Error("prism moved the green channel; it must carry the luminance untouched")
	}
	// Centre of mass of the bar in R and B must sit on opposite sides.
	com := func(img *image.RGBA, off int) float64 {
		var num, den float64
		for x := 180; x < 235; x++ {
			v := float64(img.Pix[img.PixOffset(x, 128)+off]) - 60
			if v <= 0 {
				continue
			}
			num += v * float64(x)
			den += v
		}
		return num / den
	}
	rp, bp := com(pos, 0), com(pos, 2)
	if rp <= bp+1 {
		t.Errorf("prism +1: red centre %.1f should sit outboard of blue %.1f", rp, bp)
	}
	rn, bn := com(neg, 0), com(neg, 2)
	if rn >= bn-1 {
		t.Errorf("prism -1: red centre %.1f should sit inboard of blue %.1f", rn, bn)
	}
}

func bytesEqual(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMaskFXSuppressesDetail: a defocused region must come back with the
// global clarity pass damped, or clarity re-etches a rim into it.
func TestMaskFXSuppressesDetail(t *testing.T) {
	e := fullMask(edit.MaskAdjust{Blur: 1})
	suppress := ApplyMasks(fxImage(128, 96), e, nil)
	if suppress == nil {
		t.Fatal("a full defocus produced no detail-suppression plane")
	}
	var maxS uint8
	for _, v := range suppress {
		if v > maxS {
			maxS = v
		}
	}
	if maxS < 200 {
		t.Errorf("peak suppression %d, want ≥ 200 under a full defocus", maxS)
	}
	// Streaks add light rather than destroying detail, so they suppress nothing.
	if s := ApplyMasks(fxImage(128, 96), fullMask(edit.MaskAdjust{Streaks: 1}), nil); s != nil {
		t.Error("streaks should not suppress the detail stage")
	}
}

// TestMaskFXKeepsDetailUnderLight is the 1:1 contract. The gathers run at
// fxPlaneLongEdge whatever the render's size, so anything composited back OUT
// of that buffer is capped at 1024 px of detail. That is free for an effect
// that destroyed the detail anyway — but glow, streaks and prism do not: the
// background under a bloom is as sharp as the decode. Before the delta
// composite these threw every pixel of it away, which is what made a
// full-resolution tile look blocky and, to the eye, look like the 1:1 render
// never arrived.
func TestMaskFXKeepsDetailUnderLight(t *testing.T) {
	// Well past fxPlaneLongEdge, so the working buffer is a real downscale.
	const w, h = 3072, 2304
	base := localVariation(ditherImage(w, h))

	for _, tc := range []struct {
		name string
		a    edit.MaskAdjust
		keep float64 // fraction of the 1 px detail that must survive
	}{
		{"streaks", edit.MaskAdjust{Streaks: 0.2}, 0.9},
		{"glow", edit.MaskAdjust{Glow: 0.1}, 0.9},
		{"prism", edit.MaskAdjust{Prism: 0.6}, 0.9},
		{"the Background recipe", edit.MaskAdjust{Glow: 0.1, Streaks: 0.2, Prism: 0.6, FXAngle: 25}, 0.9},
		// The other half of the contract: a defocus still has to defocus.
		{"blur", edit.MaskAdjust{Blur: 0.45}, 0},
	} {
		img := ditherImage(w, h)
		ApplyMasks(img, fullMask(tc.a), nil)
		got := localVariation(img) / base
		if tc.keep == 0 {
			if got > 0.2 {
				t.Errorf("%s: kept %.0f%% of the detail, want it gone", tc.name, 100*got)
			}
			continue
		}
		if got < tc.keep {
			t.Errorf("%s: kept %.0f%% of the detail at %dx%d, want ≥ %.0f%%",
				tc.name, 100*got, w, h, 100*tc.keep)
		}
	}
}

// ditherImage carries its detail at ONE pixel — a frequency the 1024 working
// buffer cannot represent at all, so measuring it answers "did the render's own
// pixels come through?" and nothing else. A bright disc gives the light passes
// something above the highlight knee to work with; the mid-level dither leaves
// headroom so the added light does not clip the alternation away and read as
// lost detail.
func ditherImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	cx, cy := float64(w)*0.3, float64(h)*0.4
	rad := float64(min(w, h)) * 0.08
	for y := range h {
		for x := range w {
			i := img.PixOffset(x, y)
			v := 90
			if (x+y)%2 == 0 {
				v = 130
			}
			if math.Hypot(float64(x)-cx, float64(y)-cy) <= rad {
				v = 250
			}
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(v), uint8(v), uint8(v), 0xff
		}
	}
	return img
}

// TestMaskFXLightStillLands guards the other side of the delta composite: it
// must change what the low-frequency passes changed, only more sharply. A
// bloom that preserved the detail by doing nothing at all would pass the test
// above.
func TestMaskFXLightStillLands(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    edit.MaskAdjust
	}{
		{"glow", edit.MaskAdjust{Glow: 0.6}},
		{"streaks", edit.MaskAdjust{Streaks: 0.6}},
	} {
		// 2048: past fxPlaneLongEdge (delta composite) and 1024: at it (the
		// working buffer IS the render, so the replace path runs). The same
		// effect must read at both, or the settle changes the look.
		for _, size := range []int{1024, 2048} {
			img := fxImage(size, size*3/4)
			before := meanLevel(img)
			ApplyMasks(img, fullMask(tc.a), nil)
			after := meanLevel(img)
			if after-before < 1 {
				t.Errorf("%s at %d px: mean level %.2f -> %.2f, the light never landed",
					tc.name, size, before, after)
			}
		}
	}
}

func meanLevel(img *image.RGBA) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var sum float64
	for y := range h {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := range w {
			sum += float64(row[x*4]) + float64(row[x*4+1]) + float64(row[x*4+2])
		}
	}
	return sum / float64(3*w*h)
}

// TestFXEncodingRoundTrip: fxEnc must invert fxLin exactly, so a region the FX
// leaves alone survives the linearize/re-encode trip byte-identical.
func TestFXEncodingRoundTrip(t *testing.T) {
	for v := range 256 {
		if got := fxEnc[fxLin[v]]; int(got) != v {
			t.Fatalf("fxEnc[fxLin[%d]] = %d, want %d", v, got, v)
		}
	}
}

func BenchmarkMaskFX1024(b *testing.B) {
	e := fullMask(edit.MaskAdjust{Blur: 0.45, Streaks: 0.35})
	img := fxImage(1024, 683)
	src := clonePix(img)
	b.ResetTimer()
	for b.Loop() {
		copy(img.Pix, src)
		ApplyMasks(img, e, nil)
	}
}
