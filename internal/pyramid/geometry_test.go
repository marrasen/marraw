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

func TestApplyGeometryRotate(t *testing.T) {
	img := gradient(100, 80)
	// 90° CW: (x,y) → (h-1-y, x); the source's top-right lands at the
	// output's bottom-right.
	out := ApplyGeometry(img, &edit.Params{Rotate: 1})
	if out.Bounds().Dx() != 80 || out.Bounds().Dy() != 100 {
		t.Fatalf("rotate 90 size = %v, want 80x100", out.Bounds())
	}
	so := img.PixOffset(99, 0) // source top-right
	do := out.PixOffset(79, 99)
	if out.Pix[do] != img.Pix[so] || out.Pix[do+1] != img.Pix[so+1] {
		t.Errorf("rotate 90 pixel mapping: got %v want %v", out.Pix[do:do+3], img.Pix[so:so+3])
	}
	// 180°: top-left lands at bottom-right, size unchanged.
	out = ApplyGeometry(img, &edit.Params{Rotate: 2})
	if out.Bounds().Dx() != 100 || out.Bounds().Dy() != 80 {
		t.Fatalf("rotate 180 size = %v, want 100x80", out.Bounds())
	}
	so = img.PixOffset(0, 0)
	do = out.PixOffset(99, 79)
	if out.Pix[do] != img.Pix[so] || out.Pix[do+1] != img.Pix[so+1] {
		t.Error("rotate 180 pixel mapping wrong")
	}
}

func TestApplyGeometryRotateThenCrop(t *testing.T) {
	// The crop rectangle lives in the rotated frame: cropping the top-left
	// quarter of a 90° CW turn must read the source's bottom-left region.
	img := gradient(100, 80)
	e := &edit.Params{Rotate: 1, CropW: 0.5, CropH: 0.5}
	out := ApplyGeometry(img, e)
	if w, h := e.OutputDims(100, 80); out.Bounds().Dx() != w || out.Bounds().Dy() != h {
		t.Fatalf("render %v != OutputDims %dx%d", out.Bounds(), w, h)
	}
	if out.Bounds().Dx() != 40 || out.Bounds().Dy() != 50 {
		t.Fatalf("rotated crop size = %v, want 40x50", out.Bounds())
	}
	// Output (0,0) is the rotated frame's top-left = source bottom-left (0,79).
	so := img.PixOffset(0, 79)
	do := out.PixOffset(0, 0)
	if out.Pix[do] != img.Pix[so] || out.Pix[do+1] != img.Pix[so+1] {
		t.Errorf("rotated crop origin: got %v want %v", out.Pix[do:do+3], img.Pix[so:so+3])
	}
}

func TestApplyGeometryFlipH(t *testing.T) {
	img := gradient(100, 80)
	out := ApplyGeometry(img, &edit.Params{FlipH: true})
	if out.Bounds().Dx() != 100 || out.Bounds().Dy() != 80 {
		t.Fatalf("flip size = %v, want 100x80", out.Bounds())
	}
	// Source top-left lands at the top-right.
	so := img.PixOffset(0, 0)
	do := out.PixOffset(99, 0)
	if out.Pix[do] != img.Pix[so] || out.Pix[do+1] != img.Pix[so+1] {
		t.Error("flip pixel mapping wrong")
	}
	// The mirror composes AFTER the quarter turn: source top-right goes
	// through rotate 90° CW to (79, 99), then mirrors to (0, 99).
	out = ApplyGeometry(img, &edit.Params{Rotate: 1, FlipH: true})
	if out.Bounds().Dx() != 80 || out.Bounds().Dy() != 100 {
		t.Fatalf("rotate+flip size = %v, want 80x100", out.Bounds())
	}
	so = img.PixOffset(99, 0)
	do = out.PixOffset(0, 99)
	if out.Pix[do] != img.Pix[so] || out.Pix[do+1] != img.Pix[so+1] {
		t.Error("rotate+flip pixel mapping wrong")
	}
}

func TestApplyGeometryFlipCrop(t *testing.T) {
	// The crop rectangle lives in the mirrored frame: its top-left quarter
	// reads the source's top-RIGHT pixel at the origin.
	img := gradient(100, 80)
	e := &edit.Params{FlipH: true, CropW: 0.5, CropH: 0.5}
	out := ApplyGeometry(img, e)
	if out.Bounds().Dx() != 50 || out.Bounds().Dy() != 40 {
		t.Fatalf("flipped crop size = %v, want 50x40", out.Bounds())
	}
	so := img.PixOffset(99, 0)
	do := out.PixOffset(0, 0)
	if out.Pix[do] != img.Pix[so] || out.Pix[do+1] != img.Pix[so+1] {
		t.Errorf("flipped crop origin: got %v want %v", out.Pix[do:do+3], img.Pix[so:so+3])
	}
}

func TestOutputDimsRotate(t *testing.T) {
	e := &edit.Params{Rotate: 1}
	if w, h := e.OutputDims(4000, 3000); w != 3000 || h != 4000 {
		t.Errorf("rotate-only OutputDims = %dx%d, want 3000x4000", w, h)
	}
	e = &edit.Params{Rotate: 3, CropW: 0.5, CropH: 0.5}
	if w, h := e.OutputDims(4000, 3000); w != 1500 || h != 2000 {
		t.Errorf("rotate+crop OutputDims = %dx%d, want 1500x2000", w, h)
	}
	e = &edit.Params{Rotate: 2, CropW: 0.5, CropH: 0.5}
	if w, h := e.OutputDims(4000, 3000); w != 2000 || h != 1500 {
		t.Errorf("180+crop OutputDims = %dx%d, want 2000x1500", w, h)
	}
}
