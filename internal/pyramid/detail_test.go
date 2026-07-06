package pyramid

import (
	"image"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// gradientImage is a horizontal ramp with a hard vertical edge in the middle,
// exercising both smooth midtones and a sharp transition.
func gradientImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(40 + 120*x/w)
			if x > w/2 {
				v += 60
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
	return img
}

func clonePix(img *image.RGBA) []uint8 {
	out := make([]uint8, len(img.Pix))
	copy(out, img.Pix)
	return out
}

func TestApplyDetailNeutralNoOp(t *testing.T) {
	img := gradientImage(64, 48)
	before := clonePix(img)
	ApplyDetail(img, nil)
	ApplyDetail(img, &edit.Params{})
	for i := range before {
		if img.Pix[i] != before[i] {
			t.Fatalf("neutral ApplyDetail changed pixel %d: %d -> %d", i, before[i], img.Pix[i])
		}
	}
}

// edgeContrast measures the luma step across the hard edge at w/2.
func edgeContrast(img *image.RGBA) int {
	b := img.Bounds()
	y := b.Dy() / 2
	x := b.Dx() / 2
	lo := int(img.Pix[img.PixOffset(x, y)])
	hi := int(img.Pix[img.PixOffset(x+1, y)])
	return hi - lo
}

func TestApplyDetailSharpenIncreasesEdgeContrast(t *testing.T) {
	base := gradientImage(64, 48)
	sharp := gradientImage(64, 48)
	ApplyDetail(sharp, &edit.Params{Sharpen: 1})
	if got, want := edgeContrast(sharp), edgeContrast(base); got <= want {
		t.Fatalf("sharpen did not increase edge contrast: %d <= %d", got, want)
	}
}

func TestApplyDetailClaritySigns(t *testing.T) {
	pos := gradientImage(128, 96)
	neg := gradientImage(128, 96)
	ApplyDetail(pos, &edit.Params{Clarity: 1})
	ApplyDetail(neg, &edit.Params{Clarity: -1})
	if edgeContrast(pos) <= edgeContrast(neg) {
		t.Fatalf("positive clarity (%d) should exceed negative clarity (%d) edge contrast",
			edgeContrast(pos), edgeContrast(neg))
	}
}

func TestApplyDetailTextureIncreasesEdgeContrast(t *testing.T) {
	base := gradientImage(64, 48)
	tex := gradientImage(64, 48)
	ApplyDetail(tex, &edit.Params{Texture: 1})
	if got, want := edgeContrast(tex), edgeContrast(base); got <= want {
		t.Fatalf("texture did not increase edge contrast: %d <= %d", got, want)
	}
}

// TestApplyDetailDehaze: positive dehaze must deepen a lifted black floor;
// negative must lift shadows toward the veil.
func TestApplyDetailDehaze(t *testing.T) {
	// Flat hazy frame: everything sits at a lifted floor.
	mk := func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 80, 90, 100, 0xff
		}
		return img
	}
	pos := mk()
	ApplyDetail(pos, &edit.Params{Dehaze: 1})
	if got := pos.Pix[0]; got >= 80 {
		t.Fatalf("positive dehaze did not darken the veil floor: %d >= 80", got)
	}
	neg := mk()
	ApplyDetail(neg, &edit.Params{Dehaze: -1})
	if got := neg.Pix[0]; got <= 80 {
		t.Fatalf("negative dehaze did not lift toward the veil: %d <= 80", got)
	}
}

// TestApplyDetailClarityProtectsExtremes: near-black and near-white pixels
// must move less than midtones under clarity (the midtone weight).
func TestApplyDetailClarityProtectsExtremes(t *testing.T) {
	// Two-tone image: dark region and mid region with an edge between.
	img := image.NewRGBA(image.Rect(0, 0, 96, 32))
	for y := range 32 {
		for x := range 96 {
			v := uint8(5)
			if x >= 48 {
				v = 128
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
	before := clonePix(img)
	ApplyDetail(img, &edit.Params{Clarity: 1})
	y := 16
	darkDelta := abs(int(img.Pix[img.PixOffset(44, y)]) - int(before[img.PixOffset(44, y)]))
	midDelta := abs(int(img.Pix[img.PixOffset(52, y)]) - int(before[img.PixOffset(52, y)]))
	if darkDelta > midDelta {
		t.Fatalf("clarity moved deep shadows (%d) more than midtones (%d)", darkDelta, midDelta)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestBoxBlurPlaneUniform(t *testing.T) {
	w, h := 20, 10
	src := make([]uint8, w*h)
	for i := range src {
		src[i] = 100
	}
	out := boxBlurPlane(src, w, h, 3, 2)
	for i, v := range out {
		if v != 100 {
			t.Fatalf("uniform plane changed at %d: %d", i, v)
		}
	}
}
