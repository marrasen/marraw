package pyramid

import (
	"image"
	"math"
)

// Graded defocus — the mask blur, with the radius following the mask's own
// weight ramp instead of applying one radius cross-faded by coverage.
//
// A cross-fade between the sharp pixel and a uniformly blurred copy is the
// cheap fake: across a feather it reads as two zones with a seam, because no
// pixel in the transition is actually blurred AT the in-between radius. Depth
// of field — and any defocus through a soft mask — is a RAMP: the circle of
// confusion grows with the weight, which needs a per-pixel radius. An inverted
// AI depth mask carrying Blur is therefore a true tilt shift: its weight IS
// the distance from the in-focus band, so blur grows with depth. Three
// decisions carry the result:
//
//   - A per-pixel radius is approximated by gradedBlurLevels DISCRETE blur
//     levels that the composite interpolates between, rather than by a
//     variable-width gather. A running-sum box blur costs the same at any
//     radius but only at a CONSTANT one; a per-pixel radius would force a
//     per-pixel kernel walk, orders of magnitude more work at these reaches.
//     Three levels plus the sharp original reads as continuous.
//   - Each level gathers with weight min(w/level, 1), so a pixel contributes
//     to a blur level only once it is at least that defocused itself. This is
//     the fxSource weight-normalized gather with the weight ramp in the
//     erosion's place, and it is what stops the sharp subject printing a
//     ghost of itself into the blurred surround — the tell of every cheap
//     fake bokeh.
//   - The gather weight is eroded to its local MINIMUM first (erodeMin): an
//     AI matte resolves an object boundary over a few soft pixels while the
//     photo resolves it exactly, so the sliver the matte calls half-covered
//     still holds the subject's colour and would smear a whole radius into
//     the surround. fxSource's blur-and-knee erosion is wrong here — it is
//     tuned for a matte that is 0 or 255 nearly everywhere, and on a smooth
//     ramp it would bias every gather toward its heavier neighbours, a
//     directional smear across the whole gradient. A minimum leaves a smooth
//     ramp alone and bites only at discontinuities.
//
// Levels are computed in linear light (fxLin/fxEnc) at resolutions bounded by
// the shared fxPlaneLongEdge working buffer AND by each level's own radius
// (blurLevelDims), so a 1024 draft, a 2048 settle, a 1:1 tile and a 60 MP
// export all compute the identical effect at identical cost.
//
// Known omission, deliberate: real foreground bokeh SCATTERS — an out-of-focus
// branch in front of the subject spills over it. This gathers, so the sharp
// subject keeps a crisp edge against a blurred foreground. Fixing that needs
// a scatter pass, not a tweak here.
const (
	// gradedBlurLevels is how many blur radii the ramp is quantized to (level
	// k has radius k/gradedBlurLevels of the maximum); the composite lerps
	// between the bracketing pair, with the untouched render as the implicit
	// level 0.
	gradedBlurLevels = 3
	// gradedBlurLevelRadius is the blur radius, in the level buffer's own
	// pixels, that blurLevelDims sizes each level to reach. Wide enough that
	// three iterated box passes are a convincing disc and that the bilinear
	// upsample back to the render is well below the level's own softness;
	// every pixel past it is a pixel of detail the blur is about to throw
	// away anyway.
	gradedBlurLevelRadius = 12
	// bokehLevelRadius is the disc pass's target: a hard-edged disc softens by
	// the bilinear upsample where the box blur has no edge to lose, so its
	// levels compute at twice the resolution. The disc gather is O(r) per
	// pixel (fxDiscBlur), so the bigger buffers stay affordable.
	bokehLevelRadius = 24
	// bokehBoost is how strongly the disc gather favours bright pixels: the
	// weight scales by up to (1 + bokehBoost) at clipped linear white. This is
	// what keeps a defocused point light a BRIGHT disc — a plain average would
	// dilute it into its dark surround and the disc would read as a smudge.
	bokehBoost = 12.0
)

// applyGradedBlur defocuses img in place through the mask's full-resolution
// weight plane, radius growing with the local weight up to
// amount*fxBlurFrac of the working buffer's long edge. Returns whether it
// ran — a dial too low to reach a single working-buffer pixel runs nothing,
// so nothing may claim the render's sharpness either (the applyMaskFX
// `destroyed` tracking, for the same reason).
func applyGradedBlur(img *image.RGBA, plane []uint8, amount float64, disc bool) bool {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := max(w, h)
	work := min(long, fxPlaneLongEdge)
	ww := max(4, w*work/long)
	wh := max(4, h*work/long)
	rMax := amount * fxBlurFrac * float64(max(ww, wh))
	if rMax < 1 {
		return false
	}

	lin, wp := newBlurSource(img, plane, ww, wh)
	gather := erodeMin(wp, ww, wh)
	levels := make([]blurLevelView, gradedBlurLevels)
	for k := range levels {
		// Level k blurs at (k+1)/gradedBlurLevels of the maximum radius,
		// gathering only pixels at least that defocused — and on a buffer
		// sized to that radius (blurLevelDims), which is where most of the
		// cost went before the levels were allowed to shrink.
		frac := float64(k+1) / gradedBlurLevels
		target := gradedBlurLevelRadius
		if disc {
			target = bokehLevelRadius
		}
		lw, lh := blurLevelDims(ww, wh, frac*rMax, target)
		src, g := lin, gather
		if lw != ww || lh != wh {
			src, g = blurDownscale(lin, gather, ww, wh, lw, lh)
		}
		r := max(1, int(math.Round(frac*rMax*float64(max(lw, lh))/float64(max(ww, wh)))))
		levels[k] = newBlurLevelView(blurLevel(src, g, lw, lh, frac, r, disc), w, h)
	}
	gradedBlurComposite(img, plane, levels)
	return true
}

// newBlurSource area-averages the render down to the working buffer,
// linearizing on the way, and brings the coc plane with it. Unlike newFXSource
// nothing is premultiplied here: each blur level needs its own weight, so the
// planes are shared plain and premultiplied per level.
func newBlurSource(img *image.RGBA, coc []uint8, ww, wh int) (*fxSource, []uint8) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	n := ww * wh
	s := &fxSource{
		w: ww, h: wh,
		r: make([]uint16, n), g: make([]uint16, n), b: make([]uint16, n),
	}
	out := make([]uint8, n)
	// The interactive draft renders at or below fxPlaneLongEdge, so the working
	// buffer IS the render — skip the block machinery for a straight row walk
	// (newFXSource's fast path, for the same reason).
	if ww == w && wh == h {
		fxBands(wh, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				row := img.Pix[y*img.Stride : y*img.Stride+w*4]
				for x := range ww {
					j := y*ww + x
					i := x * 4
					s.r[j] = fxLin[row[i]]
					s.g[j] = fxLin[row[i+1]]
					s.b[j] = fxLin[row[i+2]]
				}
			}
			copy(out[y0*ww:y1*ww], coc[y0*w:y1*w])
		})
		return s, out
	}
	fxBands(wh, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			sy0 := y * h / wh
			sy1 := max(sy0+1, (y+1)*h/wh)
			for x := range ww {
				sx0 := x * w / ww
				sx1 := max(sx0+1, (x+1)*w/ww)
				var ar, ag, ab, ac, cnt int64
				for sy := sy0; sy < sy1; sy++ {
					row := img.Pix[sy*img.Stride:]
					crow := coc[sy*w:]
					for sx := sx0; sx < sx1; sx++ {
						i := sx * 4
						ar += int64(fxLin[row[i]])
						ag += int64(fxLin[row[i+1]])
						ab += int64(fxLin[row[i+2]])
						ac += int64(crow[sx])
						cnt++
					}
				}
				j := y*ww + x
				s.r[j] = uint16(ar / cnt)
				s.g[j] = uint16(ag / cnt)
				s.b[j] = uint16(ab / cnt)
				out[j] = uint8(ac / cnt)
			}
		}
	})
	return s, out
}

// blurLevelDims sizes one blur level's own buffer. fxPlaneLongEdge's argument
// — a defocused region carries no detail finer than its blur radius, so
// computing it small and upsampling is invisible — applies per LEVEL, not just
// once for the stage: the widest level throws away far more detail than the
// narrowest and has no business costing the same. Each level is therefore
// computed at whatever resolution puts its radius on gradedBlurLevelRadius pixels,
// capped at the shared working buffer (which a small dial stays at, since its
// radius is only a pixel or two there to begin with).
//
// The size depends on the effect's own reach and the frame's aspect, never on
// the render's resolution, so a draft, a settle, a tile and an export still
// compute the identical effect.
func blurLevelDims(ww, wh int, r float64, target int) (int, int) {
	long := max(ww, wh)
	if r <= float64(target) {
		return ww, wh
	}
	scaled := min(long, max(16, int(math.Round(float64(long)*float64(target)/r))))
	return max(4, ww*scaled/long), max(4, wh*scaled/long)
}

// blurDownscale area-averages the linear working buffer and its gather coc to
// one level's smaller grid. The two travel together because a level's weights
// have to describe the very pixels it is about to average.
func blurDownscale(lin *fxSource, coc []uint8, ww, wh, nw, nh int) (*fxSource, []uint8) {
	n := nw * nh
	out := &fxSource{
		w: nw, h: nh,
		r: make([]uint16, n), g: make([]uint16, n), b: make([]uint16, n),
	}
	oc := make([]uint8, n)
	fxBands(nh, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			sy0 := y * wh / nh
			sy1 := max(sy0+1, (y+1)*wh/nh)
			for x := range nw {
				sx0 := x * ww / nw
				sx1 := max(sx0+1, (x+1)*ww/nw)
				var ar, ag, ab, ac, cnt int64
				for sy := sy0; sy < sy1; sy++ {
					for sx := sx0; sx < sx1; sx++ {
						j := sy*ww + sx
						ar += int64(lin.r[j])
						ag += int64(lin.g[j])
						ab += int64(lin.b[j])
						ac += int64(coc[j])
						cnt++
					}
				}
				j := y*nw + x
				out.r[j] = uint16(ar / cnt)
				out.g[j] = uint16(ag / cnt)
				out.b[j] = uint16(ab / cnt)
				oc[j] = uint8(ac / cnt)
			}
		}
	})
	return out, oc
}

// erodeMin is the gather-side erosion, fxSource's idea in the form a
// continuous depth field needs: the coc a level GATHERS with is the local
// MINIMUM over erodeMinRadius, while the coc the result is COMPOSITED with
// stays as the depth map drew it. A pixel therefore lends its colour to a
// defocus gather only if everything around it is at least that defocused too.
//
// That is what keeps a sharp subject out of the background behind it. The
// depth map resolves an object boundary over a few soft pixels while the
// photo resolves it exactly, so the sliver of pixels the map calls
// half-defocused still holds the SUBJECT's colour — and a plain weighted
// gather spreads it a whole blur radius into the background as a ghost.
//
// A local minimum rather than fxSource's blur-and-knee on purpose: the knee is
// tuned for a matte that is 0 or 255 nearly everywhere, and on a smooth depth
// ramp it would down-weight every pixel by how near-focus it is, biasing each
// gather toward its deeper neighbours — a directional smear across the whole
// gradient. A minimum leaves a smooth ramp alone (it shifts every weight by
// the same couple of pixels of gradient) and bites only at discontinuities,
// which is exactly where the ghost comes from.
func erodeMin(coc []uint8, ww, wh int) []uint8 {
	const r = 2
	tmp := make([]uint8, len(coc))
	out := make([]uint8, len(coc))
	fxBands(wh, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := coc[y*ww : (y+1)*ww]
			trow := tmp[y*ww : (y+1)*ww]
			for x := range ww {
				m := row[x]
				for k := max(0, x-r); k < min(ww, x+r+1); k++ {
					if row[k] < m {
						m = row[k]
					}
				}
				trow[x] = m
			}
		}
	})
	fxBands(wh, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := range ww {
				m := tmp[y*ww+x]
				for k := max(0, y-r); k < min(wh, y+r+1); k++ {
					if v := tmp[k*ww+x]; v < m {
						m = v
					}
				}
				out[y*ww+x] = m
			}
		}
	})
	return out
}

// tiltLevel builds one blur level: the working buffer premultiplied by
// min(coc/frac, 1), box-blurred at radius r, and divided back through. The
// weight is the whole point — a pixel sharper than this level contributes
// nothing to it, so the in-focus subject cannot bleed into the defocused
// surround (fxSource's weight-normalized gather, with coc as the mask).
//
// After resolve, r/g/b hold plain linear light and a holds the accumulated
// weight; a == 0 marks a pixel no defocused neighbour reached, which the
// composite falls back from.
func blurLevel(lin *fxSource, coc []uint8, ww, wh int, frac float64, r int, disc bool) *fxSource {
	n := ww * wh
	s := &fxSource{
		w: ww, h: wh,
		r: make([]uint16, n), g: make([]uint16, n), b: make([]uint16, n),
		a: make([]uint16, n),
	}
	// 255*frac is the coc at which a pixel counts fully toward this level;
	// below it the weight ramps, which keeps the level boundaries from
	// printing as contours in a smooth depth gradient.
	full := math.Max(1, 255*frac)
	var wq [256]uint16
	for v := range wq {
		wq[v] = uint16(math.Round(65535 * math.Min(float64(v)/full, 1)))
	}
	fxBands(wh, func(y0, y1 int) {
		for j := y0 * ww; j < y1*ww; j++ {
			pw := float64(wq[coc[j]])
			if pw == 0 {
				continue
			}
			if disc {
				// Favour bright pixels: scale the gather weight by up to
				// (1 + bokehBoost) at linear white, renormalized so the
				// weight stays ≤ the graded weight (the ratio is all that
				// matters — resolve divides it back out). Squared so the
				// emphasis belongs to the highlights, not the midtones.
				l := (299*float64(lin.r[j]) + 587*float64(lin.g[j]) + 114*float64(lin.b[j])) / 1000 / 65535
				pw *= (1 + bokehBoost*l*l) / (1 + bokehBoost)
				if pw < 1 {
					pw = 1
				}
			}
			s.r[j] = uint16(float64(lin.r[j]) * pw / 65535)
			s.g[j] = uint16(float64(lin.g[j]) * pw / 65535)
			s.b[j] = uint16(float64(lin.b[j]) * pw / 65535)
			s.a[j] = uint16(pw)
		}
	})
	if disc {
		fxDiscBlur(s, r)
	} else {
		fxBlur(s, r)
	}
	s.resolve()
	return s
}

// fxDiscBlur convolves the four planes with a hard-edged disc of radius r —
// the aperture kernel that turns a defocused highlight into a disc rather
// than a gaussian blob. A disc is a stack of horizontal spans, so with
// per-row prefix sums the gather is O(r) per pixel instead of O(r²): one
// span-sum difference per disc row. Out-of-frame rows and columns are
// skipped and the divide renormalizes, the fxSource edge contract.
func fxDiscBlur(s *fxSource, r int) {
	spans := make([]int, 2*r+1)
	for dy := -r; dy <= r; dy++ {
		spans[dy+r] = int(math.Sqrt(float64(r*r - dy*dy)))
	}
	for _, p := range []*[]uint16{&s.r, &s.g, &s.b, &s.a} {
		*p = discPlane(*p, s.w, s.h, r, spans)
	}
}

func discPlane(src []uint16, w, h, r int, spans []int) []uint16 {
	// Row prefix sums: pre[y*(w+1)+x+1] = Σ row y cols 0..x. uint32 is safe:
	// 65535 × fxPlaneLongEdge < 2³².
	pre := make([]uint32, (w+1)*h)
	fxBands(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := src[y*w : (y+1)*w]
			p := pre[y*(w+1):]
			var acc uint32
			for x, v := range row {
				acc += uint32(v)
				p[x+1] = acc
			}
		}
	})
	out := make([]uint16, len(src))
	fxBands(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			dy0 := max(-r, -y)
			dy1 := min(r, h-1-y)
			for x := 0; x < w; x++ {
				var sum, n uint64
				for dy := dy0; dy <= dy1; dy++ {
					sp := spans[dy+r]
					x0 := max(0, x-sp)
					x1 := min(w-1, x+sp)
					p := pre[(y+dy)*(w+1):]
					sum += uint64(p[x1+1] - p[x0])
					n += uint64(x1 - x0 + 1)
				}
				out[y*w+x] = uint16(sum / n)
			}
		}
	})
	return out
}

// gradedBlurComposite writes the graded defocus back over the render. Each output
// pixel's coc picks a position along the level ramp: 0 is the render's own
// pixel, gradedBlurLevels is the widest blur, and the value between them is a lerp of
// the bracketing pair in linear light.
//
// This is a REPLACE, not a difference composite: the effect destroyed the
// region's detail, so the working buffer holds everything there is to know
// about it (fxComposite's argument). A pixel at coc 0 is skipped outright, so
// the in-focus band comes through byte-identical rather than through a
// linearize/re-encode round trip.
func gradedBlurComposite(img *image.RGBA, coc []uint8, levels []blurLevelView) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	fxBands(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := img.Pix[y*img.Stride : y*img.Stride+w*4]
			crow := coc[y*w : (y+1)*w]
			for x := range w {
				c := crow[x]
				if c == 0 {
					continue // in focus: leave the render's own pixel alone
				}
				i := x * 4
				// The render's own pixel in linear light: the ramp's level 0,
				// and the fallback for a level that gathered no weight here.
				own := [3]float64{
					float64(fxLin[row[i]]), float64(fxLin[row[i+1]]), float64(fxLin[row[i+2]]),
				}
				// Position along the ramp, in level units; the bracketing pair
				// is k (the render itself at k == 0) and k+1.
				u := float64(c) / 255 * gradedBlurLevels
				k := min(int(u), gradedBlurLevels-1)
				t := u - float64(k)

				lo := own
				if k > 0 {
					lo = levels[k-1].sample(x, y, own)
				}
				hi := levels[k].sample(x, y, own)

				for ch := range 3 {
					v := lo[ch] + (hi[ch]-lo[ch])*t
					row[i+ch] = fxEnc[clampInt(int(v+0.5), 0, 65535)]
				}
			}
		}
	})
}

// blurLevelView is one blur level plus the mapping from render pixels onto its
// own grid. Each level is sized to its own radius, so they no longer share one
// scale and the composite carries a view per level rather than a pair of
// factors for all of them.
type blurLevelView struct {
	s      *fxSource
	sx, sy float64
	exact  bool
}

func newBlurLevelView(s *fxSource, w, h int) blurLevelView {
	return blurLevelView{
		s:     s,
		sx:    float64(s.w) / float64(w),
		sy:    float64(s.h) / float64(h),
		exact: s.w == w && s.h == h,
	}
}

// sample reads the level at an output pixel, falling back to the render's own
// linear value where the level accumulated no weight — a pixel deep inside a
// sharp region that no defocused neighbour reached. Blending toward a zero
// there would darken the transition to black.
func (v blurLevelView) sample(x, y int, own [3]float64) [3]float64 {
	if v.exact {
		j := y*v.s.w + x
		if v.s.a[j] == 0 {
			return own
		}
		return [3]float64{float64(v.s.r[j]), float64(v.s.g[j]), float64(v.s.b[j])}
	}
	sr, sg, sb, sa := v.s.sample((float64(x)+0.5)*v.sx-0.5, (float64(y)+0.5)*v.sy-0.5)
	if sa == 0 {
		return own
	}
	return [3]float64{float64(sr), float64(sg), float64(sb)}
}
