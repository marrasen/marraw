package pyramid

import (
	"context"
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
)

// FallbackLookGamma is used when a photo has no calibrated gamma yet
// (e.g. an edit preview requested before any base render finished).
const FallbackLookGamma = 0.72

// ComputeLookGamma calibrates the per-photo tone lift: the gamma that maps
// our RAW render's mean luminance onto the camera JPEG's mean luminance.
// Camera engines (Sony DRO etc.) lift adaptively per scene — a backlit shot
// needs far more than a bright one — so this must be measured per photo,
// not hardcoded. Means are 0..255.
func ComputeLookGamma(rawMean, cameraMean float64) float64 {
	if rawMean <= 1 || rawMean >= 254 || cameraMean <= 1 || cameraMean >= 254 {
		return FallbackLookGamma
	}
	g := math.Log(cameraMean/255) / math.Log(rawMean/255)
	return math.Min(1.1, math.Max(0.5, g))
}

// MeasureAutoBrightEV measures LibRaw's auto-brighten lift on the opened
// photo as the equivalent exposure-slider EV. The base look renders with
// auto-brighten (histogram-normalizing, scene-dependent); any edit switches
// to a deterministic ExpShift = 2^expEV. Seeding the exposure dial with this
// EV makes that switch invisible — the mimic compensation shows in the dial
// instead of silently vanishing on the first adjustment.
func MeasureAutoBrightEV(ctx context.Context, proc *libraw.Processor) (float64, error) {
	p := libraw.DefaultParams()
	p.HalfSize = true // no demosaic — only the mean luma matters
	bright, err := proc.Process(ctx, p)
	if err != nil {
		return 0, err
	}
	mb := meanLumaPacked(bright)
	p.NoAutoBright = true
	flat, err := proc.Process(ctx, p)
	if err != nil {
		return 0, err
	}
	return AutoBrightEV(mb, meanLumaPacked(flat)), nil
}

// AutoBrightEV converts the display-space mean ratio between the
// auto-brightened and flat renders of one scene into an exposure EV:
// display values are approximately linear^(1/2.222), so the linear-domain
// ratio is the display ratio raised to 2.222. Clamped to the exposure
// slider's range and rounded so the dial reads cleanly.
func AutoBrightEV(brightMean, flatMean float64) float64 {
	if brightMean <= 1 || flatMean <= 1 {
		return 0
	}
	ev := 2.222 * math.Log2(brightMean/flatMean)
	return math.Min(3, math.Max(-2, math.Round(ev*100)/100))
}

// meanLumaPacked is MeanLuma for LibRaw's 3-byte interleaved RGB output.
func meanLumaPacked(img *libraw.Image) float64 {
	if img == nil || img.Bits != 8 || img.Channels != 3 {
		return 0
	}
	pix := img.Data
	var sum, n uint64
	// Every 4th pixel is plenty for a scene mean.
	for i := 0; i+2 < len(pix); i += 12 {
		sum += (299*uint64(pix[i]) + 587*uint64(pix[i+1]) + 114*uint64(pix[i+2])) / 1000
		n++
	}
	if n == 0 {
		return 0
	}
	return float64(sum) / float64(n)
}

// MeanLuma returns the mean Rec.601 luma (0..255), subsampled for speed.
func MeanLuma(img *image.RGBA) float64 {
	pix := img.Pix
	var sum, n uint64
	// Every 4th pixel is plenty for a scene mean.
	for i := 0; i+3 < len(pix); i += 16 {
		sum += (299*uint64(pix[i]) + 587*uint64(pix[i+1]) + 114*uint64(pix[i+2])) / 1000
		n++
	}
	if n == 0 {
		return 0
	}
	return float64(sum) / float64(n)
}

// ApplyFinish runs the shared post-geometry stages in canonical order: the
// retouch spots (a pixel transplant, before the look so healed pixels develop
// like their source), the global look, then the local adjustment masks over
// the developed color, then the detail pass on the final tones. Every render
// path (pyramid levels, tiles, interactive previews, export) must go through
// this order — the one call site that can't use the helper (cache.generate's
// full-res path, which interleaves progress reports) mirrors it stage for
// stage. ai carries the photo's AI-mask maps (AIMapStore.SetFor); nil when the
// edit has none or they are unavailable.
func ApplyFinish(img *image.RGBA, gamma float64, e *edit.Params, ai AIMapSet) {
	ApplyHeal(img, e)
	ApplyLook(img, gamma, e)
	ApplyMasks(img, e, ai)
	ApplyDetail(img, e)
}

// ApplyLook warms up LibRaw's flat output to sit close to the camera's own
// JPEG rendering — the calibrated gamma lift, a mild S-curve, and a
// saturation boost — and layers the edit's look-stage adjustments (tone
// curve, saturation/vibrance, split toning, vignette) on top. e is nil for
// the base look. Applied only to RAW-decoded renditions — embedded JPEG
// thumbnails already carry the camera curve.
func ApplyLook(img *image.RGBA, gamma float64, e *edit.Params) {
	lut := buildLookLUT(gamma, e)
	satQ := int32(115) // base boost ×1.15 in 1/100 units
	if e != nil {
		satQ = int32(math.Round(115 * (1 + e.Saturation)))
	}
	// The plain LUT+saturation loop covers most edits; only vibrance, split
	// toning and vignette need per-pixel position or chroma math.
	if e == nil || (e.Vibrance == 0 && e.SplitShadowAmt == 0 && e.SplitHighlightAmt == 0 && e.Vignette == 0) {
		applyLookSimple(img, &lut, satQ)
	} else {
		applyLookFull(img, &lut, satQ, e)
	}
	// The HSL mixer runs last, over the developed color: gated so neutral
	// mixers cost nothing and existing renders stay bit-identical.
	if e.HasHSL() {
		applyHSL(img, e)
	}
}

func applyLookSimple(img *image.RGBA, lut *[256]uint8, satQ int32) {
	pix := img.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		r := int32(lut[pix[i]])
		g := int32(lut[pix[i+1]])
		b := int32(lut[pix[i+2]])
		// Saturation around Rec.601 luma, in integer math.
		luma := (299*r + 587*g + 114*b) / 1000
		pix[i] = clamp8(luma + (r-luma)*satQ/100)
		pix[i+1] = clamp8(luma + (g-luma)*satQ/100)
		pix[i+2] = clamp8(luma + (b-luma)*satQ/100)
	}
}

// applyLookFull is the position/chroma-aware variant: vibrance scales the
// saturation boost by how unsaturated the pixel already is, split toning
// pushes shadows/highlights toward their tint hues, and the vignette gain
// falls off radially from the image center.
func applyLookFull(img *image.RGBA, lut *[256]uint8, satQ int32, e *edit.Params) {
	bnd := img.Bounds()
	w, h := bnd.Dx(), bnd.Dy()
	vibQ := int32(math.Round(e.Vibrance * 100))

	sR, sG, sB := tintDir(e.SplitShadowHue, e.SplitShadowAmt)
	hR, hG, hB := tintDir(e.SplitHighlightHue, e.SplitHighlightAmt)
	split := e.SplitShadowAmt != 0 || e.SplitHighlightAmt != 0

	// Vignette: gain ×256 over quantized normalized r², precomputed so the
	// pixel loop stays in integers. The falloff (r²)^1.25 keeps the center
	// clean and concentrates the effect toward the corners.
	var vgain []int32
	var colSq []int64
	var maxR2 int64 = 1
	if e.Vignette != 0 {
		cx, cy := float64(w-1)/2, float64(h-1)/2
		maxR2 = int64(cx*cx+cy*cy) + 1
		colSq = make([]int64, w)
		for x := range colSq {
			d := float64(x) - cx
			colSq[x] = int64(d * d)
		}
		vgain = make([]int32, 1024)
		for i := range vgain {
			f := math.Pow(float64(i)/1023, 1.25)
			vgain[i] = int32(math.Round(256 * (1 - 0.75*e.Vignette*f)))
		}
	}

	pix := img.Pix
	for y := range h {
		row := pix[y*img.Stride : y*img.Stride+w*4]
		var rowSq int64
		if vgain != nil {
			d := float64(y) - float64(h-1)/2
			rowSq = int64(d * d)
		}
		for x := range w {
			i := x * 4
			r := int32(lut[row[i]])
			g := int32(lut[row[i+1]])
			b := int32(lut[row[i+2]])
			luma := (299*r + 587*g + 114*b) / 1000

			sat := satQ
			if vibQ != 0 {
				chroma := max(r, g, b) - min(r, g, b)
				sat += vibQ * (255 - chroma) / 255
			}
			r = luma + (r-luma)*sat/100
			g = luma + (g-luma)*sat/100
			b = luma + (b-luma)*sat/100

			if split {
				ws := (255 - luma) * (255 - luma) >> 8 // 0..254, peaks in shadows
				wh := luma * luma >> 8                 // peaks in highlights
				r += (sR*ws + hR*wh) >> 8
				g += (sG*ws + hG*wh) >> 8
				b += (sB*ws + hB*wh) >> 8
			}

			if vgain != nil {
				gv := vgain[(rowSq+colSq[x])*1023/maxR2]
				r = r * gv >> 8
				g = g * gv >> 8
				b = b * gv >> 8
			}

			row[i] = clamp8(r)
			row[i+1] = clamp8(g)
			row[i+2] = clamp8(b)
		}
	}
}

// tintDir converts a split-tone hue/amount into a luma-neutral RGB push,
// scaled ×64×amount so full strength shifts a fully weighted pixel by
// roughly ±45 levels.
func tintDir(hue, amt float64) (r, g, b int32) {
	if amt == 0 {
		return 0, 0, 0
	}
	fr, fg, fb := hueRGB(hue)
	l := 0.299*fr + 0.587*fg + 0.114*fb
	s := amt * 64
	return int32(math.Round((fr - l) * s)),
		int32(math.Round((fg - l) * s)),
		int32(math.Round((fb - l) * s))
}

// hueRGB is the fully saturated RGB of a hue in degrees (HSV with S=V=1).
func hueRGB(hue float64) (r, g, b float64) {
	h := math.Mod(hue, 360) / 60
	c := 1.0
	x := 1 - math.Abs(math.Mod(h, 2)-1)
	switch {
	case h < 1:
		return c, x, 0
	case h < 2:
		return x, c, 0
	case h < 3:
		return 0, c, x
	case h < 4:
		return 0, x, c
	case h < 5:
		return x, 0, c
	default:
		return c, 0, x
	}
}

func clamp8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// buildLookLUT combines the calibrated lift (x^gamma) with the S-curve —
// base strength 0.1, steepened or flattened by the edit's Contrast — then
// the edit's tone-region offsets: ToneShadows/ToneHighlights are weighted
// bumps peaking at 1/3 and 2/3, Blacks/Whites move the endpoints. The curve
// is forced monotone so extreme slider combinations can't invert tones.
func buildLookLUT(gamma float64, e *edit.Params) [256]uint8 {
	var lut [256]uint8
	s := 0.1
	var wh, bk, ts, th float64
	if e != nil {
		s += 0.5 * e.Contrast
		wh = 0.25 * e.Whites
		bk = 0.25 * e.Blacks
		ts = 0.3 * e.ToneShadows
		th = 0.3 * e.ToneHighlights
	}
	prev := 0
	for i := range lut {
		x := math.Pow(float64(i)/255, gamma)
		y := x + s*x*(1-x)*(2*x-1)*2
		y += ts * 6.75 * y * (1 - y) * (1 - y)
		y += th * 6.75 * y * y * (1 - y)
		y += bk * (1 - y) * (1 - y) * (1 - y)
		y += wh * y * y * y
		v := int(y*255 + 0.5)
		v = max(prev, min(255, max(0, v)))
		lut[i] = uint8(v)
		prev = v
	}
	return lut
}
