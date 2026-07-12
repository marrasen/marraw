package export

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/store"
)

// sampleRAW returns a RAW file to test against, or skips the test.
// Override the search dir with MARRAW_TEST_RAW_DIR.
func sampleRAW(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("MARRAW_TEST_RAW_DIR")
	if dir == "" {
		dir = `D:\Photos\2026-04-18 Velox Valor Trollhättan`
	}
	for _, pat := range []string{"*.ARW", "*.arw", "*.CR2", "*.CR3", "*.NEF", "*.DNG"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		if len(m) > 0 {
			return m[0]
		}
	}
	t.Skipf("no RAW files found in %s (set MARRAW_TEST_RAW_DIR)", dir)
	return ""
}

// meanGradient is a crude acutance metric: mean absolute horizontal luma
// delta. Output sharpening must raise it monotonically with strength.
func meanGradient(img image.Image) float64 {
	b := img.Bounds()
	var sum, n int64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		prev := -1
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			l := int((299*r + 587*g + 114*bl) / 1000 >> 8)
			if prev >= 0 {
				d := l - prev
				if d < 0 {
					d = -d
				}
				sum += int64(d)
				n++
			}
			prev = l
		}
	}
	return float64(sum) / float64(n)
}

// TestExportOutputSharpenE2E exports one real RAW through the full JPEG
// pipeline (decode → look → detail → resize → output sharpen → encode) at
// three sharpen settings and asserts the outputs differ in acutance.
func TestExportOutputSharpenE2E(t *testing.T) {
	raw := sampleRAW(t)
	photo := store.Photo{FolderPath: filepath.Dir(raw), FileName: filepath.Base(raw)}
	dir := t.TempDir()

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

	req := Request{Format: "jpeg", JpegQuality: 90, LongEdge: 1024, ColorSpace: "srgb"}
	cases := []struct {
		name, target, amount string
	}{
		{"off.jpg", "off", ""},
		{"screen.jpg", "screen", "standard"},
		{"matte.jpg", "matte", "high"},
	}
	grads := map[string]float64{}
	var dims image.Rectangle
	for i, c := range cases {
		r := req
		r.SharpenTarget, r.SharpenAmount = c.target, c.amount
		if err := exportOne(t.Context(), photo, filepath.Join(dir, c.name), r); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		img := decode(c.name)
		if i == 0 {
			dims = img.Bounds()
		} else if img.Bounds() != dims {
			t.Fatalf("%s: dimensions %v != %v", c.name, img.Bounds(), dims)
		}
		grads[c.name] = meanGradient(img)
	}
	t.Logf("mean gradient: off=%.3f screen=%.3f matte-high=%.3f",
		grads["off.jpg"], grads["screen.jpg"], grads["matte.jpg"])
	if !(grads["off.jpg"] < grads["screen.jpg"] && grads["screen.jpg"] < grads["matte.jpg"]) {
		t.Fatalf("acutance not monotonic: off=%.3f screen=%.3f matte-high=%.3f",
			grads["off.jpg"], grads["screen.jpg"], grads["matte.jpg"])
	}
}
