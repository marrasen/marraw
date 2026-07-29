package api

import (
	"context"

	"github.com/marrasen/marraw/internal/lens"
)

// LensProfileInfo describes what the lens database knows about one photo:
// which profile was matched, and which of the three corrections it can
// actually make. The panel needs all of it — a slider for a correction the
// profile doesn't carry would be a lie, and "no profile" has three different
// causes the photographer can act on differently.
type LensProfileInfo struct {
	// Lens is the lens as the FILE describes it, "" when the RAW recorded no
	// lens at all (adapted and fully manual glass usually doesn't).
	Lens string `json:"lens"`
	// Profile is the matched database entry's full name, "" when nothing
	// matched confidently.
	Profile string `json:"profile"`
	// CameraKnown reports whether the body itself is in the database. It is
	// a precondition for matching a lens — without the body's crop factor
	// there is no way to place the coefficients in the frame — so a false
	// here explains an empty Profile that an unknown lens would not.
	CameraKnown bool `json:"cameraKnown"`
	// Focal and Aperture are the values the coefficients were interpolated
	// to, echoed back so the panel can show what the correction is for.
	Focal    float64 `json:"focal"`
	Aperture float64 `json:"aperture"`
	// What the matched profile actually measured. A lens can easily carry
	// distortion and CA but no vignetting.
	HasDistortion bool `json:"hasDistortion"`
	HasVignetting bool `json:"hasVignetting"`
	HasCA         bool `json:"hasCA"`
}

// LensProfile reports the lens profile matched for one photo, so the develop
// panel can name it and enable only the corrections it provides. Cheap and
// side-effect free: the match is memoized per photo by the render cache.
func (e *Edits) LensProfile(ctx context.Context, photoID int64) (*LensProfileInfo, error) {
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	out := &LensProfileInfo{
		Lens:        photo.Lens,
		CameraKnown: lens.FindCamera(photo.Make, photo.Model) != nil,
		Focal:       photo.FocalLen,
		Aperture:    photo.Aperture,
	}
	if c := e.deps.Cache.Lenses.For(photo); c != nil {
		out.Profile = c.Name
		out.HasDistortion = c.HasDist()
		out.HasVignetting = c.HasVig
		out.HasCA = c.HasTCA
	}
	return out, nil
}
