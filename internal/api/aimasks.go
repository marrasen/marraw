package api

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"

	"github.com/marrasen/marraw/internal/aimask"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// AIMapResult reports the generated (or already present) map's version tag;
// the client stamps it into the mask's mapVer so the edit hash pins the
// generating model.
type AIMapResult struct {
	MapVer string `json:"mapVer"`
	// Generated reports that inference actually ran (false = the map was
	// already on disk). The client repaints only when true: nudging a render
	// for an already-present map forces a transient decode that cannot be
	// aborted (the HandleCache contract) — during rapid browsing those piled
	// up into multi-second "decoding RAW preview" stalls.
	Generated bool `json:"generated"`
	// Categories lists what a class map detected (class kind only), largest
	// area first — the UI offers one mask chip per entry.
	Categories []AICategory `json:"categories,omitempty"`
}

// AICategory is one detected semantic category in a class map.
type AICategory struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Fraction float64 `json:"fraction"`
}

// MaskTintPreview renders one mask's weight as a red-tinted transparent PNG
// in display space (the same OutputDims math as the render), so the develop
// overlay can stretch it 1:1 over the displayed image — the hover tint for
// AI masks, whose weights the client cannot compute itself. Sized to
// longEdge (default 1024). A missing AI map yields a fully transparent
// image, never an error.
func (e *Edits) MaskTintPreview(ctx context.Context, photoID int64, params edit.Params, maskIndex int, longEdge int) (*aprot.Blob, error) {
	if maskIndex < 0 || maskIndex >= len(params.Masks) {
		return nil, aprot.ErrInvalidParams("maskIndex out of range")
	}
	if longEdge <= 0 || longEdge > 2048 {
		longEdge = 1024
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	// Display-oriented full dims (the client's displayDims twin), then the
	// rendered (cropped) size, scaled down to the preview edge.
	dispW, dispH := photo.Width, photo.Height
	if photo.Orientation == 5 || photo.Orientation == 6 {
		dispW, dispH = dispH, dispW
	}
	ow, oh := params.OutputDims(dispW, dispH)
	if long := max(ow, oh); long > longEdge {
		ow, oh = ow*longEdge/long, oh*longEdge/long
	}
	ow, oh = max(1, ow), max(1, oh)

	ai := e.deps.Cache.AIMaps.SetFor(photo.CacheKey, &params)
	plane := pyramid.MaskWeightPlane(ow, oh, &params, maskIndex, ai)

	// The overlay red at 40% peak alpha — MaskTint's rgba(240,64,64,.4).
	img := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for i, w := range plane {
		img.Pix[i*4+0] = 240
		img.Pix[i*4+1] = 64
		img.Pix[i*4+2] = 64
		img.Pix[i*4+3] = uint8((int(w)*102 + 127) / 255)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return &aprot.Blob{ContentType: "image/png", Data: buf.Bytes()}, nil
}

// scoreSubjectMatte computes photo's subject-weighted focus score from its
// embedded thumb and subject matte, persists it (-1 = measured but no
// scoreable subject, hidden from clients by nonNegativeFloat), and on a real
// score pushes a granular per-photo patch rather than a full folder-list
// refresh. Shared by the calibrate-pass backfill and GenerateAIMap's immediate
// scoring so the sentinel convention and store call live in one place.
// Best-effort: callers ignore the returned error (the calibrate pass retries).
func (d *Deps) scoreSubjectMatte(ctx context.Context, photo store.Photo, thumb image.Image, matte *pyramid.AIMap) error {
	score, ok := pyramid.SubjectSharpnessScore(thumb, matte, photo.Orientation)
	if !ok {
		score = -1 // measured, no scoreable subject
	}
	if err := d.DB.SetSubjectSharpness(context.WithoutCancel(ctx), photo.ID, score); err != nil {
		return err
	}
	if score >= 0 {
		s := score
		d.patchFolderPhotos(photo.FolderID, []PhotoPatch{{ID: photo.ID, SubjectSharpness: &s}})
	}
	return nil
}

// scoreSubjectSharpnessAsync scores photo's subject matte off the RPC path the
// moment a subject map lands. It reuses the in-memory matte GenerateAIMap just
// produced (Save evicts the decoded plane from the map cache, so a Load here
// would be a guaranteed stat + PNG re-decode of pixels already in hand), and
// runs in its own goroutine so the multi-second inference RPC the client
// awaits to repaint the mask does not also block on a handle acquire, a
// multi-megapixel thumb read, and a jpeg decode. Best-effort.
func (e *Edits) scoreSubjectSharpnessAsync(photo store.Photo, matte *pyramid.AIMap) {
	go func() {
		ctx := context.Background()
		proc, release, err := e.deps.Handles.Acquire(photo.ID, photo.Path())
		if err != nil {
			return
		}
		thumb, err := proc.EmbeddedThumb()
		release()
		if err != nil {
			return
		}
		img, err := jpeg.Decode(bytes.NewReader(thumb))
		if err != nil {
			return
		}
		_ = e.deps.scoreSubjectMatte(ctx, photo, img, matte)
	}()
}

// grayToAIMap views a freshly-generated grayscale map as an AIMap for scoring,
// avoiding a round-trip through the PNG store. Returns nil if the plane is not
// tightly packed from the origin (SubjectSharpnessScore's indexing assumes
// stride == width), leaving the score to the calibrate pass's on-disk Load.
func grayToAIMap(g *image.Gray) *pyramid.AIMap {
	b := g.Bounds()
	if b.Min.X != 0 || b.Min.Y != 0 || g.Stride != b.Dx() {
		return nil
	}
	return &pyramid.AIMap{Pix: g.Pix, W: b.Dx(), H: b.Dy()}
}

// categoriesFor computes the detected-category chips from a stored class map.
func (e *Edits) categoriesFor(photoKey, ver string) []AICategory {
	m := e.deps.Cache.AIMaps.Load(photoKey, edit.AIClass, ver)
	if m == nil {
		return nil
	}
	var out []AICategory
	for _, c := range aimask.DetectCategories(m.Pix) {
		out = append(out, AICategory{ID: c.ID, Name: c.Name, Fraction: c.Fraction})
	}
	return out
}

// AIModelInfo reports whether a kind's model weights are on disk, and how
// large the download is when they aren't — what the client's consent dialog
// shows before the first use of an AI feature.
type AIModelInfo struct {
	Downloaded bool  `json:"downloaded"`
	Bytes      int64 `json:"bytes"`
}

// AIModelStatus reports the download state of the model serving kind.
func (e *Edits) AIModelStatus(ctx context.Context, kind edit.AIKind) (*AIModelInfo, error) {
	spec, ok := aimask.SpecFor(kind)
	if !ok {
		return nil, fmt.Errorf("ai masks: %q has no model available yet", kind)
	}
	if e.deps.Infer == nil {
		return nil, fmt.Errorf("ai masks: inference is not configured")
	}
	return &AIModelInfo{Downloaded: e.deps.Infer.HasModel(spec), Bytes: spec.Bytes}, nil
}

// aiModelNotDownloadedMsg is the sentinel the client matches to open its
// download-consent dialog. Keep in sync with isModelNotDownloaded in
// EditPanel.tsx.
const aiModelNotDownloadedMsg = "model not downloaded"

// GenerateAIMap ensures the model-generated map for (photo, kind) exists and
// returns its version tag. Idempotent and cheap when the map is already on
// disk — the client may call it freely (e.g. to restore maps for an edit
// that arrived via sidecar from another machine). The first use of a kind
// needs its model (tens of MB to ~1.3 GB): downloads happen ONLY with
// allowDownload — the client sets it after the user confirmed the consent
// dialog — and are surfaced as a shared task with progress; ctx cancellation
// aborts both download and generation. Without consent a missing model fails
// with aiModelNotDownloadedMsg so the client can ask.
//
// The inference input is a neutral base-orientation render, deliberately
// independent of the current develop settings and crop, so the map never
// shifts as the user edits.
func (e *Edits) GenerateAIMap(ctx context.Context, photoID int64, kind edit.AIKind, allowDownload bool) (*AIMapResult, error) {
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
		res := &AIMapResult{MapVer: ver}
		if kind == edit.AIClass {
			res.Categories = e.categoriesFor(photo.CacheKey, ver)
		}
		return res, nil
	}
	if spec, _ := aimask.SpecFor(kind); !allowDownload && !e.deps.Infer.HasModel(spec) {
		return nil, fmt.Errorf("ai masks: %s", aiModelNotDownloadedMsg)
	}

	rgba, err := e.previewDecode(ctx, photoID, photo, nil)
	if err != nil {
		return nil, err
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, "AI mask: "+photo.FileName, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "aimask"})
	downloaded := false // progress only fires while weights download
	gray, err := aimask.Generate(tctx, e.deps.Infer, kind, rgba, func(done, total int64) {
		downloaded = true
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
	// A freshly (re)generated map changes pixels for any SAVED edit that
	// already references it without changing that edit's hash (the restore
	// path: sidecar-imported AI masks). Drop the stale cached renditions so
	// the next render sees the map. A base-hash photo has no masks — skip.
	if photo.EditHash != edit.BaseHash {
		e.deps.Cache.InvalidateEdit(photo.CacheKey, photo.EditHash)
	}
	if kind == edit.AISubject {
		if m := grayToAIMap(gray); m != nil {
			e.scoreSubjectSharpnessAsync(photo, m)
		}
	}
	if downloaded {
		aprot.TriggerRefresh(ctx, modelsInfoKey) // Settings' model list is live
	}
	task.Err(nil)
	res := &AIMapResult{MapVer: ver, Generated: true}
	if kind == edit.AIClass {
		res.Categories = e.categoriesFor(photo.CacheKey, ver)
	}
	return res, nil
}
