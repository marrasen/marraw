package main

import (
	"fmt"
	"log"
	"reflect"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// DevUndo steps the photo's edit back a step, or on with Redo.
type DevUndo struct{ Redo bool }

// historyKeep is how many steps a photo's history keeps, as marraw's does.
const historyKeep = 50

// editHistory is a photo's edits, step by step, from the edit as the
// panel first loaded it; index is the step showing.
type editHistory struct {
	steps []editStep
	index int
}

// editStep is an edit, and what made it, for the note an undo shows.
type editStep struct {
	params marrawclient.Params
	label  string
}

// historyOf is photo id's history, begun with params as its original.
func (cu *culler) historyOf(id int64, params marrawclient.Params) *editHistory {
	d := &cu.dev
	if d.history == nil {
		d.history = map[int64]*editHistory{}
	}
	h := d.history[id]
	if h == nil {
		h = &editHistory{steps: []editStep{{params: params, label: "Original"}}}
		d.history[id] = h
	}
	return h
}

// remember records the edit just saved as a step of the photo's history,
// made by what label says; a step that changed nothing records nothing,
// and the steps that an undo had left go.
func (cu *culler) remember(label string) {
	d := &cu.dev
	h := cu.historyOf(d.id, d.params)
	if reflect.DeepEqual(h.steps[h.index].params, d.params) {
		return
	}
	h.steps = append(h.steps[:h.index+1], editStep{params: d.params, label: label})
	if len(h.steps) > historyKeep {
		h.steps = h.steps[len(h.steps)-historyKeep:]
	}
	h.index = len(h.steps) - 1
	// The panel's history list shows the step at once.
	if d.mounted {
		_ = cu.c.Update("develop", cu.developState())
	}
}

// undo steps the edit of the photo showing back, or on, saves it, and
// shows it: the sliders glide to it, the photo renders it, and a note
// says what was undone.
func (cu *culler) undoEdit(redo bool) {
	d := &cu.dev
	if !d.open || !d.mounted || !cu.culling || d.id != cu.photos[cu.at].ID {
		return
	}
	h := d.history[d.id]
	if h == nil {
		return
	}
	to, verb, step := h.index-1, "Undid", h.index
	if redo {
		to, verb, step = h.index+1, "Redid", h.index+1
	}
	if to < 0 || to >= len(h.steps) {
		what := "Nothing to undo"
		if redo {
			what = "Nothing to redo"
		}
		cu.tell(what)
		return
	}
	h.index = to
	d.params = h.steps[to].params
	_ = cu.c.Update("develop", cu.developState())
	cu.edited(true)
	cu.tell(fmt.Sprintf("%s %s", verb, h.steps[step].label))
}

// tell shows note over the photo for a moment.
func (cu *culler) tell(note string) {
	cu.notice, cu.noticeSeq = note, cu.noticeSeq+1
	cu.showCull()
}

// saveJob is an edit to save.
type saveJob struct {
	id     int64
	params marrawclient.Params
}

// save saves photo id's edit, after any saves before it: one at a time and
// in order, so a quick undo and redo never land in the other order.
func (cu *culler) save(id int64, params marrawclient.Params) {
	d := &cu.dev
	if d.saves == nil {
		d.saves = make(chan saveJob, 64)
		go func() {
			for {
				select {
				case j := <-d.saves:
					if err := cu.api.Edits.SetEditParams(cu.ctx, j.id, j.params); err != nil {
						log.Printf("develop: save: %v", err)
					}
				case <-cu.ctx.Done():
					return
				}
			}
		}()
	}
	select {
	case d.saves <- saveJob{id, params}:
	case <-cu.ctx.Done():
	}
}

// stepLabel is how an adjustment's change reads in the history: its name
// and its value as the panel shows it.
func stepLabel(key string, v float64) string {
	sp, ok := devSpecs[key]
	if !ok {
		return key
	}
	return sp.label + " " + sp.format(float32(v))
}
