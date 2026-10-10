package main

import (
	"math"
	"time"
)

// The develop panel's keyboard, as marraw's: Up and Down walk the
// controls, + and - step the one chosen, a letter chooses one.
type (
	// DevWalk chooses the control By places on, in the panel's order.
	DevWalk struct{ By int }
	// DevNudge steps the control chosen up or down, by its big step with
	// Big.
	DevNudge struct {
		Dir int
		Big bool
	}
	// DevPick chooses the control Key, or, empty, none.
	DevPick struct{ Key string }
)

// controlKeys are the letters that choose a control, as marraw's keys
// have them; D stays the panel's own key, so demosaic has none here.
var controlKeys = map[rune]string{
	'e': "expEV", 'b': "bright", 't': "wbTemp", 'i': "wbTint", 'k': "wbKelvin", 'g': "gamma",
	's': "shadow", 'c': "contrast", 'a': "saturation", 'v': "vibrance", 'o': "vignette",
	'h': "highlight", 'n': "nrThreshold", 'm': "medPasses",
}

// nudgeCommit is how long after the last + or - the edit is saved, so a
// run of presses saves once and is one step of the history.
const nudgeCommit = 600 * time.Millisecond

// controlOrder is the panel's controls in its order, as the edit shows
// them: one of the two temperatures, as the mode is Kelvin or not.
func controlOrder(kelvin bool) []string {
	var out []string
	for _, sec := range devSections {
		out = append(out, sec.choices...)
		for _, k := range sec.keys {
			if k == "wbKelvin" && !kelvin || k == "wbTemp" && kelvin {
				continue
			}
			out = append(out, k)
		}
	}
	return out
}

// setActive chooses the control key, and shows it chosen.
func (cu *culler) setActive(key string) {
	d := &cu.dev
	if !d.open || !d.mounted {
		return
	}
	// The temperature the mode shows, whichever was asked for.
	if kelvin := d.params.WBMode == "kelvin"; key == "wbTemp" && kelvin || key == "wbKelvin" && !kelvin {
		key = map[bool]string{false: "wbTemp", true: "wbKelvin"}[kelvin]
	}
	if key == d.active {
		return
	}
	d.active = key
	_ = cu.c.Update("develop", cu.developState())
	cu.showCull()
}

// devWalk chooses the control by places on from the one chosen, or the
// first or the last with none chosen.
func (cu *culler) devWalk(by int) {
	if cu.devTab == tabLocal {
		// On the Local tab the keys walk the masks' controls.
		cu.maskWalk(by)
		return
	}
	d := &cu.dev
	order := controlOrder(d.params.WBMode == "kelvin")
	at := -1
	for i, k := range order {
		if k == d.active {
			at = i
		}
	}
	switch {
	case at < 0 && by > 0:
		at = 0
	case at < 0:
		at = len(order) - 1
	default:
		at = max(0, min(at+by, len(order)-1))
	}
	cu.setActive(order[at])
}

// devNudge steps the control chosen: a slider by its step, or its big
// step, a choice to the next option. The photo shows each step at once,
// and the edit is saved a moment after the last.
func (cu *culler) devNudge(in DevNudge) {
	d := &cu.dev
	if cu.devTab == tabLocal {
		cu.maskNudge(in)
		return
	}
	if !d.open || d.active == "" || d.id != cu.photos[cu.at].ID {
		return
	}
	if ch, ok := devChoices[d.active]; ok {
		n := len(ch.options)
		if d.active == "wbMode" {
			// The eyedropper is no mode to step to.
			n = wbPickIndex
		}
		i := (min(ch.get(&d.params), n-1) + in.Dir + n) % n
		cu.devChoose(DevChoice{Key: d.active, Index: i})
		return
	}
	sp, ok := devSpecs[d.active]
	if !ok {
		return
	}
	step := float64(sp.snap)
	if in.Big {
		step = float64(sp.bigStep())
	}
	v := sp.get(&d.params) + float64(in.Dir)*step
	v = math.Round(v*1000) / 1000
	v = max(float64(sp.min), min(v, float64(sp.max)))
	sp.set(&d.params, v)
	_ = cu.c.Update("develop", cu.developState())
	cu.edited(false)
	key := d.active
	if d.nudge != nil {
		d.nudge.Stop()
	}
	d.nudge = time.AfterFunc(nudgeCommit, func() {
		select {
		case cu.do <- func() {
			d.nudge = nil
			if !d.open || d.id != cu.photos[cu.at].ID {
				return
			}
			cu.edited(true)
			cu.remember(stepLabel(key, sp.get(&d.params)))
		}:
		case <-cu.ctx.Done():
		}
	})
}
