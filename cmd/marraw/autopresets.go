package main

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The creative auto presets are marraw's own looks of autos and offsets,
// which Ctrl+1 to 9 apply. Settings → Auto presets edits them, as
// marraw's settings do.

// AutoPresetOp changes the creative auto presets, as Op says: "add" one,
// "delete" the one at Index, "name" it Name, turn its auto "section"
// Section On or off, set its "offset" Key to Value, or "restore" the six
// marraw ships with.
type AutoPresetOp struct {
	Op      string
	Index   int
	Name    string
	Section string
	On      bool
	Key     string
	Value   float64
}

// autoOffsets are the adjustments a creative auto preset can set, in
// marraw's order, each with the auto section that works it out, or none.
// An offset whose section the preset runs is added to the auto's result;
// any other is set to its value as it is.
var autoOffsets = []struct{ key, section string }{
	{"expEV", "tone"}, {"contrast", "tone"}, {"whites", "tone"}, {"blacks", "tone"},
	{"toneShadows", "tone"}, {"toneHighlights", "tone"}, {"vibrance", "color"}, {"saturation", "color"},
	{"splitShadowHue", ""}, {"splitShadowAmt", ""}, {"splitHighlightHue", ""}, {"splitHighlightAmt", ""},
	{"texture", ""}, {"clarity", ""}, {"dehaze", ""}, {"vignette", ""},
}

// autoSections are the autos a creative preset can run, as the settings
// name them.
var autoSections = []struct{ id, label string }{{"tone", "Tone"}, {"wb", "White balance"}, {"color", "Colour"}}

// offsetAdds reports whether offset key lands on the auto's result, as it
// does where the preset runs its section.
func offsetAdds(key string, sections []string) bool {
	for _, o := range autoOffsets {
		if o.key == key {
			return o.section != "" && slices.Contains(sections, o.section)
		}
	}
	return false
}

// defaultAutoPresets are the six presets marraw ships with, as marraw's
// DEFAULT_PRESETS has them, for Restore defaults.
func defaultAutoPresets() []marrawclient.AutoPreset {
	return []marrawclient.AutoPreset{
		{ID: "default-cinematic", Name: "Cinematic", Sections: []string{"tone", "wb", "color"}, Offsets: map[string]float64{
			"contrast": 0.15, "blacks": -0.1, "toneShadows": 0.1, "toneHighlights": -0.08, "vibrance": 0.1, "saturation": -0.05,
			"splitShadowHue": 195, "splitShadowAmt": 0.25, "splitHighlightHue": 40, "splitHighlightAmt": 0.22,
			"clarity": 0.08, "vignette": 0.12}},
		{ID: "default-faded", Name: "Faded film", Sections: []string{"tone", "color"}, Offsets: map[string]float64{
			"contrast": -0.2, "whites": -0.08, "blacks": 0.15, "toneHighlights": -0.1, "vibrance": -0.2, "saturation": -0.25,
			"splitShadowHue": 210, "splitShadowAmt": 0.1, "splitHighlightHue": 50, "splitHighlightAmt": 0.12,
			"texture": -0.12, "clarity": -0.1, "dehaze": -0.08}},
		{ID: "default-noir", Name: "Noir B&W", Sections: []string{"tone"}, Offsets: map[string]float64{
			"contrast": 0.3, "whites": 0.12, "blacks": -0.2, "toneShadows": -0.08, "saturation": -1, "vibrance": -1,
			"clarity": 0.25, "texture": 0.15, "dehaze": 0.1, "vignette": 0.28}},
		{ID: "default-punchy", Name: "Punchy", Sections: []string{"tone", "color"}, Offsets: map[string]float64{
			"contrast": 0.28, "whites": 0.15, "blacks": -0.15, "vibrance": 0.35, "saturation": 0.2,
			"clarity": 0.22, "texture": 0.1, "dehaze": 0.15, "vignette": 0.08}},
		{ID: "default-moody", Name: "Moody", Sections: []string{"tone", "color"}, Offsets: map[string]float64{
			"expEV": -0.35, "contrast": 0.18, "whites": -0.1, "blacks": -0.22, "toneShadows": 0.1, "vibrance": -0.15,
			"saturation": -0.2, "splitShadowHue": 220, "splitShadowAmt": 0.18, "dehaze": 0.15, "clarity": 0.1, "vignette": 0.3}},
		{ID: "default-golden", Name: "Golden hour", Sections: []string{"tone"}, Offsets: map[string]float64{
			"expEV": 0.1, "contrast": -0.05, "toneShadows": 0.12, "toneHighlights": -0.05, "vibrance": 0.2, "saturation": 0.08,
			"splitShadowHue": 35, "splitShadowAmt": 0.12, "splitHighlightHue": 45, "splitHighlightAmt": 0.28,
			"clarity": -0.05, "texture": 0.05, "vignette": 0.1}},
	}
}

// cloneAutoPresets is ps with nothing shared, so a change to one copy
// leaves the other, as the settings dialog's, as it was.
func cloneAutoPresets(ps []marrawclient.AutoPreset) []marrawclient.AutoPreset {
	out := make([]marrawclient.AutoPreset, len(ps))
	for i, p := range ps {
		p.Sections = slices.Clone(p.Sections)
		p.Offsets = maps.Clone(p.Offsets)
		out[i] = p
	}
	return out
}

// autoPresetOp changes the creative auto presets, and saves them.
func (cu *culler) autoPresetOp(in AutoPresetOp) {
	if cu.ui == nil {
		// Not loaded: a save would drop the presets kept.
		cu.notify("The auto presets have not loaded yet")
		return
	}
	ps := cloneAutoPresets(cu.ui.AutoPresets)
	ok := in.Index >= 0 && in.Index < len(ps)
	switch in.Op {
	case "add":
		ps = append(ps, marrawclient.AutoPreset{ID: newPresetID(), Name: "New preset", Sections: []string{"tone"},
			Offsets: map[string]float64{}})
		cu.settings.AutoGen++
	case "delete":
		if !ok {
			return
		}
		ps = slices.Delete(ps, in.Index, in.Index+1)
		cu.settings.AutoGen++
	case "restore":
		// The six as shipped, in their places, so Ctrl+1 to 6 stay put,
		// and the presets added after them kept.
		def := defaultAutoPresets()
		for _, p := range ps {
			if !slices.ContainsFunc(def, func(d marrawclient.AutoPreset) bool { return d.ID == p.ID }) {
				def = append(def, p)
			}
		}
		ps = def
		cu.settings.AutoGen++
	case "name":
		if !ok {
			return
		}
		ps[in.Index].Name = in.Name
	case "section":
		if !ok {
			return
		}
		p := &ps[in.Index]
		on := in.On
		var secs []string
		// In marraw's order, so the list reads the same however it was
		// made.
		for _, s := range autoSections {
			if s.id == in.Section && on || s.id != in.Section && slices.Contains(p.Sections, s.id) {
				secs = append(secs, s.id)
			}
		}
		p.Sections = secs
	case "offset":
		if !ok {
			return
		}
		if ps[in.Index].Offsets == nil {
			ps[in.Index].Offsets = map[string]float64{}
		}
		ps[in.Index].Offsets[in.Key] = in.Value
	default:
		return
	}
	cu.ui.AutoPresets = ps
	cu.saveAutoPresets()
	cu.settingsChanged()
	if cu.dev.mounted {
		_ = cu.c.Update("develop", cu.developState())
		cu.loadPresetThumbs()
	}
}

// autoSaving keeps the creative auto presets' saves in order: a save
// older than one sent already is dropped.
var autoSaving struct {
	sync.Mutex
	sent int
}

// saveAutoPresets saves the creative auto presets as they are now.
func (cu *culler) saveAutoPresets() {
	defer cu.presetsChanged()
	cu.autoSeq++
	seq, all := cu.autoSeq, cloneAutoPresets(cu.ui.AutoPresets)
	cu.call("The auto presets could not be saved", func(ctx context.Context) error {
		autoSaving.Lock()
		defer autoSaving.Unlock()
		if seq <= autoSaving.sent {
			return nil
		}
		autoSaving.sent = seq
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return cu.api.Settings.SetAutoPresets(ctx, all)
	}, nil)
}
