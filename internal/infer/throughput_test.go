package infer

// Throughput and stability measurement for the restoration models, kept
// separate from tile_test.go's correctness tests because it answers a
// different question: is ML denoise fast enough and stable enough to ship?
// design/ml-denoise.md holds the feature on the answer, and its unlock
// criterion 1 is a green 100-tile GPU soak.
//
// Everything here needs models staged by scripts/setup-devmodels.ps1 and skips
// without them. The GPU path additionally needs MARRAW_TEST_GPU=1 and a
// DirectML ORT build (scripts/setup-ort.ps1 -DirectML) pointed at by
// MARRAW_ORT_LIB.
//
//	$env:MARRAW_TEST_TILES="100"
//	$env:MARRAW_ORT_LIB="...\third_party\onnxruntime-directml\lib\onnxruntime.dll"
//	$env:MARRAW_TEST_GPU="1"
//	go test ./internal/infer -run 'TestRunTiled(Throughput|Soak)|TestGPUSessionChurn' -v -count=1 -timeout 60m

import (
	"context"
	"image"
	"os"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"
)

// modelCase pairs a staged model with the tile geometry it is measured at.
// Tile edges match what the feature would actually use: 256 for denoise (the
// design doc's VRAM note sizes a 256^2 fp32 SCUNet tile comfortably under
// 1 GB), 128 for the 2x SR model whose output tile is four times its input.
type modelCase struct {
	id   ModelID
	name string
	cfg  TileConfig
}

var modelCases = []modelCase{
	{id: "scunet", name: "SCUNet denoise", cfg: TileConfig{Size: 256, Overlap: 16, Scale: 1}},
	{id: "swin2sr", name: "Swin2SR x2", cfg: TileConfig{Size: 128, Overlap: 8, Scale: 2}},
}

// wantTiles is the tile budget: MARRAW_TEST_TILES, defaulting to a fast smoke.
// The design doc's unlock criterion asks for 100.
func wantTiles(t *testing.T) int {
	t.Helper()
	if v := os.Getenv("MARRAW_TEST_TILES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("MARRAW_TEST_TILES=%q: want a positive integer", v)
		}
		return n
	}
	return 6
}

// tileSizeOverride applies MARRAW_TEST_TILE_SIZE, which exists because the
// tile edge is not just a performance knob on the GPU path: DirectML dies with
// a native access violation above a per-model threshold, so finding and
// recording that threshold is part of the measurement.
func tileSizeOverride(t *testing.T, cfg TileConfig) TileConfig {
	t.Helper()
	v := os.Getenv("MARRAW_TEST_TILE_SIZE")
	if v == "" {
		return cfg
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 2*cfg.Overlap {
		t.Fatalf("MARRAW_TEST_TILE_SIZE=%q: want an integer > 2*overlap (%d)", v, 2*cfg.Overlap)
	}
	cfg.Size = n
	return cfg
}

// squareFor sizes a square input whose tile grid holds at least want tiles,
// and returns the exact count using RunTiled's own grid math so the reported
// number is what actually ran rather than what was asked for.
func squareFor(cfg TileConfig, want int) (edge, tiles int) {
	step := cfg.Size - 2*cfg.Overlap
	g := 1
	for g*g < want {
		g++
	}
	edge = g * step
	if edge < cfg.Size {
		edge = cfg.Size
	}
	nx := (edge + step - 1) / step
	return edge, nx * nx
}

// timedRun executes one tiled pass, returning wall time and per-tile
// durations. TileConfig.Progress fires after each tile completes, so the gap
// between callbacks is that tile's cost -- which is the only way to separate
// steady state from first-tile warmup.
func timedRun(t *testing.T, sess *Session, src *image.RGBA, cfg TileConfig) (time.Duration, []time.Duration) {
	t.Helper()
	var per []time.Duration
	last := time.Now()
	cfg.Progress = func(done, total int) {
		now := time.Now()
		per = append(per, now.Sub(last))
		last = now
	}
	start := time.Now()
	out, err := RunTiled(context.Background(), sess, src, cfg)
	total := time.Since(start)
	if err != nil {
		t.Fatalf("RunTiled: %v", err)
	}
	if got := out.Bounds(); got.Dx() != src.Bounds().Dx()*cfg.Scale || got.Dy() != src.Bounds().Dy()*cfg.Scale {
		t.Fatalf("output dims %v for %v input at scale %d", got, src.Bounds(), cfg.Scale)
	}
	return total, per
}

// tileStats returns min, median and max of per-tile durations in ms.
func tileStats(per []time.Duration) (lo, med, hi float64) {
	if len(per) == 0 {
		return 0, 0, 0
	}
	ms := make([]float64, len(per))
	for i, d := range per {
		ms[i] = float64(d.Microseconds()) / 1000
	}
	sort.Float64s(ms)
	return ms[0], ms[len(ms)/2], ms[len(ms)-1]
}

// epLabel names the execution provider a session actually got, so a RESULT
// line is unambiguous about which GPU path produced it -- "gpu" would not
// distinguish DirectML from CUDA, and they behave very differently.
func epLabel(s *Session) string {
	if !s.OnGPU {
		return "cpu"
	}
	switch runtime.GOOS {
	case "windows":
		if os.Getenv("MARRAW_GPU_EP") == "cuda" {
			return "cuda"
		}
		return "dml"
	case "darwin":
		return "coreml"
	}
	return "gpu"
}

// runtimePath reports which ORT library actually loaded, so a result line is
// self-describing about CPU-vs-DirectML build rather than relying on whatever
// env vars the operator believes they set.
func runtimePath() string {
	p, err := locateRuntime()
	if err != nil {
		return "unknown"
	}
	return p
}

// TestRunTiledThroughput is the measurement the denoise design doc quotes.
//
// It runs each model twice: a discarded warmup pass, then the timed pass. The
// warmup is not politeness. DirectML compiles kernels on first execution, and
// over a handful of tiles that one-time cost dominates the wall clock -- a
// plausible source of the spread in the original Arc 140V numbers, which were
// taken over six tiles with no warmup.
func TestRunTiledThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("real-model measurement")
	}
	tiles := wantTiles(t)
	mgr := devManager(t)
	t.Logf("runtime: %s", runtimePath())

	for _, mc := range modelCases {
		t.Run(string(mc.id), func(t *testing.T) {
			sess := devSession(t, mgr, mc.id)
			cfg := tileSizeOverride(t, mc.cfg)
			edge, want := squareFor(cfg, tiles)
			src := noisyImage(edge, edge)

			// Warmup: one small full pass, timings discarded.
			warm := noisyImage(cfg.Size, cfg.Size)
			if _, err := RunTiled(context.Background(), sess, warm, cfg); err != nil {
				t.Fatalf("warmup: %v", err)
			}

			total, per := timedRun(t, sess, src, cfg)
			mp := float64(edge*edge) / 1e6
			lo, med, hi := tileStats(per)

			// One machine-readable line per case so the design-doc tables are
			// transcription rather than arithmetic. s/MP is per INPUT
			// megapixel, matching how the existing table was computed.
			t.Logf("RESULT model=%s ep=%s tile=%d tiles=%d edge=%d mp=%.2f wall=%.1fs s_per_mp=%.1f ms_tile_min=%.0f ms_tile_med=%.0f ms_tile_max=%.0f",
				mc.id, epLabel(sess), cfg.Size, len(per), edge, mp, total.Seconds(),
				total.Seconds()/mp, lo, med, hi)
			t.Logf("%s: %d tiles (%d requested), %.2f MP in %s", mc.name, len(per), want, mp, total.Round(time.Millisecond))
		})
	}
}

// TestRunTiledSoak is unlock criterion 1's actual gate: a long single-session
// run on the GPU. The failure mode on both GPUs measured so far is a native
// access violation, which kills the test process outright -- so "this test
// completed" is the result, and a crash is the other result.
//
// Subtests share a process, so a crash in one aborts the rest. Select a single
// model with -run 'TestRunTiledSoak/swin2sr' when another is known to crash.
//
// Skipped without MARRAW_TEST_GPU=1: on CPU it measures nothing the throughput
// test does not already cover, at several minutes a run.
func TestRunTiledSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("real-model soak")
	}
	if os.Getenv("MARRAW_TEST_GPU") != "1" {
		t.Skip("GPU soak: set MARRAW_TEST_GPU=1 with a DirectML ORT build")
	}
	tiles := wantTiles(t)
	mgr := devManager(t)

	for _, mc := range modelCases {
		t.Run(string(mc.id), func(t *testing.T) {
			sess := devSession(t, mgr, mc.id)
			if !sess.OnGPU {
				t.Skipf("session fell back to CPU (runtime %s has no GPU provider)", runtimePath())
			}
			cfg := tileSizeOverride(t, mc.cfg)
			edge, _ := squareFor(cfg, tiles)
			src := noisyImage(edge, edge)

			total, per := timedRun(t, sess, src, cfg)
			mp := float64(edge*edge) / 1e6
			lo, med, hi := tileStats(per)
			t.Logf("RESULT model=%s-soak ep=%s tile=%d tiles=%d edge=%d mp=%.2f wall=%.1fs s_per_mp=%.1f ms_tile_min=%.0f ms_tile_med=%.0f ms_tile_max=%.0f",
				mc.id, epLabel(sess), cfg.Size, len(per), edge, mp, total.Seconds(),
				total.Seconds()/mp, lo, med, hi)

			// A GPU that degrades into returning garbage would otherwise pass
			// as "survived", so re-check the output is still image-like.
			// Only the denoiser has a noise-reduction contract to assert.
			out, err := RunTiled(context.Background(), sess, src, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Scale == 1 {
				if vb, va := localVar(src), localVar(out); va > vb*0.6 {
					t.Errorf("denoise too weak after soak: variance %.1f -> %.1f", vb, va)
				}
			}
		})
	}
}

// TestGPUSessionChurn exercises the one-resident-GPU-session policy: loading a
// second PreferGPU model evicts and destroys the first (infer.go). Two heavy
// DirectML sessions in one process crashed deterministically on Arc, which is
// why the policy exists, and a soak alone never reaches that path.
//
// The sequence loads swin2sr, then scunet, then swin2sr again, so each load
// after the first forces an eviction-and-destroy of a live GPU session. Only
// swin2sr executes tiles: SCUNet's DirectML kernels fault on execution (a
// measured E_INVALIDARG in its transformer Add node, see design/ml-denoise.md),
// which would kill the process and tell us nothing about session lifecycle.
// Loading it is enough -- session creation is the half that stresses the
// driver's allocator, which is what the policy guards.
func TestGPUSessionChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("real-model churn")
	}
	if os.Getenv("MARRAW_TEST_GPU") != "1" {
		t.Skip("GPU churn: set MARRAW_TEST_GPU=1 with a DirectML ORT build")
	}
	mgr := devManager(t)
	sr := modelCases[1] // swin2sr: the model that runs on DirectML today

	steps := []struct {
		id  ModelID
		run bool
	}{
		{id: sr.id, run: true},
		{id: modelCases[0].id, run: false}, // evicts the swin2sr GPU session
		{id: sr.id, run: true},             // evicts scunet, fresh session after eviction
	}
	for i, st := range steps {
		sess := devSession(t, mgr, st.id)
		if !sess.OnGPU {
			t.Skipf("session fell back to CPU (runtime %s has no GPU provider)", runtimePath())
		}
		if !st.run {
			t.Logf("churn step %d: %s loaded on gpu (not executed)", i+1, st.id)
			continue
		}
		edge, _ := squareFor(sr.cfg, 4)
		total, per := timedRun(t, sess, noisyImage(edge, edge), sr.cfg)
		_, med, _ := tileStats(per)
		t.Logf("churn step %d: %s ep=%s %d tiles in %s (med %.0f ms/tile)",
			i+1, st.id, epLabel(sess), len(per), total.Round(time.Millisecond), med)
	}
}
