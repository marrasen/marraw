package libraw

import (
	"bytes"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestOpenMetadataThumb(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	start := time.Now()
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	openDur := time.Since(start)

	md := p.Metadata()
	t.Logf("open=%v  %s %s  ISO %.0f  1/%.0fs  f/%.1f  %.0fmm  %dx%d flip=%d  taken=%s",
		openDur, md.Make, md.Model, md.ISO, 1/md.Shutter, md.Aperture, md.FocalLen,
		md.Width, md.Height, md.Orientation, md.TakenAt.Format(time.DateTime))
	if md.Make == "" || md.Width == 0 {
		t.Errorf("metadata looks empty: %+v", md)
	}

	start = time.Now()
	thumb, err := p.EmbeddedThumb()
	if err != nil {
		t.Fatalf("EmbeddedThumb: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("thumb is not decodable JPEG: %v", err)
	}
	t.Logf("thumb=%v  %d bytes, %dx%d", time.Since(start), len(thumb), cfg.Width, cfg.Height)
}

func TestProcessHalfAndReprocess(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := p.Unpack(); err != nil {
		t.Fatal(err)
	}
	unpackDur := time.Since(start)

	params := DefaultParams()
	params.HalfSize = true
	params.UserQual = DemosaicLinear

	start = time.Now()
	img, err := p.Process(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	halfDur := time.Since(start)
	if img.Channels != 3 || img.Bits != 8 || img.Width == 0 {
		t.Fatalf("unexpected image: %+v", img)
	}
	if want := img.Width * img.Height * 3; len(img.Data) != want {
		t.Fatalf("data size %d, want %d", len(img.Data), want)
	}

	// Reprocess with +1 EV — must work without re-unpacking and produce a
	// brighter image.
	params.ExpShift = 2.0
	start = time.Now()
	img2, err := p.Process(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	reprocDur := time.Since(start)
	if img2.Width != img.Width || img2.Height != img.Height {
		t.Fatalf("reprocess size changed: %dx%d vs %dx%d", img2.Width, img2.Height, img.Width, img.Height)
	}
	if m1, m2 := meanLuma(img), meanLuma(img2); m2 <= m1 {
		t.Errorf("+1 EV did not brighten: mean %v -> %v", m1, m2)
	}

	t.Logf("unpack=%v  half_size=%v (%dx%d)  reprocess=%v", unpackDur, halfDur, img.Width, img.Height, reprocDur)
}

func meanLuma(img *Image) float64 {
	var sum uint64
	for _, b := range img.Data {
		sum += uint64(b)
	}
	return float64(sum) / float64(len(img.Data))
}

// A pooled Processor is reused across jobs, and libraw_recycle() deliberately
// preserves params. So a half-size decode (the calibration pass) would leave
// half_size set, and the next job's Metadata() — which never applies params —
// would record half the real dimensions into the catalog.
func TestRecycleResetsParams(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	full := p.Metadata()

	// Decode at half size, as pyramid.MeasureAutoBrightEV does.
	params := DefaultParams()
	params.HalfSize = true
	if _, err := p.Process(t.Context(), params); err != nil {
		t.Fatal(err)
	}
	p.Recycle()

	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	got := p.Metadata()
	if got.Width != full.Width || got.Height != full.Height {
		t.Fatalf("after a half-size decode + Recycle, Metadata = %dx%d, want %dx%d",
			got.Width, got.Height, full.Width, full.Height)
	}
}
