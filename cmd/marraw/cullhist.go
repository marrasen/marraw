package main

import (
	"fmt"
	"log"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// photoMark is a photo's rating and flag.
type photoMark struct {
	rating int
	flag   marrawclient.Flag
}

// cullStep is a rating or a flag given, to undo: the photos, and their
// marks before and after.
type cullStep struct {
	what          string
	ids           []int64
	before, after []photoMark
}

// cullKeep is how many ratings and flags given can be undone.
const cullKeep = 100

// markOf is photo id's rating and flag, showing or not.
func (cu *culler) markOf(id int64) (photoMark, bool) {
	if i, ok := cu.index[id]; ok {
		p := cu.photos[i]
		return photoMark{p.Rating, p.Flag}, true
	}
	if i, ok := cu.allIndex[id]; ok {
		p := cu.all[i]
		return photoMark{p.Rating, p.Flag}, true
	}
	return photoMark{}, false
}

// remark gives photo id the mark m here, showing it where it shows.
func (cu *culler) remark(id int64, m photoMark) {
	if i, ok := cu.index[id]; ok {
		cu.photos[i].Rating, cu.photos[i].Flag = m.rating, m.flag
		cu.marked(i)
	}
	if i, ok := cu.allIndex[id]; ok {
		cu.all[i].Rating, cu.all[i].Flag = m.rating, m.flag
	}
}

// recordCull keeps a rating or flag given as a step to undo; the steps an
// undo had left go.
func (cu *culler) recordCull(s cullStep) {
	cu.cullSteps = append(cu.cullSteps[:cu.cullAt], s)
	if len(cu.cullSteps) > cullKeep {
		cu.cullSteps = cu.cullSteps[len(cu.cullSteps)-cullKeep:]
	}
	cu.cullAt = len(cu.cullSteps)
}

// undoCull takes the last rating or flag given back, or gives it again,
// here and on the backend.
func (cu *culler) undoCull(redo bool) {
	var s cullStep
	switch {
	case redo && cu.cullAt < len(cu.cullSteps):
		s = cu.cullSteps[cu.cullAt]
		cu.cullAt++
	case !redo && cu.cullAt > 0:
		cu.cullAt--
		s = cu.cullSteps[cu.cullAt]
	default:
		cu.notify(map[bool]string{false: "Nothing to undo", true: "Nothing to redo"}[redo])
		return
	}
	marks := s.before
	if redo {
		marks = s.after
	}
	// The backend takes a rating or a flag for many photos at once: one
	// call for each that differs.
	ratings := map[int][]int64{}
	flags := map[marrawclient.Flag][]int64{}
	for k, id := range s.ids {
		cu.remark(id, marks[k])
		ratings[marks[k].rating] = append(ratings[marks[k].rating], id)
		flags[marks[k].flag] = append(flags[marks[k].flag], id)
	}
	go func() {
		for r, ids := range ratings {
			if err := cu.api.Library.SetRating(cu.ctx, ids, r); err != nil {
				log.Printf("undo: %v", err)
			}
		}
		for f, ids := range flags {
			if err := cu.api.Library.SetFlag(cu.ctx, ids, f); err != nil {
				log.Printf("undo: %v", err)
			}
		}
	}()
	verb := "Undid"
	if redo {
		verb = "Redid"
	}
	cu.notify(fmt.Sprintf("%s %s", verb, s.what))
	cu.refilter()
}

// undo steps back or on: in the edit's history while the develop panel is
// open on the photo, and through the ratings and flags given otherwise, as
// marraw does.
func (cu *culler) undo(redo bool) {
	if cu.culling && cu.dev.open && cu.dev.mounted {
		cu.undoEdit(redo)
		return
	}
	cu.undoCull(redo)
}
