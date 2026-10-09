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
