package main

import (
	"math"
	"strings"
	"testing"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// A file as marraw's Electron app writes it reads here: unknown groups
// dropped, painted masks dropped, AI maps let go, and new ids.
func TestPresetFileFromElectron(t *testing.T) {
	file := `{
  "marrawUserPresets": 1,
  "presets": [
    {"id": "a1", "name": "Warm", "params": {"expEV": 0.5, "wbMode": "camera", "contrast": 0.2, "cropW": 0.5,
      "masks": [{"type": "linear", "x0": 0.1, "adjust": {}},
                {"type": "ai", "aiKind": "subject", "mapVer": "isnet-1", "x0": 0.3, "adjust": {"expEV": 0.4}},
                {"type": "range", "rangeLumaLo": 0.2, "rangeLumaHi": 0.8, "adjust": {}}]},
     "sections": ["tone", "crop"], "relative": true, "autoSections": ["tone", "bogus"]},
    {"id": "", "name": "No id", "params": {}},
    {"id": "b2", "name": "Bad", "params": {"contrast": "high"}}
  ]
}`
	got, err := parsePresetFile([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d presets, want the one whole one", len(got))
	}
	p := got[0]
	if p.ID == "a1" || p.ID == "" {
		t.Errorf("id %q: an import needs a new one", p.ID)
	}
	if strings.Join(p.Sections, ",") != "tone" || strings.Join(p.AutoSections, ",") != "tone" {
		t.Errorf("sections %v, autos %v", p.Sections, p.AutoSections)
	}
	if p.Params.CropW != 0 || p.Params.Contrast != 0.2 || !p.Relative {
		t.Errorf("params %+v", p.Params)
	}
	if len(p.Params.Masks) != 2 || p.Params.Masks[0].MapVer != "" || p.Params.Masks[0].X0 != 0 ||
		p.Params.Masks[0].Adjust.ExpEV != 0.4 || p.Params.Masks[1].Type != "range" {
		t.Errorf("masks %+v", p.Params.Masks)
	}
}

// What this app writes, it reads back as it was, but for new ids.
func TestPresetFileRoundTrip(t *testing.T) {
	in := []marrawclient.UserPreset{{ID: "x", Name: "Look", Params: marrawclient.Params{Contrast: 0.3,
		Masks: []marrawclient.Mask{{Type: "ai", AIKind: "depth", DepthLo: 0.6, DepthHi: 1}}}, BaseExpEV: 1.2}}
	b, err := presetFileBytes(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "{\n  \"marrawUserPresets\": 1,\n  \"presets\": [") {
		t.Errorf("file starts %q", string(b[:40]))
	}
	got, err := parsePresetFile(b)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v, %d presets", err, len(got))
	}
	g := got[0]
	if g.Name != "Look" || g.Params.Contrast != 0.3 || g.BaseExpEV != 1.2 || len(g.Params.Masks) != 1 || g.Params.Masks[0].DepthLo != 0.6 {
		t.Errorf("read back %+v", g)
	}
}

func TestPresetFileRefused(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{"not json", "not a JSON file"},
		{`[1, 2]`, "not a marraw preset file"},
		{`{"presets": []}`, "not a marraw preset file"},
		{`{"marrawUserPresets": 2, "presets": []}`, "newer marraw"},
	} {
		if _, err := parsePresetFile([]byte(c.file)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.file, err, c.want)
		}
	}
}

func TestPresetName(t *testing.T) {
	ps := []marrawclient.UserPreset{{Name: "Look"}, {Name: "Look (2)"}}
	if got := presetName("Look", ps); got != "Look (3)" {
		t.Errorf("got %q", got)
	}
	if got := presetName("Other", ps); got != "Other" {
		t.Errorf("got %q", got)
	}
}

// A copy is always numbered, from its original's name without a number,
// as marraw's uniqueName names a duplicate.
func TestCopyName(t *testing.T) {
	ps := []marrawclient.UserPreset{{Name: "Look"}, {Name: "Look (2)"}, {Name: "Mix (a)"}}
	for in, want := range map[string]string{"Look": "Look (3)", "Look (2)": "Look (3)", "Other": "Other (2)",
		"Mix (a)": "Mix (a) (2)", "Old (12)": "Old (2)"} {
		if got := copyName(in, ps); got != want {
			t.Errorf("copy of %q: got %q, want %q", in, got, want)
		}
	}
}

// The smart masks travel, hidden and painted ones do not, and a preset
// of them alone is told from one that carries a look.
func TestMaskRecipes(t *testing.T) {
	p := marrawclient.Params{Contrast: 0.4, Masks: []marrawclient.Mask{
		{Type: "radial"}, {Type: "ai", AIKind: "subject", MapVer: "v1"}, {Type: "range", Disabled: true}, {Type: "range"},
	}}
	r := maskRecipes(p)
	if len(r) != 2 || r[0].MapVer != "" || r[1].Type != "range" {
		t.Fatalf("recipes %+v", r)
	}
	only := marrawclient.UserPreset{Relative: true, Params: marrawclient.Params{WBMode: "camera", Masks: r}}
	if !isMasksOnlyPreset(only) {
		t.Error("a preset on neutral with masks is masks only")
	}
	look := only
	look.Params.Contrast = 0.1
	if isMasksOnlyPreset(look) {
		t.Error("a preset with a look is not masks only")
	}
	if presetBadge(only) != "masks" {
		t.Errorf("badge %q", presetBadge(only))
	}
}

// An adaptive preset laid over the photo's own auto gives the edit it was
// made from back. The auto, as the backend's, is the draft with only its
// own fields changed; the rest come back as drawn, so the preset keeps
// them as drawn.
func TestAdaptiveLookDiff(t *testing.T) {
	draft := marrawclient.Params{ExpEV: 1.5, Contrast: 0.5, Saturation: 0.3, Bright: 1.4, Clarity: 0.4, Vignette: -0.3, BW: true}
	draft.HSLSat[2] = 0.2
	auto := draft
	auto.ExpEV, auto.Contrast = 1.2, 0.1
	diff := adaptiveLookDiff(draft, auto, []string{"tone", "color"})
	if diff.Saturation != 0 || math.Abs(diff.Contrast-0.4) > 1e-9 || math.Abs(diff.ExpEV-0.3) > 1e-9 || !diff.BW {
		t.Errorf("diff %+v", diff)
	}
	if diff.Clarity != 0.4 || diff.Vignette != -0.3 || diff.HSLSat[2] != 0.2 || diff.Bright != 1.4 {
		t.Errorf("diff dropped what no auto writes: %+v", diff)
	}
	// On another photo, the same: its own auto and the rest of its edit
	// neutral.
	other := marrawclient.Params{ExpEV: 0.6, Contrast: 0.2, Saturation: 0.1}
	p := marrawclient.UserPreset{Params: diff, Relative: true, AutoSections: []string{"tone", "color"}}
	got := applyUserPreset(other, p, 0)
	if math.Abs(got.Contrast-0.6) > 1e-9 || math.Abs(got.ExpEV-0.9) > 1e-9 || got.Saturation != 0.1 ||
		math.Abs(got.Clarity-0.4) > 1e-9 || math.Abs(got.Vignette+0.3) > 1e-9 || math.Abs(got.HSLSat[2]-0.2) > 1e-9 || !got.BW {
		t.Errorf("applied %+v", got)
	}
	// Not relative, the rest lands as drawn, over the photo's own.
	other.Clarity = 0.1
	p.Relative = false
	if got := applyUserPreset(other, p, 0); math.Abs(got.Clarity-0.4) > 1e-9 || math.Abs(got.Contrast-0.6) > 1e-9 {
		t.Errorf("applied absolute %+v", got)
	}
	// Tone not auto, its fields are kept as drawn.
	if d := adaptiveLookDiff(draft, auto, []string{"color"}); d.Contrast != 0.5 || d.ExpEV != 1.5 {
		t.Errorf("tone not auto: %+v", d)
	}
}
