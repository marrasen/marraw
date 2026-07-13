package api

import (
	"context"
	"fmt"

	"github.com/marrasen/aprot/tasks"

	"github.com/marrasen/marraw/internal/aimask"
	"github.com/marrasen/marraw/internal/edit"
)

// AIMapResult reports the generated (or already present) map's version tag;
// the client stamps it into the mask's mapVer so the edit hash pins the
// generating model.
type AIMapResult struct {
	MapVer string `json:"mapVer"`
}

// GenerateAIMap ensures the model-generated map for (photo, kind) exists and
// returns its version tag. Idempotent and cheap when the map is already on
// disk — the client may call it freely (e.g. to restore maps for an edit
// that arrived via sidecar from another machine). The first use of a kind
// downloads its model (tens to hundreds of MB), surfaced as a shared task
// with progress; ctx cancellation aborts both download and generation.
//
// The inference input is a neutral base-orientation render, deliberately
// independent of the current develop settings and crop, so the map never
// shifts as the user edits.
func (e *Edits) GenerateAIMap(ctx context.Context, photoID int64, kind edit.AIKind) (*AIMapResult, error) {
	ver, ok := aimask.MapVerFor(kind)
	if !ok {
		return nil, fmt.Errorf("ai masks: %q has no model available yet", kind)
	}
	store := e.deps.Cache.AIMaps
	if store == nil || e.deps.Infer == nil {
		return nil, fmt.Errorf("ai masks: inference is not configured")
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	if store.Has(photo.CacheKey, kind, ver) {
		return &AIMapResult{MapVer: ver}, nil
	}

	rgba, err := e.previewDecode(ctx, photoID, photo, nil)
	if err != nil {
		return nil, err
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, "AI mask: "+photo.FileName, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "aimask"})
	gray, err := aimask.Generate(tctx, e.deps.Infer, kind, rgba, func(done, total int64) {
		task.Progress(int(done>>20), int(total>>20)) // model download, MB units
	})
	if err != nil {
		task.Err(err)
		return nil, err
	}
	if err := store.Save(photo.CacheKey, kind, ver, gray); err != nil {
		task.Err(err)
		return nil, err
	}
	task.Err(nil)
	return &AIMapResult{MapVer: ver}, nil
}
