package api

import (
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

func photoWithCamera(mk, md string) store.Photo {
	return store.Photo{Make: mk, Model: md}
}

// The Go preset-apply mirror must stay in lockstep with the client's
// applyUserPreset (client/src/lib/presetSections.ts). These fixtures pin
// the seeding semantics: section filtering, the exposure re-anchor formula,
// and the legacy (no baseline / no sections) behaviors.

func TestPresetLookSectionFilter(t *testing.T) {
	p := UserPreset{
		ID:   "x",
		Name: "tone only",
		Params: edit.Params{
			ExpEV:    0.8,
			Contrast: 0.3,
			Clarity:  0.2,  // presence — excluded
			Vignette: -0.4, // effects — excluded
			Sharpen:  0.5,  // detail — excluded
		},
		Sections: []string{"tone"},
	}
	out := presetLook(p, 0)
	if out.Contrast != 0.3 {
		t.Errorf("Contrast = %v, want 0.3 (tone included)", out.Contrast)
	}
	if out.Clarity != 0 || out.Vignette != 0 || out.Sharpen != 0 {
		t.Errorf("excluded sections leaked: clarity=%v vignette=%v sharpen=%v",
			out.Clarity, out.Vignette, out.Sharpen)
	}
}

func TestPresetLookAllSectionsWhenEmpty(t *testing.T) {
	p := UserPreset{
		ID:     "x",
		Name:   "legacy",
		Params: edit.Params{Contrast: 0.3, Clarity: 0.2, Vignette: -0.4},
	}
	out := presetLook(p, 0)
	if out.Contrast != 0.3 || out.Clarity != 0.2 || out.Vignette != -0.4 {
		t.Errorf("empty sections must mean all: %+v", out)
	}
	// Unknown ids (a newer build's section) match nothing rather than
	// flipping the preset to all-sections.
	p.Sections = []string{"someday-a-new-section"}
	out = presetLook(p, 0)
	if out.Contrast != 0.3 {
		t.Errorf("all-unknown sections should fall back to all, got %+v", out)
	}
}

func TestSeedExpEVReanchor(t *testing.T) {
	// Saved on a +1.3 EV photo at dial +1.8 (creative +0.5); target measured
	// +0.4 → lands at +0.9.
	p := UserPreset{Params: edit.Params{ExpEV: 1.8}, BaseExpEV: 1.3}
	if got := seedExpEV(p, 0.4); got != 0.9 {
		t.Errorf("re-anchored EV = %v, want 0.9", got)
	}
	// Legacy preset (unknown source baseline): absolute lands as stored.
	p = UserPreset{Params: edit.Params{ExpEV: 1.8}}
	if got := seedExpEV(p, 0.4); got != 1.8 {
		t.Errorf("legacy absolute EV = %v, want 1.8", got)
	}
	// Relative: creative delta on the seeded baseline.
	p = UserPreset{Params: edit.Params{ExpEV: 0.5}, Relative: true}
	if got := seedExpEV(p, 0.4); got != 0.9 {
		t.Errorf("relative EV = %v, want 0.9", got)
	}
	// Unmeasured target (baseEV 0): the creative offset lands from zero.
	p = UserPreset{Params: edit.Params{ExpEV: 1.8}, BaseExpEV: 1.3}
	if got := seedExpEV(p, 0); got != 0.5 {
		t.Errorf("unmeasured-target EV = %v, want 0.5", got)
	}
}

func TestPresetLookClampsEV(t *testing.T) {
	p := UserPreset{Params: edit.Params{ExpEV: 4.5}, BaseExpEV: -2, Sections: []string{"tone"}}
	// creative = 6.5, target base 0 → 6.5, clamped to 5.
	if out := presetLook(p, 0); out.ExpEV != 5 {
		t.Errorf("ExpEV = %v, want clamp at 5", out.ExpEV)
	}
}

func TestDefaultPresetResolver(t *testing.T) {
	r := defaultPresetResolver{
		defaults: map[string]string{"Sony ILCE-7RM3": "a", "*": "b"},
		presets: map[string]UserPreset{
			"a": {ID: "a", Name: "sony"},
			"b": {ID: "b", Name: "any"},
			"c": {ID: "c", Name: "adaptive", AutoSections: []string{"tone"}},
		},
	}
	if up := r.forPhoto(photoWithCamera("Sony", "ILCE-7RM3")); up == nil || up.ID != "a" {
		t.Errorf("exact camera match should win, got %+v", up)
	}
	if up := r.forPhoto(photoWithCamera("Canon", "EOS R5")); up == nil || up.ID != "b" {
		t.Errorf("fallthrough to *, got %+v", up)
	}
	// A deleted preset id resolves to nothing (seed plain baseline).
	r.defaults["*"] = "gone"
	if up := r.forPhoto(photoWithCamera("Canon", "EOS R5")); up != nil {
		t.Errorf("deleted preset id must not resolve, got %+v", up)
	}
	// Adaptive presets are never seeded.
	r.defaults["*"] = "c"
	if up := r.forPhoto(photoWithCamera("Canon", "EOS R5")); up != nil {
		t.Errorf("adaptive preset must not resolve, got %+v", up)
	}
}
