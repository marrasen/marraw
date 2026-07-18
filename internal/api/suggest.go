package api

import (
	"context"

	"github.com/marrasen/marraw/internal/aimask"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/pyramid"
)

// Suggestion is one scene-conditioned candidate look: a full edit state
// based on the caller's current params with only the tone/color/effects
// fields replaced — geometry, white balance, masks and spots pass through
// (the AutoAdjust contract).
type Suggestion struct {
	ID     string      `json:"id"`
	Label  string      `json:"label"`
	Params edit.Params `json:"params"`
}

// SuggestResult carries the candidates plus whether generating the class
// map would unlock scene-specific ones (the client may offer the existing
// analyze/consent flow — suggestions themselves never trigger inference).
type SuggestResult struct {
	Suggestions   []Suggestion `json:"suggestions"`
	NeedsClassMap bool         `json:"needsClassMap"`
}

// SuggestEdits computes 3–5 suggested looks for the photo from the current
// decode's histograms plus whatever AI maps are already cached (subject
// matte weights the exposure metering; the class map gates scene-specific
// recipes). Pure arithmetic over the warm preview decode: nothing is
// persisted, no task is opened, no model is downloaded — the client applies
// a picked candidate through the normal SetEditParams path.
func (e *Edits) SuggestEdits(ctx context.Context, photoID int64, params edit.Params) (*SuggestResult, error) {
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	rgba, err := e.previewDecode(ctx, photoID, photo, &params)
	if err != nil {
		return nil, err
	}
	gamma := photo.LookGamma
	if gamma == 0 {
		gamma = pyramid.FallbackLookGamma
	}
	// Cached-map loads only — suggestions never trigger an inference.
	var subject *pyramid.AIMap
	if ver, ok := aimask.MapVerFor(edit.AISubject); ok && e.deps.Cache.AIMaps != nil {
		subject = e.deps.Cache.AIMaps.Load(photo.CacheKey, edit.AISubject, ver)
	}
	profile := pyramid.SceneProfile{}
	if ver, ok := aimask.MapVerFor(edit.AIClass); ok && e.deps.Cache.AIMaps != nil {
		if m := e.deps.Cache.AIMaps.Load(photo.CacheKey, edit.AIClass, ver); m != nil {
			profile.HasClassMap = true
			for _, c := range aimask.DetectCategories(m.Pix) {
				switch c.ID {
				case aimask.CatSky:
					profile.Sky = c.Fraction
				case aimask.CatPeople:
					profile.People = c.Fraction
				case aimask.CatFoliage:
					profile.Foliage = c.Fraction
				case aimask.CatWater:
					profile.Water = c.Fraction
				case aimask.CatMountains:
					profile.Mountains = c.Fraction
				case aimask.CatArchitecture:
					profile.Architecture = c.Fraction
				}
			}
		}
	}

	stats := pyramid.GatherSceneStats(rgba, gamma, subject)
	res := &SuggestResult{NeedsClassMap: !profile.HasClassMap}
	for _, c := range pyramid.SuggestLooks(stats, profile, params) {
		res.Suggestions = append(res.Suggestions, Suggestion{ID: c.ID, Label: c.Label, Params: c.Params})
	}
	return res, nil
}
