package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// Retouch, as marraw's: spots on the photo healed, cloned from another
// place, or filled by a model. The heal tool places them, a click or a
// drag for a circle, or a brush's stroke; each takes its source from the
// backend, or fills.
type (
	// HealToggle turns the heal tool on or off.
	HealToggle struct{}
	// HealSet sets how new spots are made: Tool "spot" or "brush", Mode
	// "" heal, "clone" or "fill", and the brush's radius and feather.
	HealSet struct {
		Tool, Mode      string
		Radius, Feather float64
	}
	// SpotAdd adds Spot, as placed on the photo.
	SpotAdd struct{ Spot marrawclient.Spot }
	// SpotSet makes Spot the spot at Index, kept as Commit says, as a drag
	// of its handles or its settings give it.
	SpotSet struct {
		Index  int
		Spot   marrawclient.Spot
		Commit bool
		Label  string
	}
	// SpotSelect chooses the spot at Index, below nought none.
	SpotSelect struct{ Index int }
	// SpotDelete deletes the spot at Index.
	SpotDelete struct{ Index int }
	// HealEscape lets the chosen spot go, or the tool.
	HealEscape struct{}
)

// HealView is the heal tool as the panel and the photo show it: whether
// it is on, how new spots are made, the spot chosen, below nought none,
// and the spots whose fills are being made.
type HealView struct {
	On              bool
	Tool, Mode      string
	Radius, Feather float64
	Sel             int
	Busy            map[int]bool
}

// healState is the culler's heal tool.
type healState struct {
	on              bool
	tool, mode      string
	radius, feather float64
	sel             int
	busy            map[int]bool
	// fillAsk is the fill spot waiting for the model's download, or -1.
	fillAsk int
}

// newHealState is the heal tool as it starts, as marraw's.
func newHealState() healState {
	return healState{tool: "spot", radius: 0.02, feather: 0.5, sel: -1, fillAsk: -1}
}

// healView is the heal tool as shown.
func (cu *culler) healView() *HealView {
	h := &cu.heal
	return &HealView{On: h.on, Tool: h.tool, Mode: h.mode, Radius: h.radius, Feather: h.feather, Sel: h.sel, Busy: h.busy}
}

// healChanged shows the heal tool anew, in the panel and over the photo.
func (cu *culler) healChanged() {
	if cu.dev.mounted {
		_ = cu.c.Update("develop", cu.developState())
	}
	cu.showCull()
}

// healToggle turns the heal tool on, on the Local tab, the other tools
// let go, or off.
func (cu *culler) healToggle() {
	d := &cu.dev
	if !d.open || !cu.culling {
		return
	}
	h := &cu.heal
	h.on = !h.on
	if h.on {
		cu.disarmPick()
		cu.masks.sel, cu.masks.active, cu.masks.rangePick, cu.masks.brush.Painting = -1, "", false, false
		cu.showTab(tabLocal)
	} else {
		h.sel = -1
	}
	cu.masksChanged()
	cu.healChanged()
}

// healSet takes how new spots are made.
func (cu *culler) healSet(in HealSet) {
	h := &cu.heal
	if in.Tool != "" {
		h.tool = in.Tool
	}
	h.mode = in.Mode
	if in.Radius > 0 {
		h.radius = min(0.15, max(0.003, in.Radius))
	}
	h.feather = min(1, max(0, in.Feather))
	cu.healChanged()
}

// spotOK reports whether i is a spot of the edit showing.
func (cu *culler) spotOK(i int) bool {
	d := &cu.dev
	return d.open && d.id == cu.photos[cu.at].ID && i >= 0 && i < len(d.params.Spots)
}

// spotAdd adds a spot placed on the photo, and chooses it: a heal's or a
// clone's source comes from the backend, the spot showing its own a
// moment before; a fill's is made.
func (cu *culler) spotAdd(s marrawclient.Spot) {
	d := &cu.dev
	if !d.open || !cu.heal.on {
		return
	}
	s.Mode = marrawclient.SpotMode(cu.heal.mode)
	if s.Feather == 0 {
		s.Feather = 0.5
	}
	d.params.Spots = append(slices.Clone(d.params.Spots), s)
	i := len(d.params.Spots) - 1
	cu.heal.sel = i
	label := map[string]string{"": "Heal spot", "clone": "Clone spot", "fill": "Fill spot"}[cu.heal.mode]
	if s.Mode == "fill" {
		cu.spotKeep(label)
		return
	}
	cu.edited(false)
	cu.healChanged()
	cu.suggestSource(i, label)
}

// suggestSource has the backend choose spot i's source, and keeps the
// spot, labelled label; where it cannot, the spot keeps its own.
func (cu *culler) suggestSource(i int, label string) {
	d := &cu.dev
	id, params, spot := d.id, d.params, d.params.Spots[i]
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		got, err := cu.api.Edits.SuggestHealSource(ctx, id, params, spot)
		select {
		case cu.do <- func() {
			if d.id != id || !cu.spotOK(i) {
				return
			}
			if err == nil && got != nil {
				s := &d.params.Spots[i]
				s.SX, s.SY = got.SX, got.SY
				if s.Kind == "stroke" {
					s.CX, s.CY = got.CX, got.CY
				}
			}
			cu.spotKeep(label)
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// spotKeep saves the spots as they are, a step labelled label, and has
// the fills made.
func (cu *culler) spotKeep(label string) {
	cu.edited(true)
	cu.remember(label)
	cu.healChanged()
	cu.ensureFills(false)
}

// spotSet takes spot i as dragged or set.
func (cu *culler) spotSet(in SpotSet) {
	if !cu.spotOK(in.Index) {
		return
	}
	d := &cu.dev
	ss := slices.Clone(d.params.Spots)
	ss[in.Index] = in.Spot
	d.params.Spots = ss
	if !in.Commit {
		cu.edited(false)
		cu.healChanged()
		return
	}
	label := in.Label
	if label == "" {
		label = "Move spot"
	}
	cu.spotKeep(label)
}

// spotSelect chooses spot i, or none.
func (cu *culler) spotSelect(i int) {
	if i >= len(cu.dev.params.Spots) {
		i = -1
	}
	cu.heal.sel = i
	cu.healChanged()
}

// spotDelete deletes spot i.
func (cu *culler) spotDelete(i int) {
	if !cu.spotOK(i) {
		return
	}
	d := &cu.dev
	d.params.Spots = slices.Delete(slices.Clone(d.params.Spots), i, i+1)
	if h := &cu.heal; h.sel == i {
		h.sel = -1
	} else if h.sel > i {
		h.sel--
	}
	cu.spotKeep("Delete spot")
}

// healEscape lets the chosen spot go, or, with none chosen, the tool; it
// reports whether there was either.
func (cu *culler) healEscape() bool {
	h := &cu.heal
	switch {
	case !h.on:
		return false
	case h.sel >= 0:
		h.sel = -1
		cu.healChanged()
	default:
		cu.healToggle()
	}
	return true
}

// ensureFills has the backend make each fill spot's fill that it has not
// yet, and shows the photo anew with those made. A model not here asks
// to be downloaded first, with download not given.
func (cu *culler) ensureFills(download bool) {
	d := &cu.dev
	id, params := d.id, d.params
	var todo []int
	for i, s := range params.Spots {
		if s.Mode == "fill" && !s.Disabled && !cu.heal.busy[i] {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return
	}
	if cu.heal.busy == nil {
		cu.heal.busy = map[int]bool{}
	}
	for _, i := range todo {
		cu.heal.busy[i] = true
	}
	cu.healChanged()
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 5*time.Minute)
		defer cancel()
		made, missing := false, false
		var failed error
		for _, i := range todo {
			res, err := cu.api.Edits.GenerateFill(ctx, id, params, i, download)
			switch {
			case err != nil && strings.Contains(err.Error(), "model not downloaded"):
				missing = true
			case err != nil:
				failed = err
			case res != nil && res.Generated:
				made = true
			}
			if missing || ctx.Err() != nil {
				break
			}
		}
		select {
		case cu.do <- func() {
			for _, i := range todo {
				delete(cu.heal.busy, i)
			}
			if failed != nil && !canceled(failed) {
				cu.fail("The fill could not be made", failed)
			}
			if missing && d.id == id {
				cu.heal.fillAsk = todo[0]
				cu.askFillModel()
			}
			if made && d.id == id {
				// The photo with the fill made.
				cu.preview(true)
			}
			cu.healChanged()
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// askFillModel asks to download the fill model, its size from the
// backend.
func (cu *culler) askFillModel() {
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		var size int64
		if m, err := cu.api.Edits.FillModelStatus(ctx); err == nil && m != nil {
			size = m.Bytes
		}
		cu.askModel("fillModel", "the fill model", size)
	}()
}

// fillAnswered takes the answer to the fill model's download: fetched,
// the fills are made; declined, the fill spot waiting heals instead.
func (cu *culler) fillAnswered(ok bool) {
	i := cu.heal.fillAsk
	cu.heal.fillAsk = -1
	if ok {
		cu.ensureFills(true)
		return
	}
	if !cu.spotOK(i) {
		return
	}
	cu.dev.params.Spots[i].Mode = ""
	cu.edited(false)
	cu.suggestSource(i, "Heal spot")
}

// spotLabel is how spot s, at i in the list, is named, as marraw names it.
func spotLabel(s marrawclient.Spot, i int) string {
	kind := "Spot"
	if s.Kind == "stroke" {
		kind = "Brush"
	}
	mode := string(s.Mode)
	if mode == "" {
		mode = "heal"
	}
	return fmt.Sprintf("%s %d · %s", kind, i+1, mode)
}
