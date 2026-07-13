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

func TestSharpnessScoreDegenerate(t *testing.T) {
	if s := SharpnessScore(image.NewRGBA(image.Rect(0, 0, 2, 2))); s != 0 {
		t.Errorf("tiny image scored %v, want 0", s)
	}
	flat := image.NewRGBA(image.Rect(0, 0, 100, 100))
	if s := SharpnessScore(flat); s != 0 {
		t.Errorf("flat image scored %v, want 0", s)
	}
}
