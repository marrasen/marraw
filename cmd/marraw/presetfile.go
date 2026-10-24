package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/marrasen/gunim/driver"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The user presets travel in a file of marraw's own, the same both apps
// write and read, so presets move between them.

// presetFile is the file's shape: its version and the presets.
type presetFile struct {
	Version int                       `json:"marrawUserPresets"`
	Presets []marrawclient.UserPreset `json:"presets"`
}

// presetFileName is the name a file of presets is offered under.
const presetFileName = "marraw-presets.json"

// presetFilter is the kind of file presets are kept in, for the system's
// dialogs.
var presetFilter = []driver.FileFilter{{Name: "marraw presets", Patterns: []string{"*.json"}}}

// presetFileBytes is presets as the file holds them.
func presetFileBytes(presets []marrawclient.UserPreset) ([]byte, error) {
	return json.MarshalIndent(presetFile{Version: 1, Presets: presets}, "", "  ")
}

// parsePresetFile reads the presets of a file, as marraw's
// parseUserPresetsFile does: each is checked, and one that is not whole
// is left out, and each has a new id, so an import adds and never
// replaces.
func parsePresetFile(b []byte) ([]marrawclient.UserPreset, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		if !json.Valid(b) {
			return nil, errors.New("not a JSON file")
		}
		return nil, errors.New("not a marraw preset file")
	}
	v, ok := top["marrawUserPresets"]
	if !ok {
		return nil, errors.New("not a marraw preset file")
	}
	var version float64
	if json.Unmarshal(v, &version) != nil || version > 1 {
		return nil, errors.New("preset file from a newer marraw: update to import it")
	}
	var raw []json.RawMessage
	_ = json.Unmarshal(top["presets"], &raw)
	var out []marrawclient.UserPreset
	for _, r := range raw {
		var p marrawclient.UserPreset
		if json.Unmarshal(r, &p) != nil || p.ID == "" || p.Name == "" {
			continue
		}
		out = append(out, cleanPreset(p))
	}
	return out, nil
}

// cleanPreset is p as it may be kept: groups and autos marraw knows, a
// look with no geometry, and only smart masks, their maps let go.
func cleanPreset(p marrawclient.UserPreset) marrawclient.UserPreset {
	known := func(ids []string, ok func(string) bool) []string {
		var out []string
		for _, id := range ids {
			if ok(id) && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
		return out
	}
	p.Sections = known(p.Sections, func(id string) bool { return presetGroupAt(id) >= 0 })
	p.AutoSections = known(p.AutoSections, func(id string) bool {
		return slices.ContainsFunc(presetAutos, func(a struct{ id, label string }) bool { return a.id == id })
	})
	var masks []marrawclient.Mask
	for _, m := range p.Params.Masks {
		r := marrawclient.Mask{Type: m.Type, Invert: m.Invert, Feather: m.Feather, Adjust: m.Adjust}
		switch {
		case m.Type == "ai" && slices.Contains([]marrawclient.AIKind{marrawclient.AIKindSubject, marrawclient.AIKindBackground,
			marrawclient.AIKindClass, marrawclient.AIKindDepth, marrawclient.AIKindPerson}, m.AIKind):
			r.AIKind, r.ClassID, r.DepthLo, r.DepthHi, r.Threshold, r.Remove = m.AIKind, m.ClassID, m.DepthLo, m.DepthHi, m.Threshold, m.Remove
		case m.Type == "range":
			r.RangeLumaLo, r.RangeLumaHi, r.RangeHueLo, r.RangeHueHi, r.RangeSatMin = m.RangeLumaLo, m.RangeLumaHi, m.RangeHueLo, m.RangeHueHi, m.RangeSatMin
		default:
			continue
		}
		masks = append(masks, r)
	}
	p.Params = stripToLook(p.Params)
	if p.Params.WBMode == marrawclient.WBModeCamera {
		// The camera's, as a preset leaves white balance alone.
		p.Params.WBMode = ""
	}
	p.Params.Masks = masks
	p.ID = newPresetID()
	return p
}

// presetName is name, or with (2), (3) and so on after it, so no preset
// in ps has it.
func presetName(name string, ps []marrawclient.UserPreset) string {
	taken := func(n string) bool {
		return slices.ContainsFunc(ps, func(p marrawclient.UserPreset) bool { return p.Name == n })
	}
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s (%d)", name, i); !taken(n) {
			return n
		}
	}
}

// presetsExport writes the user presets to a file the user chooses.
func (cu *culler) presetsExport() {
	if cu.ui == nil || len(cu.ui.UserPresets) == 0 {
		return
	}
	go func() {
		path, err := cu.c.SaveFile(cu.ctx, driver.SaveOptions{Title: "Export presets", Name: presetFileName,
			Filters: presetFilter})
		cu.onDo(func() {
			if err != nil {
				cu.fail("The presets could not be exported", err)
			}
			if path != "" {
				cu.presetsWrite(path)
			}
		})
	}()
}

// presetsWrite writes the user presets to the file at path.
func (cu *culler) presetsWrite(path string) {
	if cu.ui == nil || len(cu.ui.UserPresets) == 0 {
		return
	}
	all := slices.Clone(cu.ui.UserPresets)
	go func() {
		b, err := presetFileBytes(all)
		if err == nil {
			err = os.WriteFile(path, b, 0o644)
		}
		cu.onDo(func() {
			if err != nil {
				cu.fail("The presets could not be exported", err)
				return
			}
			cu.notify(fmt.Sprintf("Exported %d preset%s to %s", len(all), map[bool]string{false: "s", true: ""}[len(all) == 1], path))
		})
	}()
}

// presetsImport adds the presets of a file the user chooses after the
// user's own.
func (cu *culler) presetsImport() {
	go func() {
		paths, err := cu.c.ChooseFiles(cu.ctx, driver.ChooseOptions{Title: "Import presets", Filters: presetFilter})
		cu.onDo(func() {
			if err != nil {
				cu.fail("The presets could not be imported", err)
			}
			if len(paths) > 0 {
				cu.presetsRead(paths[0])
			}
		})
	}()
}

// presetsRead adds the presets of the file at path after the user's own.
func (cu *culler) presetsRead(path string) {
	go func() {
		b, err := os.ReadFile(path)
		var got []marrawclient.UserPreset
		if err == nil {
			got, err = parsePresetFile(b)
		}
		cu.onDo(func() { cu.presetsImported(got, err) })
	}()
}

// presetsImported adds presets read from a file, each named apart from
// those there, or tells why none were.
func (cu *culler) presetsImported(got []marrawclient.UserPreset, err error) {
	if err != nil {
		cu.fail("Import failed", err)
		return
	}
	if len(got) == 0 {
		cu.notify("No presets found in the file")
		return
	}
	if cu.ui == nil {
		// The presets had not loaded: saving these alone would drop
		// the others.
		cu.notify("Your presets have not loaded yet, so nothing was imported")
		return
	}
	ps := slices.Clone(cu.ui.UserPresets)
	for _, p := range got {
		p.Name = presetName(p.Name, ps)
		ps = append(ps, p)
	}
	cu.ui.UserPresets = ps
	cu.savePresets()
	cu.notify(fmt.Sprintf("Imported %d preset%s", len(got), map[bool]string{false: "s", true: ""}[len(got) == 1]))
}
