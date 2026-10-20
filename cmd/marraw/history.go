package main

import (
	"fmt"
	"log"
	"reflect"
	"time"

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

// saveJob is an edit to save; own is the panel's own save (saveOwn).
type saveJob struct {
	id     int64
	params marrawclient.Params
	own    bool
}

// save saves photo id's edit, after any saves before it: one at a time and
// in order, so a quick undo and redo never land in the other order. Its
// patch comes back as an edit made elsewhere.
func (cu *culler) save(id int64, params marrawclient.Params) {
	cu.queueSave(saveJob{id: id, params: params})
}

// saveOwn saves the panel's own edit of photo id: its patch is known for
// the panel's own, and the edit counts as saved once every own save of the
// photo has come back (see saveSettle).
func (cu *culler) saveOwn(id int64, params marrawclient.Params) {
	cu.saveOut(id)
	cu.queueSave(saveJob{id: id, params: params, own: true})
}

func (cu *culler) queueSave(job saveJob) {
	d := &cu.dev
	if d.saves == nil {
		d.saves = make(chan saveJob, 64)
		go func() {
			for {
				select {
				case j := <-d.saves:
					err := cu.api.Edits.SetEditParams(cu.ctx, j.id, j.params)
					if err != nil {
						log.Printf("develop: save: %v", err)
					}
					if j.own {
						// Off this loop: the culler may be waiting to queue
						// the next save.
						go func() {
							select {
							case cu.do <- func() { cu.saveBack(j.id, err) }:
							case <-cu.ctx.Done():
							}
						}()
					}
				case <-cu.ctx.Done():
					return
				}
			}
		}()
	}
	select {
	case d.saves <- job:
	case <-cu.ctx.Done():
	}
}

// saveOut notes an own save of photo id going out: a call to answer and a
// patch to come back.
func (cu *culler) saveOut(id int64) {
	d := &cu.dev
	if d.sending == nil {
		d.sending, d.owed, d.saveRound = map[int64]int{}, map[int64]int{}, map[int64]int{}
		d.failed, d.graceRound = map[int64]error{}, map[int64]int{}
	}
	d.sending[id]++
	d.owed[id]++
	d.saveRound[id]++
}

// saveBack notes an own save's call answered. Answers come in the order
// the saves went, so the last answer says whether the edit is saved. A
// failed save is told and not sent again: its patch is given back, and the
// edit stays unsaved — its 1:1 tiles held back — until a later own save of
// the photo goes through, or the panel leaves it. Should the backend have
// applied it all the same (the connection dropped after), its patch comes
// back as an edit made elsewhere, and the panel takes that state, which is
// the one it sent.
func (cu *culler) saveBack(id int64, err error) {
	d := &cu.dev
	d.sending[id] = max(d.sending[id]-1, 0)
	switch {
	case err == nil:
		delete(d.failed, id)
	case canceled(err):
		d.owed[id] = max(d.owed[id]-1, 0)
	default:
		d.owed[id] = max(d.owed[id]-1, 0)
		d.failed[id] = err
		cu.fail("Could not save the edit", err)
	}
	cu.saveSettle(id)
}

// saveGiveBack takes back an own save of photo id that went out but is not
// one: its call and its patch are no longer awaited.
func (cu *culler) saveGiveBack(id int64) {
	d := &cu.dev
	d.sending[id] = max(d.sending[id]-1, 0)
	d.owed[id] = max(d.owed[id]-1, 0)
	cu.saveSettle(id)
}

// saveForget drops photo id's failed save as the panel leaves the photo:
// the mark only holds that photo's tiles back while it is being edited.
func (cu *culler) saveForget(id int64) {
	if cu.dev.failed != nil {
		delete(cu.dev.failed, id)
	}
}

// ownPatch reports whether a patch of photo id's edit is the answer to an
// own save, and counts it in.
func (cu *culler) ownPatch(id int64) bool {
	d := &cu.dev
	if d.owed[id] <= 0 {
		return false
	}
	d.owed[id]--
	return true
}

// ownPatchGrace is how long saveSettle waits for the patch of an own save
// whose call has answered: a patch lost to a reconnect must not hold the
// tiles back for good.
const ownPatchGrace = 2 * time.Second

// saveSettle counts the panel's edit of photo id as saved once every own
// save of it has been answered, the last one well, and its patch is in:
// only then is the photo's edit hash the one of the edit last committed,
// and its 1:1 tiles can come at once — the user is looking at it, not
// skimming past. Saves still out, a patch of an earlier one must not do
// it: the tiles would be of an edit already replaced. A patch that does not
// come within ownPatchGrace is asked of the backend instead (savedHash).
func (cu *culler) saveSettle(id int64) {
	d := &cu.dev
	if d.sending[id] > 0 || d.failed[id] != nil {
		return
	}
	if d.owed[id] > 0 {
		round := d.saveRound[id]
		if d.graceRound[id] == round {
			return // a grace timer for this round is pending already
		}
		d.graceRound[id] = round
		time.AfterFunc(ownPatchGrace, func() {
			select {
			case cu.do <- func() {
				if d.graceRound[id] == round {
					delete(d.graceRound, id)
				}
				if d.saveRound[id] == round && d.sending[id] == 0 && d.owed[id] > 0 {
					cu.savedHash(id, round)
				}
			}:
			case <-cu.ctx.Done():
			}
		})
		return
	}
	if !d.open || d.id != id {
		return
	}
	d.confirmed = d.committed
	if cu.tileNote == "tiles: once the edit is saved" {
		cu.tileNote = ""
	}
	if cu.culling && cu.photos[cu.at].ID == id && !cu.tileWant.Range.Empty() {
		cu.wantTiles(cu.tileWant, true)
	}
}

// savedHash asks the backend for photo id's edit hash as saved, for a
// round of own saves whose patch never came, and settles on it: the tiles
// must be of the edit the backend has, never of a hash left from before.
// The round's owed patches are cleared whatever comes of it — the photo
// gone from the view, the backend not answering — or a later edit made
// elsewhere would be taken for the panel's own. A newer round meanwhile
// makes it moot.
func (cu *culler) savedHash(id int64, round int) {
	d := &cu.dev
	i, ok := cu.index[id]
	if !ok {
		d.owed[id] = 0
		return
	}
	folder := cu.photos[i].FolderID
	go func() {
		photos, err := cu.api.Library.ListPhotos(cu.ctx, folder)
		select {
		case cu.do <- func() {
			if d.saveRound[id] != round || d.sending[id] > 0 {
				return
			}
			d.owed[id] = 0
			if err != nil {
				// Left unsaved-looking, tiles held back, until the next save.
				log.Printf("develop: asking for photo %d's saved edit: %v", id, err)
				return
			}
			for _, sp := range photos {
				if sp.ID != id {
					continue
				}
				if j, ok := cu.index[id]; ok && cu.photos[j].EditHash != sp.EditHash {
					cu.setEditHash(j, sp.EditHash)
					cu.tiles.drop(id)
					cu.refreshThumb(cu.photos[j])
					if d.holdID == id {
						d.holdHash = sp.EditHash
					}
				}
				cu.saveSettle(id)
				return
			}
			log.Printf("develop: photo %d is gone from its folder's list", id)
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// setEditHash sets the edit hash of the photo showing at i, and of the same
// photo in the folder's whole list, which a re-filter rebuilds the view from.
func (cu *culler) setEditHash(i int, hash string) {
	p := &cu.photos[i]
	p.EditHash = hash
	if ai, ok := cu.allIndex[p.ID]; ok {
		cu.all[ai].EditHash = hash
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
