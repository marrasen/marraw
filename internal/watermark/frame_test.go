package watermark

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// aspectClose reports whether the photo box aspect matches w:h within the
// ~1px tolerance the rounding allows.
func aspectClose(t *testing.T, l FrameLayout, w, h int) {
	t.Helper()
	want := float64(w) / float64(h)
	got := float64(l.PhotoW) / float64(l.PhotoH)
	// Rounding the canvas and the borders independently allows a bit over
	// 1px of deviation on the smaller axis.
	tol := 1.5 * want / float64(l.PhotoH)
	if math.Abs(got-want) > tol {
		t.Errorf("photo box %dx%d aspect %.4f, want %.4f ±%.4f", l.PhotoW, l.PhotoH, got, want, tol)
	}
}

func TestFrameLayoutLandscape(t *testing.T) {
	f := Frame{WidthPct: 5, BottomPct: 10}
	l := f.Layout(4000, 3000, 1000)
	if l.CanvasW != 1000 {
		t.Errorf("long edge = %d, want exactly 1000", l.CanvasW)
	}
	if l.CanvasH >= l.CanvasW {
		t.Errorf("framed canvas %dx%d should stay landscape", l.CanvasW, l.CanvasH)
	}
	if l.PhotoW != l.CanvasW-2*l.Border || l.PhotoH != l.CanvasH-2*l.Border-l.Chin {
		t.Errorf("photo box %dx%d inconsistent with borders %d/%d of canvas %dx%d",
			l.PhotoW, l.PhotoH, l.Border, l.Chin, l.CanvasW, l.CanvasH)
	}
	if want := int(math.Round(0.05 * float64(l.CanvasH))); l.Border != want {
		t.Errorf("border = %d, want %d (5%% of short edge)", l.Border, want)
	}
	if want := int(math.Round(0.10 * float64(l.CanvasH))); l.Chin != want {
		t.Errorf("chin = %d, want %d (10%% of short edge)", l.Chin, want)
	}
	aspectClose(t, l, 4000, 3000)
}

func TestFrameLayoutPortrait(t *testing.T) {
	f := Frame{WidthPct: 5, BottomPct: 10}
	l := f.Layout(3000, 4000, 1000)
	if l.CanvasH != 1000 {
		t.Errorf("long edge = %d, want exactly 1000", l.CanvasH)
	}
	if l.CanvasW >= l.CanvasH {
		t.Errorf("framed canvas %dx%d should stay portrait", l.CanvasW, l.CanvasH)
	}
	if l.PhotoW != l.CanvasW-2*l.Border || l.PhotoH != l.CanvasH-2*l.Border-l.Chin {
		t.Errorf("photo box %dx%d inconsistent with borders %d/%d", l.PhotoW, l.PhotoH, l.Border, l.Chin)
	}
	aspectClose(t, l, 3000, 4000)
}

// TestFrameLayoutFullRes: no long edge — the photo keeps native dims and the
// canvas grows.
func TestFrameLayoutFullRes(t *testing.T) {
	f := Frame{WidthPct: 3}
	l := f.Layout(4000, 3000, 0)
	if l.PhotoW != 4000 || l.PhotoH != 3000 {
		t.Fatalf("photo box %dx%d, want native", l.PhotoW, l.PhotoH)
	}
	if l.CanvasW != 4000+2*l.Border || l.CanvasH != 3000+2*l.Border || l.Chin != 0 {
		t.Errorf("canvas %dx%d chin %d inconsistent with border %d", l.CanvasW, l.CanvasH, l.Chin, l.Border)
	}
	if want := int(math.Round(0.03 * float64(l.CanvasH))); l.Border != want {
		t.Errorf("border = %d, want %d (3%% of framed short edge)", l.Border, want)
	}
}

// TestFrameLayoutFullResPortrait exercises the branch fall-through where the
// framed width, not height, is the short edge.
func TestFrameLayoutFullResPortrait(t *testing.T) {
	f := Frame{WidthPct: 5}
	l := f.Layout(3000, 4000, 0)
	if l.PhotoW != 3000 || l.PhotoH != 4000 {
		t.Fatalf("photo box %dx%d, want native", l.PhotoW, l.PhotoH)
	}
	if l.CanvasW >= l.CanvasH {
		t.Errorf("framed canvas %dx%d should stay portrait", l.CanvasW, l.CanvasH)
	}
	if want := int(math.Round(0.05 * float64(l.CanvasW))); l.Border != want {
		t.Errorf("border = %d, want %d (5%% of framed short edge)", l.Border, want)
	}
}

// TestFrameLayoutShrinkOnly: a long edge larger than the framed native size
// must never upscale — the photo keeps native dims (Case A).
func TestFrameLayoutShrinkOnly(t *testing.T) {
	f := Frame{WidthPct: 5, BottomPct: 10}
	l := f.Layout(800, 600, 10000)
	if l.PhotoW != 800 || l.PhotoH != 600 {
		t.Errorf("photo box %dx%d, want native 800x600 (no upscale)", l.PhotoW, l.PhotoH)
	}
	if l.CanvasW > 10000 && l.CanvasH > 10000 {
		t.Errorf("canvas %dx%d exceeds the requested long edge on both axes", l.CanvasW, l.CanvasH)
	}
}

// TestFrameLayoutSquareMaxChin: square photo at the extreme bounds flips the
// framed canvas to portrait via the chin.
func TestFrameLayoutSquareMaxChin(t *testing.T) {
	f := Frame{WidthPct: 15, BottomPct: 30}
	l := f.Layout(1000, 1000, 700)
	if l.CanvasH != 700 {
		t.Errorf("long edge = %d, want exactly 700 (chin makes the frame portrait)", l.CanvasH)
	}
	if l.PhotoW <= 0 || l.PhotoH <= 0 {
		t.Fatalf("degenerate photo box %dx%d", l.PhotoW, l.PhotoH)
	}
	aspectClose(t, l, 1, 1)
}

func TestFrameCompose(t *testing.T) {
	photo := image.NewRGBA(image.Rect(0, 0, 10, 8))
	red := color.NRGBA{R: 255, A: 255}
	for y := 0; y < 8; y++ {
		for x := 0; x < 10; x++ {
			photo.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	f := Frame{Color: color.NRGBA{R: 250, G: 250, B: 245}}
	l := FrameLayout{PhotoW: 10, PhotoH: 8, CanvasW: 20, CanvasH: 21, Border: 5, Chin: 3}
	dst := f.Compose(photo, l)
	if b := dst.Bounds(); b.Dx() != 20 || b.Dy() != 21 {
		t.Fatalf("canvas %v, want 20x21", b)
	}
	if c := dst.RGBAAt(0, 0); c.R != 250 || c.G != 250 || c.B != 245 {
		t.Errorf("corner = %v, want frame color", c)
	}
	if c := dst.RGBAAt(10, 18); c.R != 250 || c.G != 250 || c.B != 245 {
		t.Errorf("chin = %v, want frame color", c)
	}
	if c := dst.RGBAAt(5, 5); c.R != red.R || c.G != 0 {
		t.Errorf("photo origin = %v, want red", c)
	}
	if c := dst.RGBAAt(14, 12); c.R != 255 {
		t.Errorf("photo bottom-right = %v, want red", c)
	}
}
