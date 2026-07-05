package pyramid

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/marrasen/marraw/internal/libraw"
)

// TestCalibrateLook verifies that the per-photo adaptive gamma brings our
// RAW renders within a sane distance of the camera JPEG's mean luminance
// on real files. Run with -v for the per-file numbers.
func TestCalibrateLook(t *testing.T) {
	dir := os.Getenv("MARRAW_TEST_RAW_DIR")
	if dir == "" {
		dir = `D:\Photos\2026-04-18 Velox Valor Trollhättan`
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.ARW"))
	if len(files) == 0 {
		t.Skipf("no ARW files in %s", dir)
	}
	if len(files) > 5 {
		// Spread across the folder for scene variety.
		files = []string{files[0], files[len(files)/4], files[len(files)/2], files[3*len(files)/4], files[len(files)-1]}
	}

	for _, path := range files {
		proc, err := libraw.New()
		if err != nil {
			t.Fatal(err)
		}
		if err := proc.Open(path); err != nil {
			proc.Close()
			t.Fatalf("%s: %v", path, err)
		}
		thumbData, err := proc.EmbeddedThumb()
		if err != nil {
			proc.Close()
			continue
		}
		thumbImg, err := jpeg.Decode(bytes.NewReader(thumbData))
		if err != nil {
			proc.Close()
			continue
		}
		camera := MeanLuma(toRGBA(thumbImg))

		params := libraw.DefaultParams()
		params.HalfSize = true
		img, err := proc.Process(params)
		if err != nil {
			proc.Close()
			t.Fatalf("%s: %v", path, err)
		}
		rgba, err := FromLibraw(img)
		proc.Close()
		if err != nil {
			t.Fatal(err)
		}
		raw := MeanLuma(rgba)

		gamma := ComputeLookGamma(raw, camera)
		ApplyLook(rgba, gamma, nil)
		got := MeanLuma(rgba)

		t.Logf("%s: camera=%.1f raw=%.1f gamma=%.2f -> %.1f", filepath.Base(path), camera, raw, gamma, got)
		// The gamma is clamped and the S-curve shifts things slightly, so
		// allow a modest band rather than exact equality.
		if math.Abs(got-camera) > 20 {
			t.Errorf("%s: looked render mean %.1f too far from camera %.1f (gamma %.2f)",
				filepath.Base(path), got, camera, gamma)
		}
	}
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	dst := image.NewRGBA(img.Bounds())
	xdraw.Copy(dst, image.Point{}, img, img.Bounds(), xdraw.Src, nil)
	return dst
}
