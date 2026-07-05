package api

import (
	"context"
	"encoding/json"
	"os"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
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
// returns its hash; the client swaps the loupe image to the new URL.
// The photo's unpacked handle is kept hot, so repeated calls while dragging
// a slider skip file reading entirely.
func (e *Edits) PreviewEdit(ctx context.Context, photoID int64, params edit.Params) (*PreviewResult, error) {
	hash := params.Hash()
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(e.deps.Cache.PathFor(photo.CacheKey, "2048", hash)); err == nil {
		return &PreviewResult{EditHash: hash}, nil // already rendered
	}

	proc, release, err := e.deps.Handles.Acquire(photoID, photo.Path())
	if err != nil {
		return nil, err
	}
	defer release()
	if ctx.Err() != nil {
		return nil, ctx.Err() // superseded while waiting for the handle
	}

	img, err := proc.Process(params.LibrawParams(true))
	if err != nil {
		return nil, err
	}
	rgba, err := pyramid.FromLibraw(img)
	if err != nil {
		return nil, err
	}
	if err := e.deps.Cache.WritePreview(rgba, photo.CacheKey, hash); err != nil {
		return nil, err
	}
	return &PreviewResult{EditHash: hash}, nil
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
	for _, id := range ids {
		if err := e.saveEdit(ctx, id, &params); err != nil {
			return err
		}
		aprot.TriggerRefresh(ctx, editKey(id))
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

// saveEdit persists params (nil or neutral clears), broadcasts the patch,
// and warms the new grid thumbnail in the background.
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
	h := hash
	e.deps.Broadcast(&PhotoPatchEvent{Patches: []PhotoPatch{{ID: photoID, EditHash: &h}}})

	// Warm the grid thumb for the new state so the grid updates without a
	// scroll-triggered fetch racing the patch event.
	if p, err := e.deps.DB.GetPhoto(context.WithoutCancel(ctx), photoID); err == nil {
		go e.deps.Cache.Ensure(context.Background(), p, "512", hash, decode.PriorityVisible)
	}
	return nil
}
