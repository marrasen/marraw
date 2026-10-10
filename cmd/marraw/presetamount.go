package main

import (
	"encoding/json"
	"math"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// PresetAmount scales the preset applied last, from nought, the edit as
// it was before it, through 1, as applied, to 2, doubled; Commit says the
// slider was let go.
type PresetAmount struct {
	Value  float64
	Commit bool
}

// presetAmount is the preset applied last, for its Amount: the photo, the
// edit before it and as applied, its name, and the amount showing. Any
// other edit lets it go.
type presetAmount struct {
	id           int64
	base, result marrawclient.Params
	name         string
	amount       float64
}

// AmountView is what the panel shows of the Amount: the preset's name
// and the amount, 1 as applied.
type AmountView struct {
	Name   string
	Amount float64
}

// presetLerpKeys are the look's quantities, which the Amount scales
// between the edit before the preset and after, over their values as
// read, as marraw's lerpPresetAmount does.
var presetLerpKeys = []string{"expEV", "expPreserve", "bright", "gamma", "shadow", "contrast", "whites", "blacks",
	"toneShadows", "toneHighlights", "clarity", "texture", "dehaze", "wbTemp", "wbTint", "saturation", "vibrance",
	"splitShadowAmt", "splitHighlightAmt", "vignette", "sharpen", "nrThreshold", "medPasses", "caRed", "caBlue",
	"lensDistortion", "lensVignetting", "lensCA"}

// presetSnapKeys are the look's choices and positions, by their wire
// names: a half of either means nothing, so they take the preset's at
// half the Amount or more, and the edit's before it otherwise.
var presetSnapKeys = []string{"wbMode", "wbMul", "wbKelvin", "bw", "toneCurve", "toneCurveR", "toneCurveG", "toneCurveB",
	"splitShadowHue", "splitHighlightHue", "highlight", "fbddNoiseRd", "demosaic", "lensMode"}

// lerpPresetAmount is the look t of the way from base to result: the
// quantities scaled, the mixer's bands too, and the choices taken whole.
// The geometry, masks and spots are result's.
func lerpPresetAmount(base, result marrawclient.Params, t float64) marrawclient.Params {
	out := result
	for _, key := range presetLerpKeys {
		sp, ok := devSpecs[key]
		if !ok {
			continue
		}
		b, r := sp.get(&base), sp.get(&result)
		if b == r {
			sp.set(&out, b)
			continue
		}
		sp.set(&out, clampRound(b+t*(r-b), float64(sp.min), float64(sp.max)))
	}
	for i := range out.HSLHue {
		out.HSLHue[i] = clampRound(base.HSLHue[i]+t*(result.HSLHue[i]-base.HSLHue[i]), -1, 1)
		out.HSLSat[i] = clampRound(base.HSLSat[i]+t*(result.HSLSat[i]-base.HSLSat[i]), -1, 1)
		out.HSLLum[i] = clampRound(base.HSLLum[i]+t*(result.HSLLum[i]-base.HSLLum[i]), -1, 1)
	}
	if t < 0.5 {
		out = withFields(out, base, presetSnapKeys)
	}
	return out
}

// withFields is p with the fields named, by their wire names, taken from
// from: present or absent as they are there.
func withFields(p, from marrawclient.Params, keys []string) marrawclient.Params {
	var pm, fm map[string]json.RawMessage
	pb, _ := json.Marshal(p)
	fb, _ := json.Marshal(from)
	if json.Unmarshal(pb, &pm) != nil || json.Unmarshal(fb, &fm) != nil {
		return p
	}
	for _, k := range keys {
		if v, ok := fm[k]; ok {
			pm[k] = v
		} else {
			delete(pm, k)
		}
	}
	b, err := json.Marshal(pm)
	if err != nil {
		return p
	}
	var out marrawclient.Params
	if json.Unmarshal(b, &out) != nil {
		return p
	}
	return out
}

// setPresetAmount scales the preset applied last, the edit's masks and
// spots kept as they are; let go, it is kept, in place of the preset's
// own step in the history.
func (cu *culler) setPresetAmount(in PresetAmount) {
	d := &cu.dev
	a := cu.presetAmt
	if a == nil || !d.open || a.id != d.id {
		return
	}
	t := min(2, max(0, in.Value))
	p := lerpPresetAmount(a.base, a.result, t)
	p.Masks, p.Spots = d.params.Masks, d.params.Spots
	d.params, a.amount = p, t
	cu.amountScrub = true
	cu.edited(in.Commit)
	cu.amountScrub = false
	if in.Commit {
		label := a.name
		if math.Abs(t-1) > 1e-9 {
			label = a.name + " · " + itoa(int64(math.Round(t*100))) + "%"
		}
		cu.amendStep(label)
	}
	if d.mounted {
		_ = cu.c.Update("develop", cu.developState())
	}
}

// amendStep makes the step showing in the history the edit as it is now,
// labelled label, as the preset's Amount changes the preset's own step.
func (cu *culler) amendStep(label string) {
	d := &cu.dev
	h := cu.historyOf(d.id, d.params)
	if h.index <= 0 {
		cu.remember(label)
		return
	}
	h.steps[h.index] = editStep{params: d.params, label: label}
}

// amountView is the Amount the panel shows, or nil.
func (cu *culler) amountView() *AmountView {
	a := cu.presetAmt
	if a == nil || a.id != cu.dev.id {
		return nil
	}
	return &AmountView{Name: a.name, Amount: a.amount}
}
