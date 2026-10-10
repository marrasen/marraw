package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"image/jpeg"
	"log"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// A preset is a look: marraw's own rules for laying one over a photo's
// edit, ported as they are, so a preset lands here as it does there.
// Geometry, masks and spots never travel in a preset.

// presetGroups are the look's groups a preset may carry, as marraw names
// them.
var presetGroups = []struct{ id, label string }{
	{"tone", "Tone"}, {"presence", "Presence"}, {"wb", "White balance"}, {"color", "Color"}, {"effects", "Effects"}, {"detail", "Detail"},
}

// presetAdd are the look's numbers with a fixed neutral, by group: a
// relative preset adds its offset from neutral onto the photo's value.
var presetAdd = map[string]string{
	"expPreserve": "tone", "bright": "tone", "gamma": "tone", "shadow": "tone", "contrast": "tone", "whites": "tone",
	"blacks": "tone", "toneShadows": "tone", "toneHighlights": "tone",
	"clarity": "presence", "texture": "presence", "dehaze": "presence",
	"wbTemp": "wb", "wbTint": "wb",
	"saturation": "color", "vibrance": "color", "splitShadowAmt": "color", "splitHighlightAmt": "color",
	"vignette": "effects",
	"sharpen":  "detail", "nrThreshold": "detail", "medPasses": "detail", "caRed": "detail", "caBlue": "detail",
	"lensDistortion": "detail", "lensVignetting": "detail", "lensCA": "detail",
}

// presetWhole are the look's values that land whole, by group: hues,
// Kelvin, the white balance's own multipliers and the curves, which have
// no offset to add, and the choices. A relative preset writes them only
// where it holds something other than neutral, as neutral cannot be told
// from untouched.
var presetWhole = []struct {
	group   string
	neutral func(p *marrawclient.Params) bool
	copy    func(dst, src *marrawclient.Params)
}{
	{"tone", func(p *marrawclient.Params) bool {
		return len(p.ToneCurve)+len(p.ToneCurveR)+len(p.ToneCurveG)+len(p.ToneCurveB) == 0
	},
		func(d, s *marrawclient.Params) {
			d.ToneCurve, d.ToneCurveR = slices.Clone(s.ToneCurve), slices.Clone(s.ToneCurveR)
			d.ToneCurveG, d.ToneCurveB = slices.Clone(s.ToneCurveG), slices.Clone(s.ToneCurveB)
		}},
	{"wb", func(p *marrawclient.Params) bool { return p.WBMode == "" }, func(d, s *marrawclient.Params) { d.WBMode = s.WBMode }},
	{"wb", func(p *marrawclient.Params) bool { return p.WBMul == [4]float64{} }, func(d, s *marrawclient.Params) { d.WBMul = s.WBMul }},
	{"wb", func(p *marrawclient.Params) bool { return p.WBKelvin == 0 }, func(d, s *marrawclient.Params) { d.WBKelvin = s.WBKelvin }},
	{"color", func(p *marrawclient.Params) bool { return !p.BW }, func(d, s *marrawclient.Params) { d.BW = s.BW }},
	{"color", func(p *marrawclient.Params) bool { return p.SplitShadowHue == 0 }, func(d, s *marrawclient.Params) { d.SplitShadowHue = s.SplitShadowHue }},
	{"color", func(p *marrawclient.Params) bool { return p.SplitHighlightHue == 0 }, func(d, s *marrawclient.Params) { d.SplitHighlightHue = s.SplitHighlightHue }},
	{"detail", func(p *marrawclient.Params) bool { return p.Highlight == 0 }, func(d, s *marrawclient.Params) { d.Highlight = s.Highlight }},
	{"detail", func(p *marrawclient.Params) bool { return p.FBDDNoiseRd == 0 }, func(d, s *marrawclient.Params) { d.FBDDNoiseRd = s.FBDDNoiseRd }},
	{"detail", func(p *marrawclient.Params) bool { return p.Demosaic == "" }, func(d, s *marrawclient.Params) { d.Demosaic = s.Demosaic }},
	{"detail", func(p *marrawclient.Params) bool { return p.LensMode == "" }, func(d, s *marrawclient.Params) { d.LensMode = s.LensMode }},
}

// presetSections is the groups preset carries: all where it names none.
func presetSections(p marrawclient.UserPreset) map[string]bool {
	out := map[string]bool{}
	for _, s := range p.Sections {
		for _, g := range presetGroups {
			if g.id == s {
				out[s] = true
			}
		}
	}
	if len(out) == 0 {
		for _, g := range presetGroups {
			out[g.id] = true
		}
	}
	return out
}

func clampRound(v, lo, hi float64) float64 {
	return min(hi, max(lo, math.Round(v*1000)/1000))
}

// applyUserPreset lays preset over draft, for a photo whose camera's
// exposure rests at baseEV, as marraw's applyUserPreset does.
func applyUserPreset(draft marrawclient.Params, preset marrawclient.UserPreset, baseEV float64) marrawclient.Params {
	secs := presetSections(preset)
	out := draft
	src := preset.Params
	rel := preset.Relative
	var neutral marrawclient.Params
	if secs["tone"] {
		// The look's exposure is its offset from the camera's rest on the
		// photo it was made on, laid on this one's.
		creative := src.ExpEV - preset.BaseExpEV
		ev := baseEV + creative
		switch {
		case rel:
			ev = draft.ExpEV + creative
		case preset.BaseExpEV == 0:
			ev = src.ExpEV
		}
		out.ExpEV = clampRound(ev, -5, 5)
	}
	for key, group := range presetAdd {
		sp, ok := devSpecs[key]
		if !secs[group] || !ok {
			continue
		}
		if !rel {
			raw := sp.get(&src)
			sp.set(&out, clampRound(raw, float64(sp.min), float64(sp.max)))
			continue
		}
		d := sp.get(&src) - sp.get(&neutral)
		if d == 0 {
			continue
		}
		sp.set(&out, clampRound(sp.get(&draft)+d, float64(sp.min), float64(sp.max)))
	}
	if secs["color"] {
		for i := range 8 {
			if rel {
				out.HSLHue[i] = clampRound(draft.HSLHue[i]+src.HSLHue[i], -1, 1)
				out.HSLSat[i] = clampRound(draft.HSLSat[i]+src.HSLSat[i], -1, 1)
				out.HSLLum[i] = clampRound(draft.HSLLum[i]+src.HSLLum[i], -1, 1)
			} else {
				out.HSLHue[i], out.HSLSat[i], out.HSLLum[i] = src.HSLHue[i], src.HSLSat[i], src.HSLLum[i]
			}
		}
	}
	for _, f := range presetWhole {
		if secs[f.group] && (!rel || !f.neutral(&src)) {
			f.copy(&out, &src)
		}
	}
	return out
}

// stripToLook is draft with its geometry and local adjustments taken off:
// the shape of a preset's look.
func stripToLook(p marrawclient.Params) marrawclient.Params {
	p.Rotate, p.FlipH = 0, false
	p.CropX, p.CropY, p.CropW, p.CropH, p.CropAngle = 0, 0, 0, 0, 0
	p.Masks, p.Spots = nil, nil
	return p
}

// autoOffsetSection is the auto section that works out each of an auto
// preset's offsets, or none: an offset of a section the preset runs is
// added to what it works out, and any other is written as it is.
var autoOffsetSection = map[string]string{
	"expEV": "tone", "contrast": "tone", "whites": "tone", "blacks": "tone", "toneShadows": "tone", "toneHighlights": "tone",
	"vibrance": "color", "saturation": "color",
}

// autoPresetParams works out creative auto preset over base for photo id:
// its auto sections first, then its offsets, as marraw's
// computePresetParams does.
func (cu *culler) autoPresetParams(ctx context.Context, id int64, base marrawclient.Params, preset marrawclient.AutoPreset) (marrawclient.Params, error) {
	out := base
	if len(preset.Sections) > 0 {
		res, err := cu.api.Edits.AutoAdjust(ctx, id, base, preset.Sections)
		if err != nil {
			return base, err
		}
		if res != nil {
			out = *res
		}
	}
	for key, v := range preset.Offsets {
		sp, ok := devSpecs[key]
		if !ok {
			continue
		}
		if sec := autoOffsetSection[key]; sec != "" && slices.Contains(preset.Sections, sec) {
			v += sp.get(&out)
		}
		sp.set(&out, min(float64(sp.max), max(float64(sp.min), math.Round(v*100)/100)))
	}
	return out, nil
}

// userPresetParams works out user preset over base for photo id: an
// adaptive one runs its auto sections first.
func (cu *culler) userPresetParams(ctx context.Context, id int64, base marrawclient.Params, baseEV float64, preset marrawclient.UserPreset) (marrawclient.Params, error) {
	if len(preset.AutoSections) == 0 {
		return applyUserPreset(base, preset, baseEV), nil
	}
	res, err := cu.api.Edits.AutoAdjust(ctx, id, base, preset.AutoSections)
	if err != nil {
		return base, err
	}
	if res != nil {
		base = *res
	}
	return applyUserPreset(base, preset, baseEV), nil
}

// The presets' vocabulary.
type (
	// PresetApply lays the preset, user or creative auto, at Index of its
	// list over the photo's edit; PresetHover shows it laid there without
	// keeping it, while the pointer is over its card, and PresetHover
	// with no preset, Index below nought, goes back to the edit.
	PresetApply struct {
		Auto  bool
		Index int
	}
	PresetHover struct {
		Auto  bool
		Index int
	}
	// PresetSave saves the photo's edit as a preset named Name, carrying
	// the groups Sections, relative or not.
	PresetSave struct {
		Name     string
		Sections []string
		Relative bool
	}
	// PresetDelete deletes the user preset at Index.
	PresetDelete struct{ Index int }
	// AskPreset asks for the name and groups to save the edit as.
	AskPreset struct{}
	// PresetsShown says the panel's presets show, or not.
	PresetsShown struct{ On bool }
)

// presetsOf is the presets marraw keeps, user and creative auto.
func (cu *culler) presetsOf() ([]marrawclient.UserPreset, []marrawclient.AutoPreset) {
	if cu.ui == nil {
		return nil, nil
	}
	return cu.ui.UserPresets, cu.ui.AutoPresets
}

// presetJob is what working out a preset over the edit showing takes,
// read on the culler's goroutine.
type presetJob struct {
	id    int64
	base  marrawclient.Params
	ev    float64
	users []marrawclient.UserPreset
	autos []marrawclient.AutoPreset
}

// presetJob is the job of working out a preset over the edit showing.
func (cu *culler) presetJob() presetJob {
	d := &cu.dev
	j := presetJob{id: d.id, base: d.params}
	j.users, j.autos = cu.presetsOf()
	if i, ok := cu.index[d.id]; ok {
		j.ev = cu.photos[i].BaseExpEV
	}
	return j
}

// presetParams works out preset i of the user or the auto list over the
// job's edit.
func (cu *culler) presetParams(ctx context.Context, j presetJob, auto bool, i int) (marrawclient.Params, string, error) {
	users, autos, base, ev := j.users, j.autos, j.base, j.ev
	d := struct{ id int64 }{j.id}
	if auto {
		if i < 0 || i >= len(autos) {
			return base, "", fmt.Errorf("no preset %d", i)
		}
		p, err := cu.autoPresetParams(ctx, d.id, base, autos[i])
		return p, autos[i].Name, err
	}
	if i < 0 || i >= len(users) {
		return base, "", fmt.Errorf("no preset %d", i)
	}
	p, err := cu.userPresetParams(ctx, d.id, base, ev, users[i])
	return p, users[i].Name, err
}

// presetApply lays a preset over the edit showing, as a step of its
// history.
func (cu *culler) presetApply(in PresetApply) {
	d := &cu.dev
	if !d.open || !cu.culling || d.id != cu.photos[cu.at].ID {
		return
	}
	id, gen, job := d.id, cu.presetGen+1, cu.presetJob()
	cu.presetGen, d.hover = gen, nil
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		p, name, err := cu.presetParams(ctx, job, in.Auto, in.Index)
		select {
		case cu.do <- func() {
			if err != nil {
				cu.fail("The preset could not be applied", err)
				return
			}
			if cu.presetGen != gen || d.id != id {
				return
			}
			base := d.params
			d.params = p
			cu.edited(true)
			cu.remember(name)
			cu.presetAmt = &presetAmount{id: id, base: base, result: p, name: name, amount: 1}
			_ = cu.c.Update("develop", cu.developState())
			cu.tell(name)
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// presetHover shows a preset laid over the edit showing, as a draft, a
// moment after the pointer comes over its card, without keeping it; with
// no preset, the edit shows again.
func (cu *culler) presetHover(in PresetHover) {
	d := &cu.dev
	if !d.open || !cu.culling || d.id != cu.photos[cu.at].ID || cu.wb.on {
		return
	}
	cu.presetGen++
	gen, id, job := cu.presetGen, d.id, cu.presetJob()
	if in.Index < 0 {
		if d.hover != nil {
			d.hover = nil
			d.edits++
			cu.preview(false)
			cu.preview(true)
		}
		return
	}
	go func() {
		select {
		case <-time.After(150 * time.Millisecond):
		case <-cu.ctx.Done():
			return
		}
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		p, _, err := cu.presetParams(ctx, job, in.Auto, in.Index)
		select {
		case cu.do <- func() {
			if err != nil || cu.presetGen != gen || d.id != id {
				return
			}
			d.hover = &p
			d.edits++
			cu.preview(false)
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// PresetAsk is what the dialog to save a preset starts from.
type PresetAsk struct{ Name string }

// newPresetDialog asks for a preset's name, the groups it carries, and
// whether it adds to a photo's own edit or replaces it.
func newPresetDialog(s PresetAsk) *widget.Dialog {
	d := widget.NewDialog("Save the edit as a preset")
	name := widget.NewTextField()
	name.SetText(s.Name, nil)
	checks := make([]*widget.Checkbox, len(presetGroups))
	var boxes []gunim.Node
	for i, g := range presetGroups {
		checks[i] = widget.NewCheckbox(g.label)
		checks[i].SetChecked(true, nil)
		boxes = append(boxes, checks[i])
	}
	rel := widget.NewCheckbox("Add to a photo's own edit, rather than replace it")
	hint := widget.NewLabel("A preset is a look: crop, straighten and masks stay with the photo.")
	hint.Color, hint.Size, hint.MaxLines = widget.PaletteHint, noteSize, 2
	form := widget.NewForm().Add("Name", name).
		Add("Carries", widget.Column(widget.Row(boxes[:3]...), widget.Row(boxes[3:]...))).
		Add("", rel)
	d.Body = widget.Column(form, hint)
	d.Width = 520
	d.SetButtons("Save", "Cancel")
	d.Check = func() string {
		if strings.TrimSpace(name.Text()) == "" {
			return "What should the preset be called?"
		}
		for _, c := range checks {
			if c.Checked() {
				return ""
			}
		}
		return "Choose at least one group for the preset to carry"
	}
	d.OnAccept = func(*gunim.UI) gunim.Intent {
		var secs []string
		for i, c := range checks {
			if c.Checked() {
				secs = append(secs, presetGroups[i].id)
			}
		}
		if len(secs) == len(presetGroups) {
			// All of them is spelled as none, as marraw keeps it.
			secs = nil
		}
		return PresetSave{Name: strings.TrimSpace(name.Text()), Sections: secs, Relative: rel.Checked()}
	}
	d.OnDismiss = widget.Sends(PresetSave{})
	return d
}

// askPreset opens the dialog to save the edit showing as a preset.
func (cu *culler) askPreset() {
	d := &cu.dev
	if !d.open || !cu.culling || cu.asking {
		return
	}
	users, _ := cu.presetsOf()
	cu.asking = true
	_ = cu.c.Mount(gunim.Root, "preset", "preset", PresetAsk{Name: fmt.Sprintf("Preset %d", len(users)+1)})
}

// presetSave takes the dialog's answer: the edit showing saved as a
// preset, after marraw's others, or nothing.
func (cu *culler) presetSave(in PresetSave) {
	cu.asking = false
	_ = cu.c.Unmount("preset")
	cu.refocus()
	d := &cu.dev
	if in.Name == "" || cu.ui == nil || !d.open {
		return
	}
	p := marrawclient.UserPreset{ID: newPresetID(), Name: in.Name, Params: stripToLook(d.params), Sections: in.Sections,
		Relative: in.Relative}
	if i, ok := cu.index[d.id]; ok {
		p.BaseExpEV = cu.photos[i].BaseExpEV
	}
	cu.ui.UserPresets = append(cu.ui.UserPresets, p)
	cu.savePresets()
	cu.tell("Saved the preset " + in.Name)
}

// presetDelete deletes user preset i.
func (cu *culler) presetDelete(i int) {
	if cu.ui == nil || i < 0 || i >= len(cu.ui.UserPresets) {
		return
	}
	name := cu.ui.UserPresets[i].Name
	cu.ui.UserPresets = slices.Delete(slices.Clone(cu.ui.UserPresets), i, i+1)
	cu.savePresets()
	cu.tell("Deleted the preset " + name)
}

// savePresets saves the user presets, and shows them.
func (cu *culler) savePresets() {
	all := slices.Clone(cu.ui.UserPresets)
	go func() {
		if err := cu.api.Settings.SetUserPresets(cu.ctx, all); err != nil {
			log.Printf("presets: %v", err)
		}
	}()
	if cu.dev.mounted {
		_ = cu.c.Update("develop", cu.developState())
		cu.loadPresetThumbs()
	}
}

// newPresetID is a new preset's id, unique enough for one person's list.
func newPresetID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x", b)
}

// presetCards are the presets as the panel lists them: the user's, then
// the creative autos.
func (cu *culler) presetCards() []PresetCard {
	users, autos := cu.presetsOf()
	var out []PresetCard
	for i, p := range users {
		out = append(out, PresetCard{Key: "u:" + p.ID, Name: p.Name, Badge: presetBadge(p), Index: i})
	}
	for i, p := range autos {
		out = append(out, PresetCard{Key: "a:" + p.ID, Name: p.Name, Auto: true, Index: i})
	}
	return out
}

// presetBadge marks what kind of preset p is, as marraw's cards do: auto
// for one that adapts to the photo, ± for one that adds to its edit, and
// how many of the six groups it carries where not all.
func presetBadge(p marrawclient.UserPreset) string {
	switch {
	case len(p.AutoSections) > 0:
		return "auto"
	case p.Relative:
		return "±"
	case len(p.Sections) > 0 && len(p.Sections) < len(presetGroups):
		return fmt.Sprintf("%d/%d", len(presetSections(p)), len(presetGroups))
	}
	return ""
}

// presetThumbEdge is how large the presets' small pictures are rendered.
const presetThumbEdge = 168

// loadPresetThumbs renders each preset's small picture over the edit of
// photo id, one after another, a moment after the photo comes, so
// stepping through photos asks for none; the last photo's stop.
func (cu *culler) loadPresetThumbs() {
	d := &cu.dev
	if d.thumbStop != nil {
		d.thumbStop()
		d.thumbStop = nil
	}
	cards := cu.presetCards()
	if !d.open || !cu.presetsShown || len(cards) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	d.thumbStop = cancel
	job := cu.presetJob()
	go func() {
		select {
		case <-time.After(700 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		for _, c := range cards {
			p, _, err := cu.presetParams(ctx, job, c.Auto, c.Index)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			blob, err := cu.api.Edits.PreviewEdit(ctx, job.id, p, presetThumbEdge)
			if err != nil || blob == nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			img, err := jpeg.Decode(bytes.NewReader(blob.Data))
			if err != nil {
				continue
			}
			pi := paint.NewImage(img)
			key := c.Key
			select {
			case cu.do <- func() { _ = cu.c.Patch("develop", PresetThumb{Photo: job.id, Key: key, Img: pi}) }:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// DevTab shows the panel's tab at Index, or By tabs on from the one
// showing, as Tab and Shift+Tab do.
type DevTab struct {
	Index, By int
	Open      bool // opens the panel first, if it is shut
}

// showTab shows the panel's tab i: the presets' small pictures render
// while theirs shows.
func (cu *culler) showTab(i int) {
	if i < 0 || i >= len(devTabs) || i == cu.devTab {
		return
	}
	cu.devTab = i
	cu.presetsShown = i == tabPresets
	if i != tabLocal {
		// The picking is the Local tab's.
		cu.disarmPick()
	}
	cu.loadPresetThumbs()
	if cu.dev.mounted {
		_ = cu.c.Update("develop", cu.developState())
	}
}
