package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"slices"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The Scene and People masks: the backend finds the photo's regions, its
// scene's kinds of thing or its people, and they are picked as marraw
// picks them, by a chip of each in the panel or by a click on the photo,
// a mask added each time.
type (
	// MaskPickAt adds a mask of the region ID of the picking armed.
	MaskPickAt struct{ ID int }
	// MaskPickHover tints the region ID over the photo, nought none.
	MaskPickHover struct{ ID int }
)

// PickView is the picking as the cull view has it: the kind, the oriented
// frame's map of its regions, a pixel's value its region's ID, and the
// IDs that can be picked.
type PickView struct {
	Kind  string
	Plane *image.Gray
	IDs   map[int]bool
}

// PickChip is a region the panel offers: its ID, how it is named, and
// whether a mask of it is there already.
type PickChip struct {
	ID    int
	Label string
	Has   bool
}

// maskPick is the picking: the kind found last, "class" or "person", its
// map's version, its regions, whether the tool is armed, the map for the
// pointer, and the photo it is of.
type maskPick struct {
	kind     string
	mapVer   string
	cats     []marrawclient.AICategory
	people   []marrawclient.AIInstance
	armed    bool
	plane    *image.Gray
	planeKey string
	photo    int64
	hover    int
}

// ids are the regions that can be picked.
func (p *maskPick) ids() map[int]bool {
	out := map[int]bool{}
	for _, c := range p.cats {
		out[c.ID] = true
	}
	for _, c := range p.people {
		out[c.ID] = true
	}
	return out
}

// pickKind is the backend's kind of the buttons' Scene and People.
func pickKind(button string) string {
	return map[string]string{"scene": "class", "people": "person"}[button]
}

// maskPickArm arms the picking of kind, button "scene" or "people": the
// regions found already serve again, and found first otherwise, the
// model asked for where it is missing. Pressed while armed, it disarms.
func (cu *culler) maskPickArm(in MaskAI) {
	d := &cu.dev
	kind := pickKind(in.Kind)
	m := &cu.masks
	if m.pick.armed && m.pick.kind == kind {
		cu.disarmPick()
		return
	}
	if m.pick.photo == d.id && m.pick.kind == kind && (len(m.pick.cats) > 0 || len(m.pick.people) > 0) {
		cu.armPick()
		return
	}
	if m.aiBusy != "" {
		return
	}
	id := d.id
	m.aiBusy = in.Kind
	cu.masksChanged()
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 5*time.Minute)
		defer cancel()
		if !in.Download {
			if st, err := cu.api.Edits.AIModelStatus(ctx, marrawclient.AIKind(kind)); err == nil && st != nil && !st.Downloaded {
				select {
				case cu.do <- func() { cu.masks.aiBusy = ""; cu.masksChanged() }:
				case <-cu.ctx.Done():
				}
				what := map[string]string{"class": "the scene model", "person": "the people model"}[kind]
				cu.askModel("aiModel:"+in.Kind, what, st.Bytes)
				return
			}
		}
		res, err := cu.api.Edits.GenerateAIMap(ctx, id, marrawclient.AIKind(kind), in.Download)
		select {
		case cu.do <- func() {
			cu.masks.aiBusy = ""
			defer cu.masksChanged()
			if err != nil || res == nil {
				cu.fail("The "+map[string]string{"class": "scene", "person": "people"}[kind]+" could not be found", orNoAnswer(err))
				return
			}
			if d.id != id || !d.open {
				return
			}
			p := &cu.masks.pick
			*p = maskPick{kind: kind, mapVer: res.MapVer, cats: res.Categories, people: res.Instances, photo: id}
			if len(p.cats) == 0 && len(p.people) == 0 {
				cu.tell(map[string]string{"class": "No distinct regions found in this photo", "person": "No people found in this photo"}[kind])
				return
			}
			cu.armPick()
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// armPick arms the picking found: the brush and the colour picker let
// go, and the map for the pointer fetched for the edit's frame.
func (cu *culler) armPick() {
	d := &cu.dev
	m := &cu.masks
	m.pick.armed, m.pick.hover = true, 0
	m.brush.Painting, m.rangePick = false, false
	key := fmt.Sprint(m.pick.kind, m.pick.mapVer, d.params.Rotate, d.params.FlipH)
	if m.pick.plane != nil && m.pick.planeKey == key {
		cu.masksChanged()
		return
	}
	id, kind, params := d.id, m.pick.kind, d.params
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, time.Minute)
		defer cancel()
		blob, err := cu.api.Edits.AIMapPlane(ctx, id, marrawclient.AIKind(kind), params)
		var plane *image.Gray
		if err == nil && blob != nil {
			if img, derr := png.Decode(bytes.NewReader(blob.Data)); derr == nil {
				plane = grayOf(img)
			} else {
				err = derr
			}
		}
		select {
		case cu.do <- func() {
			if err != nil || plane == nil {
				cu.fail("The regions could not be shown", orNoAnswer(err))
				return
			}
			if p := &cu.masks.pick; p.photo == id && p.kind == kind {
				p.plane, p.planeKey = plane, key
				cu.masksChanged()
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
	cu.masksChanged()
}

// grayOf is img's grey, a pixel's value its region's ID.
func grayOf(img image.Image) *image.Gray {
	if g, ok := img.(*image.Gray); ok {
		return g
	}
	g := image.NewGray(img.Bounds())
	draw.Draw(g, g.Bounds(), img, img.Bounds().Min, draw.Src)
	return g
}

// disarmPick puts the picking away, its regions kept for the photo.
func (cu *culler) disarmPick() {
	m := &cu.masks
	if !m.pick.armed {
		return
	}
	m.pick.armed = false
	if m.hover < -1 {
		cu.maskHover(-1)
	}
	cu.masksChanged()
}

// pickMask is the mask of region id of the picking, as marraw makes it.
func (p *maskPick) pickMask(id int) marrawclient.Mask {
	m := marrawclient.Mask{Type: "ai", AIKind: marrawclient.AIKind(p.kind), MapVer: p.mapVer, ClassID: id, Feather: 0.25}
	if p.kind == "person" {
		m.Feather = 0.15
	}
	return m
}

// maskPickAt adds a mask of region id, the picking kept armed for more.
func (cu *culler) maskPickAt(id int) {
	d := &cu.dev
	p := &cu.masks.pick
	if !d.open || p.photo != d.id || id <= 0 && p.kind == "person" || !p.ids()[id] {
		return
	}
	d.params.Masks = append(slices.Clone(d.params.Masks), p.pickMask(id))
	cu.maskEdit(true, "Add AI mask")
}

// maskPickHover tints region id over the photo, as the mask of it would
// cover it, nought for none.
func (cu *culler) maskPickHover(id int) {
	d := &cu.dev
	p := &cu.masks.pick
	m := &cu.masks
	if id == p.hover {
		return
	}
	p.hover = id
	m.tintGen++
	if id <= 0 || p.photo != d.id || !p.ids()[id] {
		m.hover = -1
		cu.showCull()
		return
	}
	params := d.params
	params.Masks = append(slices.Clone(params.Masks), p.pickMask(id))
	i := len(params.Masks) - 1
	tag := -10 - id
	m.hover = tag
	cu.fetchTintFor(params, i, tag, tintKey(params, i))
}

// pickChips are the regions the panel offers, largest first, with
// whether a mask of each is there already.
func (cu *culler) pickChips() []PickChip {
	d := &cu.dev
	p := &cu.masks.pick
	if p.photo != d.id {
		return nil
	}
	has := map[int]bool{}
	for _, m := range d.params.Masks {
		if m.Type == "ai" && string(m.AIKind) == p.kind {
			has[m.ClassID] = true
		}
	}
	var out []PickChip
	for _, c := range p.cats {
		name := c.Name
		if c.ID >= 0 && c.ID < len(aiCategories) && name == "" {
			name = aiCategories[c.ID]
		}
		out = append(out, PickChip{ID: c.ID, Label: fmt.Sprintf("%s · %.0f%%", name, c.Fraction*100), Has: has[c.ID]})
	}
	for _, c := range p.people {
		out = append(out, PickChip{ID: c.ID, Label: fmt.Sprintf("Person %d · %.0f%%", c.ID, c.Fraction*100), Has: has[c.ID]})
	}
	return out
}
