package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// More of the develop panel's vocabulary.
type (
	// DevChoice sets the choice Key to the option at Index, as one of
	// devChoices lists them, and saves it.
	DevChoice struct {
		Key   string
		Index int
	}
	// DevAuto has the backend set the sections named, as marraw's Auto
	// buttons do: "tone", or "wb" and "color".
	DevAuto struct{ Sections []string }
	// EditCopy copies the edit of the photo showing, or the one the
	// grid's keyboard is on, and EditPaste puts it on the photo showing,
	// or on the photos selected in the grid.
	EditCopy  struct{}
	EditPaste struct{}
	// GridNotice is a note to show over the grid for a moment.
	GridNotice struct {
		Text string
		Seq  int
	}
)

// devChoice is a choice in the panel, as segments: its options' labels,
// how to read which is chosen, and how to choose one.
type devChoice struct {
	label   string
	options []string
	get     func(p *marrawclient.Params) int
	set     func(p *marrawclient.Params, i int)
}

// The panel's choices, as marraw's control table has them.
var devChoices = map[string]devChoice{
	"wbMode": {label: "Mode", options: []string{"As shot", "Auto", "Kelvin"},
		get: func(p *marrawclient.Params) int {
			switch p.WBMode {
			case "auto":
				return 1
			case "kelvin":
				return 2
			}
			return 0
		},
		set: func(p *marrawclient.Params, i int) {
			p.WBMul = [4]float64{}
			switch i {
			case 1:
				p.WBMode, p.WBKelvin = "auto", 0
			case 2:
				p.WBMode = "kelvin"
				if p.WBKelvin == 0 {
					p.WBKelvin = 5500
				}
			default:
				// The backend spells As shot as nothing.
				p.WBMode, p.WBKelvin = "", 0
			}
		}},
	"bw": {label: "Treatment", options: []string{"Color", "B&W"},
		get: func(p *marrawclient.Params) int {
			if p.BW {
				return 1
			}
			return 0
		},
		set: func(p *marrawclient.Params, i int) { p.BW = i == 1 }},
	"highlight": {label: "Highlights", options: []string{"Clip", "Unclip", "Blend", "Rebuild"},
		get: func(p *marrawclient.Params) int {
			for i, v := range []int{0, 1, 2, 5} {
				if p.Highlight == v {
					return i
				}
			}
			return 0
		},
		set: func(p *marrawclient.Params, i int) { p.Highlight = []int{0, 1, 2, 5}[i] }},
	"fbddNoiseRd": {label: "FBDD", options: []string{"Off", "Light", "Full"},
		get: func(p *marrawclient.Params) int { return max(0, min(p.FBDDNoiseRd, 2)) },
		set: func(p *marrawclient.Params, i int) { p.FBDDNoiseRd = i }},
	"demosaic": {label: "Demosaic", options: []string{"Auto", "VNG", "PPG", "AHD", "DHT"},
		get: func(p *marrawclient.Params) int {
			for i, v := range []string{"", "vng", "ppg", "ahd", "dht"} {
				if string(p.Demosaic) == v {
					return i
				}
			}
			return 0
		},
		set: func(p *marrawclient.Params, i int) {
			p.Demosaic = marrawclient.Demosaic([]string{"", "vng", "ppg", "ahd", "dht"}[i])
		}},
}

// devChoose takes a choice from the panel, and saves it as a step.
func (cu *culler) devChoose(in DevChoice) {
	d := &cu.dev
	ch, ok := devChoices[in.Key]
	if !ok || !d.open || d.id != cu.photos[cu.at].ID || in.Index < 0 || in.Index >= len(ch.options) {
		return
	}
	ch.set(&d.params, in.Index)
	d.active = in.Key
	// A mode that changes what the panel shows: it follows.
	_ = cu.c.Update("develop", cu.developState())
	cu.edited(true)
	cu.remember(ch.label + " " + ch.options[in.Index])
}

// devAuto has the backend set the sections named, and takes the result as
// a step.
func (cu *culler) devAuto(in DevAuto) {
	d := &cu.dev
	if !d.open || d.id != cu.photos[cu.at].ID {
		return
	}
	id, params := d.id, d.params
	cu.tell("Auto " + strings.Join(in.Sections, " and ") + "…")
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		res, err := cu.api.Edits.AutoAdjust(ctx, id, params, in.Sections)
		select {
		case cu.do <- func() {
			if err != nil || res == nil {
				log.Printf("develop: auto: %v", err)
				cu.tell("Auto did not work")
				return
			}
			if !d.open || d.id != id {
				return
			}
			d.params = *res
			_ = cu.c.Update("develop", cu.developState())
			cu.edited(true)
			label := "Auto tone"
			if len(in.Sections) > 0 && in.Sections[0] != "tone" {
				label = "Auto color"
			}
			cu.remember(label)
			cu.tell(label)
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// editCopy copies an edit: of the photo showing, or the one the grid's
// keyboard is on.
func (cu *culler) editCopy() {
	i := cu.at
	if !cu.culling {
		i = cu.cursor
	}
	if i < 0 || i >= len(cu.photos) {
		return
	}
	id := cu.photos[i].ID
	if d := &cu.dev; d.open && d.id == id {
		p := d.params
		cu.clipboard = &p
		cu.notify("Edit settings copied")
		return
	}
	go func() {
		p, err := cu.api.Edits.GetEditParams(cu.ctx, id)
		select {
		case cu.do <- func() {
			if err != nil {
				log.Printf("copy: %v", err)
				return
			}
			if p == nil {
				p = &marrawclient.Params{}
			}
			cu.clipboard = p
			cu.notify("Edit settings copied")
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// editPaste puts the edit copied on the photo showing, as a step of its
// history while the panel is open on it, or on the photos selected in the
// grid; their new pixels come as their patches do.
func (cu *culler) editPaste() {
	if cu.clipboard == nil {
		cu.notify("Nothing copied")
		return
	}
	params := *cu.clipboard
	if cu.culling {
		id := cu.photos[cu.at].ID
		if d := &cu.dev; d.open && d.id == id {
			d.params = params
			_ = cu.c.Update("develop", cu.developState())
			cu.edited(true)
			cu.remember("Paste")
		} else {
			cu.save(id, params)
		}
		cu.notify("Edit settings pasted")
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
		if err := cu.api.Edits.PasteEditParams(cu.ctx, ids, params); err != nil {
			log.Printf("paste: %v", err)
		}
	}()
	if len(ids) == 1 {
		cu.notify("Edit settings pasted")
	} else {
		cu.notify("Edit settings pasted on " + itoa(int64(len(ids))) + " photos")
	}
}

// notify shows note where the user is: over the photo in the cull view,
// or over the grid.
func (cu *culler) notify(note string) {
	if cu.culling {
		cu.tell(note)
		return
	}
	cu.noticeSeq++
	_ = cu.c.Patch("grid", GridNotice{Text: note, Seq: cu.noticeSeq})
}
