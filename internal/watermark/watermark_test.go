package watermark

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestFontsParse guards the embedded files: every bundled face must parse
// and yield a usable Face (a variable font snuck in by a bad download would
// fail here, not at export time).
func TestFontsParse(t *testing.T) {
	for _, id := range FontIDs() {
		if _, err := Font(id); err != nil {
			t.Errorf("Font(%q): %v", id, err)
		}
		if raw, ok := FontBytes(id); !ok || len(raw) == 0 {
			t.Errorf("FontBytes(%q): missing", id)
		}
	}
}

func TestAnchorOrigin(t *testing.T) {
	canvas := image.Rect(0, 0, 100, 60)
	cases := []struct {
		a    Anchor
		want image.Point
	}{
		{AnchorTopLeft, image.Pt(5, 5)},
		{AnchorTop, image.Pt(40, 5)},
		{AnchorTopRight, image.Pt(75, 5)},
		{AnchorLeft, image.Pt(5, 25)},
		{AnchorCenter, image.Pt(40, 25)},
		{AnchorRight, image.Pt(75, 25)},
		{AnchorBottomLeft, image.Pt(5, 45)},
		{AnchorBottom, image.Pt(40, 45)},
		{AnchorBottomRight, image.Pt(75, 45)},
	}
	for _, c := range cases {
		if got := anchorOrigin(canvas, 20, 10, c.a, 5); got != c.want {
			t.Errorf("anchorOrigin(%s) = %v, want %v", c.a, got, c.want)
		}
	}
}

// TestApplyText draws opaque white text bottom-right and checks ink landed
// inside the margin box and nowhere near the opposite corner.
func TestApplyText(t *testing.T) {
	dst := black(400, 200)
	spec := Spec{Elements: []Element{{
		Kind:    KindText,
		Text:    "marraw",
		Font:    FontSans,
		Color:   color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		Anchor:  AnchorBottomRight,
		SizePct: 10, MarginPct: 5, Opacity: 1,
	}}}
	if err := Apply(dst, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if lit(dst, image.Rect(200, 100, 400, 200)) == 0 {
		t.Error("no ink in the bottom-right quadrant")
	}
	if lit(dst, image.Rect(0, 0, 200, 100)) != 0 {
		t.Error("ink leaked into the top-left quadrant")
	}
	// margin = 5% of 200 = 10px: the outermost band must stay black.
	if lit(dst, image.Rect(392, 0, 400, 200)) != 0 {
		t.Error("ink inside the right margin")
	}
}

// TestApplyImage composites a red square at half opacity, centered.
func TestApplyImage(t *testing.T) {
	dir := t.TempDir()
	asset := filepath.Join(dir, "logo.png")
	writePNG(t, asset, solid(10, 10, color.NRGBA{R: 255, A: 255}))

	dst := black(400, 200)
	spec := Spec{Elements: []Element{{
		Kind: KindImage, AssetPath: asset,
		Anchor: AnchorCenter, SizePct: 50, MarginPct: 0, Opacity: 0.5,
	}}}
	if err := Apply(dst, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// 50% of shortEdge 200 = 100px square centered at (200,100).
	r, _, _, _ := dst.At(200, 100).RGBA()
	if got := uint8(r >> 8); got < 120 || got > 135 {
		t.Errorf("center red = %d, want ~128 (half opacity)", got)
	}
	if lit(dst, image.Rect(0, 0, 140, 200)) != 0 {
		t.Error("image leaked left of the centered box")
	}
}

// TestApplyMissingAsset must report the error without panicking, and still
// render the elements that work.
func TestApplyMissingAsset(t *testing.T) {
	dst := black(100, 100)
	spec := Spec{Elements: []Element{
		{Kind: KindImage, AssetPath: filepath.Join(t.TempDir(), "gone.png"),
			Anchor: AnchorCenter, SizePct: 10, Opacity: 1},
		{Kind: KindText, Text: "x", Font: FontMono,
			Color:  color.NRGBA{R: 255, G: 255, B: 255, A: 255},
			Anchor: AnchorCenter, SizePct: 20, Opacity: 1},
	}}
	if err := Apply(dst, spec); err == nil {
		t.Error("want error for missing asset")
	}
	if lit(dst, dst.Bounds()) == 0 {
		t.Error("surviving text element was not rendered")
	}
}

func TestParseHexColor(t *testing.T) {
	if c := ParseHexColor("#3a7bfF"); (c != color.NRGBA{R: 0x3a, G: 0x7b, B: 0xff, A: 0xff}) {
		t.Errorf("got %v", c)
	}
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	for _, bad := range []string{"", "3a7bff", "#12345", "#gggggg"} {
		if ParseHexColor(bad) != white {
			t.Errorf("ParseHexColor(%q): want white fallback", bad)
		}
	}
}

func black(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	return img
}

func solid(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// lit counts pixels in r with any non-black channel.
func lit(img *image.RGBA, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.R != 0 || c.G != 0 || c.B != 0 {
				n++
			}
		}
	}
	return n
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
