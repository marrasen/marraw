package pyramid

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"sync"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/lens"
	"github.com/marrasen/marraw/internal/libraw"
)

// This file implements the "decode once, fold later" interactive path. A photo
// is demosaiced a single time into a 16-bit scene-linear reference (see
// edit.LinearRefLibrawParams); every subsequent white-balance, exposure,
// brightness and gamma change is then a cheap per-pixel pass over that buffer
// instead of a fresh ~400 ms demosaic.
//
// White balance is applied where LibRaw applies it — to the camera's own
// channels, before the colour matrix. The reference is already through that
// matrix, so the fold goes back through its inverse, scales, and returns (see
// FoldParams.M). Scaling the developed pixels instead is a different operation:
// the matrix's negative cross-terms mean a big blue boost crushes green in a
// real decode, and a preview that skipped them turned magenta the moment the
// settle landed.

// FoldParams are the per-frame raw-stage adjustments folded into the linear
// reference.
//
// D is the white-balance change as per-camera-channel gain, relative to the
// balance the reference was decoded at and normalized to green (D[1] = 1), so
// white balance shifts colour and never exposure — Exp and Bright carry
// brightness alone. M/Minv convert between the reference's output space and
// the camera channels D acts on; when HasMatrix is false (a four-colour sensor
// or a file with no usable matrix) the fold falls back to scaling the output
// channels directly, as it always did. Pwr/Ts are the LibRaw output-gamma
// power and toe slope.
//
// The exact decode of the same edit normalizes multipliers by their minimum
// instead (dcraw's scale_colors), which also moves brightness; every accurate
// render folds edit.Params.WBCompEV back in so the two agree.
type FoldParams struct {
	D      [3]float64
	Exp    float64
	Bright float64
	// White is the ceiling the camera channels clip at, in the reference's
	// units. LibRaw clips at the white level AFTER dividing the multipliers by
	// their smallest, so on an edit the compensation darkens (a pick with a
	// channel below green) the decode loses highlights the reference still
	// holds. Clipping the fold at the same place is what keeps the preview
	// honest about what the settle will show. Zero means the plain white level.
	White     float64
	M, Minv   [3][3]float64
	HasMatrix bool
	Pwr       float64
	Ts        float64
}

// white is the clip ceiling, defaulting to the 16-bit white level.
func (fp FoldParams) white() float64 {
	if fp.White <= 0 {
		return 65535
	}
	return fp.White
}

// scalarGain is the per-output-channel gain for the no-matrix path: the WB
// ratio applied directly to the developed channels, times exposure and
// brightness.
func (fp FoldParams) scalarGain() [3]float64 {
	var k [3]float64
	for c := range 3 {
		k[c] = fp.D[c] * fp.Exp * fp.Bright
	}
	return k
}

// flat reports whether D is close enough to no white-balance change that the
// matrix round trip is pointless: M·(d·I)·M⁻¹ = d·I exactly, so an exposure,
// brightness or gamma drag takes the cheap scalar path with identical output.
func (fp FoldParams) flat() bool {
	lo, hi := fp.D[0], fp.D[0]
	for _, v := range fp.D {
		lo, hi = min(lo, v), max(hi, v)
	}
	return lo > 0 && hi/lo-1 < 1e-3
}

// Invert3 inverts a 3×3 matrix by its adjugate, reporting false when it is
// singular (or near enough that the inverse would amplify noise absurdly).
func Invert3(m [3][3]float64) ([3][3]float64, bool) {
	c00 := m[1][1]*m[2][2] - m[1][2]*m[2][1]
	c01 := m[1][2]*m[2][0] - m[1][0]*m[2][2]
	c02 := m[1][0]*m[2][1] - m[1][1]*m[2][0]
	det := m[0][0]*c00 + m[0][1]*c01 + m[0][2]*c02
	if math.Abs(det) < 1e-9 {
		return [3][3]float64{}, false
	}
	inv := [3][3]float64{
		{c00, m[0][2]*m[2][1] - m[0][1]*m[2][2], m[0][1]*m[1][2] - m[0][2]*m[1][1]},
		{c01, m[0][0]*m[2][2] - m[0][2]*m[2][0], m[0][2]*m[1][0] - m[0][0]*m[1][2]},
		{c02, m[0][1]*m[2][0] - m[0][0]*m[2][1], m[0][0]*m[1][1] - m[0][1]*m[1][0]},
	}
	for i := range 3 {
		for j := range 3 {
			inv[i][j] /= det
		}
	}
	return inv, true
}

// FromLibrawLinear converts a 16-bit interleaved linear RGB LibRaw image (as
// produced by edit.LinearRefLibrawParams) into an *image.RGBA64.
func FromLibrawLinear(img *libraw.Image) (*image.RGBA64, error) {
	if img.Bits != 16 || img.Channels != 3 {
		return nil, fmt.Errorf("pyramid: linear decode expects 16-bit RGB, got %d bits %d channels", img.Bits, img.Channels)
	}
	dst := image.NewRGBA64(image.Rect(0, 0, img.Width, img.Height))
	src := img.Data // host-endian uint16, 3 samples per pixel
	for i, j := 0, 0; i+5 < len(src) && j+7 < len(dst.Pix); i, j = i+6, j+8 {
		r := binary.NativeEndian.Uint16(src[i:])
		g := binary.NativeEndian.Uint16(src[i+2:])
		b := binary.NativeEndian.Uint16(src[i+4:])
		// image.RGBA64 stores big-endian samples.
		dst.Pix[j], dst.Pix[j+1] = byte(r>>8), byte(r)
		dst.Pix[j+2], dst.Pix[j+3] = byte(g>>8), byte(g)
		dst.Pix[j+4], dst.Pix[j+5] = byte(b>>8), byte(b)
		dst.Pix[j+6], dst.Pix[j+7] = 0xff, 0xff
	}
	return dst, nil
}

// gammaTable caches the most recent linear→encoded gamma table (16-bit index,
// 8-bit value). During a WB/exposure/brightness drag the gamma is constant, so
// only the per-channel gain changes and this table is reused; a gamma drag
// rebuilds it. Guarded because previews may render concurrently.
var (
	gammaMu  sync.Mutex
	gammaKey [2]float64
	gammaTab *[65536]uint8
)

func gammaTable(pwr, ts float64) *[65536]uint8 {
	gammaMu.Lock()
	defer gammaMu.Unlock()
	if gammaTab != nil && gammaKey == [2]float64{pwr, ts} {
		return gammaTab
	}
	enc := dcrawGammaEncoder(pwr, ts)
	t := new([65536]uint8)
	for i := range t {
		v := int(enc(float64(i)/65535)*255 + 0.5)
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		t[i] = uint8(v)
	}
	gammaKey, gammaTab = [2]float64{pwr, ts}, t
	return t
}

// dcrawGammaCoeffs solves dcraw's gamma_curve coefficients for output power
// pwr and toe slope ts: g[2] is the toe/power crossover in encoded domain,
// g[3] the same in linear domain, g[4] the power-segment offset.
func dcrawGammaCoeffs(pwr, ts float64) [5]float64 {
	var g [5]float64
	g[0], g[1] = pwr, ts
	bnd := [2]float64{}
	if ts >= 1 {
		bnd[1] = 1
	} else {
		bnd[0] = 1
	}
	if ts != 0 && (ts-1)*(pwr-1) <= 0 {
		for range 48 {
			g[2] = (bnd[0] + bnd[1]) / 2
			var idx int
			if pwr != 0 {
				if (math.Pow(g[2]/ts, -pwr)-1)/pwr-1/g[2] > -1 {
					idx = 1
				}
			} else if g[2]/math.Exp(1-1/g[2]) < ts {
				idx = 1
			}
			bnd[idx] = g[2]
		}
		g[3] = g[2] / ts
		if pwr != 0 {
			g[4] = g[2]*(1/pwr) - g[2]
		}
	}
	return g
}

// dcrawGammaEncoder returns the forward (linear→encoded, 0..1) gamma function
// LibRaw/dcraw bakes into its output, for output power pwr and toe slope ts.
// Reproducing it here keeps a folded transient frame matching the re-decoded
// settle. Mirrors dcraw's gamma_curve coefficient solve.
func dcrawGammaEncoder(pwr, ts float64) func(float64) float64 {
	g := dcrawGammaCoeffs(pwr, ts)
	return func(r float64) float64 {
		if r >= 1 {
			return 1
		}
		if r <= 0 {
			return 0
		}
		if r < g[3] {
			return r * ts
		}
		if pwr != 0 {
			return math.Pow(r, pwr)*(1+g[4]) - g[4]
		}
		return math.Log(r)*g[2] + 1
	}
}

// dcrawGammaDecoder returns the inverse (encoded→linear, 0..1) of
// dcrawGammaEncoder — each segment inverted analytically. Used to take
// already-encoded decode output back to linear light for an exposure fold.
func dcrawGammaDecoder(pwr, ts float64) func(float64) float64 {
	g := dcrawGammaCoeffs(pwr, ts)
	return func(y float64) float64 {
		if y >= 1 {
			return 1
		}
		if y <= 0 {
			return 0
		}
		if y < g[3]*ts { // the toe's encoded extent is enc(g[3]) = g[3]·ts
			return y / ts
		}
		if pwr != 0 {
			return math.Pow((y+g[4])/(1+g[4]), 1/pwr)
		}
		return math.Exp((y - 1) / g[2])
	}
}

// RenderPreviewLinear renders an interactive-path frame off the scene-linear
// reference lin: it bilinearly resamples to longEdge and folds the raw stage
// (WB/exposure/brightness/gamma via fp) in a single pass, then runs the same
// lens, geometry, look and detail stages as RenderPreview. Because WB and
// exposure live in fp, dragging them reuses lin with no demosaic. Geometry
// runs on the downscaled buffer (a pure-crop preview is therefore taken from
// the frame's longEdge rather than the crop's — the deferred settle crops at
// full res), which also hands the lens stage exactly what it needs: the
// whole frame, uncropped, at preview size.
func RenderPreviewLinear(lin *image.RGBA64, longEdge int, fp FoldParams, lookGamma float64, edits *edit.Params, ai AIMapSet, fills FillSet, lensc *lens.Correction) *image.RGBA {
	b := lin.Bounds()
	sw, sh := b.Dx(), b.Dy()
	ow, oh := sw, sh
	if long := max(sw, sh); long > longEdge {
		ow, oh = sw*longEdge/long, sh*longEdge/long
	}
	disp := foldScale(lin, max(1, ow), max(1, oh), fp)
	// foldScale always returns a fresh buffer, so the lens stage may correct
	// it in place when the profile carries no geometry.
	disp = ApplyLens(disp, LensWarp(lensc, edits, disp.Bounds().Dx(), disp.Bounds().Dy()), edits)
	disp = ApplyGeometry(disp, edits)
	ApplyFinish(disp, lookGamma, edits, ai, fills)
	return disp
}

// foldScale bilinearly resamples the linear reference to ow×oh and applies the
// fold in one pass, producing a display-encoded 8-bit RGBA equivalent to a
// fresh LibRaw decode at the folded settings. Cost is proportional to the
// output pixels, so decoding the reference at full half-size and downscaling
// here per frame is no more expensive than pre-scaling would be.
func foldScale(lin *image.RGBA64, ow, oh int, fp FoldParams) *image.RGBA {
	if fp.HasMatrix && !fp.flat() {
		return foldScaleMatrix(lin, ow, oh, fp)
	}
	gtab := gammaTable(fp.Pwr, fp.Ts)
	// Per-channel gain as 16.16 fixed point: index = (linear16 * kfix) >> 16.
	var kfix [3]int64
	k := fp.scalarGain()
	for c := range 3 {
		kfix[c] = int64(k[c] * 65536)
	}
	b := lin.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	sx := float64(sw) / float64(ow)
	sy := float64(sh) / float64(oh)
	for y := range oh {
		fy := (float64(y)+0.5)*sy - 0.5
		y0 := int(math.Floor(fy))
		wy := fy - float64(y0)
		y0c := clampInt(y0, 0, sh-1)
		y1c := clampInt(y0+1, 0, sh-1)
		drow := dst.Pix[y*dst.Stride : y*dst.Stride+ow*4]
		for x := range ow {
			fx := (float64(x)+0.5)*sx - 0.5
			x0 := int(math.Floor(fx))
			wx := fx - float64(x0)
			x0c := clampInt(x0, 0, sw-1)
			x1c := clampInt(x0+1, 0, sw-1)
			i00 := lin.PixOffset(x0c, y0c)
			i10 := lin.PixOffset(x1c, y0c)
			i01 := lin.PixOffset(x0c, y1c)
			i11 := lin.PixOffset(x1c, y1c)
			di := x * 4
			for c := range 3 {
				o := c * 2
				s00 := float64(uint32(lin.Pix[i00+o])<<8 | uint32(lin.Pix[i00+o+1]))
				s10 := float64(uint32(lin.Pix[i10+o])<<8 | uint32(lin.Pix[i10+o+1]))
				s01 := float64(uint32(lin.Pix[i01+o])<<8 | uint32(lin.Pix[i01+o+1]))
				s11 := float64(uint32(lin.Pix[i11+o])<<8 | uint32(lin.Pix[i11+o+1]))
				top := s00 + (s10-s00)*wx
				bot := s01 + (s11-s01)*wx
				idx := int64(top+(bot-top)*wy) * kfix[c] >> 16
				if idx > 65535 {
					idx = 65535
				} else if idx < 0 {
					idx = 0
				}
				drow[di+c] = gtab[idx]
			}
			drow[di+3] = 0xff
		}
	}
	return dst
}

// foldScaleMatrix is foldScale for a real white-balance change: it takes each
// resampled pixel back to camera channels through Minv, scales there (where
// LibRaw scales), clips at the white level the way scale_colors does, and
// returns through M with brightness folded into its rows.
//
// The clip between the two matrices is what reproduces a decode's behaviour on
// a channel driven past white: without it a boosted channel would come back
// through M as an out-of-gamut colour rather than clipping to it. What is left
// is the demosaic-order difference — LibRaw scales the CFA before interpolating
// and this scales after — which fold_verify_test measures and bounds.
func foldScaleMatrix(lin *image.RGBA64, ow, oh int, fp FoldParams) *image.RGBA {
	gtab := gammaTable(fp.Pwr, fp.Ts)
	// The white balance clips, then exposure clips again — scale_colors and
	// exp_bef are separate pre-matrix stages in LibRaw, each with its own clip
	// at the white level. Brightness is post-matrix, so it folds into M's rows
	// for free and reaches past the ceiling the way LibRaw's does.
	white := fp.white()
	d0, d1, d2 := fp.D[0], fp.D[1], fp.D[2]
	exp := fp.Exp
	bright := fp.Bright
	if bright <= 0 {
		bright = 1
	}
	// Hoisted into locals so the inner loop keeps them in registers.
	n00, n01, n02 := fp.Minv[0][0], fp.Minv[0][1], fp.Minv[0][2]
	n10, n11, n12 := fp.Minv[1][0], fp.Minv[1][1], fp.Minv[1][2]
	n20, n21, n22 := fp.Minv[2][0], fp.Minv[2][1], fp.Minv[2][2]
	m00, m01, m02 := fp.M[0][0]*bright, fp.M[0][1]*bright, fp.M[0][2]*bright
	m10, m11, m12 := fp.M[1][0]*bright, fp.M[1][1]*bright, fp.M[1][2]*bright
	m20, m21, m22 := fp.M[2][0]*bright, fp.M[2][1]*bright, fp.M[2][2]*bright

	b := lin.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	sx := float64(sw) / float64(ow)
	sy := float64(sh) / float64(oh)
	for y := range oh {
		fy := (float64(y)+0.5)*sy - 0.5
		y0 := int(math.Floor(fy))
		wy := fy - float64(y0)
		y0c := clampInt(y0, 0, sh-1)
		y1c := clampInt(y0+1, 0, sh-1)
		drow := dst.Pix[y*dst.Stride : y*dst.Stride+ow*4]
		for x := range ow {
			fx := (float64(x)+0.5)*sx - 0.5
			x0 := int(math.Floor(fx))
			wx := fx - float64(x0)
			x0c := clampInt(x0, 0, sw-1)
			x1c := clampInt(x0+1, 0, sw-1)
			i00 := lin.PixOffset(x0c, y0c)
			i10 := lin.PixOffset(x1c, y0c)
			i01 := lin.PixOffset(x0c, y1c)
			i11 := lin.PixOffset(x1c, y1c)
			var p [3]float64
			for c := range 3 {
				o := c * 2
				s00 := float64(uint32(lin.Pix[i00+o])<<8 | uint32(lin.Pix[i00+o+1]))
				s10 := float64(uint32(lin.Pix[i10+o])<<8 | uint32(lin.Pix[i10+o+1]))
				s01 := float64(uint32(lin.Pix[i01+o])<<8 | uint32(lin.Pix[i01+o+1]))
				s11 := float64(uint32(lin.Pix[i11+o])<<8 | uint32(lin.Pix[i11+o+1]))
				top := s00 + (s10-s00)*wx
				bot := s01 + (s11-s01)*wx
				p[c] = top + (bot-top)*wy
			}
			// Output space → camera channels, white-balance, clip, expose,
			// clip — the two pre-matrix stages LibRaw runs, in its order.
			c0 := min(max((n00*p[0]+n01*p[1]+n02*p[2])*d0, 0), white)
			c1 := min(max((n10*p[0]+n11*p[1]+n12*p[2])*d1, 0), white)
			c2 := min(max((n20*p[0]+n21*p[1]+n22*p[2])*d2, 0), white)
			c0 = min(c0*exp, white)
			c1 = min(c1*exp, white)
			c2 = min(c2*exp, white)
			// Back to output space, brightness already in the rows.
			di := x * 4
			drow[di] = gtab[clampIdx(m00*c0+m01*c1+m02*c2)]
			drow[di+1] = gtab[clampIdx(m10*c0+m11*c1+m12*c2)]
			drow[di+2] = gtab[clampIdx(m20*c0+m21*c1+m22*c2)]
			drow[di+3] = 0xff
		}
	}
	return dst
}

// clampIdx rounds a linear value onto the gamma table's 16-bit index range.
func clampIdx(v float64) int {
	if v <= 0 {
		return 0
	}
	if v >= 65535 {
		return 65535
	}
	return int(v)
}
