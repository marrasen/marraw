package pyramid

import (
	"fmt"
	"image"
	"math"
	"math/rand/v2"
	"slices"
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

// renderWholeForTest is the 1:1 render's stage order (Cache.generate).
func renderWholeForTest(src *image.RGBA, gamma float64, e *edit.Params, expEV float64, lensc *lens.Correction, ai AIMapSet, fills FillSet) *image.RGBA {
	img := copyRGBA(src, src.Bounds())
	ApplyExposureEV(img, expEV, e)
	img = ApplyLens(img, LensWarp(lensc, e, img.Bounds().Dx(), img.Bounds().Dy()), e)
	img = ApplyGeometry(img, e)
	ApplyHeal(img, e, ai, fills)
	ApplyLook(img, gamma, e)
	suppress := ApplyMasks(img, e, ai)
	ApplyBW(img, e)
	ApplyDetail(img, e, suppress)
	return img
}

// TestRenderWindowMatchesWhole: a window renders the pixels the whole
// render has there, for every windowable stage, at edges and corners too.
func TestRenderWindowMatchesWhole(t *testing.T) {
	src := windowTestSource(2400, 1600)
	lensc := lens.Resolve("Panasonic", "DC-LX100M2", "", 10.9, 1.7)
	if lensc == nil {
		t.Fatal("no lens profile")
	}
	store, key := testStoreWithMap(t, edit.AIClass, "m1", paintedClassMap(300, 200))

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
		"spots": with(detail, func(e *edit.Params) {
			e.Spots = append(e.Spots,
				spot(edit.SpotHeal, 0.5, 0.5, 0.1, 0.1, 0.03),
				spot(edit.SpotClone, 0.12, 0.12, 0.8, 0.85, 0.05), // copies the spot above
				spot(edit.SpotHeal, 0.99, 0.02, 0.5, 0.5, 0.04),   // at the corner
				stroke)
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
		"everything": with(detail, func(e *edit.Params) {
			e.Rotate, e.CropX, e.CropY, e.CropW, e.CropH, e.CropAngle = 3, 0.05, 0.05, 0.9, 0.9, 2
			e.Masks = append(e.Masks, radial, aiM, brush)
			e.Spots = append(e.Spots, spot(edit.SpotHeal, 0.3, 0.6, 0.7, 0.2, 0.04), stroke)
		}),
	}
	lensFor := map[string]bool{"lens": true, "turned": true, "everything": true, "everything-noai": true, "spots": true}

	rnd := rand.New(rand.NewPCG(3, 4))
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			e.Normalize()
			if !Windowable(&e) {
				t.Fatal("not windowable")
			}
			var lc *lens.Correction
			if lensFor[name] {
				lc = lensc
			} else {
				e.LensMode = edit.LensOff
			}
			ai := store.SetFor(key, &e)
			// The AI edge refinement sums floats over its own window, so a
			// weight at a mask edge can land a step off, and sharpening
			// spreads it: allow a value in a thousand off by up to three.
			aiCase := slices.ContainsFunc(e.Masks, func(m edit.Mask) bool { return m.Type == edit.MaskAI })
			const expEV = 0.35
			whole := renderWholeForTest(src, 0.72, &e, expEV, lc, ai, nil)
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
				got := RenderWindow(src, win, 0.72, &e, expEV, lc, ai, nil)
				want := copyRGBA(whole, win)
				if got == nil {
					t.Fatalf("window %v: nil", win)
				}
				if n, worst, d := windowDiff(got, want); d != "" && !(aiCase && worst <= 3 && n*1000 <= len(want.Pix)) {
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
			got := RenderWindow(src, win, 0.72, &e, 0, nil, nil, nil)
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
	got := RenderWindow(src, win, 0.72, nil, 0, nil, nil, nil)
	if got == nil {
		t.Fatal("nil window")
	}
	if _, _, d := windowDiff(got, copyRGBA(whole, win)); d != "" {
		t.Error(d)
	}
	if RenderWindow(src, image.Rect(900, 0, 1000, 10), 0.72, nil, 0, nil, nil, nil) != nil {
		t.Error("a window outside the output rendered")
	}
}

// TestNotWindowable: dehaze and mask effects need the whole frame.
func TestNotWindowable(t *testing.T) {
	for name, e := range map[string]*edit.Params{
		"dehaze": {Dehaze: 0.3},
		"blur":   {Masks: []edit.Mask{{Type: edit.MaskRadial, RX: 0.2, RY: 0.2, Adjust: edit.MaskAdjust{Blur: 0.5}}}},
	} {
		if Windowable(e) {
			t.Errorf("%s: windowable", name)
		}
		if RenderWindow(windowTestSource(64, 64), image.Rect(0, 0, 8, 8), 0.72, e, 0, nil, nil, nil) != nil {
			t.Errorf("%s: rendered a window", name)
		}
	}
	off := &edit.Params{Masks: []edit.Mask{{Type: edit.MaskRadial, Disabled: true, Adjust: edit.MaskAdjust{Blur: 0.5}}}}
	if !Windowable(off) {
		t.Error("a hidden mask's effects block the window")
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
