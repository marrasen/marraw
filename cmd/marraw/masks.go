package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"log"
	"slices"
	"time"

	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The masks' vocabulary.
type (
	// MaskAdd adds a mask of Kind: "linear", "radial", "brush" or "range".
	MaskAdd struct{ Kind string }
	// MaskAI adds an AI mask of Kind: "subject", "background", "depth" or
	// "tilt", the backend making its map first; Download allows its model
	// to be fetched.
	MaskAI struct {
		Kind     string
		Download bool
	}
	// MaskSelect chooses the mask at Index to work on, below nought none.
	MaskSelect struct{ Index int }
	// MaskSet sets Key of the mask at Index, an adjustment or a part of
	// its shape, kept as Commit says.
	MaskSet struct {
		Index  int
		Key    string
		Value  float64
		Commit bool
	}
	// MaskFlag turns What of the mask at Index on or off: "invert",
	// "hide" or "remove".
	MaskFlag struct {
		Index int
		What  string
	}
	// MaskDelete deletes the mask at Index.
	MaskDelete struct{ Index int }
	// MaskGeom makes Mask the mask at Index, as a drag of its handles or a
	// brush stroke on the photo gives it, kept as Commit says.
	MaskGeom struct {
		Index  int
		Mask   marrawclient.Mask
		Commit bool
	}
	// BrushSet sets the brush's tool.
	BrushSet struct{ Tool BrushTool }
	// BrushClear takes all the strokes of the brush mask at Index off.
	BrushClear struct{ Index int }
	// RangePick turns the range mask's colour picker on or off, and RangeAt
	// picks at X, Y, 0 to 1 across the photo as it shows.
	RangePick struct{ On bool }
	RangeAt   struct{ X, Y float64 }
	// MaskHover shows the mask at Index tinted over the photo, below
	// nought none, as the pointer is over its row.
	MaskHover struct{ Index int }
	// MaskEscape lets the masks' tools go, one at a time.
	MaskEscape struct{}
)

// BrushTool is how the brush paints: its radius, a fraction of the
// frame's long edge, its feather, a fraction of the radius, its flow, and
// whether it paints, and whether it erases.
type BrushTool struct {
	Radius, Feather, Flow float64
	Erase, Painting       bool
}

// defaultBrush is the brush as it starts, as marraw's.
var defaultBrush = BrushTool{Radius: 0.05, Feather: 0.5, Flow: 1}

// MaskView is what the cull view shows of the masks: the edit, for their
// geometry and to map it to the photo, the one chosen, below nought none,
// the oriented frame's size, the brush, whether the range picker is out,
// and a mask's tint over the photo, of the mask TintOf, or nil.
type MaskView struct {
	Params    marrawclient.Params
	Selected  int
	Frame     [2]float64
	Brush     BrushTool
	RangePick bool
	Tint      *paint.Image
	TintOf    int
	// Pick is the scene's or the people's picking, while it is armed,
	// and Heal the heal tool, while it is on.
	Pick *PickView
	Heal *HealView
}

// maskState is the develop side's masks: the one chosen, the brush, the
// range picker, the AI kind being made, and the tint showing.
type maskState struct {
	sel       int
	brush     BrushTool
	rangePick bool
	aiBusy    string
	hover     int
	tint      *paint.Image
	tintOf    int
	tintKey   string
	tintGen   int
	// active is the chosen mask's control the keys act on, or "".
	active string
	// tints are the tints fetched, by their keys, the oldest first in
	// tintOrder; pick is the scene's or the people's picking.
	tints     map[string]*paint.Image
	tintOrder []string
	pick      maskPick
}

// maskView is the masks as the cull view shows them, or nil while none is
// chosen and none tinted.
func (cu *culler) maskView() *MaskView {
	d := &cu.dev
	m := &cu.masks
	if !d.open || cu.crop.on || (m.sel < 0 && m.tint == nil && !m.pick.armed && !cu.heal.on) {
		return nil
	}
	v := &MaskView{Params: d.params, Selected: m.sel, Brush: m.brush, RangePick: m.rangePick, TintOf: -1}
	if m.sel >= len(d.params.Masks) {
		v.Selected = -1
	}
	if m.tint != nil && m.tintOf == m.hover {
		v.Tint, v.TintOf = m.tint, m.tintOf
	}
	if cu.heal.on {
		v.Heal = cu.healView()
	}
	if m.pick.armed && m.pick.plane != nil {
		v.Pick = &PickView{Kind: m.pick.kind, Plane: m.pick.plane, IDs: m.pick.ids()}
	}
	if i, ok := cu.index[d.id]; ok {
		f := frameSize(cu.photos[i], d.params)
		v.Frame = [2]float64{float64(f.X), float64(f.Y)}
	}
	return v
}

// masksChanged shows the masks anew, in the panel and over the photo.
func (cu *culler) masksChanged() {
	if cu.dev.mounted {
		_ = cu.c.Update("develop", cu.developState())
	}
	cu.showCull()
}

// maskEdit takes an edit of the masks: shown, and kept as commit says, as a
// step named label.
func (cu *culler) maskEdit(commit bool, label string) {
	cu.edited(commit)
	if commit {
		cu.remember(label)
	}
	cu.masksChanged()
}

// maskOK reports whether i is a mask of the edit showing.
func (cu *culler) maskOK(i int) bool {
	d := &cu.dev
	return d.open && cu.culling && d.id == cu.photos[cu.at].ID && i >= 0 && i < len(d.params.Masks)
}

// maskAdd adds a mask, chosen, as a step.
func (cu *culler) maskAdd(kind string) {
	d := &cu.dev
	if !d.open || !cu.culling || d.id != cu.photos[cu.at].ID {
		return
	}
	d.params.Masks = append(slices.Clone(d.params.Masks), newMask(kind))
	cu.masks.sel = len(d.params.Masks) - 1
	cu.masks.rangePick = false
	cu.masks.brush.Painting = kind == "brush"
	cu.maskEdit(true, "Add "+kind+" mask")
}

// maskAI adds an AI mask: the model asked for first where it is missing,
// then the photo's map made, then the mask.
func (cu *culler) maskAI(in MaskAI) {
	d := &cu.dev
	if !d.open || !cu.culling || d.id != cu.photos[cu.at].ID {
		return
	}
	if pickKind(in.Kind) != "" {
		// Scene and People are picked, region by region.
		cu.maskPickArm(in)
		return
	}
	if cu.masks.aiBusy != "" {
		return
	}
	mapKind := map[string]string{"subject": "subject", "background": "subject", "depth": "depth", "tilt": "depth"}[in.Kind]
	if mapKind == "" {
		return
	}
	id := d.id
	cu.masks.aiBusy = in.Kind
	cu.masksChanged()
	cu.tell("Finding the " + map[string]string{"subject": "subject", "depth": "depth"}[mapKind] + "…")
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 5*time.Minute)
		defer cancel()
		if !in.Download {
			if st, err := cu.api.Edits.AIModelStatus(ctx, marrawclient.AIKind(mapKind)); err == nil && st != nil && !st.Downloaded {
				select {
				case cu.do <- func() { cu.masks.aiBusy = ""; cu.masksChanged() }:
				case <-cu.ctx.Done():
				}
				what := map[string]string{"subject": "the subject detection model", "depth": "the depth estimation model"}[mapKind]
				cu.askModel("aiModel:"+in.Kind, what, st.Bytes)
				return
			}
		}
		res, err := cu.api.Edits.GenerateAIMap(ctx, id, marrawclient.AIKind(mapKind), in.Download)
		select {
		case cu.do <- func() {
			cu.masks.aiBusy = ""
			if err != nil || res == nil {
				cu.fail("The AI mask could not be made", orNoAnswer(err))
				cu.masksChanged()
				return
			}
			if d.id != id || !d.open {
				cu.masksChanged()
				return
			}
			d.params.Masks = append(slices.Clone(d.params.Masks), aiMaskOf(in.Kind, res.MapVer, -1))
			cu.masks.sel = len(d.params.Masks) - 1
			cu.maskEdit(true, "Add AI mask")
			// What it covers, tinted a moment, to see it took the right thing.
			cu.masks.hover = cu.masks.sel
			cu.fetchTint(cu.masks.sel)
			cu.afterTint(cu.masks.sel, 1600*time.Millisecond)
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// afterTint lets mask i's tint go after a while, unless the pointer took it
// on.
func (cu *culler) afterTint(i int, after time.Duration) {
	gen := cu.masks.tintGen
	go func() {
		select {
		case <-time.After(after):
		case <-cu.ctx.Done():
			return
		}
		select {
		case cu.do <- func() {
			if cu.masks.tintGen == gen && cu.masks.hover == i {
				cu.maskHover(-1)
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// maskSelect chooses a mask to work on, or none.
func (cu *culler) maskSelect(i int) {
	d := &cu.dev
	if i >= len(d.params.Masks) {
		i = -1
	}
	cu.masks.sel, cu.masks.active = i, ""
	cu.masks.rangePick = false
	cu.masks.pick.armed = false
	cu.masks.brush.Painting = i >= 0 && d.params.Masks[i].Type == "brush" && len(d.params.Masks[i].Strokes) == 0
	cu.masksChanged()
}

// maskSet sets an adjustment or a part of a mask's shape.
func (cu *culler) maskSet(in MaskSet) {
	if !cu.maskOK(in.Index) {
		return
	}
	d := &cu.dev
	ms := slices.Clone(d.params.Masks)
	m := &ms[in.Index]
	v := in.Value
	if sp, ok := maskSpecOf(in.Key); ok {
		*sp.get(&m.Adjust) = min(float64(sp.max), max(float64(sp.min), v))
	} else {
		switch in.Key {
		case "threshold":
			m.Threshold = min(1, max(0.02, v))
		case "feather":
			m.Feather = min(1, max(0, v))
		case "depthCentre", "depthWidth":
			c, w := (m.DepthLo+m.DepthHi)/2, m.DepthHi-m.DepthLo
			if in.Key == "depthCentre" {
				c = v
			} else {
				w = max(0.02, v)
			}
			c = min(max(c, w/2), 1-w/2)
			m.DepthLo, m.DepthHi = c-w/2, c+w/2
		case "lumaLo":
			m.RangeLumaLo = min(v, m.RangeLumaHi)
		case "lumaHi":
			m.RangeLumaHi = max(v, m.RangeLumaLo)
		case "hueCentre", "hueRange":
			c, w := hueWindow(*m)
			if in.Key == "hueCentre" {
				c = v
			} else {
				w = v
			}
			if w >= 0.5 {
				m.RangeHueLo, m.RangeHueHi = 0, 1
			} else {
				m.RangeHueLo, m.RangeHueHi = wrap01(c-w), wrap01(c+w)
			}
		case "satMin":
			m.RangeSatMin = min(1, max(0, v))
		default:
			return
		}
	}
	d.params.Masks = ms
	if in.Index == cu.masks.sel {
		// The control moved is the one the keys act on now.
		cu.masks.active = in.Key
	}
	cu.maskEdit(in.Commit, "Adjust mask")
}

// hueWindow is the range mask's hue window as a centre and a half-width,
// 0 to 1 around the wheel; the whole wheel is half-width a half.
func hueWindow(m marrawclient.Mask) (float64, float64) {
	lo, hi := m.RangeHueLo, m.RangeHueHi
	if lo == 0 && hi == 1 {
		return 0, 0.5
	}
	w := hi - lo
	if w < 0 {
		w += 1
	}
	return wrap01(lo + w/2), w / 2
}

func wrap01(v float64) float64 {
	for v < 0 {
		v++
	}
	for v >= 1 {
		v--
	}
	return v
}

// maskFlag turns a mask's invert, hide or remove on or off.
func (cu *culler) maskFlag(in MaskFlag) {
	if !cu.maskOK(in.Index) {
		return
	}
	d := &cu.dev
	ms := slices.Clone(d.params.Masks)
	m := &ms[in.Index]
	label := ""
	switch in.What {
	case "invert":
		m.Invert, label = !m.Invert, "Invert mask"
		if !canRemove(*m) {
			m.Remove = false
		}
	case "hide":
		m.Disabled = !m.Disabled
		label = map[bool]string{false: "Show mask", true: "Hide mask"}[m.Disabled]
	case "remove":
		if !canRemove(*m) && !m.Remove {
			return
		}
		m.Remove, label = !m.Remove, "Remove under mask"
	default:
		return
	}
	d.params.Masks = ms
	cu.maskEdit(true, label)
}

// maskDelete deletes a mask.
func (cu *culler) maskDelete(i int) {
	if !cu.maskOK(i) {
		return
	}
	d := &cu.dev
	d.params.Masks = slices.Delete(slices.Clone(d.params.Masks), i, i+1)
	switch {
	case cu.masks.sel == i:
		cu.masks.sel = -1
	case cu.masks.sel > i:
		cu.masks.sel--
	}
	cu.masks.brush.Painting, cu.masks.rangePick = false, false
	cu.maskHover(-1)
	cu.maskEdit(true, "Remove mask")
}

// maskGeom takes a mask's new geometry from the photo: a handle dragged or
// a stroke painted.
func (cu *culler) maskGeom(in MaskGeom) {
	if !cu.maskOK(in.Index) {
		return
	}
	d := &cu.dev
	ms := slices.Clone(d.params.Masks)
	ms[in.Index] = in.Mask
	d.params.Masks = ms
	label := "Move mask"
	if in.Mask.Type == "brush" {
		label = "Brush stroke"
	}
	cu.maskEdit(in.Commit, label)
}

// brushClear takes a brush mask's strokes off.
func (cu *culler) brushClear(i int) {
	if !cu.maskOK(i) {
		return
	}
	d := &cu.dev
	ms := slices.Clone(d.params.Masks)
	ms[i].Strokes = nil
	ms[i].Remove = false
	d.params.Masks = ms
	cu.maskEdit(true, "Clear brush")
}

// rangeAt picks the range mask's colour at x, y of the photo as it shows.
func (cu *culler) rangeAt(x, y float64) {
	d := &cu.dev
	i := cu.masks.sel
	if !cu.masks.rangePick || !cu.maskOK(i) {
		return
	}
	id, params := d.id, d.params
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		res, err := cu.api.Edits.PickRangeColor(ctx, id, params, x, y, i)
		select {
		case cu.do <- func() {
			if err != nil || res == nil {
				cu.tell(whyNot(err))
				return
			}
			if d.id != id {
				return
			}
			d.params = *res
			cu.maskEdit(true, "Pick range colour")
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// maskHover shows mask i tinted over the photo, or none: the backend
// renders the tint, which is kept for the mask as it is.
func (cu *culler) maskHover(i int) {
	m := &cu.masks
	m.hover = i
	m.tintGen++
	if i < 0 || !cu.maskOK(i) {
		m.hover = -1
		cu.showCull()
		return
	}
	cu.fetchTint(i)
}

// tintKey names mask i's tint as the edit has it: the mask, the crop and
// turns, and for a range mask the rest of the edit, which it selects from.
func tintKey(p marrawclient.Params, i int) string {
	m := p.Masks[i]
	q := marrawclient.Params{Rotate: p.Rotate, FlipH: p.FlipH, CropX: p.CropX, CropY: p.CropY, CropW: p.CropW, CropH: p.CropH, CropAngle: p.CropAngle}
	if m.Type == "range" {
		q = p
		q.Masks = nil
	}
	b, _ := json.Marshal(struct {
		M marrawclient.Mask
		P marrawclient.Params
	}{m, q})
	return string(b)
}

// fetchTint has the backend render mask i's tint, unless it is the one
// showing.
func (cu *culler) fetchTint(i int) {
	d := &cu.dev
	cu.fetchTintFor(d.params, i, i, tintKey(d.params, i))
}

// fetchTintFor has the backend render the tint of mask i of params, to
// show for tag, the hover it answers, named key: the one showing, or one
// kept, at once.
func (cu *culler) fetchTintFor(params marrawclient.Params, i, tag int, key string) {
	d := &cu.dev
	m := &cu.masks
	if m.tint != nil && m.tintKey == key && m.tintOf == tag {
		cu.showCull()
		return
	}
	if img, ok := m.tints[key]; ok {
		m.tint, m.tintOf, m.tintKey = img, tag, key
		cu.showCull()
		return
	}
	gen, id := m.tintGen, d.id
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		blob, err := cu.api.Edits.MaskTintPreview(ctx, id, params, i, 1024)
		var img *paint.Image
		if err == nil && blob != nil {
			if m, derr := png.Decode(bytes.NewReader(blob.Data)); derr == nil {
				img = paint.NewImage(m)
			} else {
				err = derr
			}
		}
		select {
		case cu.do <- func() {
			if err != nil || img == nil {
				log.Printf("mask tint: %v", err)
				return
			}
			if d.id != id {
				return
			}
			cu.keepTint(key, img)
			if cu.masks.tintGen != gen {
				return
			}
			cu.masks.tint, cu.masks.tintOf, cu.masks.tintKey = img, tag, key
			cu.showCull()
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// maxTints is how many tints are kept, for the pointer to come back to.
const maxTints = 32

// keepTint keeps tint img named key, the oldest going past maxTints.
func (cu *culler) keepTint(key string, img *paint.Image) {
	m := &cu.masks
	if m.tints == nil {
		m.tints = map[string]*paint.Image{}
	}
	if _, ok := m.tints[key]; !ok {
		m.tintOrder = append(m.tintOrder, key)
	}
	m.tints[key] = img
	for len(m.tintOrder) > maxTints {
		delete(m.tints, m.tintOrder[0])
		m.tintOrder = m.tintOrder[1:]
	}
}

// masksEscape lets the masks' tools go, one at a time, as Escape does: the
// brush stops painting, the colour picker goes, the mask is let go. It
// reports whether there was one.
func (cu *culler) masksEscape() bool {
	m := &cu.masks
	switch {
	case m.brush.Painting:
		m.brush.Painting = false
	case m.pick.armed:
		cu.disarmPick()
	case m.rangePick:
		m.rangePick = false
	case m.sel >= 0 || m.active != "":
		m.sel, m.active = -1, ""
	default:
		return false
	}
	cu.masksChanged()
	return true
}
