package export

import (
	"context"
	"image"
	"image/color"
	"math/rand"
	"os"
	"testing"

	"github.com/marrasen/marraw/internal/infer"
)

// TestDenoiseSkip pins the two guards that decide whether the denoiser earns
// its minutes. These are the whole point of unlock criterion 4: cost is linear
// in megapixels, and on a heavily downscaled export the model makes the result
// worse, not better.
func TestDenoiseSkip(t *testing.T) {
	tests := []struct {
		name       string
		w, h       int
		longEdge   int
		budgetMP   float64
		wantSkip   bool
		wantReason string
	}{
		{
			name: "web export off a 33MP master downscales 4.4x",
			w:    7028, h: 4688, longEdge: 1600,
			wantSkip: true, wantReason: "downscales",
		},
		{
			name: "exactly 2x downscale is already too much",
			w:    3200, h: 2400, longEdge: 1600,
			wantSkip: true, wantReason: "downscales",
		},
		{
			name: "just under 2x is worth denoising",
			w:    3000, h: 2250, longEdge: 1600,
			wantSkip: false,
		},
		{
			name: "near-native export of a crop",
			w:    2000, h: 1500, longEdge: 1800,
			wantSkip: false,
		},
		{
			name: "full resolution export of a small crop",
			w:    2000, h: 1500, longEdge: 0,
			wantSkip: false,
		},
		{
			name: "full resolution export of the whole master busts the budget",
			w:    7028, h: 4688, longEdge: 0,
			wantSkip: true, wantReason: "budget",
		},
		{
			name: "a generous budget admits the whole master",
			w:    7028, h: 4688, longEdge: 0, budgetMP: 40,
			wantSkip: false,
		},
		{
			name: "upscaling export never trips the downscale guard",
			w:    2000, h: 1500, longEdge: 4000,
			wantSkip: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := denoiseSkip(tc.w, tc.h, tc.longEdge, tc.budgetMP)
			if (got != "") != tc.wantSkip {
				t.Fatalf("denoiseSkip = %q, want skip=%v", got, tc.wantSkip)
			}
			if tc.wantReason != "" && !contains(got, tc.wantReason) {
				t.Errorf("reason %q does not mention %q", got, tc.wantReason)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestBlend(t *testing.T) {
	mk := func(v uint8) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
		}
		return img
	}
	tests := []struct {
		weight float64
		want   uint8
	}{
		{0, 100},   // strength 0 leaves the input alone
		{1, 200},   // strength 1 is the model's raw output
		{0.5, 150}, // and it interpolates in between
		{0.25, 125},
	}
	for _, tc := range tests {
		dst, src := mk(100), mk(200)
		blend(dst, src, tc.weight)
		if got := dst.Pix[0]; got != tc.want {
			t.Errorf("blend weight %v = %d, want %d", tc.weight, got, tc.want)
		}
		if dst.Pix[3] != 255 {
			t.Errorf("blend weight %v clobbered alpha: %d", tc.weight, dst.Pix[3])
		}
	}
}

func TestRestoreOptionsInactive(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	// A nil *RestoreOptions must be a no-op, matching the same
	// absent-means-disabled contract as Request.AIMaps/Lenses/Fills.
	var nilOpts *RestoreOptions
	if out, err := nilOpts.apply(context.Background(), img, 0); err != nil || out != img {
		t.Fatalf("nil options: got (%p, %v), want (%p, nil)", out, err, img)
	}
	// So must a populated struct with nothing switched on.
	off := &RestoreOptions{Models: infer.NewManager(t.TempDir())}
	if out, err := off.apply(context.Background(), img, 0); err != nil || out != img {
		t.Fatalf("all stages off: got (%p, %v), want (%p, nil)", out, err, img)
	}
}

// TestRestoreDenoiseStrength runs the real denoiser and proves the strength
// control is monotonic: more strength means less noise. Without that the knob
// could be wired to nothing and every other test would still pass.
//
// Needs models staged by scripts/setup-devmodels.ps1; skips otherwise.
func TestRestoreDenoiseStrength(t *testing.T) {
	if testing.Short() {
		t.Skip("real-model test")
	}
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

	mgr := infer.NewManager(dir)
	base := noisySquare(384)
	if _, err := mgr.Session(context.Background(),
		restoreSpec(denoiseModel, false), nil); err != nil {
		t.Skipf("denoise model unavailable: %v", err)
	}

	varAt := func(strength float64) float64 {
		img := image.NewRGBA(base.Bounds())
		copy(img.Pix, base.Pix)
		opts := &RestoreOptions{Models: mgr, DenoiseStrength: strength}
		out, err := opts.apply(context.Background(), img, 0)
		if err != nil {
			t.Fatalf("strength %v: %v", strength, err)
		}
		if out.Bounds() != base.Bounds() {
			t.Fatalf("strength %v changed dims to %v", strength, out.Bounds())
		}
		return localVariance(out)
	}

	v0 := localVariance(base)
	vHalf := varAt(0.5)
	vFull := varAt(1)
	t.Logf("local variance: input %.1f, strength 0.5 %.1f, strength 1.0 %.1f", v0, vHalf, vFull)

	if vFull >= v0 {
		t.Errorf("full strength did not denoise: %.1f -> %.1f", v0, vFull)
	}
	if vHalf >= v0 || vHalf <= vFull {
		t.Errorf("strength is not monotonic: input %.1f, half %.1f, full %.1f", v0, vHalf, vFull)
	}
}

// TestRestoreUpscale covers the SR stage: it must double the pixels when the
// export asks for more than the source has, and stay out of the way otherwise.
// Unlike denoise this path does run on the DirectML provider that ships with
// the app, so it is the half of the stage that needs no extra download.
//
// Needs models staged by scripts/setup-devmodels.ps1; skips otherwise.
func TestRestoreUpscale(t *testing.T) {
	if testing.Short() {
		t.Skip("real-model test")
	}
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
	mgr := infer.NewManager(dir)
	if _, err := mgr.Session(context.Background(), restoreSpec(upscaleModel, false), nil); err != nil {
		t.Skipf("upscale model unavailable: %v", err)
	}

	src := noisySquare(192)
	opts := &RestoreOptions{Models: mgr, Upscale: true}

	// Export asks for 2x the source long edge, so SR should run and land on it.
	out, err := opts.apply(context.Background(), src, 384)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Bounds(); got.Dx() != 384 || got.Dy() != 384 {
		t.Errorf("upscaled dims %v, want 384x384", got)
	}

	// Target between 1x and 2x of the source: the stage must downscale to half
	// the target first so the model's 2x output lands on the target, rather
	// than upscaling the full source and discarding most of the result.
	mid := image.NewRGBA(src.Bounds())
	copy(mid.Pix, src.Pix)
	out3, err := opts.apply(context.Background(), mid, 288)
	if err != nil {
		t.Fatal(err)
	}
	if got := out3.Bounds(); got.Dx() != 288 || got.Dy() != 288 {
		t.Errorf("dims %v for a 288 px target, want 288x288 (half=144, SR x2)", got)
	}

	// Export fits inside the source, so upscaling would only waste time.
	same := image.NewRGBA(src.Bounds())
	copy(same.Pix, src.Pix)
	out2, err := opts.apply(context.Background(), same, 192)
	if err != nil {
		t.Fatal(err)
	}
	if out2 != same {
		t.Errorf("upscale ran for a non-enlarging export: dims %v", out2.Bounds())
	}

	// Full-resolution export: SR over the whole frame is out of scope.
	full := image.NewRGBA(src.Bounds())
	copy(full.Pix, src.Pix)
	out4, err := opts.apply(context.Background(), full, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out4 != full {
		t.Errorf("upscale ran for a full-resolution export: dims %v", out4.Bounds())
	}
}

func noisySquare(edge int) *image.RGBA {
	rng := rand.New(rand.NewSource(7))
	img := image.NewRGBA(image.Rect(0, 0, edge, edge))
	for y := 0; y < edge; y++ {
		for x := 0; x < edge; x++ {
			base := 90 + 50*(x/48+y/48)%2
			v := uint8(min(255, max(0, base+rng.Intn(61)-30)))
			img.SetRGBA(x, y, color.RGBA{v, v, uint8(min(255, int(v)+8)), 255})
		}
	}
	return img
}

// localVariance is a noise proxy: mean 3x3 luma variance over the interior.
func localVariance(img *image.RGBA) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	luma := func(x, y int) float64 {
		i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
		return 0.299*float64(img.Pix[i]) + 0.587*float64(img.Pix[i+1]) + 0.114*float64(img.Pix[i+2])
	}
	var sum float64
	n := 0
	for y := 1; y < h-1; y += 2 {
		for x := 1; x < w-1; x += 2 {
			var m, m2 float64
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					v := luma(x+dx, y+dy)
					m += v
					m2 += v * v
				}
			}
			m /= 9
			sum += m2/9 - m*m
			n++
		}
	}
	return sum / float64(n)
}
