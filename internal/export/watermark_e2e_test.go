package export

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/watermark"
)

// TestExportWatermarkE2E exports one real RAW twice — clean and with a
// two-element watermark (text bottom-right, logo top-left) — and asserts the
// pixels differ exactly where the elements land and nowhere near the other
// corners.
func TestExportWatermarkE2E(t *testing.T) {
	raw := sampleRAW(t)
	photo := store.Photo{FolderPath: filepath.Dir(raw), FileName: filepath.Base(raw)}
	dir := t.TempDir()

	logo := filepath.Join(dir, "logo.png")
	logoImg := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for i := range logoImg.Pix {
		logoImg.Pix[i] = 0xff // opaque white square
	}
	f, err := os.Create(logo)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, logoImg); err != nil {
		t.Fatal(err)
	}
	f.Close()

	req := Request{Format: "jpeg", JpegQuality: 95, LongEdge: 1024, ColorSpace: "srgb"}
	if err := exportOne(t.Context(), photo, filepath.Join(dir, "clean.jpg"), req); err != nil {
		t.Fatalf("clean: %v", err)
	}
	req.Watermark = &watermark.Spec{Elements: []watermark.Element{
		{
			Kind: watermark.KindText, Text: "© marraw e2e", Font: watermark.FontSans,
			Color:  color.NRGBA{R: 255, G: 255, B: 255, A: 255},
			Anchor: watermark.AnchorBottomRight, SizePct: 6, MarginPct: 3, Opacity: 1,
		},
		{
			Kind: watermark.KindImage, AssetPath: logo,
			Anchor: watermark.AnchorTopLeft, SizePct: 10, MarginPct: 3, Opacity: 0.8,
		},
	}}
	if err := exportOne(t.Context(), photo, filepath.Join(dir, "marked.jpg"), req); err != nil {
		t.Fatalf("marked: %v", err)
	}

	decode := func(name string) image.Image {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return img
	}
	clean, marked := decode("clean.jpg"), decode("marked.jpg")
	if clean.Bounds() != marked.Bounds() {
		t.Fatalf("dimensions changed: %v != %v", clean.Bounds(), marked.Bounds())
	}

	// maxDiff reports the largest per-pixel luma delta inside r.
	maxDiff := func(r image.Rectangle) int {
		var worst int
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				cr, cg, cb, _ := clean.At(x, y).RGBA()
				mr, mg, mb, _ := marked.At(x, y).RGBA()
				d := (abs(int(cr)-int(mr))*299 + abs(int(cg)-int(mg))*587 + abs(int(cb)-int(mb))*114) / 1000 >> 8
				if d > worst {
					worst = d
				}
			}
		}
		return worst
	}

	b := clean.Bounds()
	w, h := b.Dx(), b.Dy()
	quad := func(qx, qy int) image.Rectangle {
		return image.Rect(b.Min.X+qx*w/2, b.Min.Y+qy*h/2, b.Min.X+(qx+1)*w/2, b.Min.Y+(qy+1)*h/2)
	}
	if d := maxDiff(quad(1, 1)); d < 32 {
		t.Errorf("bottom-right text barely visible: max luma diff %d", d)
	}
	if d := maxDiff(quad(0, 0)); d < 32 {
		t.Errorf("top-left logo barely visible: max luma diff %d", d)
	}
	// The untouched quadrants may differ by JPEG noise only.
	if d := maxDiff(quad(1, 0)); d > 12 {
		t.Errorf("top-right quadrant changed (max diff %d) — watermark leaked", d)
	}
	if d := maxDiff(quad(0, 1)); d > 12 {
		t.Errorf("bottom-left quadrant changed (max diff %d) — watermark leaked", d)
	}
}

// TestExportFrameE2E exports one real RAW with a polaroid frame and a text
// element in the chin: the framed long edge must hit the request exactly,
// the border must be the frame color, and the EXIF pixel dims must describe
// the framed canvas.
func TestExportFrameE2E(t *testing.T) {
	raw := sampleRAW(t)
	photo := store.Photo{FolderPath: filepath.Dir(raw), FileName: filepath.Base(raw)}
	dir := t.TempDir()

	frameColor := color.NRGBA{R: 250, G: 250, B: 245, A: 255}
	req := Request{Format: "jpeg", JpegQuality: 95, LongEdge: 1024, ColorSpace: "srgb"}
	req.Watermark = &watermark.Spec{
		Frame: &watermark.Frame{WidthPct: 5, BottomPct: 15, Color: frameColor},
		Elements: []watermark.Element{{
			Kind: watermark.KindText, Text: "polaroid", Font: watermark.FontSans,
			Color:  color.NRGBA{A: 255}, // black on the light chin
			Anchor: watermark.AnchorBottom, SizePct: 5, MarginPct: 3, Opacity: 1,
		}},
	}
	out := filepath.Join(dir, "framed.jpg")
	if err := exportOne(t.Context(), photo, out, req); err != nil {
		t.Fatalf("framed: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if max(b.Dx(), b.Dy()) != 1024 {
		t.Fatalf("framed long edge = %dx%d, want exactly 1024", b.Dx(), b.Dy())
	}

	// nearFrame allows JPEG noise around the flat frame color.
	nearFrame := func(x, y int) bool {
		r, g, bb, _ := img.At(x, y).RGBA()
		return abs(int(r>>8)-int(frameColor.R)) < 12 &&
			abs(int(g>>8)-int(frameColor.G)) < 12 &&
			abs(int(bb>>8)-int(frameColor.B)) < 12
	}
	for _, p := range []image.Point{
		{b.Min.X + 2, b.Min.Y + 2}, {b.Max.X - 3, b.Min.Y + 2},
		{b.Min.X + 2, b.Max.Y - 3}, {b.Max.X - 3, b.Max.Y - 3},
	} {
		if !nearFrame(p.X, p.Y) {
			t.Errorf("corner %v is not the frame color: %v", p, img.At(p.X, p.Y))
		}
	}

	// The chin (bottom band) must hold the text ink: at least some clearly
	// non-frame pixels in the centered bottom strip.
	short := min(b.Dx(), b.Dy())
	chinTop := b.Max.Y - int(0.15*float64(short))
	ink := 0
	for y := chinTop; y < b.Max.Y; y++ {
		for x := b.Min.X + b.Dx()/4; x < b.Max.X-b.Dx()/4; x++ {
			if !nearFrame(x, y) {
				ink++
			}
		}
	}
	if ink == 0 {
		t.Error("no text ink found in the chin")
	}

	// EXIF pixel dims must describe the framed canvas.
	idx := bytes.Index(data, []byte("Exif\x00\x00"))
	if idx < 0 {
		t.Fatal("no EXIF payload in framed JPEG")
	}
	_, exif := parseExifIn(t, data, uint32(idx+6))
	if int(exif[tagPixelXDimension].value) != b.Dx() || int(exif[tagPixelYDimension].value) != b.Dy() {
		t.Errorf("EXIF dims %dx%d, want framed %dx%d",
			exif[tagPixelXDimension].value, exif[tagPixelYDimension].value, b.Dx(), b.Dy())
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
