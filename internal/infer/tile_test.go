package infer

import (
	"context"
	"image"
	"image/color"
	"math/rand"
	"os"
	"testing"
	"time"
)

func TestRampWeight(t *testing.T) {
	// Interior = 1, symmetric ramps at both ends.
	if w := rampWeight(50, 100, 16); w != 1 {
		t.Errorf("interior weight %v, want 1", w)
	}
	if w := rampWeight(0, 100, 16); w >= 0.1 {
		t.Errorf("leading edge weight %v, want small", w)
	}
	if a, b := rampWeight(3, 100, 16), rampWeight(96, 100, 16); a != b {
		t.Errorf("ramp asymmetric: %v vs %v", a, b)
	}
	if w := rampWeight(0, 100, 0); w != 1 {
		t.Errorf("no-overlap weight %v, want 1", w)
	}
}

// devModel loads a model staged under .devdata/models, skipping when absent.
func devModel(t *testing.T, id ModelID) *Session {
	t.Helper()
	dir := os.Getenv("MARRAW_TEST_MODELS_DIR")
	if dir == "" {
		dir = "../../.devdata/models"
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("model dir unavailable: %v", err)
	}
	if err := EnsureRuntime(); err != nil {
		t.Skipf("runtime unavailable: %v", err)
	}
	m := NewManager(dir)
	spec := ModelSpec{ID: id, Version: "1", PreferGPU: os.Getenv("MARRAW_TEST_GPU") == "1"}
	s, err := m.Session(context.Background(), spec, nil)
	if err != nil {
		t.Skipf("model %s unavailable: %v", id, err)
	}
	if spec.PreferGPU {
		t.Logf("session OnGPU=%v", s.OnGPU)
	}
	return s
}

func noisyImage(w, h int) *image.RGBA {
	rng := rand.New(rand.NewSource(42))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			base := 80 + 60*(x/32+y/32)%2
			n := rng.Intn(61) - 30
			v := uint8(min(255, max(0, base+n)))
			img.SetRGBA(x, y, color.RGBA{v, v, uint8(min(255, int(v)+10)), 255})
		}
	}
	return img
}

// TestRunTiledSCUNet proves the tiled harness against the real denoiser and
// logs per-megapixel CPU cost — the number the denoise design doc quotes.
func TestRunTiledSCUNet(t *testing.T) {
	if testing.Short() {
		t.Skip("real-model benchmark")
	}
	sess := devModel(t, "scunet")
	src := noisyImage(512, 384)
	start := time.Now()
	out, err := RunTiled(context.Background(), sess, src, TileConfig{Size: 256, Overlap: 16, Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	dur := time.Since(start)
	mp := float64(512*384) / 1e6
	t.Logf("SCUNet CPU: %.2f MP in %s → %.1f s/MP", mp, dur, dur.Seconds()/mp)

	if got := out.Bounds(); got.Dx() != 512 || got.Dy() != 384 {
		t.Fatalf("output dims %v", got)
	}
	// The denoiser must actually reduce high-frequency noise: compare local
	// pixel variance before and after on an interior region.
	if vb, va := localVar(src), localVar(out); va > vb*0.6 {
		t.Errorf("denoise too weak: variance %.1f → %.1f", vb, va)
	}
}

// TestRunTiledSwin2SR proves the 2x SR path: doubled dims, sane runtime.
func TestRunTiledSwin2SR(t *testing.T) {
	if testing.Short() {
		t.Skip("real-model benchmark")
	}
	sess := devModel(t, "swin2sr")
	src := noisyImage(256, 192)
	start := time.Now()
	out, err := RunTiled(context.Background(), sess, src, TileConfig{Size: 128, Overlap: 8, Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	dur := time.Since(start)
	mp := float64(256*192) / 1e6
	t.Logf("Swin2SR CPU: %.2f MP in %s → %.1f s/MP", mp, dur, dur.Seconds()/mp)
	if got := out.Bounds(); got.Dx() != 512 || got.Dy() != 384 {
		t.Fatalf("SR output dims %v, want 512x384", got)
	}
}

func TestRunTiledCancel(t *testing.T) {
	sess := devModel(t, "scunet")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunTiled(ctx, sess, noisyImage(300, 300), TileConfig{Size: 256, Overlap: 16, Scale: 1}); err == nil {
		t.Fatal("cancelled tiled run did not fail")
	}
}

// localVar measures mean local (3x3) luma variance — a noise proxy.
func localVar(img *image.RGBA) float64 {
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
