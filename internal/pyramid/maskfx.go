package pyramid

import (
	"image"
	"math"
	"runtime"

	"github.com/marrasen/marraw/internal/edit"
	"golang.org/x/sync/errgroup"
)

// Spatial mask effects — defocus, mosaic, motion and zoom smears, and
// anamorphic light streaks. Every other mask adjustment is a point operation
// that the per-row Q8 weight seam serves directly (see ApplyMasks); these are
// gathers, so they need the mask's weight for pixels the row they are writing
// does not touch. applyMaskFX therefore materializes the weight plane once
// (weightPlaneFrom, shared with the develop overlay's hover tint) and gathers
// through it.
//
// Three decisions carry the quality:
//
//   - The gather is WEIGHT-NORMALIZED: Σw·c / Σw, using the mask's own weight
//     as the gather weight. Blurring the whole frame and blending by weight
//     would smear subject pixels into the halo — a ghost of the subject
//     printed into the background, the tell of every cheap fake-bokeh. With
//     the normalization, pixels outside the mask contribute nothing and the
//     divide renormalizes against the reduced sample count, so a pixel next to
//     the silhouette gets the average of the background alone instead of
//     darkening toward black.
//   - The gather runs in LINEAR LIGHT (fxLin/fxEnc, the decode's own display
//     encoding). Averaging a 250-level highlight with 20-level surroundings in
//     display space gives dull grey; in linear light it gives the bright smear
//     that reads as light. Streaks are nearly invisible without this.
//   - It runs at a FIXED working resolution (fxPlaneLongEdge), so the 2048
//     settle, the 1:1 tile and the export produce identical effects and the
//     memory cost is constant regardless of the photo's megapixels. What comes
//     back out of that buffer therefore carries no detail finer than 1024 px,
//     which is only acceptable when the effect destroyed that detail anyway —
//     so the passes that don't (glow, streaks, prism) composite as a
//     DIFFERENCE against the full-resolution pixel rather than replacing it.
//     See fxComposite.
const (
	// fxPlaneLongEdge caps the resolution the spatial effects are computed at.
	// A defocused region carries no detail finer than its own blur radius, so
	// computing it small and upsampling bilinearly is invisible — the
	// brushPlaneLongEdge argument, and the same value for the same reason.
	//
	// 1024 is deliberately at (not above) the interactive draft's long edge,
	// so the draft, the 2048 settle, the 1:1 tiles and the export all compute
	// the identical effect: the background does not visibly re-render as the
	// preview settles, and zooming to 1:1 cannot change the look. It also
	// bounds cost and memory to a constant — a 60 MP export runs the same
	// gathers as a thumbnail.
	fxPlaneLongEdge = 1024

	// Reaches, as fractions of the working buffer's long edge (which tracks
	// the oriented frame's long edge), so a 1024 draft, a 2048 settle, a 1:1
	// tile and an export all describe the same effect.
	fxBlurFrac   = 0.06 // defocus radius at Blur = 1
	fxMotionFrac = 0.15 // smear length at MotionBlur = 1
	fxStreakFrac = 0.50 // streak length at Streaks = 1 — streaks must be LONG
	fxMosaicFrac = 0.08 // mosaic block edge at Mosaic = 1
	fxZoomSpread = 0.125

	fxBlurPasses = 3  // 2 leaves visible square blobs once highlights are in play
	fxMotionTaps = 32 // upper bound; short smears use fewer
	fxZoomTaps   = 24
	fxStreakTaps = 32

	// The highlight knee streaks are drawn from, in display levels. Low
	// enough that an ordinary bright edge (a lit railing, a window frame)
	// qualifies, not just a clipped specular.
	fxStreakKnee = 176
	// fxStreakGain scales the smeared highlight energy added back in linear
	// light at Streaks = 1. It has to be large: the gather divides by the
	// whole kernel weight, so a highlight a few pixels wide spread over a
	// half-frame streak keeps only a few percent of its energy per pixel.
	// That is physically what a streak IS — which is why real anamorphic
	// flares are a bright source and a dim trail — but it means the trail
	// needs a double-digit multiplier to read at all.
	fxStreakGain = 10
	// Glow: the bloom radius as a fraction of the (half-resolution) working
	// buffer's long edge, and its gain. A box blur conserves energy, so the
	// gain plays the same role as the streaks' — a highlight spread over a
	// disc keeps only its area ratio.
	fxGlowFrac = 0.06
	fxGlowGain = 6
	// Prism: the per-channel radial scale at ±1. Real lateral chromatic
	// aberration is well under 0.1%; this is a creative dial, so 1% — a
	// clearly coloured fringe at the frame edge, invisible at the centre —
	// which keeps the whole slider usable instead of putting everything
	// photographic in its bottom fifth.
	fxPrismScale = 0.01

	// Gather-side erosion. The AI matte is never pixel-perfect at hair edges,
	// so the weight the gather SAMPLES with is pulled a pixel or so inside the
	// silhouette while the weight the result is COMPOSITED with stays as the
	// mask drew it. The floor keeps a hairline mask from collapsing to a zero
	// denominator; the ratio Σw·c/Σw is unaffected by a common scale, so
	// flooring costs precision, never colour.
	fxErodeRadius = 2
	fxErodeKnee   = 88
	fxErodeFloor  = 8
)

// fxLin linearizes a display-encoded level for the gather. The exponent is
// previewExposureGamma — the decode's own display encoding, the same one
// buildMaskLUTs undoes for exposure and the white balance gains — so pushing a
// region half a stop and then blurring it compose correctly.
// Forced strictly increasing: the curve is so flat at the bottom that levels
// 0..2 would otherwise all land on linear 0, and a non-injective forward table
// has no exact inverse — an FX-free region would come back a level off.
var fxLin = func() *[256]uint16 {
	var t [256]uint16
	prev := -1
	for i := range t {
		v := int(math.Round(65535 * math.Pow(float64(i)/255, previewExposureGamma)))
		if v <= prev {
			v = prev + 1
		}
		t[i] = uint16(v)
		prev = v
	}
	return &t
}()

// fxEnc re-encodes linear light back to display levels. Built by inverting
// fxLin by scan rather than by 65536 calls to math.Pow: it costs microseconds
// at init and round-trips fxLin exactly, so an FX-free region of the buffer
// survives the trip byte-identical.
var fxEnc = func() *[65536]uint8 {
	var t [65536]uint8
	v := 0
	for i := range t {
		for v < 255 && i >= (int(fxLin[v])+int(fxLin[v+1])+1)/2 {
			v++
		}
		t[i] = uint8(v)
	}
	return &t
}()

// fxSource is the gather's working buffer: linear-light colour premultiplied
// by the (eroded) mask weight, plus that weight as the denominator. Every
// stage that averages — mosaic, box blur, the line and zoom gathers — averages
// all four planes together, which is what keeps the premultiplied invariant
// true and the normalization exact.
//
// After resolve() the invariant changes: r/g/b hold plain linear light and a
// still holds the coverage, which is what the streak pass and the compositor
// read.
type fxSource struct {
	w, h    int
	r, g, b []uint16
	a       []uint16 // coverage ŵ scaled to 0..65535
}

// applyMaskFX runs one mask's spatial effects over img, in place, blending the
// result back with the mask's own weight. It returns the full-resolution
// weight plane it rasterized (nil when the mask covers nothing), which the
// caller folds into the detail-suppression plane.
//
// Canonical intra-FX order: mosaic → blur → motion → zoom → prism → glow →
// streaks. Mosaic runs first so a following blur softens the block edges; the
// two light passes run last and additively, drawn from the already-defocused
// and already-fringed buffer, which is the anamorphic ordering — defocus the
// background, then let its highlights bloom and streak.
func applyMaskFX(img *image.RGBA, ev maskEvaluator, f maskFrame, m *edit.Mask) []uint8 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 4 || h < 4 {
		return nil
	}
	plane, box := weightPlaneFrom(ev, w, h)
	if box.Empty() {
		return nil
	}

	long := max(w, h)
	work := min(long, fxPlaneLongEdge)
	ww := max(4, w*work/long)
	wh := max(4, h*work/long)
	workLong := float64(max(ww, wh))

	a := &m.Adjust
	s := newFXSource(img, plane, ww, wh)

	// Whether a pass actually threw the region's detail away. Tracked around
	// the passes rather than read off the amounts, so a dial too low to round
	// up to a single pixel of reach counts as what it is — nothing ran — and
	// costs the render no sharpness. It picks the composite mode below.
	destroyed := false
	if a.Mosaic > 0 {
		fxMosaic(s, a.Mosaic*fxMosaicFrac)
		destroyed = true
	}
	if a.Blur > 0 {
		if r := int(math.Round(a.Blur * fxBlurFrac * workLong)); r >= 1 {
			fxBlur(s, r)
			destroyed = true
		}
	}
	if a.MotionBlur > 0 {
		if l := a.MotionBlur * fxMotionFrac * workLong; l >= 1 {
			dx, dy := fxDirection(f, a.FXAngle)
			taps := min(fxMotionTaps, max(3, int(math.Round(l/1.5))))
			fxLineSmear(s, dx, dy, l, taps, nil)
			destroyed = true
		}
	}
	// The zoom smear and the prism split share a centre, so a photo dialled
	// with both reads as one lens rather than two effects.
	var cx, cy float64
	if a.ZoomBlur > 0 || a.Prism != 0 {
		cx, cy = fxCenterOutput(m, ev, f, ww, wh, w, h)
	}
	if a.ZoomBlur > 0 {
		fxZoomSmear(s, cx, cy, a.ZoomBlur*fxZoomSpread)
		destroyed = true
	}
	s.resolve()
	// Record what the remaining passes change, rather than what they leave
	// behind. Worth its allocation only when nothing has destroyed the detail
	// the render still holds AND the working buffer is smaller than the
	// render — otherwise the plain replace below is both right and cheaper.
	var delta *fxDelta
	if !destroyed && (ww != w || wh != h) {
		delta = newFXDelta(s)
	}
	// Prism before the light passes, so the bloom and streaks inherit its
	// fringing — the order a real lens and a real emulsion sit in.
	if a.Prism != 0 {
		fxPrism(s, cx, cy, a.Prism)
	}
	if a.Glow > 0 {
		fxGlow(s, a.Glow)
	}
	if a.Streaks > 0 {
		if l := a.Streaks * fxStreakFrac * workLong; l >= 1 {
			dx, dy := fxDirection(f, a.FXAngle)
			fxStreaks(s, dx, dy, l, a.Streaks)
		}
	}
	delta.complete(s)
	fxComposite(img, plane, s, delta)
	return plane
}

// fxDelta is what the detail-preserving passes (prism, glow, streaks) ADD, in
// linear light at the working resolution — the low-frequency part of the
// result, which is the only part that survives the upsample back to the
// render's own resolution intact. See fxComposite for why that matters.
//
// It is seeded with the NEGATED pre-pass buffer and finished by adding the
// post-pass one, so the snapshot and the difference share one allocation. A
// nil *fxDelta is the "not worth recording" state and takes both methods.
type fxDelta struct {
	w, h    int
	r, g, b []int32
}

func newFXDelta(s *fxSource) *fxDelta {
	return &fxDelta{w: s.w, h: s.h, r: fxNegate(s.r), g: fxNegate(s.g), b: fxNegate(s.b)}
}

func fxNegate(p []uint16) []int32 {
	out := make([]int32, len(p))
	for i, v := range p {
		out[i] = -int32(v)
	}
	return out
}

func (d *fxDelta) complete(s *fxSource) {
	if d == nil {
		return
	}
	fxBands(d.h, func(y0, y1 int) {
		for j := y0 * d.w; j < y1*d.w; j++ {
			d.r[j] += int32(s.r[j])
			d.g[j] += int32(s.g[j])
			d.b[j] += int32(s.b[j])
		}
	})
}

// sample bilinearly reads the three planes, clamping at the edges — sample's
// signed sibling, and the only thing fxComposite needs in delta mode (where a
// pixel the working buffer never covered has a zero delta, so the coverage
// test the replace path makes is already implied).
func (d *fxDelta) sample(x, y float64) (r, g, b int32) {
	x0f, y0f := math.Floor(x), math.Floor(y)
	fx, fy := x-x0f, y-y0f
	x0 := clampInt(int(x0f), 0, d.w-1)
	y0 := clampInt(int(y0f), 0, d.h-1)
	x1 := clampInt(x0+1, 0, d.w-1)
	y1 := clampInt(y0+1, 0, d.h-1)
	var ar, ag, ab float64
	for _, t := range [4]struct {
		x, y int
		w    float64
	}{
		{x0, y0, (1 - fx) * (1 - fy)},
		{x1, y0, fx * (1 - fy)},
		{x0, y1, (1 - fx) * fy},
		{x1, y1, fx * fy},
	} {
		j := t.y*d.w + t.x
		ar += float64(d.r[j]) * t.w
		ag += float64(d.g[j]) * t.w
		ab += float64(d.b[j]) * t.w
	}
	return int32(math.Round(ar)), int32(math.Round(ag)), int32(math.Round(ab))
}

// fxDetailSuppression is how strongly this mask's FX destroyed detail that the
// global detail stage must not re-amplify. Streaks add light rather than
// destroying detail, so they do not contribute.
func fxDetailSuppression(a *edit.MaskAdjust) float64 {
	return max(a.Blur, a.MotionBlur, a.ZoomBlur, a.Mosaic)
}

// fxDirection converts a smear angle in oriented-frame degrees into a unit
// direction in output pixels. The rotation is outputPoint's linear part, so
// straightening the photo does not tilt the streaks relative to its content.
func fxDirection(f maskFrame, deg float64) (dx, dy float64) {
	rad := deg * math.Pi / 180
	cx, sy := math.Cos(rad), math.Sin(rad)
	return cx*f.cos + sy*f.sin, -cx*f.sin + sy*f.cos
}

// fxCentroid is implemented by evaluators whose coverage has a meaningful
// centre of mass, in frame fractions; the parametric types answer from their
// geometry instead.
type fxCentroid interface {
	centroid() (fx, fy float64, ok bool)
}

// maskFXCenter is the point a zoom blur radiates from, in frame fractions.
// A radial mask uses the handle already on screen; a brush or AI mask uses the
// centre of mass of its UN-INVERTED coverage. Un-inverted is the whole point:
// in the headline case the mask IS the background, whose centre of mass is
// meaningless, while the subject's is exactly what the streaks should radiate
// from — and reading it pre-invert makes zoom blur behave identically on a
// subject mask and on its inverse.
func maskFXCenter(m *edit.Mask, ev maskEvaluator) (fx, fy float64) {
	if m.Type == edit.MaskRadial {
		return m.CX, m.CY
	}
	if c, ok := ev.(fxCentroid); ok {
		if x, y, ok := c.centroid(); ok {
			return x, y
		}
	}
	return 0.5, 0.5
}

// fxCenterOutput maps maskFXCenter into working-buffer pixels.
func fxCenterOutput(m *edit.Mask, ev maskEvaluator, f maskFrame, ww, wh, w, h int) (float64, float64) {
	cfx, cfy := maskFXCenter(m, ev)
	ox, oy := f.outputPoint(cfx*f.frameW, cfy*f.frameH)
	return ox * float64(ww) / float64(w), oy * float64(wh) / float64(h)
}

// newFXSource area-averages img down to ww×wh, linearizing and premultiplying
// by the mask weight on the way, then applies the gather-side erosion.
func newFXSource(img *image.RGBA, plane []uint8, ww, wh int) *fxSource {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	n := ww * wh
	s := &fxSource{
		w: ww, h: wh,
		r: make([]uint16, n), g: make([]uint16, n), b: make([]uint16, n),
		a: make([]uint16, n),
	}
	cov := make([]uint8, n)
	// The interactive draft renders at or below fxPlaneLongEdge, so the
	// working buffer IS the render — the hot path skips the block machinery
	// (two nested loops and four variable divisions per output pixel) for a
	// straight row walk.
	oneToOne := ww == w && wh == h

	fxBands(wh, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			if oneToOne {
				row := img.Pix[y*img.Stride : y*img.Stride+w*4]
				prow := plane[y*w : (y+1)*w]
				for x := range ww {
					pw := int(prow[x])
					if pw == 0 {
						continue
					}
					j := y*ww + x
					i := x * 4
					s.r[j] = uint16(int(fxLin[row[i]]) * pw / 255)
					s.g[j] = uint16(int(fxLin[row[i+1]]) * pw / 255)
					s.b[j] = uint16(int(fxLin[row[i+2]]) * pw / 255)
					s.a[j] = uint16(pw * 257)
					cov[j] = uint8(pw)
				}
				continue
			}
			sy0 := y * h / wh
			sy1 := max(sy0+1, (y+1)*h/wh)
			for x := range ww {
				sx0 := x * w / ww
				sx1 := max(sx0+1, (x+1)*w/ww)
				var ar, ag, ab, aw, cnt int64
				for sy := sy0; sy < sy1; sy++ {
					row := img.Pix[sy*img.Stride:]
					prow := plane[sy*w:]
					for sx := sx0; sx < sx1; sx++ {
						cnt++
						pw := int64(prow[sx])
						if pw == 0 {
							continue
						}
						i := sx * 4
						ar += int64(fxLin[row[i]]) * pw
						ag += int64(fxLin[row[i+1]]) * pw
						ab += int64(fxLin[row[i+2]]) * pw
						aw += pw
					}
				}
				j := y*ww + x
				d := 255 * cnt
				s.r[j] = uint16(ar / d)
				s.g[j] = uint16(ag / d)
				s.b[j] = uint16(ab / d)
				s.a[j] = uint16(aw * 65535 / d)
				cov[j] = uint8(aw / cnt)
			}
		}
	})

	// Erosion scales numerator and denominator by the same factor, so it
	// leaves each pixel's own colour exactly where it was and only shifts how
	// much its neighbours weigh it — which is precisely the silhouette
	// contamination it is there to suppress.
	er := boxBlurPlane(cov, ww, wh, fxErodeRadius, 1)
	fxBands(wh, func(y0, y1 int) {
		for j := y0 * ww; j < y1*ww; j++ {
			if s.a[j] == 0 {
				continue
			}
			e := (int32(er[j]) - fxErodeKnee) * 255 / (255 - fxErodeKnee)
			e = min(max(e, fxErodeFloor), 255)
			if e == 255 {
				continue
			}
			s.r[j] = uint16(int32(s.r[j]) * e / 255)
			s.g[j] = uint16(int32(s.g[j]) * e / 255)
			s.b[j] = uint16(int32(s.b[j]) * e / 255)
			s.a[j] = uint16(max(1, int32(s.a[j])*e/255))
		}
	})
	return s
}

// fxBands runs fn over disjoint horizontal bands of rows concurrently — the
// ApplyLens banding pattern. Every FX stage writes only the rows it was handed
// and reads an immutable snapshot, so no stage needs any other synchronization.
func fxBands(h int, fn func(y0, y1 int)) {
	const band = 64
	if h <= band {
		fn(0, h)
		return
	}
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	for y0 := 0; y0 < h; y0 += band {
		y1 := min(y0+band, h)
		g.Go(func() error { fn(y0, y1); return nil })
	}
	_ = g.Wait()
}

// resolve divides the premultiplied planes through by the accumulated weight,
// leaving plain linear light in r/g/b and the coverage in a.
func (s *fxSource) resolve() {
	fxBands(s.h, func(y0, y1 int) {
		for j := y0 * s.w; j < y1*s.w; j++ {
			a := int(s.a[j])
			if a == 65535 {
				continue // fully covered: already plain linear light
			}
			if a == 0 {
				s.r[j], s.g[j], s.b[j] = 0, 0, 0
				continue
			}
			// One division per pixel rather than three: a 16.16 reciprocal is
			// exact enough here (the planes are 16-bit and the result is
			// re-encoded to 8).
			inv := (65535<<16 + a/2) / a
			s.r[j] = uint16(min(65535, (int(s.r[j])*inv+1<<15)>>16))
			s.g[j] = uint16(min(65535, (int(s.g[j])*inv+1<<15)>>16))
			s.b[j] = uint16(min(65535, (int(s.b[j])*inv+1<<15)>>16))
		}
	})
}

// fxMosaic averages the working buffer into square blocks. frac is the block
// edge as a fraction of the frame's long edge, and the grid is walked by block
// INDEX rather than by a rounded integer block size: rounding the size first
// would drift the boundaries apart between a 1024 draft and a 2048 settle, so
// the blocks would visibly crawl across the photo as the preview settled. The
// grid is anchored at the buffer origin, whose place in the frame is fixed by
// the crop.
func fxMosaic(s *fxSource, frac float64) {
	step := math.Max(2, frac*float64(max(s.w, s.h)))
	edge := func(k, size int) int {
		return min(size, int(math.Round(float64(k)*step)))
	}
	nx := int(math.Ceil(float64(s.w) / step))
	ny := int(math.Ceil(float64(s.h) / step))
	for ky := range ny {
		by, y1 := edge(ky, s.h), edge(ky+1, s.h)
		if by >= y1 {
			continue
		}
		for kx := range nx {
			bx, x1 := edge(kx, s.w), edge(kx+1, s.w)
			if bx >= x1 {
				continue
			}
			var ar, ag, ab, aa, n int64
			for y := by; y < y1; y++ {
				for x := bx; x < x1; x++ {
					j := y*s.w + x
					ar += int64(s.r[j])
					ag += int64(s.g[j])
					ab += int64(s.b[j])
					aa += int64(s.a[j])
					n++
				}
			}
			mr, mg, mb, ma := uint16(ar/n), uint16(ag/n), uint16(ab/n), uint16(aa/n)
			for y := by; y < y1; y++ {
				for x := bx; x < x1; x++ {
					j := y*s.w + x
					s.r[j], s.g[j], s.b[j], s.a[j] = mr, mg, mb, ma
				}
			}
		}
	}
}

// fxBlur defocuses with a separable running-sum box, iterated fxBlurPasses
// times. boxBlurPlaneU16 bands each axis internally, so the planes run in
// sequence rather than nesting two levels of errgroup.
func fxBlur(s *fxSource, r int) {
	for _, p := range []*[]uint16{&s.r, &s.g, &s.b, &s.a} {
		*p = boxBlurPlaneU16(*p, s.w, s.h, r, fxBlurPasses)
	}
}

// fxLineSmear gathers the working buffer along a constant direction — motion
// blur, and the streak pass's long smear.
func fxLineSmear(s *fxSource, dx, dy, length float64, taps int, weights []int32) {
	out := fxLineGather([][]uint16{s.r, s.g, s.b, s.a}, s.w, s.h, dx, dy, length, taps, weights)
	s.r, s.g, s.b, s.a = out[0], out[1], out[2], out[3]
}

// fxLineGather smears each plane along a constant direction. The tap offsets
// are the same for every pixel, so they collapse to flat slice offsets and the
// interior of the buffer — where no tap can fall outside — runs without a
// single bounds clamp. weights nil means a uniform kernel, which is what makes
// a motion blur read as motion rather than as a glow.
//
// Cost is set by the tap COUNT, not by the smear length: a half-frame streak
// costs exactly what a short one does.
func fxLineGather(planes [][]uint16, w, h int, dx, dy, length float64, taps int, weights []int32) [][]uint16 {
	if taps < 2 {
		return planes
	}
	offX := make([]int, taps)
	offY := make([]int, taps)
	wq := make([]int64, taps)
	flat := make([]int, taps)
	var wsum int64
	minOX, maxOX, minOY, maxOY := 0, 0, 0, 0
	for k := range taps {
		t := (float64(k)/float64(taps-1) - 0.5) * length
		offX[k] = int(math.Round(dx * t))
		offY[k] = int(math.Round(dy * t))
		flat[k] = offY[k]*w + offX[k]
		minOX, maxOX = min(minOX, offX[k]), max(maxOX, offX[k])
		minOY, maxOY = min(minOY, offY[k]), max(maxOY, offY[k])
		if weights == nil {
			wq[k] = 1
		} else {
			wq[k] = int64(weights[k])
		}
		wsum += wq[k]
	}
	if wsum == 0 {
		return planes
	}
	uniform := weights == nil
	ix0, ix1 := max(0, -minOX), min(w, w-maxOX)
	iy0, iy1 := max(0, -minOY), min(h, h-maxOY)

	out := make([][]uint16, len(planes))
	for pi := range planes {
		out[pi] = make([]uint16, w*h)
	}
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	const band = 64
	for pi := range planes {
		for y0 := 0; y0 < h; y0 += band {
			y1 := min(y0+band, h)
			g.Go(func() error {
				src, dst := planes[pi], out[pi]
				for y := y0; y < y1; y++ {
					base := y * w
					fx0, fx1 := ix0, ix1
					if y < iy0 || y >= iy1 {
						fx0, fx1 = 0, 0 // whole row needs clamping
					}
					edge := func(x0, x1 int) {
						for x := x0; x < x1; x++ {
							var acc int64
							for k := range taps {
								j := clampInt(y+offY[k], 0, h-1)*w + clampInt(x+offX[k], 0, w-1)
								acc += int64(src[j]) * wq[k]
							}
							dst[base+x] = uint16(acc / wsum)
						}
					}
					edge(0, fx0)
					if uniform {
						for x := fx0; x < fx1; x++ {
							var acc int64
							j := base + x
							for _, o := range flat {
								acc += int64(src[j+o])
							}
							dst[j] = uint16(acc / wsum)
						}
					} else {
						for x := fx0; x < fx1; x++ {
							var acc int64
							j := base + x
							for k, o := range flat {
								acc += int64(src[j+o]) * wq[k]
							}
							dst[j] = uint16(acc / wsum)
						}
					}
					edge(fx1, w)
				}
				return nil
			})
		}
	}
	_ = g.Wait()
	return out
}

// fxZoomSmear gathers along the ray from the effect centre, so the smear grows
// with distance from it. The scale spread is symmetric about 1 so the region
// does not appear to change size, only to streak outward.
//
// The tap coordinates are affine in x for a fixed row, so each tap walks the
// row as a 16.16 fixed-point ramp and the per-row source offset is computed
// once — no float arithmetic in the pixel loop.
func fxZoomSmear(s *fxSource, cx, cy, spread float64) {
	w, h, taps := s.w, s.h, fxZoomTaps
	scale := make([]float64, taps)
	for k := range taps {
		scale[k] = (float64(k)/float64(taps-1) - 0.5) * 2 * spread
	}
	planes := [][]uint16{s.r, s.g, s.b, s.a}
	out := make([][]uint16, len(planes))
	for pi := range planes {
		out[pi] = make([]uint16, w*h)
	}
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	const band = 64
	for pi := range planes {
		for y0 := 0; y0 < h; y0 += band {
			y1 := min(y0+band, h)
			g.Go(func() error {
				src, dst := planes[pi], out[pi]
				rowBase := make([]int, taps)
				stepX := make([]int, taps)
				baseX := make([]int, taps)
				for k := range taps {
					stepX[k] = int(math.Round((1 + scale[k]) * 65536))
					baseX[k] = int(math.Round(-cx*scale[k]*65536)) + 1<<15
				}
				for y := y0; y < y1; y++ {
					for k := range taps {
						sy := int(math.Round(float64(y) + (float64(y)-cy)*scale[k]))
						rowBase[k] = clampInt(sy, 0, h-1) * w
					}
					for x := range w {
						var acc int64
						for k := range taps {
							sx := (baseX[k] + stepX[k]*x) >> 16
							acc += int64(src[rowBase[k]+clampInt(sx, 0, w-1)])
						}
						dst[y*w+x] = uint16(acc / int64(taps))
					}
				}
				return nil
			})
		}
	}
	_ = g.Wait()
	s.r, s.g, s.b, s.a = out[0], out[1], out[2], out[3]
}

// fxStreaks draws the anamorphic light streaks: extract everything above the
// highlight knee, gate it by the mask's coverage (so a bright subject cannot
// throw streaks across a sky it is not part of), smear it a long way along the
// direction, and ADD it back in linear light. Additive-in-linear is what makes
// it read as light rather than as paint. Drawing it from the already-defocused
// buffer gives the anamorphic ordering for free.
//
// s must already be resolved: r/g/b are plain linear light, a is coverage.
// The extraction and the long gather run at HALF the working resolution: a
// streak is a glow, it carries no detail finer than its own width, and this
// gather is otherwise the most expensive thing in the stage by a wide margin.
func fxStreaks(s *fxSource, dx, dy, length, amount float64) {
	hi, hw, hh := fxHighlights(s)

	// Spread each highlight over at least the tap spacing before gathering.
	// Without this a highlight narrower than the spacing lands as one blob
	// per tap and the streak comes out DASHED — very visible, and the reason
	// a bounded-tap gather is otherwise not enough for a long smear.
	if r := int(math.Round(length / 2 / float64(fxStreakTaps-1) / 2)); r >= 1 {
		for i := range hi {
			hi[i] = boxBlurPlaneU16(hi[i], hw, hh, r, 2)
		}
	}

	// An exponential falloff so the streak fades toward its ends instead of
	// stopping dead.
	const tau = 0.35
	weights := make([]int32, fxStreakTaps)
	for k := range fxStreakTaps {
		t := math.Abs(float64(k)/float64(fxStreakTaps-1) - 0.5)
		weights[k] = int32(math.Round(256 * math.Exp(-t/tau)))
	}
	hi = fxLineGather(hi, hw, hh, dx, dy, length/2, fxStreakTaps, weights)
	fxAddLight(s, hi, hw, hh, amount*fxStreakGain)
}

// fxGlow is the isotropic sibling of fxStreaks: the same highlights, spread
// evenly instead of along a direction, and added back the same way. It is what
// a real defocus does that a box blur does not — highlights SWELL and bleed
// into their surroundings rather than merely averaging with them — so it is
// the cheap perceptual stand-in for disc bokeh.
//
// s must already be resolved.
func fxGlow(s *fxSource, amount float64) {
	hi, hw, hh := fxHighlights(s)
	r := max(1, int(math.Round(amount*fxGlowFrac*float64(max(hw, hh)))))
	for i := range hi {
		hi[i] = boxBlurPlaneU16(hi[i], hw, hh, r, 3)
	}
	fxAddLight(s, hi, hw, hh, amount*fxGlowGain)
}

// fxHighlights extracts everything above the highlight knee, gated by the
// mask's coverage (so a bright subject cannot throw light across a sky it is
// not part of), at HALF the working resolution — a glow carries no detail
// finer than its own width, and the spreading that follows is otherwise the
// most expensive thing in the stage by a wide margin. Shared by the streak and
// glow passes, which differ only in HOW they spread it.
//
// s must already be resolved: r/g/b are plain linear light, a is coverage.
func fxHighlights(s *fxSource) (planes [][]uint16, hw, hh int) {
	knee := int(fxLin[fxStreakKnee])
	hw, hh = max(1, (s.w+1)/2), max(1, (s.h+1)/2)
	hi := [][]uint16{make([]uint16, hw*hh), make([]uint16, hw*hh), make([]uint16, hw*hh)}
	fxBands(hh, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := range hw {
				var ar, ag, ab, n int
				for sy := 2 * y; sy < min(2*y+2, s.h); sy++ {
					for sx := 2 * x; sx < min(2*x+2, s.w); sx++ {
						j := sy*s.w + sx
						n++
						cov := int(s.a[j])
						if cov == 0 {
							continue
						}
						ar += max(0, int(s.r[j])-knee) * cov / 65535
						ag += max(0, int(s.g[j])-knee) * cov / 65535
						ab += max(0, int(s.b[j])-knee) * cov / 65535
					}
				}
				j := y*hw + x
				hi[0][j] = uint16(ar / n)
				hi[1][j] = uint16(ag / n)
				hi[2][j] = uint16(ab / n)
			}
		}
	})
	return hi, hw, hh
}

// fxAddLight adds a half-resolution light plane triple back into the resolved
// buffer, in linear light. Additive-in-linear is what makes both the streaks
// and the glow read as light rather than as paint.
func fxAddLight(s *fxSource, hi [][]uint16, hw, hh int, gain float64) {
	gainQ := int(math.Round(gain * 256))
	if gainQ <= 0 {
		return
	}
	fxBands(s.h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := range s.w {
				j := y*s.w + x
				if s.a[j] == 0 {
					continue
				}
				sr, sg, sb := fxSampleHalf(hi, hw, hh, x, y)
				s.r[j] = uint16(min(65535, int(s.r[j])+sr*gainQ>>8))
				s.g[j] = uint16(min(65535, int(s.g[j])+sg*gainQ>>8))
				s.b[j] = uint16(min(65535, int(s.b[j])+sb*gainQ>>8))
			}
		}
	})
}

// fxPrism throws the red and blue channels apart radially about the effect
// centre — lateral chromatic aberration, the signature of a cheap or vintage
// lens, as a creative dial. The displacement grows with distance from the
// centre (it is a per-channel SCALE, not a shift), which is what makes it read
// as a lens property rather than as a misregistration.
//
// Sampling is nearest-neighbour on purpose: the displacement changes by a
// fraction of a pixel between neighbours, so it acts as a local shift with no
// stair-stepping, and green — which carries most of the luminance — is never
// resampled at all.
//
// s must already be resolved.
func fxPrism(s *fxSource, cx, cy, amount float64) {
	w, h := s.w, s.h
	k := amount * fxPrismScale
	or := make([]uint16, w*h)
	ob := make([]uint16, w*h)
	fxBands(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			// This is a GATHER, so the signs are inverted against the effect:
			// to throw red OUTWARD the red channel must sample INWARD.
			fy := float64(y) - cy
			ry := clampInt(int(math.Round(float64(y)-fy*k)), 0, h-1) * w
			by := clampInt(int(math.Round(float64(y)+fy*k)), 0, h-1) * w
			for x := range w {
				j := y*w + x
				if s.a[j] == 0 {
					continue
				}
				fx := float64(x) - cx
				jr := ry + clampInt(int(math.Round(float64(x)-fx*k)), 0, w-1)
				jb := by + clampInt(int(math.Round(float64(x)+fx*k)), 0, w-1)
				// Outside the mask there is no colour to borrow — keeping the
				// pixel's own channel fringes toward neutral at the edge
				// instead of pulling in black.
				if s.a[jr] != 0 {
					or[j] = s.r[jr]
				} else {
					or[j] = s.r[j]
				}
				if s.a[jb] != 0 {
					ob[j] = s.b[jb]
				} else {
					ob[j] = s.b[j]
				}
			}
		}
	})
	s.r, s.b = or, ob
}

// fxSampleHalf bilinearly reads a half-resolution plane triple at a
// full-resolution coordinate. The glow is smooth, so this upsample is
// invisible — which is the whole reason the gather ran small.
func fxSampleHalf(p [][]uint16, hw, hh, x, y int) (int, int, int) {
	// Full pixel centre (x+0.5) in half-res coordinates, minus the half-res
	// pixel centre: ((x+0.5)/2 - 0.5) in quarter steps → (2x-1)/4.
	fx, fy := 2*x-1, 2*y-1
	x0, y0 := fx>>2, fy>>2
	tx, ty := fx&3, fy&3
	if fx < 0 {
		x0, tx = 0, 0
	}
	if fy < 0 {
		y0, ty = 0, 0
	}
	x1, y1 := min(x0+1, hw-1), min(y0+1, hh-1)
	x0, y0 = min(x0, hw-1), min(y0, hh-1)
	w00 := (4 - tx) * (4 - ty)
	w10 := tx * (4 - ty)
	w01 := (4 - tx) * ty
	w11 := tx * ty
	at := func(pl []uint16) int {
		return (int(pl[y0*hw+x0])*w00 + int(pl[y0*hw+x1])*w10 +
			int(pl[y1*hw+x0])*w01 + int(pl[y1*hw+x1])*w11) / 16
	}
	return at(p[0]), at(p[1]), at(p[2])
}

// fxComposite encodes the working buffer back to display levels and blends it
// into img with the mask's own (un-eroded) weight, in Q8 exactly like the tone
// composite in ApplyMasks. Pixels the working buffer never covered — a mask so
// thin it vanished in the downscale — are left alone rather than blended
// toward black.
//
// delta, when non-nil, switches the blend from REPLACE to ADD-THE-DIFFERENCE:
// the pixel keeps its own full-resolution value and only picks up the light
// the recorded passes added. That distinction is the difference between a
// sharp 1:1 tile and a soft one. Replacing is right for an effect that
// destroyed the region's detail (a defocus, a mosaic, a smear) — the working
// buffer then holds everything there is to know. It is WRONG for the passes
// that only add light or split colour: the background under a bloom is still
// as sharp as the decode, so replacing it with an upsample of a 1024 plane
// throws away every pixel of detail the render just paid a full RAW decode
// for, and the region reads as blocky at 1:1 (and never sharpens, however
// long you wait — the tiles ARE the sharpest thing there is). The difference
// carries only the low-frequency light those passes contribute, which is
// exactly the part that survives the upsample intact.
func fxComposite(img *image.RGBA, plane []uint8, s *fxSource, delta *fxDelta) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	exact := s.w == w && s.h == h
	sx := float64(s.w) / float64(w)
	sy := float64(s.h) / float64(h)

	fxBands(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := img.Pix[y*img.Stride : y*img.Stride+w*4]
			prow := plane[y*w : (y+1)*w]
			fy := (float64(y)+0.5)*sy - 0.5
			for x := range w {
				pw := prow[x]
				if pw == 0 {
					continue
				}
				fx := (float64(x)+0.5)*sx - 0.5
				i0 := x * 4
				var lr, lg, lb int
				if delta != nil {
					dr, dg, db := delta.sample(fx, fy)
					if dr == 0 && dg == 0 && db == 0 {
						continue
					}
					// The delta cancels the mask normalization and the erosion
					// bias its two ends share, so what lands on the render's own
					// pixel is the added light alone — no step at the silhouette.
					lr = clampInt(int(fxLin[row[i0]])+int(dr), 0, 65535)
					lg = clampInt(int(fxLin[row[i0+1]])+int(dg), 0, 65535)
					lb = clampInt(int(fxLin[row[i0+2]])+int(db), 0, 65535)
				} else {
					var la int
					if exact {
						j := y*w + x
						lr, lg, lb, la = int(s.r[j]), int(s.g[j]), int(s.b[j]), int(s.a[j])
					} else {
						lr, lg, lb, la = s.sample(fx, fy)
					}
					if la == 0 {
						continue
					}
				}
				wq := int32(covToWeight[pw])
				r0, g0, b0 := int32(row[i0]), int32(row[i0+1]), int32(row[i0+2])
				r := int32(fxEnc[lr])
				gg := int32(fxEnc[lg])
				bl := int32(fxEnc[lb])
				row[i0] = clamp8(r0 + (r-r0)*wq>>8)
				row[i0+1] = clamp8(g0 + (gg-g0)*wq>>8)
				row[i0+2] = clamp8(b0 + (bl-b0)*wq>>8)
			}
		}
	})
}

// sample bilinearly reads the four planes, clamping at the edges.
func (s *fxSource) sample(x, y float64) (r, g, b, a int) {
	x0f, y0f := math.Floor(x), math.Floor(y)
	fx, fy := x-x0f, y-y0f
	x0 := clampInt(int(x0f), 0, s.w-1)
	y0 := clampInt(int(y0f), 0, s.h-1)
	x1 := clampInt(x0+1, 0, s.w-1)
	y1 := clampInt(y0+1, 0, s.h-1)
	var ar, ag, ab, aa float64
	for _, t := range [4]struct {
		x, y int
		w    float64
	}{
		{x0, y0, (1 - fx) * (1 - fy)},
		{x1, y0, fx * (1 - fy)},
		{x0, y1, (1 - fx) * fy},
		{x1, y1, fx * fy},
	} {
		j := t.y*s.w + t.x
		ar += float64(s.r[j]) * t.w
		ag += float64(s.g[j]) * t.w
		ab += float64(s.b[j]) * t.w
		aa += float64(s.a[j]) * t.w
	}
	return int(ar + 0.5), int(ag + 0.5), int(ab + 0.5), int(aa + 0.5)
}
