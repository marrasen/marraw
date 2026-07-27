package watermark

import (
	"image"
	"image/color"
	"testing"
)

// TestRectSolid draws a full-width half-opacity white band at the bottom and
// checks exact coverage plus the blended value.
func TestRectSolid(t *testing.T) {
	dst := black(400, 200)
	Apply(dst, Spec{Elements: []Element{{
		Kind:     KindRect,
		Color:    color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		WidthPct: 100, HeightPct: 25,
		Anchor: AnchorBottom, MarginPct: 0, Opacity: 0.5,
	}}})
	// 25% of height 200 = 50px band: y in [150, 200).
	if lit(dst, image.Rect(0, 0, 400, 150)) != 0 {
		t.Error("ink above the band")
	}
	band := image.Rect(0, 150, 400, 200)
	if got := lit(dst, band); got != band.Dx()*band.Dy() {
		t.Errorf("band coverage %d, want %d", got, band.Dx()*band.Dy())
	}
	r, _, _, _ := dst.At(200, 175).RGBA()
	if got := uint8(r >> 8); got < 120 || got > 135 {
		t.Errorf("band value = %d, want ~128 (half opacity)", got)
	}
}

// TestRectGradient checks the endpoint rows and monotone fade for each
// direction, and that opacity2 = 0 leaves the end row untouched.
func TestRectGradient(t *testing.T) {
	el := Element{
		Kind: KindRect, Gradient: true,
		Color:   color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		Color2:  color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		Opacity: 1, Opacity2: 0,
		WidthPct: 100, HeightPct: 100, Anchor: AnchorCenter,
	}
	probe := func(dir GradientDir, start, end image.Point) {
		t.Helper()
		dst := black(100, 100)
		el.GradientDir = dir
		Apply(dst, Spec{Elements: []Element{el}})
		if c := dst.RGBAAt(start.X, start.Y); c.R != 255 {
			t.Errorf("%s start pixel R = %d, want 255", dir, c.R)
		}
		if c := dst.RGBAAt(end.X, end.Y); c.R != 0 {
			t.Errorf("%s end pixel untouched: R = %d, want 0", dir, c.R)
		}
		if c := dst.RGBAAt(50, 50); c.R < 100 || c.R > 155 {
			t.Errorf("%s midpoint R = %d, want ~128", dir, c.R)
		}
	}
	probe(GradientDown, image.Pt(50, 0), image.Pt(50, 99))
	probe(GradientUp, image.Pt(50, 99), image.Pt(50, 0))
	probe(GradientRight, image.Pt(0, 50), image.Pt(99, 50))
	probe(GradientLeft, image.Pt(99, 50), image.Pt(0, 50))
}

// TestRectGradientColors lerps between two colors at full opacity.
func TestRectGradientColors(t *testing.T) {
	dst := black(100, 100)
	Apply(dst, Spec{Elements: []Element{{
		Kind: KindRect, Gradient: true, GradientDir: GradientDown,
		Color:   color.NRGBA{R: 200, G: 0, B: 0, A: 255},
		Color2:  color.NRGBA{R: 0, G: 0, B: 200, A: 255},
		Opacity: 1, Opacity2: 1,
		WidthPct: 100, HeightPct: 100, Anchor: AnchorCenter,
	}}})
	if c := dst.RGBAAt(5, 0); c.R != 200 || c.B != 0 {
		t.Errorf("top = %v, want pure start color", c)
	}
	if c := dst.RGBAAt(5, 99); c.R != 0 || c.B != 200 {
		t.Errorf("bottom = %v, want pure end color", c)
	}
	if c := dst.RGBAAt(5, 50); c.R < 80 || c.R > 120 || c.B < 80 || c.B > 120 {
		t.Errorf("middle = %v, want ~half/half", c)
	}
}

// TestRectOrientation: HeightPct follows the short-edge rule, so the same
// bottom bar is equally thick on a landscape and a portrait canvas — it must
// not grow with the long edge the way a %-of-height rule would.
func TestRectOrientation(t *testing.T) {
	el := Element{
		Kind:     KindRect,
		Color:    color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		WidthPct: 100, HeightPct: 25,
		Anchor: AnchorBottom, Opacity: 1,
	}
	thickness := func(w, h int) int {
		dst := black(w, h)
		Apply(dst, Spec{Elements: []Element{el}})
		return lit(dst, dst.Bounds()) / w
	}
	if land, port := thickness(200, 100), thickness(100, 200); land != port {
		t.Errorf("band thickness landscape = %d, portrait = %d, want equal", land, port)
	}
}

// TestRectTiny: a 1-px rect must not divide by zero and paints the start
// color.
func TestRectTiny(t *testing.T) {
	dst := black(100, 100)
	Apply(dst, Spec{Elements: []Element{{
		Kind: KindRect, Gradient: true, GradientDir: GradientRight,
		Color:   color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		Color2:  color.NRGBA{A: 255},
		Opacity: 1, Opacity2: 0,
		WidthPct: 1, HeightPct: 1, Anchor: AnchorTopLeft,
	}}})
	if c := dst.RGBAAt(0, 0); c.R != 255 {
		t.Errorf("1px rect = %v, want start color", c)
	}
}
