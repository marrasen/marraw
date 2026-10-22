package pyramid

import (
	"bytes"
	"context"
	"image"
	"os"
	"testing"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/jpegturbo"
)

// TestFrameStatsKeepEffects: the small render keeps what each effect
// needs: the blur's levels, the shared buffer, and what the light passes
// add only where no effect threw the detail away; nothing for a mask
// without effects, and the veil for dehaze.
func TestFrameStatsKeepEffects(t *testing.T) {
	src := windowTestSource(2400, 1600)
	radial := func(a edit.MaskAdjust) edit.Mask {
		return edit.Mask{Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.3, Feather: 0.5, Adjust: a}
	}
	e := edit.Params{Dehaze: 0.5, LensMode: edit.LensOff, Masks: []edit.Mask{
		radial(edit.MaskAdjust{ExpEV: 0.5}),
		radial(edit.MaskAdjust{Blur: 0.5}),
		radial(edit.MaskAdjust{Glow: 0.6, Streaks: 0.5}),
		radial(edit.MaskAdjust{MotionBlur: 0.5}),
	}}
	e.Normalize()
	st := FrameStatsFor(src, &e, 0, 0.72, nil, nil, nil)
	if st == nil {
		t.Fatal("no stats")
	}
	if st.veil < 24 {
		t.Errorf("veil %d", st.veil)
	}
	if st.fx[0] != nil {
		t.Error("a tone-only mask kept effects")
	}
	if b := st.fx[1]; b == nil || len(b.blur) != gradedBlurLevels || b.shared != nil {
		t.Errorf("blur: %+v", b)
	}
	if g := st.fx[2]; g == nil || g.shared == nil || g.delta == nil {
		t.Errorf("glow: %+v", g)
	}
	if m := st.fx[3]; m == nil || m.shared == nil || m.delta != nil {
		t.Errorf("motion: %+v", m)
	}
	if s := st.fx[2].shared; max(s.w, s.h) != fxPlaneLongEdge {
		t.Errorf("working size %dx%d", s.w, s.h)
	}
	if FrameStatsFor(src, &edit.Params{Dehaze: -0.3, Masks: []edit.Mask{radial(edit.MaskAdjust{ExpEV: 1})}}, 0, 0.72, nil, nil, nil) != nil {
		t.Error("stats for an edit that reads nothing over the frame")
	}
}

// TestWindowShowsEffects: a window of an edit with effects shows them —
// it differs from the same edit without — so the window tests compare
// renders that carry them.
func TestWindowShowsEffects(t *testing.T) {
	src := windowTestSource(2400, 1600)
	with := edit.Params{Dehaze: 0.5, LensMode: edit.LensOff, Masks: []edit.Mask{{Type: edit.MaskRadial,
		CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.3, Feather: 0.5, Adjust: edit.MaskAdjust{Blur: 0.6, Glow: 0.5, ExpEV: 0.2}}}}
	with.Normalize()
	without := with
	without.Dehaze, without.Masks = 0, []edit.Mask{with.Masks[0]}
	without.Masks[0].Adjust = edit.MaskAdjust{ExpEV: 0.2}
	win := image.Rect(1000, 600, 1400, 1000)
	a := RenderWindow(t.Context(), src, win, 0.72, &with, Fold{}, nil, nil, nil)
	b := RenderWindow(t.Context(), src, win, 0.72, &without, Fold{}, nil, nil, nil)
	if n, worst, _ := windowDiff(a, b); n < len(a.Pix)/4 || worst < 20 {
		t.Errorf("effects changed %d values, worst by %d", n, worst)
	}
}

// TestWindowMatchesTilesWithEffects: on a real RAW, a window of an edit
// with dehaze and mask effects, rendered from the kept decode, is the 1:1
// tile there, byte for byte: the full render measures the frame as the
// window does.
func TestWindowMatchesTilesWithEffects(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	params := edit.Params{Contrast: 0.2, Clarity: 0.3, Dehaze: 0.5, ExpEV: 0.3, Masks: []edit.Mask{
		{Type: edit.MaskRadial, CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.25, Feather: 0.5, Invert: true,
			Adjust: edit.MaskAdjust{Blur: 0.5, ExpEV: -0.3}},
		{Type: edit.MaskLinear, X0: 0.2, Y0: 0.1, X1: 0.6, Y1: 0.7, Adjust: edit.MaskAdjust{Glow: 0.7, Streaks: 0.5}},
	}}
	p := f.edit(0, params)
	params.Normalize()
	cache := f.cache
	for _, at := range []image.Point{{0, 0}, {1, 1}, {2, 1}} {
		path, err := cache.EnsureTile(t.Context(), p, at.X, at.Y, p.EditHash, decode.PriorityVisible)
		if err != nil {
			t.Fatalf("tile %v: %v", at, err)
		}
		tile, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src, fold, _, ok := cache.KeptForWindow(p, &params)
		if !ok {
			t.Fatal("no decode kept for the window")
		}
		gamma := f.photo(0).LookGamma
		win := image.Rect(at.X*TileSize, at.Y*TileSize, (at.X+1)*TileSize, (at.Y+1)*TileSize)
		img := RenderWindow(t.Context(), src, win, gamma, &params, fold, cache.Lenses.For(p),
			cache.AIMaps.SetFor(p.CacheKey, &params), cache.Fills.SetFor(p.CacheKey, &params))
		if img == nil {
			t.Fatalf("tile %v: no window", at)
		}
		var buf bytes.Buffer
		if err := jpegturbo.EncodeRGBA(&buf, img, 90); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), tile) {
			t.Errorf("tile %v: the window differs from the 1:1 render", at)
		}
	}
}

// TestWindowStatsReused: a pan, or a drag of a stage after the measure,
// reuses what the last window measured; an edit the measure reads does
// not. Neither changes the pixels.
func TestWindowStatsReused(t *testing.T) {
	forgetWindowStats()
	t.Cleanup(forgetWindowStats)
	src := windowTestSource(2400, 1600)
	e := edit.Params{Dehaze: 0.4, LensMode: edit.LensOff, Masks: []edit.Mask{{Type: edit.MaskRadial,
		CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.3, Feather: 0.5, Adjust: edit.MaskAdjust{Glow: 0.6}}}}
	e.Normalize()
	window := func(e edit.Params, win image.Rectangle) {
		t.Helper()
		got := RenderWindow(t.Context(), src, win, 0.72, &e, Fold{}, nil, nil, nil)
		want := copyRGBA(renderWholeForTest(src, 0.72, &e, 0, nil, nil, nil), win)
		if _, _, d := windowDiff(got, want); d != "" {
			t.Errorf("window %v: %s", win, d)
		}
	}
	n0 := frameMeasures.Load()
	window(e, image.Rect(100, 100, 600, 500))
	window(e, image.Rect(900, 700, 1300, 1000)) // a pan
	sharper := e
	sharper.Clarity, sharper.Dehaze = 0.5, 0.7
	window(sharper, image.Rect(900, 700, 1300, 1000))
	if n := frameMeasures.Load() - n0; n != 1 {
		t.Errorf("%d measures, want 1", n)
	}
	glowier := e
	glowier.Masks = []edit.Mask{e.Masks[0]}
	glowier.Masks[0].Adjust.Glow = 0.9
	window(glowier, image.Rect(900, 700, 1300, 1000))
	if n := frameMeasures.Load() - n0; n != 2 {
		t.Errorf("%d measures after a mask change, want 2", n)
	}
}

// TestTightCropShrinkBounded: a crop of a tenth of the frame does not ask
// for the decode at full size, and its window still matches.
func TestTightCropShrinkBounded(t *testing.T) {
	src := windowTestSource(9000, 6000)
	e := edit.Params{Dehaze: 0.3, LensMode: edit.LensOff, CropX: 0.4, CropY: 0.4, CropW: 0.1, CropH: 0.1,
		Masks: []edit.Mask{{Type: edit.MaskRadial, CX: 0.45, CY: 0.45, RX: 0.03, RY: 0.03, Feather: 0.5,
			Adjust: edit.MaskAdjust{Blur: 0.5}}}}
	e.Normalize()
	if b := shrinkDecode(src, &e).Bounds(); max(b.Dx(), b.Dy()) > proxyMaxEdge {
		t.Errorf("shrunk to %v", b.Size())
	}
	whole := renderWholeForTest(src, 0.72, &e, 0, nil, nil, nil)
	win := image.Rect(200, 150, 700, 500).Intersect(whole.Bounds())
	got := RenderWindow(t.Context(), src, win, 0.72, &e, Fold{}, nil, nil, nil)
	if _, _, d := windowDiff(got, copyRGBA(whole, win)); d != "" {
		t.Error(d)
	}
}

// TestWindowStatsShrinkSurvivesCancel: a window called off after its
// shrink still keeps it, so the next frame of a drag does not shrink the
// decode again; and nothing is kept once the kept decode goes.
func TestWindowStatsShrinkSurvivesCancel(t *testing.T) {
	forgetWindowStats()
	t.Cleanup(forgetWindowStats)
	src := windowTestSource(2400, 1600)
	e := edit.Params{Dehaze: 0.4, LensMode: edit.LensOff}
	e.Normalize()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if windowStats(ctx, src, &e, Fold{}, 0.72, nil, nil, nil) != nil {
		t.Fatal("a cancelled window measured")
	}
	windowKept.Lock()
	shrunk := windowKept.shrunk
	windowKept.Unlock()
	if shrunk == nil {
		t.Fatal("the shrink was thrown away")
	}
	if windowStats(t.Context(), src, &e, Fold{}, 0.72, nil, nil, nil) == nil {
		t.Fatal("no stats")
	}
	windowKept.Lock()
	again := windowKept.shrunk
	windowKept.Unlock()
	if again != shrunk {
		t.Error("the decode was shrunk again")
	}
	forgetWindowStats()
	windowKept.Lock()
	gone := windowKept.shrunk == nil && windowKept.st == nil
	windowKept.Unlock()
	if !gone {
		t.Error("kept after forgetWindowStats")
	}
}
