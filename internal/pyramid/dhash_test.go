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
	h1 := DHash(base)
	if h2 := DHash(base); h2 != h1 {
		t.Fatalf("hash not deterministic: %x vs %x", h1, h2)
	}
	// Same scene rendered at a different thumb size (camera vs pyramid).
	small := image.NewRGBA(image.Rect(0, 0, 150, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 150; x++ {
			small.SetRGBA(x, y, base.RGBAAt(x*2, y*2))
		}
	}
	hs := DHash(small)
	if d := HammingDist(h1, hs); d > 6 {
		t.Errorf("rescale moved hash %d bits", d)
	}
	// Exposure drift between burst frames must not move the hash much.
	hb := DHash(brightened(base, 25))
	if d := HammingDist(h1, hb); d > 4 {
		t.Errorf("brightening moved hash %d bits", d)
	}
}

func TestDHashSeparatesCompositions(t *testing.T) {
	a := DHash(gradientScene(300, 200, 90, 100, 40, false))
	// Same scene, subject nudged slightly — a burst re-frame — stays close.
	near := DHash(gradientScene(300, 200, 100, 104, 40, false))
	if d := HammingDist(a, near); d > 10 {
		t.Errorf("burst re-frame distance %d, want <= 10", d)
	}
	// Subject moved across the frame — a recomposition — lands far away.
	far := DHash(gradientScene(300, 200, 240, 40, 40, true))
	if d := HammingDist(a, far); d <= 10 {
		t.Errorf("recomposition distance %d, want > 10", d)
	}
}

func TestDHashTotal(t *testing.T) {
	// DHash is total: even a sub-grid thumb hashes deterministically (the
	// scaler upsamples it), so the calibrate pass reaches a terminal phash
	// state instead of re-working tiny thumbs on every folder open.
	tiny := image.NewRGBA(image.Rect(0, 0, 4, 4))
	tiny.SetRGBA(0, 0, color.RGBA{255, 255, 255, 255})
	if DHash(tiny) != DHash(tiny) {
		t.Error("tiny image did not hash deterministically")
	}
}
