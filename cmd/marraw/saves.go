package main

import (
	"log"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The panel's own saves, and telling them from edits made elsewhere.
//
// Own saves are numbered. A save's answer carries the edit hash the backend
// saved (the one the photo's patch carries too); the answer of the
// highest-numbered own save that has answered well sets the photo's hash,
// in whatever order the answers come, and the edit counts as saved once the
// latest own save is that one.
//
// Patches and answers reach the culler loop in any order: patches through
// the folder's queue, each answer on its own. So a patch is judged against
// the own saves' hashes, not only the photo's: while own saves of the photo
// are out, its patches are held, in order, since one may be theirs; once
// none is out each is judged in turn. A hash an own save answered with in
// the last ownHashTTL is the panel's own, and nothing to do — its answer
// set the hash already; any other hash than the photo's is an edit made
// elsewhere.

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

// ownHashTTL is how long after its answer an own save's hash is still taken
// as the panel's own when its patch comes: long enough for a patch held up
// in a busy folder queue, short enough that an edit made elsewhere back to
// an earlier own state is not taken for one for long.
const ownHashTTL = 10 * time.Second

// heldCap bounds the patches held per photo while own saves are out; the
// oldest go first.
const heldCap = 16

// ownHash is a hash an own save answered with, and when.
type ownHash struct {
	hash string
	at   time.Time
}

// saveLedger counts own saves out per photo, which answer is the newest
// applied, the hashes own saves answered with lately, and the patches held
// while saves are out. It decides; the culler acts.
type saveLedger struct {
	out     map[int64]int
	applied map[int64]int // highest own save seq answered well
	held    map[int64][]string
	own     map[int64][]ownHash
	now     func() time.Time // time.Now unless a test sets it
}

func (l *saveLedger) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// sent counts an own save of id out.
func (l *saveLedger) sent(id int64) {
	if l.out == nil {
		l.out = map[int64]int{}
	}
	l.out[id]++
}

// answered counts own save seq of id in, answering with hash, and reports
// whether its answer is the newest so far, to apply: it answered well, and
// no later own save of the photo has answered well before it. A hash
// answered well is the panel's own for ownHashTTL, applied or not.
func (l *saveLedger) answered(id int64, seq int, hash string, ok bool) (apply bool) {
	if l.out[id]--; l.out[id] <= 0 {
		delete(l.out, id)
	}
	if !ok {
		return false
	}
	if l.own == nil {
		l.own = map[int64][]ownHash{}
	}
	l.own[id] = append(l.pruned(id), ownHash{hash: hash, at: l.clock()})
	if seq <= l.applied[id] {
		return false
	}
	if l.applied == nil {
		l.applied = map[int64]int{}
	}
	l.applied[id] = seq
	return true
}

// patch takes a patch of id's edit hash, holding it, after any held before
// it, while own saves of the photo are out. It reports whether it was held.
func (l *saveLedger) patch(id int64, hash string) (held bool) {
	if l.out[id] == 0 {
		return false
	}
	if l.held == nil {
		l.held = map[int64][]string{}
	}
	h := append(l.held[id], hash)
	if len(h) > heldCap {
		h = h[len(h)-heldCap:]
	}
	l.held[id] = h
	return true
}

// release hands back the patches of id held, in the order they came, once
// no own save of it is out.
func (l *saveLedger) release(id int64) []string {
	if l.out[id] > 0 {
		return nil
	}
	h := l.held[id]
	delete(l.held, id)
	return h
}

// isOwn reports whether hash is one an own save of id answered with lately.
func (l *saveLedger) isOwn(id int64, hash string) bool {
	for _, o := range l.pruned(id) {
		if o.hash == hash {
			return true
		}
	}
	return false
}

// pruned drops id's own hashes older than ownHashTTL and returns the rest.
func (l *saveLedger) pruned(id int64) []ownHash {
	now := l.clock()
	keep := l.own[id][:0]
	for _, o := range l.own[id] {
		if now.Sub(o.at) < ownHashTTL {
			keep = append(keep, o)
		}
	}
	if len(keep) == 0 {
		delete(l.own, id)
		return nil
	}
	l.own[id] = keep
	return keep
}

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
	seq := cu.recordOwn(id, params)
	cu.sendOwn(id, seq, params, func(hash string, err error) { cu.saveBack(id, seq, err) })
}

// recordOwn makes params photo id's latest own save and returns its number.
// Every own save, a reset's included, is recorded here.
func (cu *culler) recordOwn(id int64, params marrawclient.Params) int {
	d := &cu.dev
	d.saveSeq++
	if d.last == nil {
		d.last = map[int64]ownSave{}
	}
	d.last[id] = ownSave{seq: d.saveSeq, params: params}
	cu.stopRetry(id)
	return d.saveSeq
}

// sendOwn sends own save seq of photo id. Its answer is applied (the
// photo's hash) by the ledger's rule, then done gets it; a patch held while
// it was out is judged once none is.
func (cu *culler) sendOwn(id int64, seq int, params marrawclient.Params, done func(hash string, err error)) {
	d := &cu.dev
	d.ledger.sent(id)
	cu.queueSave(saveJob{id: id, params: params, done: func(hash string, err error) {
		if d.ledger.answered(id, seq, hash, err == nil) {
			cu.setHashOf(id, hash)
		}
		done(hash, err)
		changed := false
		for _, held := range d.ledger.release(id) {
			changed = cu.judgeHash(id, held) || changed
		}
		if changed {
			cu.showCull()
			cu.refilter()
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

// saveBack takes the answer to own save seq of photo id, its hash applied
// already. The latest one answering well counts the edit as saved, so its
// 1:1 tiles can come at once — the user is looking at it, not skimming
// past. An earlier one answering says nothing more. The latest failing is
// told, keeps the tiles held back, and is sent again while the panel stays
// on the photo (retrySave).
func (cu *culler) saveBack(id int64, seq int, err error) {
	d := &cu.dev
	last, ok := d.last[id]
	if !ok || last.seq != seq {
		return
	}
	switch {
	case err == nil:
		delete(d.failed, id)
		cu.confirmSaved(id)
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

// setHashOf sets photo id's edit hash, as an own save left it.
func (cu *culler) setHashOf(id int64, hash string) {
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
}

// confirmSaved counts the panel's edit of photo id as saved, when the panel
// is on it and its latest own save is the newest applied: its 1:1 tiles may
// come.
func (cu *culler) confirmSaved(id int64) {
	d := &cu.dev
	last, ok := d.last[id]
	if !d.open || d.id != id || !ok || d.ledger.applied[id] != last.seq || d.failed[id] != nil {
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
			cu.sendOwn(id, seq, last.params, func(_ string, err error) { cu.saveBack(id, seq, err) })
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
// drop just before leaving does not lose the edit; its answer still sets
// the photo's hash by the ledger's rule.
func (cu *culler) saveForget(id int64) {
	d := &cu.dev
	cu.stopRetry(id)
	if d.failed[id] != nil {
		last := d.last[id]
		cu.sendOwn(id, last.seq, last.params, func(_ string, err error) {
			if err != nil {
				log.Printf("develop: last try at saving photo %d's edit: %v", id, err)
			} else {
				log.Printf("develop: photo %d's edit saved on the last try", id)
			}
		})
	}
	delete(d.failed, id)
	delete(d.last, id)
}

// patchHash takes a patch of photo id's edit hash: held while own saves of
// the photo are out (it may be theirs), judged at once otherwise. It
// reports whether the photo showing changed, for the caller to redraw.
func (cu *culler) patchHash(id int64, hash string) (changed bool) {
	if cu.dev.ledger.patch(id, hash) {
		return false
	}
	return cu.judgeHash(id, hash)
}

// judgeHash judges a patch of photo id's edit hash with no own save of it
// out. A hash an own save answered with lately is the panel's own: its
// answer set the hash already, and a late patch of an earlier save must not
// undo the later one. Any other hash than the photo's is an edit made
// elsewhere. It reports whether the photo showing changed.
func (cu *culler) judgeHash(id int64, hash string) (changed bool) {
	if cu.dev.ledger.isOwn(id, hash) {
		return false
	}
	if i, ok := cu.index[id]; ok {
		if hash == cu.photos[i].EditHash {
			return false
		}
		cu.editedElsewhere(i, hash)
		return true
	}
	if ai, ok := cu.allIndex[id]; ok {
		cu.all[ai].EditHash = hash
	}
	return false
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
