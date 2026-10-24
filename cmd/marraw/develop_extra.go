package main

import (
	"fmt"
	"log"
	"math"
	"strings"
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
	// Loaded says the backend answered, and CameraKnown that it knows
	// the camera.
	Loaded, CameraKnown bool
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

// exifLine is p's camera and exposure in a line, as marraw's panel header
// has it: model · ƒ/x · shutter · ISO n · nnmm.
func exifLine(p marrawclient.Photo) string {
	if !p.MetaLoaded {
		return ""
	}
	var parts []string
	if p.Model != "" {
		parts = append(parts, p.Model)
	}
	if p.Aperture > 0 {
		parts = append(parts, strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("ƒ/%.1f", p.Aperture), "0"), "."))
	}
	switch {
	case p.Shutter >= 1:
		parts = append(parts, fmt.Sprintf("%gs", math.Round(p.Shutter*10)/10))
	case p.Shutter > 0:
		parts = append(parts, fmt.Sprintf("1/%ds", int(math.Round(1/p.Shutter))))
	}
	if p.ISO > 0 {
		parts = append(parts, fmt.Sprintf("ISO %.0f", p.ISO))
	}
	if p.FocalLen > 0 {
		parts = append(parts, fmt.Sprintf("%.0fmm", p.FocalLen))
	}
	return strings.Join(parts, " · ")
}

// resetLoad takes a reset of photo id, own save seq, into the panel once
// it has the edit as the backend gives it now (the measured exposure, say),
// asking again while that fails: the edit counts as saved only then, never
// with the panel still showing the edit before. The first failure is told:
// the panel goes on showing the old edit meanwhile, and an edit made on it
// is the user's (a newer own save) and stands.
func (cu *culler) resetLoad(id int64, seq int, tries int) {
	go func() {
		p, err := cu.api.Edits.GetEditParams(cu.ctx, id)
		select {
		case cu.do <- func() {
			d := &cu.dev
			last, ok := d.last[id]
			if !ok || last.seq != seq || !d.open || d.id != id {
				// Edited again since, or left: the newer save or the
				// photo's own hash says.
				return
			}
			if err != nil {
				if canceled(err) {
					return
				}
				if tries == 0 {
					cu.fail("Reset saved, but the panel could not reload it — retrying", err)
				} else {
					log.Printf("develop: loading photo %d's edit after the reset (try %d): %v", id, tries+1, err)
				}
				wait := saveRetryWaits[min(tries+1, len(saveRetryWaits))-1]
				time.AfterFunc(wait, func() { cu.resetLoad(id, seq, tries+1) })
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
			cu.confirmSaved(id)
			cu.preview(true)
			cu.remember("Reset")
			cu.tell("Reset")
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// devReset resets the edit: of the photo showing as a step of its history,
// with the panel open; otherwise of the photos selected or showing.
func (cu *culler) devReset() {
	d := &cu.dev
	if cu.culling && d.open && d.mounted && d.id == cu.photos[cu.at].ID && len(cu.targets()) == 1 {
		id := d.id
		// Saved as the neutral edit, which clears it as a reset does: an own
		// save like any, so its answer sets the hash the photo is on, and
		// one before it answering late cannot undo it.
		prev, prevErr := d.last[id], d.failed[id]
		seq := cu.recordOwn(id, marrawclient.Params{})
		cu.sendOwn(id, seq, marrawclient.Params{}, func(_ marrawclient.EditSaved, err error) {
			last, onIt := d.last[id]
			if err != nil && !canceled(err) && (!onIt || last.seq == seq) {
				// Told while the reset is the latest save, or the panel has
				// left the photo: the reset did not happen. A newer save
				// of the panel's carries the reset's state, and says.
				cu.fail("Could not reset the edit", err)
			}
			if !onIt || last.seq != seq {
				return // edited again since, or left: the newer save says
			}
			if err != nil {
				// Left as it was: the panel keeps the edit it shows. An
				// earlier save answered well is the photo's already; one
				// failing goes on being sent; one out says when it answers.
				if prev.seq == 0 {
					delete(d.last, id)
					return
				}
				d.last[id] = prev
				if prevErr != nil {
					cu.retrySave(id, max(prev.tries, 1))
				} else {
					cu.confirmSaved(id)
				}
				return
			}
			delete(d.failed, id)
			cu.resetLoad(id, seq, 0)
		})
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
		// Not the panel's own save: the patches tell the view.
		if _, err := cu.api.Edits.ResetEdits(cu.ctx, ids); err != nil {
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

// loadLens fetches the lens profile matched for photo id, for the Lens
// section.
func (cu *culler) loadLens(id int64) {
	go func() {
		info, err := cu.api.Edits.LensProfile(cu.ctx, id)
		select {
		case cu.do <- func() {
			if err != nil || info == nil {
				log.Printf("lens: %v", err)
				return
			}
			if cu.dev.id != id {
				return
			}
			cu.dev.lens = LensInfo{Lens: info.Lens, Profile: info.Profile, Matched: info.Profile != "",
				Distortion: info.HasDistortion, Vignetting: info.HasVignetting, CA: info.HasCA,
				Loaded: true, CameraKnown: info.CameraKnown}
			if cu.dev.mounted {
				_ = cu.c.Update("develop", cu.developState())
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}
