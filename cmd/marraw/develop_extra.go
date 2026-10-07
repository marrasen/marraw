package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// More of the develop panel's vocabulary.
type (
	// DevReset resets the edit of the photo showing, or of the photos
	// selected in the grid.
	DevReset struct{}
	// DevJump goes to step Index of the photo's history.
	DevJump struct{ Index int }
	// DevWBPick turns the white-balance eyedropper on or off, and
	// DevWBAt picks at X and Y, 0 to 1 across the photo as it shows.
	DevWBPick struct{ On bool }
	DevWBAt   struct{ X, Y float64 }
)

// PhotoInfo is what the panel says of the photo: its file, size and when
// and how it was taken.
type PhotoInfo struct {
	File, Folder, Camera, Taken   string
	Width, Height                 int
	Size                          int64
	ISO, Aperture, Shutter, Focal float64
}

// LensInfo is the lens profile matched for the photo, for the Lens section.
type LensInfo struct {
	Lens, Profile              string
	Matched                    bool
	Distortion, Vignetting, CA bool
}

// photoInfo is what the panel says of photo p.
func photoInfo(p marrawclient.Photo, folder string) PhotoInfo {
	in := PhotoInfo{File: p.FileName, Folder: folder, Width: p.Width, Height: p.Height, Size: p.FileSize,
		ISO: p.ISO, Aperture: p.Aperture, Shutter: p.Shutter, Focal: p.FocalLen}
	in.Camera = p.Make
	if p.Model != "" {
		if in.Camera != "" {
			in.Camera += " "
		}
		in.Camera += p.Model
	}
	if p.TakenAt > 0 {
		in.Taken = time.Unix(p.TakenAt, 0).Format("2 Jan 2006, 15:04:05")
	}
	return in
}

// devReset resets the edit: of the photo showing as a step of its history,
// with the panel open; otherwise of the photos selected or showing.
func (cu *culler) devReset() {
	d := &cu.dev
	if cu.culling && d.open && d.mounted && d.id == cu.photos[cu.at].ID {
		id := d.id
		if d.saved == nil {
			d.saved = map[int64]bool{}
		}
		d.saved[id] = true
		go func() {
			err := cu.api.Edits.ResetEdits(cu.ctx, []int64{id})
			var p *marrawclient.Params
			if err == nil {
				p, err = cu.api.Edits.GetEditParams(cu.ctx, id)
			}
			select {
			case cu.do <- func() {
				if err != nil {
					log.Printf("reset: %v", err)
					return
				}
				if d.id != id {
					return
				}
				d.params = marrawclient.Params{}
				if p != nil {
					d.params = *p
				}
				_ = cu.c.Update("develop", cu.developState())
				// Saved by the reset itself: the preview only.
				d.edits++
				d.committed = d.edits
				cu.preview(true)
				cu.remember("Reset")
				cu.tell("Reset")
			}:
			case <-cu.ctx.Done():
			}
		}()
		return
	}
	var ids []int64
	for _, i := range cu.targets() {
		ids = append(ids, cu.photos[i].ID)
	}
	if len(ids) == 0 {
		return
	}
	go func() {
		if err := cu.api.Edits.ResetEdits(cu.ctx, ids); err != nil {
			log.Printf("reset: %v", err)
		}
	}()
	cu.notify(fmt.Sprintf("Reset %d %s", len(ids), map[bool]string{false: "photos", true: "photo"}[len(ids) == 1]))
}

// devJump goes to step i of the photo's history, as a click in the
// history list does.
func (cu *culler) devJump(i int) {
	d := &cu.dev
	h := d.history[d.id]
	if !d.open || h == nil || i < 0 || i >= len(h.steps) || i == h.index {
		return
	}
	h.index = i
	d.params = h.steps[i].params
	_ = cu.c.Update("develop", cu.developState())
	cu.edited(true)
	cu.tell("Back to: " + h.steps[i].label)
}

// historyOfShowing is the history's labels and the step showing, for the
// panel's list.
func (cu *culler) historyOfShowing() ([]string, int) {
	h := cu.dev.history[cu.dev.id]
	if h == nil {
		return nil, 0
	}
	out := make([]string, len(h.steps))
	for i, s := range h.steps {
		out[i] = s.label
	}
	return out, h.index
}

// devWBPick turns the eyedropper on, pinning the edit it samples, or off.
func (cu *culler) devWBPick(on bool) {
	d := &cu.dev
	if on && (!d.open || !cu.culling) {
		return
	}
	if on == cu.wbPicking {
		return
	}
	cu.wbPicking = on
	if on {
		// Sampled as the edit was when the eyedropper came, for every pick.
		cu.wbBase = d.params
		cu.tell("Click something neutral grey or white")
	}
	cu.showCull()
}

// devWBAt picks the white balance that makes the photo neutral at x, y.
func (cu *culler) devWBAt(x, y float64) {
	d := &cu.dev
	if !cu.wbPicking || !d.open || d.id != cu.photos[cu.at].ID {
		return
	}
	id, params, base := d.id, d.params, cu.wbBase
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		res, err := cu.api.Edits.PickWhiteBalance(ctx, id, params, base, x, y)
		select {
		case cu.do <- func() {
			if err != nil || res == nil {
				log.Printf("white balance pick: %v", err)
				cu.tell("That spot could not be picked")
				return
			}
			if d.id != id {
				return
			}
			d.params = *res
			_ = cu.c.Update("develop", cu.developState())
			cu.edited(true)
			cu.remember("White balance pick")
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// loadLens fetches the lens profile matched for photo id, for the Lens
// section.
func (cu *culler) loadLens(id int64) {
	go func() {
		info, err := cu.api.Edits.LensProfile(cu.ctx, id)
		select {
		case cu.do <- func() {
			if err != nil || info == nil || cu.dev.id != id {
				return
			}
			cu.dev.lens = LensInfo{Lens: info.Lens, Profile: info.Profile, Matched: info.Profile != "",
				Distortion: info.HasDistortion, Vignetting: info.HasVignetting, CA: info.HasCA}
			if cu.dev.mounted {
				_ = cu.c.Update("develop", cu.developState())
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}
