package main

import (
	"math"
	"slices"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// MaskMove moves the mask at From to To in the list: a mask applies over
// the ones above it.
type MaskMove struct{ From, To int }

// maskControls are mask m's controls in the panel's order, as marraw
// walks them: its shape's first, then its adjustments, the effects'
// direction only while an effect has one.
func maskControls(m marrawclient.Mask) []string {
	var out []string
	switch {
	case m.Type == "ai" && (m.AIKind == "subject" || m.AIKind == "background"):
		out = []string{"threshold", "feather"}
	case m.Type == "ai":
		out = []string{"feather"}
	case m.Type == "range":
		out = []string{"satMin", "feather"}
	}
	for _, sp := range maskTone {
		out = append(out, sp.key)
	}
	for _, sp := range maskFX {
		if sp.key == "fxAngle" && m.Adjust.MotionBlur == 0 && m.Adjust.Streaks == 0 {
			continue
		}
		out = append(out, sp.key)
	}
	return out
}

// maskWalk chooses the mask control by places on, over all the masks'
// controls in order: into the next mask past the last of one, choosing
// it, and no further than the ends. With none chosen it starts at the
// chosen mask's first or last, or the list's.
func (cu *culler) maskWalk(by int) {
	d := &cu.dev
	ms := d.params.Masks
	if len(ms) == 0 || by == 0 {
		return
	}
	type control struct {
		i   int
		key string
	}
	var flat []control
	for i, m := range ms {
		for _, k := range maskControls(m) {
			flat = append(flat, control{i, k})
		}
	}
	m := &cu.masks
	at := slices.IndexFunc(flat, func(c control) bool { return c.i == m.sel && c.key == m.active })
	switch {
	case at >= 0:
		at = max(0, min(at+by, len(flat)-1))
	case m.sel >= 0 && by > 0:
		at = slices.IndexFunc(flat, func(c control) bool { return c.i == m.sel })
	case m.sel >= 0:
		for j, c := range flat {
			if c.i == m.sel {
				at = j
			}
		}
	case by > 0:
		at = 0
	default:
		at = len(flat) - 1
	}
	if at < 0 {
		return
	}
	c := flat[at]
	if c.i != m.sel {
		m.sel, m.rangePick, m.brush.Painting = c.i, false, false
	}
	m.active = c.key
	cu.masksChanged()
}

// maskStep is how far + and - step a mask's control, and Shift with them.
func maskStep(key string) (step, big float64) {
	switch key {
	case "threshold", "feather", "satMin":
		return 0.01, 0.05
	case "expEV":
		return 0.05, 0.25
	case "fxAngle":
		return 1, 15
	}
	return 0.02, 0.1
}

// maskValue is the chosen mask's control key as its slider reads it.
func maskValue(m marrawclient.Mask, key string) (float64, bool) {
	if sp, ok := maskSpecOf(key); ok {
		return *sp.get(&m.Adjust), true
	}
	switch key {
	case "threshold":
		if m.Threshold == 0 {
			return 0.5, true
		}
		return m.Threshold, true
	case "feather":
		return m.Feather, true
	case "satMin":
		return m.RangeSatMin, true
	}
	return 0, false
}

// maskNudge steps the chosen mask's control chosen, the photo showing
// each step at once, the edit saved a moment after the last.
func (cu *culler) maskNudge(in DevNudge) {
	d := &cu.dev
	m := &cu.masks
	if !cu.maskOK(m.sel) || m.active == "" {
		return
	}
	v, ok := maskValue(d.params.Masks[m.sel], m.active)
	if !ok {
		return
	}
	step, big := maskStep(m.active)
	if in.Big {
		step = big
	}
	v = math.Round((v+float64(in.Dir)*step)*1000) / 1000
	i, key := m.sel, m.active
	cu.maskSet(MaskSet{Index: i, Key: key, Value: v})
	if d.nudge != nil {
		d.nudge.Stop()
	}
	d.nudge = time.AfterFunc(nudgeCommit, func() {
		select {
		case cu.do <- func() {
			d.nudge = nil
			if cu.maskOK(i) {
				if v, ok := maskValue(d.params.Masks[i], key); ok {
					cu.maskSet(MaskSet{Index: i, Key: key, Value: v, Commit: true})
				}
			}
		}:
		case <-cu.ctx.Done():
		}
	})
}

// maskMove moves the mask at from to to, the one chosen and the one
// tinted following their masks; one step in the history.
func (cu *culler) maskMove(in MaskMove) {
	d := &cu.dev
	n := len(d.params.Masks)
	if !cu.maskOK(in.From) || in.To < 0 || in.To >= n || in.From == in.To {
		return
	}
	ms := slices.Clone(d.params.Masks)
	m := ms[in.From]
	ms = slices.Delete(ms, in.From, in.From+1)
	ms = slices.Insert(ms, in.To, m)
	d.params.Masks = ms
	remap := func(i int) int {
		if i < 0 {
			return i
		}
		return movedIndex(i, in.From, in.To)
	}
	cu.masks.sel, cu.masks.hover = remap(cu.masks.sel), remap(cu.masks.hover)
	cu.maskEdit(true, "Reorder masks")
}

// movedIndex is where the item at i goes as the item at from moves to
// to, as marraw remaps its mask indices.
func movedIndex(i, from, to int) int {
	if i == from {
		return to
	}
	j := i
	if i > from {
		j = i - 1
	}
	if j >= to {
		return j + 1
	}
	return j
}
