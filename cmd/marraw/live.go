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
			return cur, nil
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

// listed takes a new listing of the folder: what the backend has measured
// of each photo since, its size and the exposure it rests at.
func (cu *culler) listed(ps []marrawclient.Photo) {
	for _, f := range ps {
		i, ok := cu.index[f.ID]
		if !ok {
			continue
		}
		p := &cu.photos[i]
		p.BaseExpEV, p.MetaLoaded = f.BaseExpEV, f.MetaLoaded
		p.Width, p.Height, p.Orientation = f.Width, f.Height, f.Orientation
	}
}
