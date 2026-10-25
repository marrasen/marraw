package main

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// A preset can carry two things beyond a plain look, as marraw's can: it
// can adapt, running autos again on each photo and laying the edit's
// difference from the auto over them; and it can carry the photo's smart
// masks as recipes, the AI masks and the range masks, which mean
// something on any photo, unlike a painted one.

// autoFields are the quantities each auto section writes, as marraw's
// AUTO_FIELDS has them: tone's and colour's in the backend's
// pyramid/auto.go, white balance's in its AutoAdjust. Only these have a
// photo's own auto for an adaptive preset to adapt to. White balance's
// mode and multipliers land whole, as drawn.
var autoFields = map[string][]string{
	"tone":  {"expEV", "contrast", "whites", "blacks", "toneShadows", "toneHighlights"},
	"color": {"saturation", "vibrance"},
	"wb":    {"wbTemp", "wbTint"},
}

// adaptiveLookDiff is the look of an adaptive preset, as marraw's
// adaptiveLookDiff makes it: for the quantities the auto sections write,
// the edit draft's difference from auto, the photo's own auto, laid on
// their neutral. The rest stay draft's: the choices, hues and curves land
// whole, and the other quantities have no auto to differ from, so they
// land on each photo as in any relative preset.
func adaptiveLookDiff(draft, auto marrawclient.Params, sections []string) marrawclient.Params {
	out := stripToLook(draft)
	var neutral marrawclient.Params
	for _, s := range sections {
		for _, key := range autoFields[s] {
			sp, ok := devSpecs[key]
			if !ok {
				continue
			}
			delta := sp.get(&draft) - sp.get(&auto)
			if delta == 0 {
				// Untouched reads as neutral, so a relative apply skips it.
				sp.set(&out, sp.get(&neutral))
				continue
			}
			v := math.Round((sp.get(&neutral)+delta)*1000) / 1000
			if v == 0 {
				// A difference must never round to nought, which reads as
				// untouched where nought is the field's own spelling of it.
				v = math.Copysign(0.001, delta)
			}
			sp.set(&out, v)
		}
	}
	return out
}

// maskRecipes are the smart masks of p as a preset carries them: the AI
// masks, their maps let go so each photo makes its own, and the range
// masks. Painted masks are tied to one photo, and hidden ones are not
// part of the look.
func maskRecipes(p marrawclient.Params) []marrawclient.Mask {
	var out []marrawclient.Mask
	for _, m := range p.Masks {
		switch {
		case m.Disabled:
		case m.Type == "ai" && m.AIKind != "":
			m.MapVer = ""
			out = append(out, m)
		case m.Type == "range":
			out = append(out, m)
		}
	}
	return out
}

// isMasksOnlyPreset reports whether p carries its smart masks and nothing
// else: relative, on neutral, as the save dialog makes one with no
// groups.
func isMasksOnlyPreset(p marrawclient.UserPreset) bool {
	if !p.Relative || len(p.Params.Masks) == 0 {
		return false
	}
	look := stripToLook(p.Params)
	if look.WBMode == marrawclient.WBModeCamera {
		look.WBMode = ""
	}
	for _, c := range []*[]marrawclient.CurvePoint{&look.ToneCurve, &look.ToneCurveR, &look.ToneCurveG, &look.ToneCurveB} {
		if len(*c) == 0 {
			*c = nil
		}
	}
	return reflect.DeepEqual(look, marrawclient.Params{})
}

// aiMapKind is the map an AI mask of kind reads: a background reads the
// subject's.
func aiMapKind(kind marrawclient.AIKind) marrawclient.AIKind {
	if kind == marrawclient.AIKindBackground {
		return marrawclient.AIKindSubject
	}
	return kind
}

// presetMasks adds user preset p's smart masks to photo id's edit, as a
// step of their own after its look, as marraw's esApplyPresetMasks does:
// each AI mask's map is made first. A model not downloaded yet is asked
// for, and download says the user said yes.
func (cu *culler) presetMasks(id int64, p marrawclient.UserPreset, download bool) {
	var ai, ranges []marrawclient.Mask
	for _, m := range p.Params.Masks {
		switch {
		case m.Type == "ai" && m.AIKind != "":
			ai = append(ai, m)
		case m.Type == "range":
			ranges = append(ranges, m)
		}
	}
	if len(ai)+len(ranges) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 10*time.Minute)
		defer cancel()
		vers := map[marrawclient.AIKind]string{}
		for _, m := range ai {
			k := aiMapKind(m.AIKind)
			if _, ok := vers[k]; ok {
				continue
			}
			if !download {
				if st, err := cu.api.Edits.AIModelStatus(ctx, k); err == nil && st != nil && !st.Downloaded {
					cu.askModel(fmt.Sprintf("presetMasks:%d:%s", id, p.ID), aiModelWhat[string(k)], st.Bytes)
					return
				}
			}
			res, err := cu.api.Edits.GenerateAIMap(ctx, id, k, download)
			if err != nil || res == nil {
				cu.onDo(func() { cu.fail("The preset's AI masks could not be made", orNoAnswer(err)) })
				return
			}
			vers[k] = res.MapVer
		}
		cu.onDo(func() {
			d := &cu.dev
			if !d.open || d.id != id || cu.batchShown {
				return
			}
			ms := slices.Clone(d.params.Masks)
			for _, m := range ai {
				m.MapVer = vers[aiMapKind(m.AIKind)]
				ms = append(ms, m)
			}
			ms = append(ms, ranges...)
			// The masks are added to the photo's own: a second apply adds
			// them again, there to see and delete. The preset's Amount
			// keeps the masks, so it stays.
			keep := cu.presetAmt
			d.params.Masks = ms
			cu.edited(true)
			cu.remember(p.Name + " · masks")
			cu.presetAmt = keep
			if d.mounted {
				_ = cu.c.Update("develop", cu.developState())
			}
		})
	}()
}

// presetMasksDownload adds the smart masks of a user preset to the photo
// they were asked for, its model downloaded with the user's leave: what
// says "<photo>:<preset>". A photo stepped away from meanwhile takes none.
func (cu *culler) presetMasksDownload(what string) {
	d := &cu.dev
	photo, id, _ := strings.Cut(what, ":")
	if cu.ui == nil || !d.open || photo != strconv.FormatInt(d.id, 10) {
		return
	}
	i := slices.IndexFunc(cu.ui.UserPresets, func(p marrawclient.UserPreset) bool { return p.ID == id })
	if i < 0 {
		return
	}
	cu.presetMasks(d.id, cu.ui.UserPresets[i], true)
}
