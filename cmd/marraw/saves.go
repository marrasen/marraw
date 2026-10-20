package main

import (
	"log"
	"slices"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The panel's own saves, and telling them from edits made elsewhere.
//
// A save's answer carries the edit hash the backend saved (the one the
// photo's patch carries too), so the panel never guesses which patch is
// its own: the answer to the latest own save of a photo sets its hash and
// counts the edit as saved. A patch that comes while an own save of the
// photo is still out may be that save's: it is held until every own save
// out has answered, then dropped if its hash is one they came back with,
// and taken as an edit made elsewhere if not.

// saveJob is an edit to save. done, if set, gets the answer on the culler
// loop.
type saveJob struct {
	id     int64
	params marrawclient.Params
	done   func(hash string, err error)
}

// ownSave is a photo's latest own save: the edit counts as saved once it
// answers well.
type ownSave struct {
	seq    int
	params marrawclient.Params
	tries  int // failed sends of it so far
}

// ownHashKeep is how many of a photo's own save hashes are remembered.
const ownHashKeep = 16

// saveRetryWaits are the waits before each new send of a failed own save,
// the last repeated for as long as the panel stays on the photo.
var saveRetryWaits = []time.Duration{time.Second, 5 * time.Second, 15 * time.Second}

// save saves photo id's edit, after any saves before it: one at a time and
// in order, so a quick undo and redo never land in the other order. It is
// not the panel's own: its patch comes back as an edit made elsewhere.
func (cu *culler) save(id int64, params marrawclient.Params) {
	cu.queueSave(saveJob{id: id, params: params})
}

// saveOwn saves the panel's own edit of photo id. The edit counts as saved
// once this save, the latest, answers well (saveBack).
func (cu *culler) saveOwn(id int64, params marrawclient.Params) {
	d := &cu.dev
	d.saveSeq++
	if d.last == nil {
		d.last = map[int64]ownSave{}
	}
	d.last[id] = ownSave{seq: d.saveSeq, params: params}
	cu.stopRetry(id)
	cu.sendOwn(id, d.saveSeq, params)
}

// sendOwn sends own save seq of photo id.
func (cu *culler) sendOwn(id int64, seq int, params marrawclient.Params) {
	cu.queueOwn(id, params, func(hash string, err error) { cu.saveBack(id, seq, hash, err) })
}

// queueOwn queues an own save of photo id, counted out until it answers;
// done then gets the answer.
func (cu *culler) queueOwn(id int64, params marrawclient.Params, done func(hash string, err error)) {
	d := &cu.dev
	if d.out == nil {
		d.out = map[int64]int{}
	}
	d.out[id]++
	cu.queueSave(saveJob{id: id, params: params, done: func(hash string, err error) {
		d.out[id]--
		if err == nil {
			cu.ownHash(id, hash)
		}
		done(hash, err)
		if d.out[id] <= 0 {
			delete(d.out, id)
			cu.judgeHeldPatch(id)
		}
	}})
}

func (cu *culler) queueSave(job saveJob) {
	d := &cu.dev
	if d.saves == nil {
		d.saves = make(chan saveJob, 64)
		go func() {
			for {
				select {
				case j := <-d.saves:
					hash, err := cu.api.Edits.SetEditParams(cu.ctx, j.id, j.params)
					if err != nil && !canceled(err) {
						log.Printf("develop: save: %v", err)
					}
					if j.done != nil {
						// Off this loop: the culler may be waiting to queue
						// the next save.
						go func() {
							select {
							case cu.do <- func() { j.done(hash, err) }:
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

// ownHash remembers hash as one an own save of photo id came back with.
func (cu *culler) ownHash(id int64, hash string) {
	d := &cu.dev
	if d.ownHashes == nil {
		d.ownHashes = map[int64][]string{}
	}
	hs := d.ownHashes[id]
	if slices.Contains(hs, hash) {
		return
	}
	if len(hs) >= ownHashKeep {
		hs = hs[1:]
	}
	d.ownHashes[id] = append(hs, hash)
}

// saveBack takes the answer to own save seq of photo id. The latest one
// answering well sets the photo's edit hash and counts the edit as saved,
// so its 1:1 tiles can come at once — the user is looking at it, not
// skimming past. An earlier one answering says nothing: its edit is
// replaced already. The latest failing is told, keeps the tiles held back,
// and is sent again while the panel stays on the photo (retrySave).
func (cu *culler) saveBack(id int64, seq int, hash string, err error) {
	d := &cu.dev
	last, ok := d.last[id]
	if !ok {
		// The panel has left the photo since: the photo's hash is still the
		// one saved, but nothing waits on it.
		if err == nil {
			cu.savedAs(id, hash)
		}
		return
	}
	if last.seq != seq {
		return
	}
	switch {
	case err == nil:
		delete(d.failed, id)
		cu.savedAs(id, hash)
	case canceled(err):
	default:
		if d.failed == nil {
			d.failed = map[int64]error{}
		}
		if d.failed[id] == nil {
			cu.fail("Could not save the edit", err)
		}
		d.failed[id] = err
		last.tries++
		d.last[id] = last
		cu.retrySave(id, last.tries)
	}
}

// savedAs takes photo id's edit as saved under hash: the photo's hash, and
// the panel's edit counted as saved.
func (cu *culler) savedAs(id int64, hash string) {
	d := &cu.dev
	if i, ok := cu.index[id]; ok {
		if cu.photos[i].EditHash != hash {
			cu.setEditHash(i, hash)
			cu.refreshThumb(cu.photos[i])
		}
	} else if ai, ok := cu.allIndex[id]; ok {
		cu.all[ai].EditHash = hash
	}
	if d.holdID == id {
		// The held edit is this one now: loading the photo again keeps
		// the hold.
		d.holdHash = hash
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

// retrySave sends photo id's latest own save again after the wait for its
// tries-th failure, if it is still the latest, still failing, and the panel
// still on the photo — a short connection drop must not lose the edit the
// panel goes on showing.
func (cu *culler) retrySave(id int64, tries int) {
	d := &cu.dev
	cu.stopRetry(id)
	wait := saveRetryWaits[min(tries, len(saveRetryWaits))-1]
	seq := d.last[id].seq
	if d.retry == nil {
		d.retry = map[int64]*time.Timer{}
	}
	d.retry[id] = time.AfterFunc(wait, func() {
		select {
		case cu.do <- func() {
			last, ok := d.last[id]
			if !ok || last.seq != seq || d.failed[id] == nil || !d.open || d.id != id {
				return
			}
			delete(d.retry, id)
			cu.sendOwn(id, seq, last.params)
		}:
		case <-cu.ctx.Done():
		}
	})
}

func (cu *culler) stopRetry(id int64) {
	if t := cu.dev.retry[id]; t != nil {
		t.Stop()
		delete(cu.dev.retry, id)
	}
}

// saveForget lets photo id go as the panel leaves it or closes. A save of
// it still failing is sent once more, nobody waiting on the answer, so a
// drop just before leaving does not lose the edit; its hashes go, as the
// panel will not tell its patches apart any more.
func (cu *culler) saveForget(id int64) {
	d := &cu.dev
	cu.stopRetry(id)
	if d.failed[id] != nil {
		last := d.last[id]
		cu.queueSave(saveJob{id: id, params: last.params, done: func(_ string, err error) {
			if err != nil {
				log.Printf("develop: last try at saving photo %d's edit: %v", id, err)
			} else {
				log.Printf("develop: photo %d's edit saved on the last try", id)
			}
		}})
	}
	delete(d.failed, id)
	delete(d.last, id)
	if d.out[id] == 0 {
		delete(d.ownHashes, id)
	}
}

// judgePatch tells what a patch of photo id's edit hash is: an own save's
// answer (nothing to do: the answer sets the hash), one to hold until the
// own saves out have answered, or an edit made elsewhere.
func (cu *culler) judgePatch(id int64, hash string) (own, held bool) {
	d := &cu.dev
	if d.out[id] > 0 {
		if d.heldPatch == nil {
			d.heldPatch = map[int64]string{}
		}
		d.heldPatch[id] = hash
		return false, true
	}
	return slices.Contains(d.ownHashes[id], hash), false
}

// judgeHeldPatch judges the patch of photo id held while own saves of it
// were out, now they have all answered.
func (cu *culler) judgeHeldPatch(id int64) {
	d := &cu.dev
	hash, ok := d.heldPatch[id]
	if !ok {
		return
	}
	delete(d.heldPatch, id)
	if slices.Contains(d.ownHashes[id], hash) {
		return
	}
	i, ok := cu.index[id]
	if !ok {
		if ai, ok := cu.allIndex[id]; ok {
			cu.all[ai].EditHash = hash
		}
		return
	}
	if hash != cu.photos[i].EditHash {
		cu.editedElsewhere(i, hash)
		cu.showCull()
		cu.refilter()
	}
}

// editedElsewhere takes the photo showing at i as edited elsewhere, to
// hash: new pixels, and the panel on it takes the new edit. A save of it
// failing is not sent again: it would overwrite the newer edit.
func (cu *culler) editedElsewhere(i int, hash string) {
	p := &cu.photos[i]
	d := &cu.dev
	cu.stopRetry(p.ID)
	delete(d.failed, p.ID)
	delete(d.last, p.ID)
	cu.setEditHash(i, hash)
	cu.tiles.drop(p.ID)
	if d.open && d.id == p.ID {
		cu.loadEdit(i)
	}
	cu.cache.drop(p.ID)
	cu.refreshThumb(*p)
	if cu.culling && i == cu.at {
		// Again, with the new edit's pixels.
		cu.load = nil
		cu.goTo(i)
	}
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
