package main

import (
	"encoding/json"
	"encoding/json/jsontext"
	"log"

	"github.com/marrasen/aprot/client"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// followFolder subscribes to the folder's photos, and hands the patches
// and the listings to the culler's goroutine, in order. It returns the
// way to stop.
func (cu *culler) followFolder() func() {
	// The patches arrive on the client's read goroutine, which must not
	// wait: a queue carries them on, a listing's place kept by the same.
	queue := make(chan func(), 1024)
	hand := func(fn func()) {
		select {
		case queue <- fn:
		default:
			log.Print("marraw: photo patches fell behind; one dropped")
		}
	}
	sub := cu.api.Library.SubscribeListPhotos(cu.ctx, cu.folder,
		client.WithPatch(func(cur []marrawclient.Photo, raw jsontext.Value) ([]marrawclient.Photo, error) {
			var ev marrawclient.PhotoPatchEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				log.Printf("marraw: a photo patch: %v", err)
				return cur, nil
			}
			hand(func() { cu.patched(ev.Patches) })
			// The listing the subscription hands on after a patch is the
			// last full one: it takes the patch too, or it would undo it.
			return patchedList(cur, ev.Patches), nil
		}))
	go func() {
		for ps := range sub.C {
			hand(func() { cu.listed(ps) })
		}
	}()
	go func() {
		for {
			select {
			case fn := <-queue:
				select {
				case cu.do <- fn:
				case <-cu.ctx.Done():
					return
				}
			case <-cu.ctx.Done():
				return
			}
		}
	}()
	return sub.Close
}

// listed takes a new listing of the folder: what the backend has read and
// measured of each photo since, as when it was taken, its size and the
// exposure it rests at, and its culling aids: its sharpness, its burst,
// its eyes. A change of those orders and groups the photos anew.
func (cu *culler) listed(ps []marrawclient.Photo) {
	aided := false
	for _, f := range ps {
		for _, p := range []*marrawclient.Photo{cu.listedPhoto(f.ID, true), cu.listedPhoto(f.ID, false)} {
			if p == nil {
				continue
			}
			if !sameAids(*p, f) || p.TakenAt != f.TakenAt {
				aided = true
			}
			// The listing is the backend's word on all but the marks and
			// the edit, which change here first and come as patches.
			n := f
			n.Rating, n.Flag, n.EditHash = p.Rating, p.Flag, p.EditHash
			*p = n
		}
	}
	if aided {
		cu.aidsChanged()
	}
}

// listedPhoto is photo id as it shows, or in the folder's whole list.
func (cu *culler) listedPhoto(id int64, showing bool) *marrawclient.Photo {
	if showing {
		if i, ok := cu.index[id]; ok {
			return &cu.photos[i]
		}
		return nil
	}
	if i, ok := cu.allIndex[id]; ok {
		return &cu.all[i]
	}
	return nil
}

// sameAids reports whether a and b say the same of their aids.
func sameAids(a, b marrawclient.Photo) bool {
	eq := func(x, y *float64) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	eqi := func(x, y *int64) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	return eq(a.Sharpness, b.Sharpness) && eq(a.SubjectSharpness, b.SubjectSharpness) && eqi(a.GroupID, b.GroupID) &&
		eq(a.EyesClosed, b.EyesClosed) && a.EyesAnalyzed == b.EyesAnalyzed && a.SubjectAnalyzed == b.SubjectAnalyzed
}

// patchedList is list with patches ps applied, a copy.
func patchedList(list []marrawclient.Photo, ps []marrawclient.PhotoPatch) []marrawclient.Photo {
	at := make(map[int64]int, len(list))
	for i, p := range list {
		at[p.ID] = i
	}
	out := append([]marrawclient.Photo(nil), list...)
	for _, pp := range ps {
		i, ok := at[pp.ID]
		if !ok {
			continue
		}
		p := &out[i]
		if pp.Rating != nil {
			p.Rating = *pp.Rating
		}
		if pp.Flag != nil {
			p.Flag = *pp.Flag
		}
		if pp.EditHash != nil {
			p.EditHash = *pp.EditHash
		}
		takeShape(p, pp)
		takeAids(p, pp)
	}
	return out
}

// takeShape copies the turn and the crop's size patch pp carries into p:
// its full resolution's size, which its tiles are laid out by.
func takeShape(p *marrawclient.Photo, pp marrawclient.PhotoPatch) {
	if pp.Rotate != nil {
		p.Rotate = *pp.Rotate
	}
	if pp.CropW != nil {
		p.CropW = *pp.CropW
	}
	if pp.CropH != nil {
		p.CropH = *pp.CropH
	}
}
