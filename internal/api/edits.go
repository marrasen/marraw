package api

import (
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"math"
	"os"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/pyramid"
)

// Edits handles non-destructive editing.
type Edits struct {
	deps *Deps
}

// GetEditParams returns the stored edit state, or null when untouched.
func (e *Edits) GetEditParams(ctx context.Context, photoID int64) (*edit.Params, error) {
	aprot.RegisterRefreshTrigger(ctx, editKey(photoID))
	p, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	if !p.EditParams.Valid {
		return nil, nil
	}
	return edit.Parse(p.EditParams.String)
}

// PreviewEdit renders a 2048px preview of the (unsaved) edit state and
// returns the JPEG itself as a binary Blob riding the WebSocket — no second
// HTTP round trip. The rendition is also written to the pyramid cache, so a
// following commit serves the same pixels instantly over /img.
// The photo's unpacked handle is kept hot, so repeated calls while dragging
// a slider skip file reading entirely.
func (e *Edits) PreviewEdit(ctx context.Context, photoID int64, params edit.Params) (*aprot.Blob, error) {
	path, err := e.ensurePreview(ctx, photoID, params)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &aprot.Blob{ContentType: "image/jpeg", Data: data}, nil
}

// ensurePreview guarantees the 2048 rendition of the given (possibly
// unsaved) edit state exists in the cache and returns its path.
func (e *Edits) ensurePreview(ctx context.Context, photoID int64, params edit.Params) (string, error) {
	hash := params.Hash()
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return "", err
	}
	path := e.deps.Cache.PathFor(photo.CacheKey, "2048", hash)
	if _, err := os.Stat(path); err == nil {
		return path, nil // already rendered
	}

	// Neutral params must render the exact base look (auto-brighten), not
	// the deterministic edit pipeline — they share the "base" cache slot.
	var ep *edit.Params
	if !params.IsNeutral() {
		ep = &params
	}

	proc, release, err := e.deps.Handles.Acquire(photoID, photo.Path())
	if err != nil {
		return "", err
	}
	defer release()
	if ctx.Err() != nil {
		return "", ctx.Err() // superseded while waiting for the handle
	}

	img, err := proc.Process(ep.LibrawParams(true))
	if err != nil {
		return "", err
	}
	rgba, err := pyramid.FromLibraw(img)
	if err != nil {
		return "", err
	}
	gamma := photo.LookGamma
	if gamma == 0 {
		gamma = pyramid.FallbackLookGamma
	}
	if err := e.deps.Cache.WritePreview(rgba, photo.CacheKey, hash, gamma, ep); err != nil {
		return "", err
	}
	return path, nil
}

// PickWhiteBalance samples the current rendition at the given relative
// coordinates (0..1 in the displayed, orientation-corrected image) and
// returns the edit state that makes that spot neutral: wbMode=custom with
// computed multipliers. The inverse of the display curve is approximate, so
// a second pick refines the result.
func (e *Edits) PickWhiteBalance(ctx context.Context, photoID int64, params edit.Params, x, y float64) (*edit.Params, error) {
	if x < 0 || x > 1 || y < 0 || y > 1 {
		return nil, aprot.ErrInvalidParams("pick coordinates must be within 0..1")
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	path, err := e.ensurePreview(ctx, photoID, params)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	img, err := jpeg.Decode(f)
	f.Close()
	if err != nil {
		return nil, err
	}

	lookGamma := photo.LookGamma
	if lookGamma == 0 {
		lookGamma = pyramid.FallbackLookGamma
	}
	// The look's saturation boost scales with the edit's Saturation; the
	// floor keeps a near-grayscale edit from exploding the inversion.
	satFactor := math.Max(0.2, 1.15*(1+params.Saturation))
	rl, gl, bl := samplePatchLinear(img, x, y, lookGamma, satFactor)
	if gl < 1e-4 || rl < 1e-4 || bl < 1e-4 {
		return nil, aprot.ErrInvalidParams("picked area is too dark — pick a brighter neutral area")
	}

	// The effective multipliers the sampled rendition was produced with.
	base := params.WBMul
	temp := params.WBTemp
	if params.WBMode == edit.WBKelvin && params.WBKelvin > 0 {
		proc, release, err := e.deps.Handles.Acquire(photoID, photo.Path())
		if err != nil {
			return nil, err
		}
		base = proc.KelvinMul(params.WBKelvin)
		release()
		temp = 0
	} else if params.WBMode != edit.WBCustom || base == ([4]float64{}) {
		proc, release, err := e.deps.Handles.Acquire(photoID, photo.Path())
		if err != nil {
			return nil, err
		}
		base = proc.CamMul()
		release()
	}
	eff := libraw.AdjustWB(base, temp, params.WBTint)

	// Scale so the sampled patch comes out neutral, then normalize G to 1.
	mul := eff
	mul[0] *= gl / rl
	mul[2] *= gl / bl
	if mul[3] > 0 {
		mul[3] = mul[1]
	}
	for i := range mul {
		mul[i] /= eff[1]
	}

	out := params
	out.WBMode = edit.WBCustom
	out.WBMul = mul
	out.WBTemp, out.WBTint, out.WBKelvin = 0, 0, 0
	return &out, nil
}

// samplePatchLinear averages a small patch around (x,y) in approximately
// linear light: the display look (BT.709 gamma × calibrated lift) is
// inverted with a single combined power, and the look's saturation boost
// (satFactor) is undone around luma. The edit's tone-curve adjustments are
// not inverted — the pick is approximate by design and a second pick
// refines it.
func samplePatchLinear(img image.Image, x, y, lookGamma, satFactor float64) (r, g, b float64) {
	bnd := img.Bounds()
	cx := bnd.Min.X + int(x*float64(bnd.Dx()-1))
	cy := bnd.Min.Y + int(y*float64(bnd.Dy()-1))
	const rad = 3
	decodePow := 2.222 / lookGamma

	var n float64
	for py := cy - rad; py <= cy+rad; py++ {
		for px := cx - rad; px <= cx+rad; px++ {
			if px < bnd.Min.X || px >= bnd.Max.X || py < bnd.Min.Y || py >= bnd.Max.Y {
				continue
			}
			pr, pg, pb, _ := img.At(px, py).RGBA() // 16-bit
			fr, fg, fb := float64(pr)/65535, float64(pg)/65535, float64(pb)/65535
			// Undo the look's saturation boost around Rec.601 luma.
			luma := 0.299*fr + 0.587*fg + 0.114*fb
			fr = luma + (fr-luma)/satFactor
			fg = luma + (fg-luma)/satFactor
			fb = luma + (fb-luma)/satFactor
			r += math.Pow(math.Max(0, fr), decodePow)
			g += math.Pow(math.Max(0, fg), decodePow)
			b += math.Pow(math.Max(0, fb), decodePow)
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return r / n, g / n, b / n
}

// SetEditParams persists the edit state (neutral params clear it).
func (e *Edits) SetEditParams(ctx context.Context, photoID int64, params edit.Params) error {
	if err := e.saveEdit(ctx, photoID, &params); err != nil {
		return err
	}
	aprot.TriggerRefresh(ctx, editKey(photoID))
	return nil
}

// ResetEdits clears the edit state of the given photos.
func (e *Edits) ResetEdits(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if err := e.saveEdit(ctx, id, nil); err != nil {
			return err
		}
		aprot.TriggerRefresh(ctx, editKey(id))
	}
	return nil
}

// PasteEditParams applies one edit state to many photos (the copy side is
// client-local: GetEditParams into a clipboard).
func (e *Edits) PasteEditParams(ctx context.Context, ids []int64, params edit.Params) error {
	for i, id := range ids {
		if err := e.saveEdit(ctx, id, &params); err != nil {
			return err
		}
		aprot.TriggerRefresh(ctx, editKey(id))
		aprot.Progress(ctx).Update(i+1, len(ids), "")
	}
	return nil
}

// ApplyBatchEdit applies a relative adjustment to many photos, e.g.
// "+0.5 EV on the current selection".
func (e *Edits) ApplyBatchEdit(ctx context.Context, ids []int64, delta edit.Delta) error {
	for i, id := range ids {
		p, err := e.deps.DB.GetPhoto(ctx, id)
		if err != nil {
			return err
		}
		var params edit.Params
		if p.EditParams.Valid {
			if ep, err := edit.Parse(p.EditParams.String); err == nil {
				params = *ep
			}
		}
		delta.Apply(&params)
		if err := e.saveEdit(ctx, id, &params); err != nil {
			return err
		}
		aprot.TriggerRefresh(ctx, editKey(id))
		aprot.Progress(ctx).Update(i+1, len(ids), p.FileName)
	}
	return nil
}

// saveEdit persists params (nil or neutral clears), pushes the patch to
// folder subscribers, and warms the new grid thumbnail in the background.
func (e *Edits) saveEdit(ctx context.Context, photoID int64, params *edit.Params) error {
	params.Normalize()
	var jsonPtr *string
	hash := edit.BaseHash
	if !params.IsNeutral() {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		s := string(b)
		jsonPtr = &s
		hash = params.Hash()
	}
	if err := e.deps.DB.SetEdit(ctx, photoID, jsonPtr, hash); err != nil {
		return err
	}

	// Warm the grid thumb for the new state so the grid updates without a
	// scroll-triggered fetch racing the patch.
	if p, err := e.deps.DB.GetPhoto(context.WithoutCancel(ctx), photoID); err == nil {
		h := hash
		e.deps.patchFolderPhotos(p.FolderID, []PhotoPatch{{ID: photoID, EditHash: &h}})
		go e.deps.Cache.Ensure(context.Background(), p, "512", hash, decode.PriorityVisible)
	}
	return nil
}
