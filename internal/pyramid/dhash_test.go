package pyramid

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// gradientScene draws a smooth two-axis gradient with a bright disc at
// (cx, cy) — enough structure that moving the disc flips hash bits while a
// plain exposure shift flips none. flipped reverses the gradient direction,
// standing in for a genuinely different composition.
func gradientScene(w, h, cx, cy, r int, flipped bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gx, gy := x, y
			if flipped {
				gx, gy = w-1-x, h-1-y
			}
			v := uint8((gx*160/w + gy*80/h) % 256)
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy < r*r {
				v = 250
			}
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

func brightened(src *image.RGBA, delta int) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(b)
	for i := 0; i < len(src.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			out.Pix[i+c] = uint8(math.Min(255, float64(int(src.Pix[i+c])+delta)))
		}
		out.Pix[i+3] = 255
	}
	return out
}

func TestDHashStableUnderExposureAndScale(t *testing.T) {
	base := gradientScene(300, 200, 90, 100, 40, false)
	h1, ok := DHash(base)
	if !ok {
		t.Fatal("hash not computed")
	}
	if h2, _ := DHash(base); h2 != h1 {
		t.Fatalf("hash not deterministic: %x vs %x", h1, h2)
	}
	// Same scene rendered at a different thumb size (camera vs pyramid).
	small := image.NewRGBA(image.Rect(0, 0, 150, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 150; x++ {
			small.SetRGBA(x, y, base.RGBAAt(x*2, y*2))
		}
	}
	hs, _ := DHash(small)
	if d := HammingDist(h1, hs); d > 6 {
		t.Errorf("rescale moved hash %d bits", d)
	}
	// Exposure drift between burst frames must not move the hash much.
	hb, _ := DHash(brightened(base, 25))
	if d := HammingDist(h1, hb); d > 4 {
		t.Errorf("brightening moved hash %d bits", d)
	}
}

func TestDHashSeparatesCompositions(t *testing.T) {
	a, _ := DHash(gradientScene(300, 200, 90, 100, 40, false))
	// Same scene, subject nudged slightly — a burst re-frame — stays close.
	near, _ := DHash(gradientScene(300, 200, 100, 104, 40, false))
	if d := HammingDist(a, near); d > 10 {
		t.Errorf("burst re-frame distance %d, want <= 10", d)
	}
	// Subject moved across the frame — a recomposition — lands far away.
	far, _ := DHash(gradientScene(300, 200, 240, 40, 40, true))
	if d := HammingDist(a, far); d <= 10 {
		t.Errorf("recomposition distance %d, want > 10", d)
	}
}

func TestDHashDegenerate(t *testing.T) {
	if _, ok := DHash(image.NewRGBA(image.Rect(0, 0, 4, 4))); ok {
		t.Error("tiny image should not hash")
	}
}
