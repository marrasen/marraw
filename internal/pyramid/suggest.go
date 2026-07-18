package pyramid

import (
	"math"

	"github.com/marrasen/marraw/internal/edit"
)

// SceneProfile summarizes what the photo depicts, as far as the cached AI
// maps can tell. Category fields are frame fractions from the class map;
// all zero with HasClassMap false when no map is cached, so category-gated
// recipes simply never fire (suggestions degrade, they never trigger an
// inference or download). Computed by the API layer — pyramid stays
// aimask-free.
type SceneProfile struct {
	HasClassMap                                          bool
	Sky, People, Foliage, Water, Mountains, Architecture float64
}

// Candidate is one suggested look: a full edit state based on the caller's
// params with only the tone/color/effects fields replaced.
type Candidate struct {
	ID     string
	Label  string
	Params edit.Params
}

// derived condenses the histograms into the scalar features the recipe
// gates and offsets read. Values describe the scene as measured, before
// any candidate's own exposure move.
type derived struct {
	median        float64 // display-space luma median, 0..255
	spread        float64 // interquartile luma spread, 0..1
	shadowMass    float64 // fraction of near-black samples
	highlightMass float64 // fraction of near-white samples
	chromaMean    float64 // mean chroma, 0..255
	subjectUsable bool    // matte present and covering 3–70% of the frame
}

func deriveStats(stats *SceneStats) derived {
	d := derived{
		median:        histPercentile(&stats.Luma, stats.N, 0.50),
		spread:        (histPercentile(&stats.Luma, stats.N, 0.75) - histPercentile(&stats.Luma, stats.N, 0.25)) / 255,
		shadowMass:    histMassBelow(&stats.Luma, stats.N, 10),
		highlightMass: 1 - histMassBelow(&stats.Luma, stats.N, 245),
	}
	if stats.N > 0 {
		sum := 0
		for c, cnt := range stats.Chroma {
			sum += c * cnt
		}
		d.chromaMean = float64(sum) / float64(stats.N)
		// Same coverage window as autoTone's subject-aware metering.
		frac := float64(stats.SubjTotal) / (255 * float64(stats.N))
		d.subjectUsable = frac >= 0.03 && frac <= 0.7
	}
	return d
}

// recipe is one candidate look: a relevance gate over the scene features and
// an offset layer over the shared auto base. This split is the seam for a
// learned scorer later — a model can replace gate scores (or emit offsets)
// without touching the RPC or the client.
type recipe struct {
	id, label string
	// gate returns a relevance score; <= 0 means "not for this scene".
	// Always-on recipes have no gate.
	gate func(d derived, pr SceneProfile) float64
	// apply layers the recipe's scene-scaled offsets onto the auto base.
	apply func(p *edit.Params, d derived, pr SceneProfile)
}

// suggestGatedMax caps how many scene-gated recipes join the three
// always-on ones, keeping the gallery at 3–5 cards.
const suggestGatedMax = 2

var suggestRecipes = []recipe{
	{
		id: "balanced", label: "Balanced",
		// The auto base as-is: today's Auto tone+colour result.
		apply: func(p *edit.Params, d derived, pr SceneProfile) {},
	},
	{
		id: "punchy", label: "Punchy",
		apply: func(p *edit.Params, d derived, pr SceneProfile) {
			p.Contrast = adj(p.Contrast, 0.2)
			// Vibrance push fades out as the scene's chroma approaches the
			// auto target — an already-colorful frame gets contrast only.
			p.Vibrance = adj(p.Vibrance, 0.2*clampF(1-d.chromaMean/autoChromaTarget, 0, 1))
			p.Blacks = adj(p.Blacks, -0.10)
			p.Clarity = adj(p.Clarity, 0.15)
			p.Dehaze = adj(p.Dehaze, 0.08)
		},
	},
	{
		id: "airy", label: "Bright & airy",
		apply: func(p *edit.Params, d derived, pr SceneProfile) {
			p.ExpEV = clampF(p.ExpEV+0.3, edit.MinExpEV, edit.MaxExpEV)
			p.ToneShadows = adj(p.ToneShadows, 0.2)
			p.Contrast = adj(p.Contrast, -0.15)
			p.ToneHighlights = adj(p.ToneHighlights, -0.1)
			p.Saturation = adj(p.Saturation, -0.08)
			p.Clarity = adj(p.Clarity, -0.05)
		},
	},
	{
		id: "sky", label: "Sky drama",
		gate: func(d derived, pr SceneProfile) float64 {
			if pr.Sky < 0.25 {
				return 0
			}
			return pr.Sky
		},
		apply: func(p *edit.Params, d derived, pr SceneProfile) {
			// Pull highlights harder the more clipped mass the sky holds.
			p.ToneHighlights = adj(p.ToneHighlights, -(0.25 + 0.15*clampF(d.highlightMass/0.05, 0, 1)))
			p.Dehaze = adj(p.Dehaze, 0.15)
			p.Contrast = adj(p.Contrast, 0.12)
			p.Vibrance = adj(p.Vibrance, 0.12)
			p.Whites = adj(p.Whites, -0.15)
		},
	},
	{
		id: "portrait", label: "Portrait pop",
		gate: func(d derived, pr SceneProfile) float64 {
			if pr.People >= 0.05 {
				return math.Min(1, 3*pr.People)
			}
			if d.subjectUsable {
				return 0.3
			}
			return 0
		},
		apply: func(p *edit.Params, d derived, pr SceneProfile) {
			p.ToneShadows = adj(p.ToneShadows, 0.12)
			p.ToneHighlights = adj(p.ToneHighlights, -0.1)
			p.SplitShadowHue, p.SplitShadowAmt = 35, adj(p.SplitShadowAmt, 0.08)
			p.SplitHighlightHue, p.SplitHighlightAmt = 45, adj(p.SplitHighlightAmt, 0.1)
			p.Clarity = adj(p.Clarity, -0.08)
			p.Vignette = adj(p.Vignette, 0.15)
		},
	},
	{
		id: "vivid", label: "Vivid landscape",
		gate: func(d derived, pr SceneProfile) float64 {
			nature := pr.Foliage + pr.Water + pr.Mountains
			if nature < 0.4 || pr.People >= 0.05 {
				return 0
			}
			return math.Min(1, nature)
		},
		apply: func(p *edit.Params, d derived, pr SceneProfile) {
			p.Vibrance = adj(p.Vibrance, 0.2)
			p.Clarity = adj(p.Clarity, 0.12)
			p.Dehaze = adj(p.Dehaze, 0.1)
			p.Contrast = adj(p.Contrast, 0.08)
		},
	},
	{
		id: "lowkey", label: "Low-key",
		gate: func(d derived, pr SceneProfile) float64 {
			// A genuinely dark scene, not just underexposed highlights:
			// dim median with nothing near white. Histogram-only — this
			// recipe works without any AI map.
			if d.median >= 60 || d.highlightMass >= 0.01 || d.median < 1 {
				return 0
			}
			return (60 - d.median) / 60
		},
		apply: func(p *edit.Params, d derived, pr SceneProfile) {
			// Counter part of auto's lift: low-key keeps the scene dark.
			p.ExpEV = clampF(p.ExpEV-0.3, edit.MinExpEV, edit.MaxExpEV)
			p.Blacks = adj(p.Blacks, -0.2)
			p.Contrast = adj(p.Contrast, 0.15)
			p.SplitShadowHue, p.SplitShadowAmt = 220, adj(p.SplitShadowAmt, 0.12)
			p.Vignette = adj(p.Vignette, 0.25)
			p.Vibrance = adj(p.Vibrance, -0.1)
		},
	},
	{
		id: "mono", label: "Black & white",
		gate: func(d derived, pr SceneProfile) float64 {
			if d.chromaMean < 12 {
				// Near-monochrome scene: B&W is the honest rendering.
				return 0.8 * (1 - d.chromaMean/12)
			}
			if d.spread > 0.5 && d.chromaMean < 25 {
				// Wide tonal range with muted color: a classic B&W look.
				return 0.3
			}
			return 0
		},
		apply: func(p *edit.Params, d derived, pr SceneProfile) {
			p.Saturation, p.Vibrance = -1, -1
			p.Contrast = adj(p.Contrast, 0.2)
			p.Clarity = adj(p.Clarity, 0.15)
			p.Blacks = adj(p.Blacks, -0.12)
		},
	},
}

// adj layers a recipe offset onto an auto-base value: ±1 slider domain,
// rounded like every other auto result.
func adj(v, delta float64) float64 { return autoRound(clampF(v+delta, -1, 1)) }

// SuggestLooks returns 3–5 scene-conditioned develop candidates for the
// photo: the three always-on recipes plus the best-scoring scene-gated ones.
// base is the photo's current params; each candidate replaces only the
// tone/color/effects fields the recipes touch — geometry, white balance,
// masks and spots pass through untouched (AutoAdjust's contract; WB stays
// fixed so every candidate renders off the same cached decode).
func SuggestLooks(stats *SceneStats, profile SceneProfile, base edit.Params) []Candidate {
	d := deriveStats(stats)

	// The shared auto base: today's Auto tone+colour, subject-weighted when
	// a matte contributed counts.
	autoBase := base
	autoTone(&stats.Luma, stats.N, &stats.Subj, stats.SubjTotal, &autoBase)
	autoColor(&stats.Chroma, stats.N, &autoBase)

	type scored struct {
		r     recipe
		score float64
	}
	var gated []scored
	out := make([]Candidate, 0, 5)
	for _, r := range suggestRecipes {
		if r.gate == nil {
			p := autoBase
			r.apply(&p, d, profile)
			out = append(out, Candidate{ID: r.id, Label: r.label, Params: p})
			continue
		}
		if s := r.gate(d, profile); s > 0 {
			gated = append(gated, scored{r, s})
		}
	}
	for i := 1; i < len(gated); i++ { // insertion sort, best first (≤5 items)
		for j := i; j > 0 && gated[j].score > gated[j-1].score; j-- {
			gated[j], gated[j-1] = gated[j-1], gated[j]
		}
	}
	for _, g := range gated[:min(len(gated), suggestGatedMax)] {
		p := autoBase
		g.r.apply(&p, d, profile)
		out = append(out, Candidate{ID: g.r.id, Label: g.r.label, Params: p})
	}
	return out
}
