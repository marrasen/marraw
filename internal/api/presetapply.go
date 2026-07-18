package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

// The server-side mirror of the client's user-preset apply — used for the
// ONE apply that can't run in the client: seeding a configured default
// preset onto a freshly calibrated, never-edited photo (jobs.go
// calibratePass). Grid thumbs, prerender and export key off the stored edit
// hash, so the seed must be a real persisted edit; a virtual read-time
// merge in GetEditParams would never reach them.

// presetLookSections narrows a preset's stored section list to the known
// look groups; empty (or all-unknown) means every section — the legacy
// shape. Lockstep with presetSections in client/src/lib/presetSections.ts.
func presetLookSections(p UserPreset) map[string]bool {
	all := []string{"tone", "presence", "wb", "color", "effects", "detail"}
	known := map[string]bool{}
	for _, s := range all {
		known[s] = true
	}
	set := map[string]bool{}
	for _, s := range p.Sections {
		if known[s] {
			set[s] = true
		}
	}
	if len(set) == 0 {
		for _, s := range all {
			set[s] = true
		}
	}
	return set
}

// presetLook composes the develop state a default preset seeds onto an
// untouched photo: the preset's included look sections over a neutral
// draft, with exposure re-anchored to the photo's measured camera-mimic
// baseline. The draft being neutral collapses the client's
// absolute/relative distinction — a delta-from-neutral lands on neutral, so
// both modes reduce to copying the stored field — which keeps this mirror
// to the section filter plus the exposure formula (lockstep with
// applyUserPreset / presetExpEV in client/src/lib/presetSections.ts).
// Adaptive presets (AutoSections) need a decode + AutoAdjust and are not
// seeded; the Settings UI excludes them from the default-preset choices and
// the caller skips them.
func presetLook(p UserPreset, baseEV float64) edit.Params {
	s := presetLookSections(p)
	src := p.Params
	out := edit.Params{ExpEV: baseEV}
	if s["tone"] {
		out.ExpEV = clampF(seedExpEV(p, baseEV), -5, 5)
		out.ExpPreserve = src.ExpPreserve
		out.Bright = src.Bright
		out.Gamma = src.Gamma
		out.Shadow = src.Shadow
		out.Contrast = src.Contrast
		out.Whites = src.Whites
		out.Blacks = src.Blacks
		out.ToneShadows = src.ToneShadows
		out.ToneHighlights = src.ToneHighlights
	}
	if s["presence"] {
		out.Clarity = src.Clarity
		out.Texture = src.Texture
		out.Dehaze = src.Dehaze
	}
	if s["wb"] {
		out.WBMode = src.WBMode
		out.WBMul = src.WBMul
		out.WBTemp = src.WBTemp
		out.WBTint = src.WBTint
		out.WBKelvin = src.WBKelvin
	}
	if s["color"] {
		out.Saturation = src.Saturation
		out.Vibrance = src.Vibrance
		out.SplitShadowHue = src.SplitShadowHue
		out.SplitShadowAmt = src.SplitShadowAmt
		out.SplitHighlightHue = src.SplitHighlightHue
		out.SplitHighlightAmt = src.SplitHighlightAmt
		out.HSLHue = src.HSLHue
		out.HSLSat = src.HSLSat
		out.HSLLum = src.HSLLum
	}
	if s["effects"] {
		out.Vignette = src.Vignette
	}
	if s["detail"] {
		out.Sharpen = src.Sharpen
		out.Highlight = src.Highlight
		out.NRThreshold = src.NRThreshold
		out.FBDDNoiseRd = src.FBDDNoiseRd
		out.MedPasses = src.MedPasses
		out.Demosaic = src.Demosaic
		out.CARed = src.CARed
		out.CABlue = src.CABlue
	}
	return out
}

// seedExpEV resolves the exposure a preset lands at over a neutral draft
// whose seeded exposure is baseEV. The preset's stored ExpEV includes its
// SOURCE photo's baseline; the creative intent is the offset from it.
// BaseExpEV of 0 means unknown (legacy preset or unmeasured source): an
// absolute preset then lands as stored rather than double-compensating.
func seedExpEV(p UserPreset, baseEV float64) float64 {
	creative := p.Params.ExpEV - p.BaseExpEV
	if !p.Relative && p.BaseExpEV == 0 {
		return p.Params.ExpEV
	}
	return baseEV + creative
}

// defaultPresetResolver reads the configured per-camera default presets
// once and resolves a photo's default by "Make Model" key, falling back to
// the "*" any-camera entry. Adaptive presets never resolve (see presetLook).
type defaultPresetResolver struct {
	defaults map[string]string
	presets  map[string]UserPreset
}

func newDefaultPresetResolver(ctx context.Context, db *store.DB) defaultPresetResolver {
	r := defaultPresetResolver{defaults: jsonSetting(ctx, db, settingUIDefaultPresets, map[string]string{})}
	if len(r.defaults) > 0 {
		r.presets = map[string]UserPreset{}
		for _, up := range jsonSetting(ctx, db, settingUIUserPresets, []UserPreset{}) {
			r.presets[up.ID] = up
		}
	}
	return r
}

func cameraKey(make, model string) string {
	return strings.TrimSpace(strings.TrimSpace(make) + " " + strings.TrimSpace(model))
}

func (r defaultPresetResolver) forPhoto(p store.Photo) *UserPreset {
	if len(r.defaults) == 0 {
		return nil
	}
	id, ok := r.defaults[cameraKey(p.Make, p.Model)]
	if !ok {
		id, ok = r.defaults["*"]
	}
	if !ok {
		return nil
	}
	up, ok := r.presets[id]
	if !ok || len(up.AutoSections) > 0 {
		return nil
	}
	return &up
}

// seedDefaultPreset persists the default preset onto a photo that has never
// been edited, with the same post-save side effects as a user edit (folder
// patch, sidecar) minus the thumb warm. The untouched check and the write are one
// conditional statement (SetEditSeed), so a user edit racing the calibrate
// pass can never be clobbered — the seed just doesn't land.
func (d *Deps) seedDefaultPreset(ctx context.Context, photoID int64, up UserPreset, baseEV float64) error {
	params := presetLook(up, baseEV)
	params.Normalize()
	if params.IsNeutral() {
		return nil
	}
	b, err := json.Marshal(&params)
	if err != nil {
		return err
	}
	landed, err := d.DB.SetEditSeed(ctx, photoID, string(b), params.Hash(), time.Now().UnixMilli())
	if err != nil || !landed {
		return err
	}
	if p, err := d.DB.GetPhoto(ctx, photoID); err == nil {
		h := params.Hash()
		d.patchFolderPhotos(p.FolderID, []PhotoPatch{{ID: photoID, EditHash: &h}})
		d.writeSidecarFor(ctx, p)
		// No warmEdit here (unlike a user edit commit): the calibrate pass is
		// the sole caller, and a per-seed Prefetch warm would outrank the
		// remaining Background calibrate jobs in the shared pool — starving
		// the very pass that spawned it. It's also duplicated work: on-screen
		// cells re-fetch at Visible priority when the patch changes their img
		// URL, and the prerender pass that follows renders the seeded hash's
		// 2048, which yields the 512 the warm would have produced.
	}
	return nil
}
