package pyramid

import (
	"image"
	"image/color"
	"testing"
)

// texturedImage paints a high-frequency checker — an in-focus stand-in.
func texturedImage(w, h, cell int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(60)
			if (x/cell+y/cell)%2 == 0 {
				v = 200
			}
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

// blurred returns a heavily box-blurred copy — the soft/missed-focus stand-in.
func blurred(src *image.RGBA, radius int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(b)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl, n int
			for dy := -radius; dy <= radius; dy++ {
				for dx := -radius; dx <= radius; dx++ {
					sx, sy := min(max(x+dx, 0), w-1), min(max(y+dy, 0), h-1)
					i := sy*src.Stride + sx*4
					r += int(src.Pix[i])
					g += int(src.Pix[i+1])
					bl += int(src.Pix[i+2])
					n++
				}
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255})
		}
	}
	return out
}

func TestSharpnessScoreSeparatesSharpFromSoft(t *testing.T) {
	sharp := texturedImage(512, 384, 3)
	soft := blurred(sharp, 6)

	ss := SharpnessScore(sharp)
	sb := SharpnessScore(soft)
	if ss < 10*sb {
		t.Errorf("sharp %.1f vs soft %.1f: want an order of magnitude apart", ss, sb)
	}
}

// splitFocusImage composites a "background sharp, subject soft" stand-in:
// left half textured/sharp, right half the blurred copy.
func splitFocusImage(w, h int) *image.RGBA {
	sharp := texturedImage(w, h, 3)
	soft := blurred(sharp, 6)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				img.SetRGBA(x, y, sharp.RGBAAt(x, y))
			} else {
				img.SetRGBA(x, y, soft.RGBAAt(x, y))
			}
		}
	}
	return img
}

// halfMatte builds an AIMap with one half at 255, split vertically (left/right)
// or horizontally (top/bottom).
func halfMatte(w, h int, vertical, firstHalf bool) *AIMap {
	pix := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			inFirst := x < w/2
			if !vertical {
				inFirst = y < h/2
			}
			if inFirst == firstHalf {
				pix[y*w+x] = 255
			}
		}
	}
	return &AIMap{Pix: pix, W: w, H: h}
}

func TestSubjectSharpnessScoreCatchesSoftSubject(t *testing.T) {
	img := splitFocusImage(512, 384)

	softSubject, ok := SubjectSharpnessScore(img, halfMatte(1024, 768, true, false), 0)
	if !ok {
		t.Fatal("soft-subject matte did not score")
	}
	sharpSubject, ok := SubjectSharpnessScore(img, halfMatte(1024, 768, true, true), 0)
	if !ok {
		t.Fatal("sharp-subject matte did not score")
	}
	if sharpSubject < 10*softSubject {
		t.Errorf("sharp subject %.1f vs soft subject %.1f: want an order of magnitude apart", sharpSubject, softSubject)
	}
	// The scenario the score exists for: the global score is propped up by the
	// sharp half and would never badge this frame.
	if global := SharpnessScore(img); global < 5*softSubject {
		t.Errorf("global %.1f vs soft subject %.1f: want the subject score well below", global, softSubject)
	}
}

// TestSubjectSharpnessScoreOrientation feeds the matte in the display frame of
// a 90° CW rotated photo (LibRaw flip 6) and checks it lands on the same
// region as the sensor-frame matte on the unrotated thumb.
func TestSubjectSharpnessScoreOrientation(t *testing.T) {
	img := splitFocusImage(512, 384) // sensor frame: sharp left half

	base, ok := SubjectSharpnessScore(img, halfMatte(1024, 768, true, true), 0)
	if !ok {
		t.Fatal("base matte did not score")
	}
	// After a 90° CW display rotation the sharp left half sits at the top, so
	// the display-frame matte covers the top half of a portrait map.
	rotated, ok := SubjectSharpnessScore(img, halfMatte(768, 1024, false, true), 6)
	if !ok {
		t.Fatal("display-frame matte did not score")
	}
	if diff := rotated/base - 1; diff > 0.05 || diff < -0.05 {
		t.Errorf("flip-6 score %.1f vs base %.1f: want within 5%%", rotated, base)
	}
}

func TestSubjectSharpnessScoreDegenerate(t *testing.T) {
	img := texturedImage(512, 384, 3)
	if _, ok := SubjectSharpnessScore(img, nil, 0); ok {
		t.Error("nil matte scored")
	}
	if _, ok := SubjectSharpnessScore(img, &AIMap{Pix: make([]uint8, 1024*768), W: 1024, H: 768}, 0); ok {
		t.Error("empty matte scored")
	}
	// A matte covering well under the coverage floor must refuse to score.
	tiny := &AIMap{Pix: make([]uint8, 1024*768), W: 1024, H: 768}
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			tiny.Pix[y*1024+x] = 255
		}
	}
	if _, ok := SubjectSharpnessScore(img, tiny, 0); ok {
		t.Error("sub-coverage matte scored")
	}
	// Transposed aspects that no flip code explains: better no score than a
	// mis-placed one.
	if _, ok := SubjectSharpnessScore(img, halfMatte(768, 1024, false, true), 0); ok {
		t.Error("aspect-mismatched matte scored")
	}
}

func TestSharpnessScoreDegenerate(t *testing.T) {
	if s := SharpnessScore(image.NewRGBA(image.Rect(0, 0, 2, 2))); s != 0 {
		t.Errorf("tiny image scored %v, want 0", s)
	}
	flat := image.NewRGBA(image.Rect(0, 0, 100, 100))
	if s := SharpnessScore(flat); s != 0 {
		t.Errorf("flat image scored %v, want 0", s)
	}
}
