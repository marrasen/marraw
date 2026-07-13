package aimask

import (
	"context"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/infer"
)

// testManager returns a Manager over pre-downloaded model files, skipping
// when they aren't present. Set MARRAW_TEST_MODELS_DIR to a directory holding
// <id>-<version>.onnx files (dev default: .devdata/models at the repo root).
func testManager(t *testing.T, spec infer.ModelSpec) *infer.Manager {
	t.Helper()
	dir := os.Getenv("MARRAW_TEST_MODELS_DIR")
	if dir == "" {
		dir = "../../.devdata/models"
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("model dir unavailable: %v", err)
	}
	m := infer.NewManager(dir)
	// Probe without a URL: absent file → skip rather than download in tests.
	probe := spec
	probe.URL = ""
	if _, err := m.Session(context.Background(), probe, nil); err != nil {
		t.Skipf("model %s unavailable: %v", spec.ID, err)
	}
	return m
}

// scenePhoto paints a synthetic scene with an obvious subject: a large bright
// warm disc on a dark cool gradient background.
func scenePhoto(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(20 + 30*y/h), G: uint8(30 + 30*y/h), B: uint8(60 + 40*y/h), A: 255})
		}
	}
	cx, cy, r := w/2, h/2, min(w, h)/4
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(x, y, color.RGBA{R: 235, G: 200, B: 160, A: 255})
			}
		}
	}
	return img
}

// TestGenerateSubjectRealModel proves the ISNet graph runs end-to-end through
// our pre/post-processing: right output dims, and the matte separates the
// synthetic subject from the background.
func TestGenerateSubjectRealModel(t *testing.T) {
	mgr := testManager(t, subjectModel)
	src := scenePhoto(640, 480)
	gray, err := Generate(context.Background(), mgr, edit.AISubject, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gray.Rect.Dx() != 1024 || gray.Rect.Dy() != 768 {
		t.Fatalf("map dims %dx%d, want 1024x768", gray.Rect.Dx(), gray.Rect.Dy())
	}
	center := mean(gray, 448, 320, 576, 448) // inside the disc
	corner := mean(gray, 0, 0, 128, 96)      // background
	if center < corner+64 {
		t.Errorf("matte does not separate subject: center %.0f vs corner %.0f", center, corner)
	}
}

// TestGenerateDepthRealModel proves the Depth Anything graph runs end-to-end:
// right dims and a non-degenerate (normalized full-range) depth map.
func TestGenerateDepthRealModel(t *testing.T) {
	mgr := testManager(t, depthModel)
	src := scenePhoto(640, 480)
	gray, err := Generate(context.Background(), mgr, edit.AIDepth, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gray.Rect.Dx() != 1024 || gray.Rect.Dy() != 768 {
		t.Fatalf("map dims %dx%d, want 1024x768", gray.Rect.Dx(), gray.Rect.Dy())
	}
	var lo, hi uint8 = 255, 0
	for _, v := range gray.Pix {
		lo, hi = min(lo, v), max(hi, v)
	}
	if lo > 8 || hi < 247 {
		t.Errorf("depth map not min-max normalized: range %d..%d", lo, hi)
	}
}

func TestSpecFor(t *testing.T) {
	if _, ok := SpecFor(edit.AIClass); ok {
		t.Error("class kind must report unavailable until a license-clean model is hosted")
	}
	ver, ok := MapVerFor(edit.AISubject)
	if !ok || ver != "isnet-1" {
		t.Errorf("MapVerFor(subject) = %q, %v", ver, ok)
	}
}

func TestFitMultipleOf14(t *testing.T) {
	w, h := fitMultipleOf14(6000, 4000, 518)
	if w%14 != 0 || h%14 != 0 {
		t.Errorf("dims %dx%d not multiples of 14", w, h)
	}
	if w < 490 || w > 532 {
		t.Errorf("long edge %d strays from 518", w)
	}
}

func mean(g *image.Gray, x0, y0, x1, y1 int) float64 {
	var sum, n int
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			sum += int(g.GrayAt(x, y).Y)
			n++
		}
	}
	return float64(sum) / float64(n)
}
