package pyramid

import (
	"image"
	"math"
	"runtime"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/lens"
	"github.com/marrasen/marraw/internal/store"
)

// ApplyLens undoes the lens's own signature — geometric distortion, lateral
// chromatic aberration and vignetting — on a full-frame render.
//
// It runs FIRST, before ApplyGeometry and before anything in ApplyFinish:
// the correction is a property of the projected image circle, so it has to
// happen while the frame is still the whole frame, in the orientation the
// sensor recorded. Everything downstream — the crop rectangle, masks,
// spots, the tone calibration against the camera's own JPEG — then works on
// a frame that is already geometrically what the lens was pointed at, which
// is also what the camera's embedded JPEG shows.
//
// The output keeps the input's dimensions. Distortion correction alone
// would either shrink the frame or leave black wedges at the edges, so the
// warp folds in an auto-scale that zooms just enough to keep it full (see
// lens.Warp) — the same bargain every raw developer makes.
//
// w may be nil, in which case src is returned untouched.
func ApplyLens(src *image.RGBA, w *lens.Warp, e *edit.Params) *image.RGBA {
	if w == nil {
		return src
	}
	if !w.Geometric() {
		// Vignetting on its own moves no pixels: correct the brightness in
		// place and skip the resample entirely.
		applyVignette(src, w, e)
		return src
	}
	b := src.Bounds()
	width, height := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	dec, enc := linearCodec(e)
	vig := w.HasVignetting()

	// The whole frame is resampled three times over (once per channel), so
	// this loop is the stage's entire cost — 42 megapixels of it on a 1:1
	// render. It reads the source plane directly rather than through
	// image.RGBA's accessors, and skips the red and blue lookups entirely
	// when there is no CA to correct, which is the common case.
	sp := plane{pix: src.Pix, stride: src.Stride, w: width, h: height}
	sp.ox = src.Bounds().Min.X
	sp.oy = src.Bounds().Min.Y
	tca := w.Geometric() && w.HasTCA()

	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	const band = 64
	for y0 := 0; y0 < height; y0 += band {
		y1 := min(y0+band, height)
		g.Go(func() error {
			for oy := y0; oy < y1; oy++ {
				do := dst.PixOffset(0, oy)
				fy := float64(oy) + 0.5
				for ox := range width {
					rx, ry, gx, gy, bx, by := w.Source(float64(ox)+0.5, fy)
					r, gg, bl := sp.sample3(gx-0.5, gy-0.5)
					if tca {
						// Each channel samples its own coordinate — that is
						// what makes this a CA correction rather than three
						// copies of the same resample.
						r = sp.sample1(rx-0.5, ry-0.5, 0)
						bl = sp.sample1(bx-0.5, by-0.5, 2)
					}
					if vig {
						// The fall-off happened where the light landed, so
						// the gain is evaluated at the source position.
						k := w.VigGain(gx, gy)
						r = enc(dec(r) * k)
						gg = enc(dec(gg) * k)
						bl = enc(dec(bl) * k)
					}
					o := do + ox*4
					dst.Pix[o+0] = r
					dst.Pix[o+1] = gg
					dst.Pix[o+2] = bl
					dst.Pix[o+3] = 0xff
				}
			}
			return nil
		})
	}
	_ = g.Wait() // the workers never fail; the group is only for the limit
	return dst
}

// applyVignette corrects fall-off in place, for profiles that carry no
// geometry to correct.
func applyVignette(img *image.RGBA, w *lens.Warp, e *edit.Params) {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	dec, enc := linearCodec(e)

	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	const band = 64
	for y0 := 0; y0 < height; y0 += band {
		y1 := min(y0+band, height)
		g.Go(func() error {
			for y := y0; y < y1; y++ {
				o := img.PixOffset(b.Min.X, b.Min.Y+y)
				for x := range width {
					k := w.VigGain(float64(x)+0.5, float64(y)+0.5)
					if k != 1 {
						img.Pix[o+0] = enc(dec(img.Pix[o+0]) * k)
						img.Pix[o+1] = enc(dec(img.Pix[o+1]) * k)
						img.Pix[o+2] = enc(dec(img.Pix[o+2]) * k)
					}
					o += 4
				}
			}
			return nil
		})
	}
	_ = g.Wait()
}

// linearCodec returns the pair that takes an 8-bit encoded sample into the
// linear light the decode was gamma-encoded from, and back again.
//
// Vignetting has to be corrected in linear light: the fall-off is a
// transmission ratio, so multiplying gamma-encoded values by it would
// overshoot badly — a corner needing +1.5 EV would come out nearer +3.
// The encoding is LibRaw's own output gamma for this edit, the same one
// ApplyExposureEV inverts, so a corrected frame re-encodes onto exactly the
// curve the rest of the pipeline expects.
func linearCodec(e *edit.Params) (dec func(uint8) float64, enc func(float64) uint8) {
	pwr, ts := outputEncoding(e)
	decode := dcrawGammaDecoder(pwr, ts)
	var decLUT [256]float64
	for i := range decLUT {
		decLUT[i] = decode(float64(i) / 255)
	}
	encLUT := gammaTable(pwr, ts)
	return func(v uint8) float64 { return decLUT[v] },
		func(v float64) uint8 {
			i := int(math.Round(v * 65535))
			if i < 0 {
				i = 0
			} else if i > 65535 {
				i = 65535
			}
			return encLUT[i]
		}
}

// plane is a flat view of an RGBA image for the resample loop: the bounds
// offset is folded in once so the inner loop indexes the pixel slice
// directly instead of going through image.RGBA's accessors.
type plane struct {
	pix    []uint8
	stride int
	w, h   int
	ox, oy int
}

// clampCoord splits a fractional coordinate into its two neighbouring
// integer samples and the fraction between them, repeating the edge pixel
// outside the image.
//
// Straighten wants black outside (sampleBilinear in geometry.go): its
// exposed corners are genuinely outside the photo, and the crop overlay
// keeps the user clear of them. A lens correction has no outside — the
// auto-scale guarantees every sample lands on real pixels — so the only
// coordinates that can leave the frame are rounding at the last row and
// column, where a fade to black would draw a dark hairline along the border.
func clampCoord(v float64, n int) (i0, i1 int, f float64) {
	if v <= 0 {
		return 0, 0, 0
	}
	if v >= float64(n-1) {
		return n - 1, n - 1, 0
	}
	fl := math.Floor(v)
	i0 = int(fl)
	return i0, i0 + 1, v - fl
}

// sample3 bilinearly reads all three channels at one coordinate — the path
// taken for green always, and for every channel when there is no CA to
// correct.
func (p *plane) sample3(x, y float64) (r, g, b uint8) {
	x0, x1, fx := clampCoord(x, p.w)
	y0, y1, fy := clampCoord(y, p.h)
	o00 := (p.oy+y0)*p.stride + (p.ox+x0)*4
	o01 := (p.oy+y0)*p.stride + (p.ox+x1)*4
	o10 := (p.oy+y1)*p.stride + (p.ox+x0)*4
	o11 := (p.oy+y1)*p.stride + (p.ox+x1)*4
	w00 := (1 - fx) * (1 - fy)
	w01 := fx * (1 - fy)
	w10 := (1 - fx) * fy
	w11 := fx * fy
	mix := func(c int) uint8 {
		return uint8(float64(p.pix[o00+c])*w00 + float64(p.pix[o01+c])*w01 +
			float64(p.pix[o10+c])*w10 + float64(p.pix[o11+c])*w11 + 0.5)
	}
	return mix(0), mix(1), mix(2)
}

// sample1 bilinearly reads one channel, for the red and blue planes of a CA
// correction which sample coordinates of their own.
func (p *plane) sample1(x, y float64, c int) uint8 {
	x0, x1, fx := clampCoord(x, p.w)
	y0, y1, fy := clampCoord(y, p.h)
	r0 := (p.oy + y0) * p.stride
	r1 := (p.oy + y1) * p.stride
	c0 := (p.ox+x0)*4 + c
	c1 := (p.ox+x1)*4 + c
	return uint8(float64(p.pix[r0+c0])*(1-fx)*(1-fy) +
		float64(p.pix[r0+c1])*fx*(1-fy) +
		float64(p.pix[r1+c0])*(1-fx)*fy +
		float64(p.pix[r1+c1])*fx*fy + 0.5)
}

// LensProfiles resolves a photo's lens profile and memoizes the result.
//
// The lookup itself is a scan of ~1500 database entries plus an
// interpolation, which is cheap but not free, and it is asked for on every
// render of every level — hence the memo. The answer depends only on the
// photo's EXIF, never on the edit, so one entry per cache key is enough; a
// photo with no matching profile memoizes the nil.
// maxLensMemo caps the memo at far more photos than any one editing session
// works through, so the bound is a backstop rather than something the hot path
// ever notices.
const maxLensMemo = 4096

type LensProfiles struct {
	mu    sync.Mutex
	byKey map[string]*lens.Correction
}

func NewLensProfiles() *LensProfiles {
	return &LensProfiles{byKey: map[string]*lens.Correction{}}
}

// For returns the photo's resolved profile, or nil when its body or lens
// isn't in the database. Nil-safe.
func (p *LensProfiles) For(photo store.Photo) *lens.Correction {
	// An empty photo.Lens is NOT a reason to stop: a fixed-lens compact
	// records no lens string, and lens.Resolve finds its built-in lens
	// through the body's mount.
	if p == nil || photo.CacheKey == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.byKey[photo.CacheKey]; ok {
		return c
	}
	c := lens.Resolve(photo.Make, photo.Model, photo.Lens, photo.FocalLen, photo.Aperture)
	// Bound it. One entry per photo is small, but nothing ever removed one, so
	// a long session across a large library grew this for the life of the
	// process — the only per-photo cache here without a cap. A miss costs one
	// database scan, not a stall, so dropping the lot on overflow is enough:
	// the working set is a folder at a time and refills immediately.
	if len(p.byKey) >= maxLensMemo {
		clear(p.byKey)
	}
	p.byKey[photo.CacheKey] = c
	return c
}

// LensWarp binds a resolved profile to one render's pixel grid, honoring the
// edit's per-component amounts. Returns nil — meaning "correct nothing" —
// for a photo with no profile, for an edit with the correction switched off,
// and for a frame too small to warp.
//
// width and height must be the FULL frame's, before any crop: the
// coefficients are radial about the optical centre, so a warp bound to a
// cropped frame would put that centre in the wrong place.
func LensWarp(c *lens.Correction, e *edit.Params, width, height int) *lens.Warp {
	if c == nil || !e.LensCorrects() {
		return nil
	}
	dist, vig, ca := e.LensAmounts()
	return c.Warp(width, height, lens.Strengths{Distortion: dist, Vignetting: vig, TCA: ca})
}
