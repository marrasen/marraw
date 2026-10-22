package pyramid

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/lens"
)

// windowTestSource is a synthetic decode with what the stages react to:
// smooth gradients, fine noise, hard edges and saturated colour.
func windowTestSource(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rnd := rand.New(rand.NewPCG(1, 2))
	for y := range h {
		for x := range w {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			r := 40 + 150*fx + 30*math.Sin(fy*17)
			g := 30 + 120*fy + 40*math.Sin(fx*23+fy*5)
			b := 60 + 90*(1-fx)*fy
			if (x/97+y/61)%5 == 0 { // hard-edged blocks
				r, g, b = r*0.4, g*1.3, b*0.5
			}
			n := float64(rnd.IntN(25)) - 12
			o := img.PixOffset(x, y)
			img.Pix[o] = uint8(clampF(r+n, 0, 255))
			img.Pix[o+1] = uint8(clampF(g+n, 0, 255))
			img.Pix[o+2] = uint8(clampF(b+n, 0, 255))
			img.Pix[o+3] = 255
		}
	}
	return img
}

// renderWholeForTest is the 1:1 render (Cache.generate): exposure, lens,
// geometry, then ApplyFinish, whose stages generate mirrors — so a stage
// added there and not to RenderWindow fails the test.
func renderWholeForTest(src *image.RGBA, gamma float64, e *edit.Params, expEV float64, lensc *lens.Correction, ai AIMapSet, fills FillSet) *image.RGBA {
	st := FrameStatsFor(src, e, expEV, gamma, lensc, ai, fills)
	img := copyRGBA(src, src.Bounds())
	ApplyExposureEV(img, expEV, e)
	img = ApplyLens(img, LensWarp(lensc, e, img.Bounds().Dx(), img.Bounds().Dy()), e)
	img = ApplyGeometry(img, e)
	ApplyFinishWith(img, gamma, e, ai, fills, st)
	return img
}

// fillsForTest makes a patch for every fill the edit composites: fill
// spots and Remove masks.
func fillsForTest(e *edit.Params) FillSet {
	patch := func(seed int) *FillPatch {
		img := image.NewRGBA(image.Rect(0, 0, 96, 80))
		for i := 0; i < len(img.Pix); i += 4 {
			x, y := i/4%96, i/4/96
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(x*2+seed), uint8(y*3), uint8((x+y)*seed%256), 255
		}
		return &FillPatch{Img: img}
	}
	fills := FillSet{}
	for i := range e.Spots {
		if e.Spots[i].Mode == edit.SpotFill {
			fills[e.SpotFillKey(&e.Spots[i])] = patch(i + 1)
		}
	}
	for i := range e.Masks {
		if e.Masks[i].Remove {
			fills[e.MaskFillKey(&e.Masks[i])] = patch(i + 40)
		}
	}
	return fills
}

// TestRenderWindowMatchesWhole: a window renders the pixels the whole
// render has there, for every stage, at edges and corners too.
func TestRenderWindowMatchesWhole(t *testing.T) {
	src := windowTestSource(2400, 1600)
	lensc := lens.Resolve("Panasonic", "DC-LX100M2", "", 10.9, 1.7)
	if lensc == nil {
		t.Fatal("no lens profile")
	}
	store, key := testStoreWithMap(t, edit.AIClass, "m1", paintedClassMap(300, 200))
	// A second map, of another size, under the same photo.
	matte := image.NewGray(image.Rect(0, 0, 480, 320))
	for i := range matte.Pix {
		x, y := i%480, i/480
		if (x-200)*(x-200)+(y-150)*(y-150) < 90*90 {
			matte.Pix[i] = 255
		}
	}
	if err := store.Save(key, edit.AISubject, "s1", matte); err != nil {
		t.Fatal(err)
	}

	look := edit.Params{Contrast: 0.3, ToneShadows: 0.4, ToneHighlights: -0.3, Vibrance: 0.2,
		SplitShadowAmt: 0.3, SplitShadowHue: 200, Vignette: -0.4,
		ToneCurve: []edit.CurvePoint{{X: 0, Y: 0}, {X: 0.3, Y: 0.25}, {X: 0.7, Y: 0.8}, {X: 1, Y: 1}}}
	look.HSLSat[2] = 0.4
	look.HSLHue[4] = 0.3
	detail := look
	detail.Clarity, detail.Texture, detail.Sharpen = 0.4, 0.3, 0.5
	with := func(base edit.Params, f func(*edit.Params)) edit.Params {
		base.Masks = append([]edit.Mask(nil), base.Masks...)
		base.Spots = append([]edit.Spot(nil), base.Spots...)
		f(&base)
		return base
	}
	radial := edit.Mask{Type: edit.MaskRadial, CX: 0.4, CY: 0.5, RX: 0.25, RY: 0.15, Angle: 20, Feather: 0.6,
		Adjust: edit.MaskAdjust{ExpEV: 0.7, Saturation: -0.4, Temp: 0.3}}
	linear := edit.Mask{Type: edit.MaskLinear, X0: 0.2, Y0: 0.1, X1: 0.5, Y1: 0.6,
		Adjust: edit.MaskAdjust{Contrast: 0.5, ToneHighlights: -0.5}}
	brush := edit.Mask{Type: edit.MaskBrush, Strokes: []edit.Stroke{{Radius: 0.03, Feather: 0.5,
		Pts: []float64{0.1, 0.2, 0.3, 0.25, 0.5, 0.5, 0.7, 0.4}}},
		Adjust: edit.MaskAdjust{ExpEV: -0.8}}
	rangeM := edit.Mask{Type: edit.MaskRange, RangeLumaLo: 0.3, RangeLumaHi: 0.7, RangeHueLo: 0.1, RangeHueHi: 0.4,
		Feather: 0.4, Adjust: edit.MaskAdjust{Saturation: 0.6}}
	aiM := edit.Mask{Type: edit.MaskAI, AIKind: edit.AIClass, MapVer: "m1", ClassID: 3, Feather: 0.3,
		Adjust: edit.MaskAdjust{ExpEV: 1, Saturation: 0.3}}
	spot := func(mode edit.SpotMode, cx, cy, sx, sy, r float64) edit.Spot {
		return edit.Spot{Mode: mode, CX: cx, CY: cy, SX: sx, SY: sy, Radius: r, Feather: 0.4}
	}
	stroke := edit.Spot{Kind: "stroke", Mode: edit.SpotHeal, CX: 0.6, CY: 0.3, SX: 0.2, SY: 0.8,
		Strokes: []edit.Stroke{{Radius: 0.02, Pts: []float64{0.55, 0.25, 0.65, 0.35}}}}

	cases := map[string]edit.Params{
		"look":    look,
		"sharpen": with(look, func(e *edit.Params) { e.Sharpen = 0.8 }),
		"detail":  detail,
		"crop":    with(detail, func(e *edit.Params) { e.CropX, e.CropY, e.CropW, e.CropH = 0.1, 0.15, 0.7, 0.6 }),
		"angle": with(detail, func(e *edit.Params) {
			e.CropX, e.CropY, e.CropW, e.CropH, e.CropAngle = 0.1, 0.1, 0.8, 0.8, 4.5
		}),
		"turned": with(detail, func(e *edit.Params) {
			e.Rotate, e.FlipH, e.CropX, e.CropY, e.CropW, e.CropH, e.CropAngle = 1, true, 0.05, 0.1, 0.85, 0.8, -3
		}),
		"turned180": with(look, func(e *edit.Params) { e.Rotate = 2 }),
		"turned270": with(look, func(e *edit.Params) { e.Rotate, e.Sharpen = 3, 0.3 }),
		"mirror":    with(look, func(e *edit.Params) { e.FlipH, e.Texture = true, 0.5 }),
		"lens":      with(detail, func(e *edit.Params) {}),
		"masks": with(detail, func(e *edit.Params) {
			e.Masks = append(e.Masks, radial, linear, brush, rangeM)
		}),
		"ai": with(detail, func(e *edit.Params) { e.Masks = append(e.Masks, aiM, rangeM) }),
		"ais": with(detail, func(e *edit.Params) {
			second := aiM
			second.Adjust = edit.MaskAdjust{ExpEV: -1.2, Contrast: 0.8}
			subject := edit.Mask{Type: edit.MaskAI, AIKind: edit.AISubject, MapVer: "s1", Feather: 0.2,
				Adjust: edit.MaskAdjust{ExpEV: 1.5, Saturation: -0.6}}
			e.Masks = append(e.Masks, aiM, second, subject)
		}),
		"spots": with(detail, func(e *edit.Params) {
			e.Spots = append(e.Spots,
				spot(edit.SpotHeal, 0.5, 0.5, 0.1, 0.1, 0.03),
				spot(edit.SpotClone, 0.12, 0.12, 0.8, 0.85, 0.05), // copies the spot above
				spot(edit.SpotHeal, 0.99, 0.02, 0.5, 0.5, 0.04),   // at the corner
				stroke)
		}),
		"fills": with(detail, func(e *edit.Params) {
			e.CropX, e.CropY, e.CropW, e.CropH, e.CropAngle = 0.05, 0.05, 0.9, 0.9, 3
			e.Spots = append(e.Spots,
				edit.Spot{Mode: edit.SpotFill, CX: 0.4, CY: 0.4, Radius: 0.05, Feather: 0.3},
				edit.Spot{Kind: "stroke", Mode: edit.SpotFill, CX: 0.7, CY: 0.6,
					Strokes: []edit.Stroke{{Radius: 0.02, Pts: []float64{0.65, 0.55, 0.75, 0.65}}}},
				spot(edit.SpotHeal, 0.2, 0.7, 0.4, 0.4, 0.03)) // copies from inside the fill
			remove := brush
			remove.Remove, remove.Adjust = true, edit.MaskAdjust{}
			e.Masks = append(e.Masks, remove)
		}),
		"bw": with(detail, func(e *edit.Params) {
			e.BW = true
			e.SplitHighlightAmt, e.SplitHighlightHue = 0.3, 40
			e.HSLLum[3] = 0.5
			e.Masks = append(e.Masks, radial)
		}),
		"everything-noai": with(detail, func(e *edit.Params) {
			e.Rotate, e.CropX, e.CropY, e.CropW, e.CropH, e.CropAngle = 3, 0.05, 0.05, 0.9, 0.9, 2
			e.Masks = append(e.Masks, radial, brush)
			e.Spots = append(e.Spots, spot(edit.SpotHeal, 0.3, 0.6, 0.7, 0.2, 0.04), stroke)
		}),
		"dehaze": with(detail, func(e *edit.Params) { e.Dehaze = 0.6 }),
		"haze":   with(detail, func(e *edit.Params) { e.Dehaze = -0.4 }),
		"blur": with(detail, func(e *edit.Params) {
			m := radial
			m.Adjust = edit.MaskAdjust{Blur: 0.6, ExpEV: 0.3}
			e.Masks = append(e.Masks, m)
		}),
		"bokeh-ai": with(detail, func(e *edit.Params) {
			m := aiM
			m.Invert, m.Adjust = true, edit.MaskAdjust{Blur: 0.4, Bokeh: 0.7}
			e.Masks = append(e.Masks, m, rangeM)
		}),
		"glow": with(detail, func(e *edit.Params) {
			m := linear
			m.Adjust = edit.MaskAdjust{Glow: 0.8, Streaks: 0.6, FXAngle: 30, Prism: 0.5}
			e.Masks = append(e.Masks, m)
		}),
		"smears": with(look, func(e *edit.Params) {
			e.Rotate, e.CropX, e.CropY, e.CropW, e.CropH, e.CropAngle = 1, 0.05, 0.1, 0.85, 0.8, -3
			mo, zo := brush, radial
			mo.Adjust = edit.MaskAdjust{MotionBlur: 0.5, FXAngle: 70, Mosaic: 0.3}
			zo.Adjust = edit.MaskAdjust{ZoomBlur: 0.6, Prism: -0.4, Glow: 0.3}
			e.Masks = append(e.Masks, mo, zo)
		}),
		"fx-everything": with(detail, func(e *edit.Params) {
			e.Dehaze = 0.4
			e.CropX, e.CropY, e.CropW, e.CropH, e.CropAngle = 0.05, 0.05, 0.9, 0.9, 2
			blur := aiM
			blur.Adjust = edit.MaskAdjust{Blur: 0.5, Streaks: 0.4}
			e.Masks = append(e.Masks, radial, blur, aiM)
			e.Spots = append(e.Spots, spot(edit.SpotHeal, 0.3, 0.6, 0.7, 0.2, 0.04))
		}),
		"everything": with(detail, func(e *edit.Params) {
			e.Rotate, e.CropX, e.CropY, e.CropW, e.CropH, e.CropAngle = 3, 0.05, 0.05, 0.9, 0.9, 2
			e.Masks = append(e.Masks, radial, aiM, brush)
			e.Spots = append(e.Spots, spot(edit.SpotHeal, 0.3, 0.6, 0.7, 0.2, 0.04), stroke)
		}),
	}
	lensFor := map[string]bool{"lens": true, "turned": true, "everything": true, "everything-noai": true, "spots": true,
		"smears": true, "fx-everything": true}

	rnd := rand.New(rand.NewPCG(3, 4))
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			e.Normalize()
			var lc *lens.Correction
			if lensFor[name] {
				lc = lensc
			} else {
				e.LensMode = edit.LensOff
			}
			ai := store.SetFor(key, &e)
			fills := fillsForTest(&e)
			const expEV = 0.35
			whole := renderWholeForTest(src, 0.72, &e, expEV, lc, ai, fills)
			ow, oh := whole.Bounds().Dx(), whole.Bounds().Dy()
			if w, h := OutputSize(2400, 1600, &e); w != ow || h != oh {
				t.Fatalf("OutputSize = %dx%d, render is %dx%d", w, h, ow, oh)
			}
			wins := []image.Rectangle{
				image.Rect(0, 0, ow, oh),                   // all of it
				image.Rect(0, 0, 300, 200),                 // corner
				image.Rect(ow-257, oh-129, ow, oh),         // far corner
				image.Rect(ow/2-1, oh/2, ow/2+1, oh/2+300), // a sliver
				image.Rect(ow/3, 100, ow/3+1, 150),         // a pixel wide
				image.Rect(0, 100, 2, 150),                 // two, at the edge
				image.Rect(0, 0, 1, 1),                     // one pixel
			}
			for range 8 {
				w, h := 1+rnd.IntN(500), 1+rnd.IntN(400)
				x, y := rnd.IntN(ow-1)-w/4, rnd.IntN(oh-1)-h/4
				wins = append(wins, image.Rect(x, y, x+w, y+h))
			}
			for _, win := range wins {
				got := RenderWindow(t.Context(), src, win, 0.72, &e, Fold{ExpEV: expEV}, lc, ai, fills)
				want := copyRGBA(whole, win)
				if got == nil {
					t.Fatalf("window %v: nil", win)
				}
				if _, _, d := windowDiff(got, want); d != "" {
					t.Errorf("window %v: %s", win, d)
				}
			}
		})
	}
}

// TestRenderWindowLargeStroke: a painted heal spot on a large frame, where
// one pixel of the brush plane its heal fit samples on spans several
// output pixels.
func TestRenderWindowLargeStroke(t *testing.T) {
	if testing.Short() {
		t.Skip("renders a 32 MP frame")
	}
	src := windowTestSource(7000, 4600)
	for _, r := range []float64{0.0015, 0.002, 0.003} {
		e := edit.Params{Contrast: 0.2, LensMode: edit.LensOff, Spots: []edit.Spot{{
			Kind: "stroke", Mode: edit.SpotHeal, CX: 0.5, CY: 0.5, SX: 0.3, SY: 0.4,
			Strokes: []edit.Stroke{{Radius: r, Pts: []float64{0.45, 0.48, 0.55, 0.52}}},
		}}}
		e.Normalize()
		whole := renderWholeForTest(src, 0.72, &e, 0, nil, nil, nil)
		for _, win := range []image.Rectangle{
			image.Rect(3300, 2200, 3600, 2400), // the stroke's left end
			image.Rect(3450, 2250, 3550, 2350), // its middle
			image.Rect(3800, 2300, 4000, 2500), // its right end
		} {
			got := RenderWindow(t.Context(), src, win, 0.72, &e, Fold{}, nil, nil, nil)
			if _, _, d := windowDiff(got, copyRGBA(whole, win)); d != "" {
				t.Errorf("radius %v, window %v: %s", r, win, d)
			}
		}
	}
}

// TestRenderWindowBaseLook: the unedited look (nil edits) renders a window
// too.
func TestRenderWindowBaseLook(t *testing.T) {
	src := windowTestSource(900, 600)
	whole := renderWholeForTest(src, 0.72, nil, 0, nil, nil, nil)
	win := image.Rect(100, 50, 400, 350)
	got := RenderWindow(t.Context(), src, win, 0.72, nil, Fold{}, nil, nil, nil)
	if got == nil {
		t.Fatal("nil window")
	}
	if _, _, d := windowDiff(got, copyRGBA(whole, win)); d != "" {
		t.Error(d)
	}
	if RenderWindow(t.Context(), src, image.Rect(900, 0, 1000, 10), 0.72, nil, Fold{}, nil, nil, nil) != nil {
		t.Error("a window outside the output rendered")
	}
}

// TestRenderWindowCancelled: a cancelled window renders nothing.
func TestRenderWindowCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e := &edit.Params{Contrast: 0.3}
	if RenderWindow(ctx, windowTestSource(64, 64), image.Rect(0, 0, 8, 8), 0.72, e, Fold{}, nil, nil, nil) != nil {
		t.Error("a cancelled window rendered")
	}
}

// windowDiff counts the values two renders differ in and by how much at
// most, and describes it ("" when they are the same).
func windowDiff(got, want *image.RGBA) (n, worst int, desc string) {
	if got.Bounds().Size() != want.Bounds().Size() {
		return -1, -1, fmt.Sprintf("size %v, want %v", got.Bounds().Size(), want.Bounds().Size())
	}
	var at image.Point
	w := want.Bounds().Dx()
	for i := range want.Pix {
		d := int(got.Pix[i]) - int(want.Pix[i])
		if d != 0 {
			n++
			if d < 0 {
				d = -d
			}
			if d > worst {
				worst, at = d, image.Pt(i/4%w, i/4/w)
			}
		}
	}
	if n == 0 {
		return 0, 0, ""
	}
	return n, worst, fmt.Sprintf("%d of %d values differ, worst by %d at %v", n, len(want.Pix), worst, at)
}

// TestRenderWindowFuzz renders random edits — turns, mirror, crop and
// straighten, lens, B&W, detail, dehaze, spots of every kind with sources
// clamped at the edges, a soft AI matte, a radial mask, effects on both —
// and random windows of them, which must match the whole render exactly. MARRAW_WINDOW_FUZZ sets
// the number of edits (default 30).
func TestRenderWindowFuzz(t *testing.T) {
	n := 30
	if v, err := strconv.Atoi(os.Getenv("MARRAW_WINDOW_FUZZ")); err == nil {
		n = v
	}
	src := windowTestSource(1800, 1200)
	lensc := lens.Resolve("Panasonic", "DC-LX100M2", "", 10.9, 1.7)
	// A soft matte, as a real model's: a smooth falloff, no hard edge.
	matte := image.NewGray(image.Rect(0, 0, 1536, 1024))
	for i := range matte.Pix {
		x, y := float64(i%1536), float64(i/1536)
		d := math.Hypot(x-700, y-480) / 420
		matte.Pix[i] = uint8(255 * clampF(1.4-d, 0, 1))
	}
	store, key := testStoreWithMap(t, edit.AISubject, "soft", matte)
	for seed := range n {
		rnd := rand.New(rand.NewPCG(uint64(seed), 99))
		f := func(lo, hi float64) float64 { return lo + rnd.Float64()*(hi-lo) }
		f4 := func(lo, hi float64) float64 { return math.Round(f(lo, hi)*1e4) / 1e4 }
		e := edit.Params{Contrast: f(-0.5, 0.5), Vibrance: f(-0.3, 0.5), Vignette: f(-0.5, 0.5),
			Clarity: f(0, 0.5), Texture: f(0, 0.5), Sharpen: f(0, 0.8), Rotate: rnd.IntN(4), FlipH: rnd.IntN(2) == 0,
			BW: rnd.IntN(5) == 0}
		if rnd.IntN(2) == 0 {
			e.CropX, e.CropY, e.CropW, e.CropH = f4(0, 0.2), f4(0, 0.2), f4(0.5, 0.8), f4(0.5, 0.8)
		}
		if rnd.IntN(2) == 0 {
			e.CropAngle = f(-10, 10)
		}
		var lc *lens.Correction
		if rnd.IntN(2) == 0 {
			lc = lensc
		} else {
			e.LensMode = edit.LensOff
		}
		for range rnd.IntN(4) {
			switch rnd.IntN(3) {
			case 0, 1:
				mode := edit.SpotHeal
				if rnd.IntN(2) == 0 {
					mode = edit.SpotClone
				}
				e.Spots = append(e.Spots, edit.Spot{Mode: mode, CX: f4(0, 1), CY: f4(0, 1), SX: f4(0, 1), SY: f4(0, 1),
					Radius: f4(0.01, 0.08), Feather: f(0, 1)})
			case 2:
				cx, cy := f4(0.1, 0.9), f4(0.1, 0.9)
				e.Spots = append(e.Spots, edit.Spot{Kind: "stroke", Mode: edit.SpotHeal, CX: cx, CY: cy, SX: f4(0, 1), SY: f4(0, 1),
					Strokes: []edit.Stroke{{Radius: f4(0.005, 0.03), Pts: []float64{cx - 0.05, cy, cx + 0.05, cy + 0.03}}}})
			}
		}
		if rnd.IntN(3) == 0 {
			e.Dehaze = f(-0.5, 1)
		}
		// Each effect at random, on about one mask in two.
		fx := func(a edit.MaskAdjust) edit.MaskAdjust {
			if rnd.IntN(2) == 0 {
				return a
			}
			for _, p := range []*float64{&a.Blur, &a.Bokeh, &a.MotionBlur, &a.ZoomBlur, &a.Streaks, &a.Glow, &a.Mosaic, &a.Prism} {
				if rnd.IntN(4) == 0 {
					*p = f(0, 1)
				}
			}
			a.FXAngle = f(0, 180)
			return a
		}
		if rnd.IntN(2) == 0 {
			e.Masks = append(e.Masks, edit.Mask{Type: edit.MaskAI, AIKind: edit.AISubject, MapVer: "soft", Feather: f(0, 0.5),
				Invert: rnd.IntN(3) == 0, Adjust: fx(edit.MaskAdjust{ExpEV: f(-1.5, 1.5), Saturation: f(-0.5, 0.5)})})
		}
		if rnd.IntN(2) == 0 {
			e.Masks = append(e.Masks, edit.Mask{Type: edit.MaskRadial, CX: f(0.2, 0.8), CY: f(0.2, 0.8), RX: f(0.1, 0.4), RY: f(0.1, 0.4),
				Angle: f(0, 90), Feather: f(0.1, 1), Adjust: fx(edit.MaskAdjust{Contrast: f(-0.5, 0.5), Temp: f(-0.5, 0.5)})})
		}
		e.Normalize()
		ai := store.SetFor(key, &e)
		whole := renderWholeForTest(src, 0.72, &e, 0.2, lc, ai, nil)
		ow, oh := whole.Bounds().Dx(), whole.Bounds().Dy()
		for range 6 {
			w, h := 1+rnd.IntN(400), 1+rnd.IntN(300)
			x, y := rnd.IntN(ow)-w/4, rnd.IntN(oh)-h/4
			win := image.Rect(x, y, x+w, y+h)
			got := RenderWindow(t.Context(), src, win, 0.72, &e, Fold{ExpEV: 0.2}, lc, ai, nil)
			want := win.Intersect(whole.Bounds())
			if got == nil {
				if !want.Empty() {
					t.Errorf("seed %d, window %v: nil", seed, win)
				}
				continue
			}
			if _, _, d := windowDiff(got, copyRGBA(whole, want)); d != "" {
				t.Errorf("seed %d, window %v: %s", seed, win, d)
			}
		}
	}
}

// TestRenderWindowClampedSpot: a clone spot under a quarter turn whose
// source the frame edge clamps, the reviewer's case.
func TestRenderWindowClampedSpot(t *testing.T) {
	src := windowTestSource(1800, 1200)
	for _, rot := range []int{1, 3} {
		e := edit.Params{Rotate: rot, LensMode: edit.LensOff, Contrast: 0.3, Spots: []edit.Spot{
			{Mode: edit.SpotClone, CX: 0.1804, CY: 0.2323, Radius: 0.067, SX: 0.0925, SY: 0.6639, Feather: 0.5},
			{Mode: edit.SpotHeal, CX: 0.1804, CY: 0.2323, Radius: 0.067, SX: 0.0925, SY: 0.6639, Feather: 0.5},
		}}
		e.Normalize()
		whole := renderWholeForTest(src, 0.72, &e, 0, nil, nil, nil)
		ow, oh := whole.Bounds().Dx(), whole.Bounds().Dy()
		f := newMaskFrame(ow, oh, &e)
		dx, dy := f.outputPoint(0.1804*f.frameW, 0.2323*f.frameH)
		for _, d := range []int{60, 150, 251} {
			win := image.Rect(int(dx)-d, int(dy)-d/2, int(dx)+d/3, int(dy)+d)
			got := RenderWindow(t.Context(), src, win, 0.72, &e, Fold{}, nil, nil, nil)
			if _, _, diff := windowDiff(got, copyRGBA(whole, win.Intersect(whole.Bounds()))); diff != "" {
				t.Errorf("rotate %d, window %v: %s", rot, win, diff)
			}
		}
	}
}

// strongStraightenStroke is the edit review round 8's fuzz found: painted
// heal spots under a 41-degree straighten, one of them with a thin stroke
// and an erase stroke setting its box, whose band reaches past a pad that
// ignored the straighten.
const strongStraightenStroke = `{"contrast":0.29673283673262985,"texture":0,"clarity":0.21494742490350516,"sharpen":0.1483895745923057,"lensMode":"off","rotate":0,"flipH":true,"cropAngle":-41.4652,"masks":[{"type":"brush","strokes":[{"radius":0.0383,"pts":[0.0221,0.0036,0.9946,0.0122]}],"adjust":{},"remove":true}],"spots":[{"kind":"stroke","mode":"fill","cx":0.8923,"cy":0.6389,"radius":0,"sx":0,"sy":0,"strokes":[{"radius":0.0305,"feather":0.53,"pts":[0.8552,0.664,0.8634,0.5805]}]},{"cx":0.4853,"cy":0.9521,"radius":0.0151,"sx":-0.4672,"sy":0.1037,"feather":0.4049},{"mode":"fill","cx":0.9501,"cy":0.3746,"radius":0.0493,"sx":0,"sy":0,"feather":0.0443},{"mode":"clone","cx":0.9617,"cy":0.0051,"radius":0.0393,"sx":-0.3852,"sy":0.8109,"feather":0.1795},{"kind":"stroke","cx":0.6939,"cy":0.7562,"radius":0,"sx":0.0252,"sy":-0.4647,"opacity":0.6814,"strokes":[{"radius":0.025,"feather":0.346,"pts":[0.7563,0.827,0.6827,0.8012,0.6371,0.6928,0.7526,0.7069,0.7531,0.8205]}]},{"kind":"stroke","cx":0.0516,"cy":0.0934,"radius":0,"sx":-0.0549,"sy":0.3016,"strokes":[{"radius":0.0253,"feather":0.7388,"pts":[0.0933,0.0861,0.0813,0.0783]},{"radius":0.0032,"feather":0.3184,"pts":[0.1269,0.1721,0.1033,0.0471,-0.0189,0.0959]},{"erase":true,"radius":0.0344,"feather":0.7278,"pts":[-0.0098,0.0367,0.0945,0.136,0.034,0.1189]}]}]}`

func TestRenderWindowStraightenedStroke(t *testing.T) {
	var e edit.Params
	if err := json.Unmarshal([]byte(strongStraightenStroke), &e); err != nil {
		t.Fatal(err)
	}
	e.Normalize()
	src := windowTestSource(1800, 1200)
	fills := fillsForTest(&e)
	whole := renderWholeForTest(src, 0.75, &e, 0.1, nil, nil, fills)
	win := image.Rect(-97, 536, 13, 711)
	got := RenderWindow(t.Context(), src, win, 0.75, &e, Fold{ExpEV: 0.1}, nil, nil, fills)
	if _, _, d := windowDiff(got, copyRGBA(whole, win.Intersect(whole.Bounds()))); d != "" {
		t.Errorf("window %v: %s", win, d)
	}
}
