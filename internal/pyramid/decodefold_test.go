package pyramid

import (
	"image"
	"math"
	"os"
	"testing"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/jpegturbo"
)

// TestFoldWithoutWBIsExposure: without a change of white balance the fold
// is the exposure fold, byte for byte, so a window of the decode's own
// balance still matches the tiles.
func TestFoldWithoutWBIsExposure(t *testing.T) {
	e := &edit.Params{}
	a, b := windowTestSource(300, 200), windowTestSource(300, 200)
	ApplyFold(a, Fold{ExpEV: 0.4}, e)
	ApplyExposureEV(b, 0.4, e)
	if _, _, d := windowDiff(a, b); d != "" {
		t.Error(d)
	}
}

// TestWBFoldIdentity: folding a balance into itself changes nothing but
// for rounding, and a warmer balance warms.
func TestWBFoldIdentity(t *testing.T) {
	rgbCam := [3][4]float64{{1.8, -0.6, -0.2}, {-0.2, 1.5, -0.3}, {0.05, -0.5, 1.45}}
	mul := [4]float64{2.1, 1, 1.6, 1}
	same, ok := newWBFold(mul, mul, rgbCam, 0)
	if !ok {
		t.Fatal("no fold")
	}
	src := windowTestSource(300, 200)
	img := copyRGBA(src, src.Bounds())
	ApplyFold(img, Fold{WB: &same}, &edit.Params{})
	if _, worst, _ := windowDiff(img, src); worst > 1 {
		t.Errorf("an unchanged balance moved a value by %d", worst)
	}
	warm, _ := newWBFold(mul, [4]float64{2.6, 1, 1.3, 1}, rgbCam, 0)
	ApplyFold(img, Fold{WB: &warm}, &edit.Params{})
	var dr, db int
	for i := 0; i < len(img.Pix); i += 4 {
		dr += int(img.Pix[i]) - int(src.Pix[i])
		db += int(img.Pix[i+2]) - int(src.Pix[i+2])
	}
	if dr <= 0 || db >= 0 {
		t.Errorf("a warmer balance: red moved %d, blue %d", dr, db)
	}
	// Clipped white stays white: the exact decode clips every channel.
	white := image.NewRGBA(image.Rect(0, 0, 1, 1))
	copy(white.Pix, []uint8{255, 255, 255, 255})
	ApplyFold(white, Fold{WB: &warm}, &edit.Params{})
	if p := white.Pix; max(p[0], p[1], p[2])-min(p[0], p[1], p[2]) > 2 {
		t.Errorf("clipped white folds to %v", p[:3])
	}
}

// TestDecodeClipped: a decode missing a channel across its lit pixels
// cannot fold a white balance; one missing it in a few highlights can.
func TestDecodeClipped(t *testing.T) {
	img := windowTestSource(400, 300)
	if decodeClipped(img) {
		t.Error("an ordinary frame reads as clipped")
	}
	for i := 0; i < len(img.Pix)/10; i += 4 {
		img.Pix[i+1] = 0
	}
	if decodeClipped(img) {
		t.Error("a tenth of the frame without green reads as clipped")
	}
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 40, 0, 220 // blue stage light
	}
	if !decodeClipped(img) {
		t.Error("a frame without green reads as whole")
	}
}

// TestWBFoldNearTheDecode: on a real RAW, a window that folds another
// white balance into the kept decode lands near the 1:1 tiles of that
// balance, decoded for it, over six tiles: the colour change and the
// brightness match, and blown highlights stay white.
func TestWBFoldNearTheDecode(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	start := edit.Params{Contrast: 0.2}
	p := f.edit(0, start)
	start.Normalize()
	if _, err := f.cache.EnsureTile(t.Context(), p, 1, 1, p.EditHash, decode.PriorityVisible); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		wb func(*edit.Params)
		// How far a channel may be off: on average, on average absolute,
		// and on average in the highlights.
		mean, abs, hi float64
	}{
		{func(e *edit.Params) { e.WBMode, e.WBKelvin = edit.WBKelvin, 7500 }, 1, 3, 6},
		{func(e *edit.Params) { e.WBMode, e.WBKelvin = edit.WBKelvin, 3200 }, 1, 3, 6},
		{func(e *edit.Params) { e.WBTemp, e.WBTint = -0.6, 0.4 }, 1, 3, 6},
		// Bright, so the highlights clip and must stay white. Folding 1.5
		// stops into 8-bit pixels is off by 3.1 levels on average already,
		// with no change of balance (the exposure fold windows had before).
		{func(e *edit.Params) { e.WBMode, e.WBKelvin, e.ExpEV = edit.WBKelvin, 2500, 1.5 }, 2.5, 4.5, 8},
	} {
		e := start
		tc.wb(&e)
		e.Normalize()
		// The window first, from the as-shot decode kept: then the tiles
		// of e, decoded under its balance.
		f.edit(0, start)
		if _, err := f.cache.EnsureTile(t.Context(), f.photo(0), 1, 1, f.photo(0).EditHash, decode.PriorityVisible); err != nil {
			t.Fatal(err)
		}
		src, fold, _, ok := f.cache.KeptForWindow(f.photo(0), &e)
		if !ok || fold.WB == nil {
			t.Fatalf("%+v: no fold (ok %v)", e, ok)
		}
		gamma := f.photo(0).LookGamma
		var wins []*image.RGBA
		for ty := range 2 {
			for tx := range 3 {
				win := image.Rect(tx*TileSize, ty*TileSize, (tx+1)*TileSize, (ty+1)*TileSize)
				wins = append(wins, RenderWindow(t.Context(), src, win, gamma, &e, fold, f.cache.Lenses.For(p), nil, nil))
			}
		}
		q := f.edit(0, e)
		var sum, abs, hiSum [3]float64
		n, hiN := 0, 0
		for k, got := range wins {
			path, err := f.cache.EnsureTile(t.Context(), q, k%3, k/3, q.EditHash, decode.PriorityVisible)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want, err := jpegturbo.DecodeRGBA(b)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i+3 < len(want.Pix) && i+3 < len(got.Pix); i += 4 {
				hi := min(want.Pix[i], want.Pix[i+1], want.Pix[i+2]) > 240
				for c := range 3 {
					d := float64(got.Pix[i+c]) - float64(want.Pix[i+c])
					sum[c] += d
					abs[c] += math.Abs(d)
					if hi {
						hiSum[c] += d
					}
				}
				n++
				if hi {
					hiN++
				}
			}
		}
		for c := range 3 {
			sum[c] /= float64(n)
			abs[c] /= float64(n)
			if hiN > 0 {
				hiSum[c] /= float64(hiN)
			}
		}
		t.Logf("wb %s %v/%v/%v exp %v: mean difference %.2f %.2f %.2f, mean absolute %.2f %.2f %.2f; %d highlights off by %.1f %.1f %.1f",
			e.WBMode, e.WBKelvin, e.WBTemp, e.WBTint, e.ExpEV, sum[0], sum[1], sum[2], abs[0], abs[1], abs[2], hiN, hiSum[0], hiSum[1], hiSum[2])
		for c := range 3 {
			if math.Abs(sum[c]) > tc.mean || abs[c] > tc.abs || (hiN > 1000 && math.Abs(hiSum[c]) > tc.hi) {
				t.Errorf("wb %+v: channel %d off by %.2f on average (%.2f absolute, %.1f in highlights)", e, c, sum[c], abs[c], hiSum[c])
			}
		}
	}
}
