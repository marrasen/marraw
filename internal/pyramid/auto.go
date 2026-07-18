package pyramid

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/edit"
)

// AutoSection names one parameter group AutoAdjust can compute.
type AutoSection string

const (
	// AutoTone computes ExpEV, Contrast, Whites, Blacks, ToneShadows and
	// ToneHighlights from the luma histogram.
	AutoTone AutoSection = "tone"
	// AutoWB is resolved by the caller (it selects LibRaw's use_auto_wb,
	// which changes the decode itself); AutoAdjust ignores it.
	AutoWB AutoSection = "wb"
	// AutoColor computes Vibrance and Saturation from the chroma histogram.
	AutoColor AutoSection = "color"
)

func AutoSectionValues() []AutoSection { return []AutoSection{AutoTone, AutoWB, AutoColor} }

// Auto-adjust tuning. Targets are display-space (post-baseline-look) values;
// gains map the miss onto the sliders' effect strength in buildLookLUT.
const (
	autoMedianTarget = 107.0 // median luma lands at ~0.42 display
	autoEVLimit      = 1.5   // max exposure move, EV
	autoBlackTarget  = 0.02  // 0.5th-percentile luma target
	autoWhiteTarget  = 0.97  // 99.5th-percentile luma target
	autoSpreadTarget = 0.34  // interquartile luma spread target
	autoChromaTarget = 34.0  // mean chroma target (0..255)
)

// AutoAdjust computes automatic values for the requested sections from the
// decoded (pre-look) pixels and writes them into p, replacing only that
// section's fields. Statistics are gathered in display space through the
// baseline look — the calibrated gamma lift, base S-curve and base
// saturation boost, with the target section's own parameters neutral — so
// the result is absolute for the section but composes with the current
// decode state (white balance, the seeded exposure compensation, crop).
// The wb section is not handled here: it changes the decode, so the caller
// sets WBMode before decoding.
//
// subject, when non-nil, is the photo's AI subject matte (same base
// orientation as img): the exposure decision then meters toward the subject
// like a camera's face-priority mode — a backlit subject gets lifted for
// its own sake, not the sky's. Only used when already generated; auto never
// triggers an inference.
func AutoAdjust(img *image.RGBA, lookGamma float64, p *edit.Params, sections []AutoSection, subject *AIMap) {
	tone, color := false, false
	for _, s := range sections {
		switch s {
		case AutoTone:
			tone = true
		case AutoColor:
			color = true
		}
	}
	if !tone && !color {
		return
	}

	stats := GatherSceneStats(img, lookGamma, subject)
	if tone {
		autoTone(&stats.Luma, stats.N, &stats.Subj, stats.SubjTotal, p)
	}
	if color {
		autoColor(&stats.Chroma, stats.N, p)
	}
}

// SceneStats holds the display-space histograms AutoAdjust and SuggestLooks
// derive their decisions from: luma and chroma over the whole frame, plus
// matte-weighted luma counts when a subject matte was supplied.
type SceneStats struct {
	Luma, Chroma, Subj [256]int
	N, SubjTotal       int
}

// GatherSceneStats measures img through the baseline look (see AutoAdjust for
// the display-space rationale) in one strided pass.
func GatherSceneStats(img *image.RGBA, lookGamma float64, subject *AIMap) *SceneStats {
	lut := buildLookLUT(lookGamma, nil)
	stats := &SceneStats{}
	pix := img.Pix
	w := img.Bounds().Dx()
	var msx, msy float64
	if subject != nil && subject.W > 0 && subject.H > 0 && w > 0 {
		msx = float64(subject.W) / float64(w)
		msy = float64(subject.H) / float64(img.Bounds().Dy())
	}
	// Every 4th pixel is plenty for scene statistics (same stride as MeanLuma).
	for i := 0; i+3 < len(pix); i += 16 {
		r := int32(lut[pix[i]])
		g := int32(lut[pix[i+1]])
		b := int32(lut[pix[i+2]])
		luma := (299*r + 587*g + 114*b) / 1000
		stats.Luma[luma]++
		// The base look boosts saturation ×1.15 around luma, which scales
		// chroma by exactly that factor — apply it so color statistics see
		// what the screen shows.
		chroma := min(255, (max(r, g, b)-min(r, g, b))*115/100)
		stats.Chroma[chroma]++
		stats.N++
		if msx > 0 {
			// Matte-weighted luma counts (nearest sample is plenty here).
			px := (i % img.Stride) / 4
			py := i / img.Stride
			mx := min(subject.W-1, int(float64(px)*msx))
			my := min(subject.H-1, int(float64(py)*msy))
			if m := int(subject.Pix[my*subject.W+mx]); m > 0 {
				stats.Subj[luma] += m
				stats.SubjTotal += m
			}
		}
	}
	return stats
}

// autoTone derives the tone-section values from the display-space luma
// histogram: an exposure delta brings the median to target, then the
// percentile endpoints (rescaled by the exposure move) set Blacks/Whites,
// the interquartile spread sets Contrast, and clipped mass at the extremes
// drives shadow lift / highlight pull.
//
// When a subject matte contributed weighted counts (subjHist/subjTotal), the
// exposure median leans toward the subject — but only when the subject
// covers a meaningful minority of the frame: a sliver is noise, and a
// subject filling the frame IS the global histogram. The endpoint sliders
// (Blacks/Whites/Contrast) stay global — those are scene-wide by nature.
func autoTone(hist *[256]int, n int, subjHist *[256]int, subjTotal int, p *edit.Params) {
	p.Contrast, p.Whites, p.Blacks, p.ToneShadows, p.ToneHighlights = 0, 0, 0, 0, 0
	med := histPercentile(hist, n, 0.50)
	if subjTotal > 0 && n > 0 {
		frac := float64(subjTotal) / (255 * float64(n))
		if frac >= 0.03 && frac <= 0.7 {
			subjMed := histPercentile(subjHist, subjTotal, 0.50)
			med = 0.6*subjMed + 0.4*med
		}
	}
	p001 := histPercentile(hist, n, 0.005) / 255
	p999 := histPercentile(hist, n, 0.995) / 255
	// Empty or near-black scenes get a neutral tone section rather than an
	// exposure rocket derived from noise.
	if n == 0 || med < 1 {
		return
	}

	// Display values are approximately linear^(1/2.222) (see AutoBrightEV),
	// so a display-domain ratio maps to EV via the 2.222 power.
	dEV := 2.222 * math.Log2(autoMedianTarget/med)
	dEV = math.Round(clampF(dEV, -autoEVLimit, autoEVLimit)/0.05) * 0.05
	// The histogram was measured on the decode, which carries BakedExpEV (the
	// residual beyond LibRaw's exp_shift range folds in at render time), so
	// the move lands relative to that — not to the nominal dial value.
	p.ExpEV = clampF(p.BakedExpEV()+dEV, edit.MinExpEV, edit.MaxExpEV)

	// Rescale the percentiles by the exposure move so the remaining sliders
	// judge the histogram as it will look after the EV lands.
	k := math.Pow(2, dEV/2.222)
	if p999 > p001 { // a flat single-tone histogram has nothing to stretch
		lo := math.Min(1, p001*k)
		hi := math.Min(1, p999*k)
		q25 := math.Min(1, histPercentile(hist, n, 0.25)/255*k)
		q75 := math.Min(1, histPercentile(hist, n, 0.75)/255*k)

		// buildLookLUT's endpoint terms move y by ≈0.25×slider at the extremes.
		p.Blacks = autoRound(clampF((autoBlackTarget-lo)/0.25, -1, 0.5))
		p.Whites = autoRound(clampF((autoWhiteTarget-hi)/0.25, -0.6, 0.6))
		p.Contrast = autoRound(clampF(1.8*(autoSpreadTarget-(q75-q25)), -0.4, 0.4))
	}

	// Rescue clipped mass: values below 10 / above 245 after the EV move,
	// i.e. below 10/k (above 245/k) in the measured histogram.
	shadowMass := histMassBelow(hist, n, 10/k)
	highlightMass := 1 - histMassBelow(hist, n, 245/k)
	p.ToneShadows = autoRound(clampF(8*shadowMass, 0, 0.5))
	p.ToneHighlights = autoRound(clampF(-8*highlightMass, -0.5, 0))
}

// autoColor derives Vibrance (and, only to rein in extremes, a negative
// Saturation) from the chroma histogram. Vibrance is the primary control
// because the look stage already weights it toward unsaturated pixels.
func autoColor(hist *[256]int, n int, p *edit.Params) {
	p.Vibrance, p.Saturation = 0, 0
	if n == 0 {
		return
	}
	var sum int
	for c, cnt := range hist {
		sum += c * cnt
	}
	mean := float64(sum) / float64(n)
	if mean < 2 {
		return // effectively a monochrome scene — leave color alone
	}
	miss := (autoChromaTarget - mean) / autoChromaTarget
	if miss > 0 {
		p.Vibrance = autoRound(clampF(0.9*miss, 0, 0.5))
	} else {
		p.Vibrance = autoRound(clampF(0.5*miss, -0.3, 0))
	}
	if p95 := histPercentile(hist, n, 0.95); p95 > 200 {
		p.Saturation = autoRound(clampF((200-p95)/255, -0.25, 0))
	}
}

// histPercentile returns the 0..255 value at cumulative fraction q.
func histPercentile(hist *[256]int, n int, q float64) float64 {
	if n == 0 {
		return 0
	}
	target := int(q * float64(n))
	cum := 0
	for v, cnt := range hist {
		cum += cnt
		if cum > target {
			return float64(v)
		}
	}
	return 255
}

// histMassBelow returns the fraction of samples strictly below value v.
func histMassBelow(hist *[256]int, n int, v float64) float64 {
	if n == 0 {
		return 0
	}
	limit := min(256, max(0, int(math.Ceil(v))))
	cum := 0
	for i := 0; i < limit; i++ {
		cum += hist[i]
	}
	return float64(cum) / float64(n)
}

// autoRound keeps auto results at two decimals so the dials read cleanly.
func autoRound(v float64) float64 { return math.Round(v*100) / 100 }

func clampF(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }
