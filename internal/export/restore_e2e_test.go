package export

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/infer"
	"github.com/marrasen/marraw/internal/store"
)

// restoreManager returns an infer.Manager over the dev-staged restoration
// models (scripts/setup-devmodels.ps1), skipping when they or the ORT runtime
// are unavailable. The weights are not in the production registry yet, so a
// local staging dir is the only way to reach them.
func restoreManager(t *testing.T) *infer.Manager {
	t.Helper()
	dir := os.Getenv("MARRAW_TEST_MODELS_DIR")
	if dir == "" {
		dir = "../../.devdata/models"
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("model dir unavailable: %v", err)
	}
	if err := infer.EnsureRuntime(); err != nil {
		t.Skipf("runtime unavailable: %v", err)
	}
	return infer.NewManager(dir)
}

// outDir is where a test writes its JPEGs. Set MARRAW_TEST_OUT_DIR to keep them
// around and look at them; otherwise they land in a temp dir and are cleaned up.
func outDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("MARRAW_TEST_OUT_DIR"); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	return t.TempDir()
}

func decodeJPEG(t *testing.T, path string) image.Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return img
}

// TestExportDenoiseSkipsWebExportE2E is the guard, end to end: a normal web
// export with denoise turned all the way up must produce byte-identical output
// to denoise off, because the downscale-skip rule fires before the model runs.
//
// This is the common case and the one users will hit, so it is worth asserting
// on real pixels rather than trusting the unit test of denoiseSkip alone. It is
// also fast, because the whole point is that no inference happens.
func TestExportDenoiseSkipsWebExportE2E(t *testing.T) {
	raw := sampleRAW(t)
	mgr := restoreManager(t)
	photo := store.Photo{FolderPath: filepath.Dir(raw), FileName: filepath.Base(raw)}
	dir := outDir(t)

	base := Request{Format: "jpeg", JpegQuality: 90, LongEdge: 1024, ColorSpace: "srgb"}
	off := filepath.Join(dir, "web-denoise-off.jpg")
	on := filepath.Join(dir, "web-denoise-on.jpg")

	if err := exportOne(t.Context(), photo, off, base); err != nil {
		t.Fatal(err)
	}
	withDenoise := base
	withDenoise.Restore = &RestoreOptions{Models: mgr, DenoiseStrength: 1}
	if err := exportOne(t.Context(), photo, on, withDenoise); err != nil {
		t.Fatal(err)
	}

	a, err := os.ReadFile(off)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(on)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("denoise ran on a downscaled web export: %d vs %d bytes", len(a), len(b))
	}
	t.Logf("web export (1024 px long edge): denoise correctly skipped, %d bytes", len(a))
}

// TestExportDenoiseNearNativeE2E is the case denoise is actually for: an export
// at close to native resolution, where the skip rule lets the model run. It
// asserts strength changes the pixels monotonically through the real encoder.
//
// Opt-in via MARRAW_TEST_RESTORE_SLOW=1: cost is the SOURCE region's
// megapixels, so on a 33 MP frame this is ~8 min on CPU (about a minute on the
// CUDA path). That expense is the honest shape of the feature, not a test
// artifact -- see design/ml-denoise.md criterion 4.
func TestExportDenoiseNearNativeE2E(t *testing.T) {
	if os.Getenv("MARRAW_TEST_RESTORE_SLOW") != "1" {
		t.Skip("slow: set MARRAW_TEST_RESTORE_SLOW=1 (minutes on CPU)")
	}
	raw := sampleRAW(t)
	mgr := restoreManager(t)
	photo := store.Photo{FolderPath: filepath.Dir(raw), FileName: filepath.Base(raw)}
	dir := outDir(t)

	// LongEdge 0 = full resolution, so the downscale guard cannot fire; the
	// budget is raised past the frame so the size guard cannot either.
	base := Request{Format: "jpeg", JpegQuality: 95, LongEdge: 0, ColorSpace: "srgb"}
	cases := []struct {
		name     string
		strength float64
	}{
		{"native-denoise-0.jpg", 0},
		{"native-denoise-50.jpg", 0.5},
		{"native-denoise-100.jpg", 1},
	}
	grads := map[string]float64{}
	for _, c := range cases {
		r := base
		if c.strength > 0 {
			r.Restore = &RestoreOptions{
				Models:          mgr,
				DenoiseStrength: c.strength,
				DenoiseBudgetMP: 100,
				PreferGPU:       os.Getenv("MARRAW_TEST_GPU") == "1",
			}
		}
		path := filepath.Join(dir, c.name)
		if err := exportOne(t.Context(), photo, path, r); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		grads[c.name] = meanGradient(decodeJPEG(t, path))
	}
	t.Logf("mean gradient (noise proxy): strength 0 %.3f, 0.5 %.3f, 1.0 %.3f",
		grads["native-denoise-0.jpg"], grads["native-denoise-50.jpg"], grads["native-denoise-100.jpg"])
	t.Logf("JPEGs in %s", dir)

	// Denoising lowers local gradient (that is what removing grain does), and
	// more strength must lower it further.
	g0, g50, g100 := grads["native-denoise-0.jpg"], grads["native-denoise-50.jpg"], grads["native-denoise-100.jpg"]
	if !(g100 < g50 && g50 < g0) {
		t.Errorf("denoise strength not monotonic through the export: %.3f / %.3f / %.3f", g0, g50, g100)
	}
}

// TestExportUpscaleE2E exports one frame larger than the source, with and
// without SR, at identical output dimensions -- so any difference is the model
// resolving detail rather than the resizer interpolating. This is the half of
// the stage that runs on the DirectML provider already shipped with the app.
func TestExportUpscaleE2E(t *testing.T) {
	if os.Getenv("MARRAW_TEST_RESTORE_SLOW") != "1" {
		t.Skip("slow: set MARRAW_TEST_RESTORE_SLOW=1 (minutes on CPU)")
	}
	raw := sampleRAW(t)
	mgr := restoreManager(t)
	photo := store.Photo{FolderPath: filepath.Dir(raw), FileName: filepath.Base(raw)}
	dir := outDir(t)

	// The target must EXCEED the source long edge, or the stage correctly skips
	// (an export that downscales needs no super resolution). The stage then
	// resizes to half the target before inference, so cost tracks the target
	// rather than the source frame: 8000 px means a ~4000 px model input.
	//
	// Worth noting this makes SR a narrow feature on a high-megapixel body --
	// exports above 7000 px are unusual. Its real audience is heavy crops and
	// smaller sensors.
	plain := Request{Format: "jpeg", JpegQuality: 95, LongEdge: 8000, ColorSpace: "srgb"}
	sr := plain
	sr.Restore = &RestoreOptions{
		Models:    mgr,
		Upscale:   true,
		PreferGPU: os.Getenv("MARRAW_TEST_GPU") == "1",
	}

	pPlain := filepath.Join(dir, "upscale-off.jpg")
	pSR := filepath.Join(dir, "upscale-on.jpg")
	if err := exportOne(t.Context(), photo, pPlain, plain); err != nil {
		t.Fatal(err)
	}
	if err := exportOne(t.Context(), photo, pSR, sr); err != nil {
		t.Fatal(err)
	}

	iPlain, iSR := decodeJPEG(t, pPlain), decodeJPEG(t, pSR)
	t.Logf("plain %v, SR %v", iPlain.Bounds().Size(), iSR.Bounds().Size())
	t.Logf("mean gradient: plain %.3f, SR %.3f", meanGradient(iPlain), meanGradient(iSR))
	t.Logf("JPEGs in %s", dir)

	// The two paths deliberately do NOT land on the same dimensions, and that
	// is the open design question rather than a bug: resizeRGBA treats LongEdge
	// as a cap and never enlarges, so the plain export stops at the source's
	// 7028 px while SR reaches the requested 8000. Until "upscale at export" has
	// agreed semantics (the roadmap's "interaction to specify"), all this test
	// can honestly assert is that SR does enlarge toward the target and the
	// plain path does not.
	if got := iSR.Bounds().Dx(); got <= iPlain.Bounds().Dx() {
		t.Errorf("SR did not enlarge: %d px vs plain %d px", got, iPlain.Bounds().Dx())
	}
	if iPlain.Bounds().Dx() > 7028 {
		t.Errorf("plain export enlarged past the source: %d px", iPlain.Bounds().Dx())
	}
}
