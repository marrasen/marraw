package pyramid

import (
	"image"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			o := img.PixOffset(x, y)
			img.Pix[o+0] = uint8(x * 255 / max(1, w-1))
			img.Pix[o+1] = uint8(y * 255 / max(1, h-1))
			img.Pix[o+2] = 128
			img.Pix[o+3] = 255
		}
	}
	return img
}

func TestApplyGeometryNeutral(t *testing.T) {
	img := gradient(64, 48)
	if got := ApplyGeometry(img, nil); got != img {
		t.Error("nil edit must return the same image")
	}
	if got := ApplyGeometry(img, &edit.Params{}); got != img {
		t.Error("neutral edit must return the same image")
	}
}

func TestApplyGeometryCrop(t *testing.T) {
	img := gradient(100, 80)
	e := &edit.Params{CropX: 0.25, CropY: 0.5, CropW: 0.5, CropH: 0.25}
	out := ApplyGeometry(img, e)
	if out.Bounds().Dx() != 50 || out.Bounds().Dy() != 20 {
		t.Fatalf("cropped size = %v, want 50x20", out.Bounds())
	}
	// Result is tightly packed (stride == width*4), not a SubImage view.
	if out.Stride != 50*4 {
		t.Errorf("stride = %d, want %d", out.Stride, 50*4)
	}
	// Top-left of the crop equals source (25, 40).
	so := img.PixOffset(25, 40)
	do := out.PixOffset(0, 0)
	if out.Pix[do] != img.Pix[so] || out.Pix[do+1] != img.Pix[so+1] {
		t.Errorf("crop origin pixel mismatch: got %v want %v", out.Pix[do:do+3], img.Pix[so:so+3])
	}
}

func TestOutputDimsMatchesRender(t *testing.T) {
	img := gradient(200, 150)
	for _, e := range []*edit.Params{
		{CropW: 0.5, CropH: 0.5},
		{CropX: 0.1, CropY: 0.2, CropW: 0.8, CropH: 0.6},
		{CropX: 0.3, CropY: 0.3, CropW: 0.4, CropH: 0.4, CropAngle: 8},
	} {
		w, h := e.OutputDims(200, 150)
		out := ApplyGeometry(img, e)
		if out.Bounds().Dx() != w || out.Bounds().Dy() != h {
			t.Errorf("%+v: render %dx%d != OutputDims %dx%d", e, out.Bounds().Dx(), out.Bounds().Dy(), w, h)
		}
	}
}

func TestApplyGeometryStraightenCorners(t *testing.T) {
	img := gradient(120, 120)
	// A crop that spans nearly the whole frame plus a big angle guarantees
	// some output pixels sample outside the source → opaque black corners.
	e := &edit.Params{CropX: 0.02, CropY: 0.02, CropW: 0.96, CropH: 0.96, CropAngle: 15}
	out := ApplyGeometry(img, e)
	corner := out.PixOffset(0, 0)
	if !(out.Pix[corner] == 0 && out.Pix[corner+1] == 0 && out.Pix[corner+2] == 0) {
		t.Errorf("expected black exposed corner, got %v", out.Pix[corner:corner+3])
	}
	// The center pixel came from inside the source and keeps mid values.
	c := out.PixOffset(out.Bounds().Dx()/2, out.Bounds().Dy()/2)
	if out.Pix[c] == 0 && out.Pix[c+1] == 0 {
		t.Error("center should sample real image content, got black")
	}
}

func TestApplyGeometryStraightenPreservesContentSize(t *testing.T) {
	// Straighten with no crop keeps the full frame dimensions.
	img := gradient(90, 60)
	out := ApplyGeometry(img, &edit.Params{CropAngle: 5})
	if out.Bounds().Dx() != 90 || out.Bounds().Dy() != 60 {
		t.Errorf("straighten-only changed size to %v", out.Bounds())
	}
}
