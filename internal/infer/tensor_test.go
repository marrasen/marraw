package infer

import (
	"image"
	"image/color"
	"testing"
)

func TestNCHWFromRGBA(t *testing.T) {
	// Use a sub-image with a non-zero origin so PixOffset handling is
	// exercised, not just the common bounds-at-zero case.
	base := image.NewRGBA(image.Rect(0, 0, 4, 4))
	base.SetRGBA(1, 1, color.RGBA{R: 255, G: 0, B: 51, A: 255})
	base.SetRGBA(2, 1, color.RGBA{R: 0, G: 128, B: 0, A: 255})
	img := base.SubImage(image.Rect(1, 1, 3, 2)).(*image.RGBA) // 2×1

	got := NCHWFromRGBA(img, [3]float32{0, 0, 0}, [3]float32{1, 1, 1})
	want := []float32{
		1, 0, // R plane
		0, 128.0 / 255, // G plane
		51.0 / 255, 0, // B plane
	}
	if len(got) != len(want) {
		t.Fatalf("length %d, want %d", len(got), len(want))
	}
	for i := range want {
		if diff := got[i] - want[i]; diff > 1e-6 || diff < -1e-6 {
			t.Errorf("[%d] = %v, want %v", i, got[i], want[i])
		}
	}

	// Mean/std normalization: (1 - 0.5) / 0.5 = 1 for the R=255 pixel.
	norm := NCHWFromRGBA(img, [3]float32{0.5, 0.5, 0.5}, [3]float32{0.5, 0.5, 0.5})
	if norm[0] != 1 {
		t.Errorf("normalized R = %v, want 1", norm[0])
	}
}

func TestArgmaxPlane(t *testing.T) {
	// 3 classes over a 2×2 plane.
	logits := []float32{
		9, 0, 0, 1, // class 0
		1, 5, 0, 2, // class 1
		0, 1, 7, 3, // class 2
	}
	got, err := ArgmaxPlane(logits, 3, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint8{0, 1, 2, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %d, want %d", i, got[i], want[i])
		}
	}

	if _, err := ArgmaxPlane(logits, 300, 2, 2); err == nil {
		t.Error("expected error for classes > 256")
	}
	if _, err := ArgmaxPlane(logits, 3, 5, 5); err == nil {
		t.Error("expected error for length mismatch")
	}
}

func TestNormalizePlane(t *testing.T) {
	got := NormalizePlane([]float32{-1, 0, 1})
	if got[0] != 0 || got[2] != 255 {
		t.Errorf("endpoints = %d, %d, want 0, 255", got[0], got[2])
	}
	if got[1] != 128 { // (0 - -1) / 2 * 255 + 0.5 rounds to 128
		t.Errorf("midpoint = %d, want 128", got[1])
	}
	flat := NormalizePlane([]float32{3, 3, 3})
	for _, v := range flat {
		if v != 0 {
			t.Errorf("constant plane should map to 0, got %d", v)
		}
	}
	if out := NormalizePlane(nil); len(out) != 0 {
		t.Error("nil input should yield empty output")
	}
}
