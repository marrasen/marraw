package api

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"
	"golang.org/x/sync/errgroup"

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
// scoreable subject, hidden from clients by nonNegativeFloat), and pushes a
// granular per-photo patch rather than a full folder-list refresh. Shared by
// the calibrate-pass backfill and GenerateAIMap's immediate scoring so the
// sentinel convention and store call live in one place. Best-effort: callers
// ignore the returned error (the calibrate pass retries).
func (d *Deps) scoreSubjectMatte(ctx context.Context, photo store.Photo, thumb image.Image, matte *pyramid.AIMap) error {
	score, ok := pyramid.SubjectSharpnessScore(thumb, matte, photo.Orientation)
	if !ok {
		score = -1 // measured, no scoreable subject
	}
	if err := d.DB.SetSubjectSharpness(context.WithoutCancel(ctx), photo.ID, score); err != nil {
		return err
	}
	// Always flip the analyzed flag so the subject-scan indicator stops
	// counting this frame as pending — even when there was no subject to score
	// (score < 0), where re-analyzing would forever change nothing. The numeric
	// score rides along only when there is a subject.
	analyzed := true
	patch := PhotoPatch{ID: photo.ID, SubjectAnalyzed: &analyzed}
	if score >= 0 {
		s := score
		patch.SubjectSharpness = &s
	}
	d.patchFolderPhotos(photo.FolderID, []PhotoPatch{patch})
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
// This is the single-photo path and surfaces one "AI mask: <file>" task. The
// batch paths — the folder-wide subject scan (AnalyzeSubjects) and the
// selection-wide preset-mask materialization (GenerateAIMaps) — do NOT go
// through here: they drive generateAIMap directly under one aggregate task, so
// a batch reports a single task and toast rather than one per frame.
func (e *Edits) GenerateAIMap(ctx context.Context, photoID int64, kind edit.AIKind, allowDownload bool) (*AIMapResult, error) {
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	// Fast path: an already-present map returns without opening a task, so the
	// cheap sidecar-restore calls don't each spawn a task and a "done" toast.
	if ver, ok := aimask.MapVerFor(kind); ok && e.deps.Cache.AIMaps != nil &&
		e.deps.Cache.AIMaps.Has(photo.CacheKey, kind, ver) {
		res := &AIMapResult{MapVer: ver}
		if kind == edit.AIClass {
			res.Categories = e.categoriesFor(photo.CacheKey, ver)
		}
		return res, nil
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, "AI mask: "+photo.FileName, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "aimask"})
	res, _, err := e.generateAIMap(tctx, photo, kind, allowDownload, true, func(done, total int64) {
		task.Progress(int(done>>20), int(total>>20)) // model download, MB units
	})
	task.Err(err)
	return res, err
}

// generateAIMap ensures the model map for (photo, kind) exists on disk, scores
// it when it's a subject matte, and returns its result. It opens NO task: the
// caller owns progress reporting, so a folder-wide scan can report one
// aggregate task instead of one per photo. onProgress fires only while model
// weights download (nil to ignore); the second return reports whether a
// download ran.
//
// The inference input is a neutral base-orientation render, deliberately
// independent of the current develop settings and crop, so the map never
// shifts as the user edits. cacheDecode gates the single-entry preview decode
// cache: the user-initiated single-photo path warms it (the user is likely to
// edit that photo next), while the folder-wide scan must not — each scanned
// frame would evict the interactive editor's warm decode (see decodePreview).
func (e *Edits) generateAIMap(ctx context.Context, photo store.Photo, kind edit.AIKind, allowDownload, cacheDecode bool, onProgress func(done, total int64)) (*AIMapResult, bool, error) {
	ver, ok := aimask.MapVerFor(kind)
	if !ok {
		return nil, false, fmt.Errorf("ai masks: %q has no model available yet", kind)
	}
	store := e.deps.Cache.AIMaps
	if store == nil || e.deps.Infer == nil {
		return nil, false, fmt.Errorf("ai masks: inference is not configured")
	}
	if store.Has(photo.CacheKey, kind, ver) {
		res := &AIMapResult{MapVer: ver}
		if kind == edit.AIClass {
			res.Categories = e.categoriesFor(photo.CacheKey, ver)
		}
		return res, false, nil
	}
	if spec, _ := aimask.SpecFor(kind); !allowDownload && !e.deps.Infer.HasModel(spec) {
		return nil, false, fmt.Errorf("ai masks: %s", aiModelNotDownloadedMsg)
	}

	rgba, err := e.decodePreview(ctx, photo.ID, photo, nil, cacheDecode)
	if err != nil {
		return nil, false, err
	}

	downloaded := false // progress only fires while weights download
	gray, err := aimask.Generate(ctx, e.deps.Infer, kind, rgba, func(done, total int64) {
		downloaded = true
		if onProgress != nil {
			onProgress(done, total)
		}
	})
	if err != nil {
		return nil, downloaded, err
	}
	if err := store.Save(photo.CacheKey, kind, ver, gray); err != nil {
		return nil, downloaded, err
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
	res := &AIMapResult{MapVer: ver, Generated: true}
	if kind == edit.AIClass {
		res.Categories = e.categoriesFor(photo.CacheKey, ver)
	}
	return res, downloaded, nil
}

// GenerateAIMaps materializes the model maps for every (photo, kind) pair as
// one shared, cancellable background task — the batch companion to
// GenerateAIMap. A preset apply across a selection persists the same AI-mask
// RECIPES to every photo, but each photo needs its own inference before the
// recipe renders as anything (a missing map is a silent render no-op, see
// pyramid.AIMapSet.SetFor); this is where the non-focused photos get theirs.
// Photos whose requested maps are all on disk are skipped up front; returns a
// nil ref when that leaves nothing to do.
//
// The maps land AFTER the recipes were persisted and their thumbs warmed, and
// a map is a render input OUTSIDE the edit hash — so each landed map
// broadcasts AIMapsGeneratedEvent telling every client to cache-bust that
// photo's unchanged /img URLs (the batch twin of GenerateAIMap's
// generated=true contract).
//
// Same task shape and download-consent rules as AnalyzeSubjects: one aggregate
// task riding a cancel-free context (the user cancels from the tray), and
// without allowDownload a missing model fails up front with
// aiModelNotDownloadedMsg. In the preset-apply flow the focused photo's
// GenerateAIMap already settled consent, so this never trips there.
func (e *Edits) GenerateAIMaps(ctx context.Context, photoIDs []int64, kinds []edit.AIKind, allowDownload bool) (*tasks.TaskRef, error) {
	if e.deps.Cache.AIMaps == nil || e.deps.Infer == nil {
		return nil, aprot.ErrInvalidParams("ai masks: inference is not configured")
	}
	vers := make(map[edit.AIKind]string, len(kinds))
	for _, k := range kinds {
		ver, ok := aimask.MapVerFor(k)
		if !ok {
			return nil, aprot.ErrInvalidParams(fmt.Sprintf("ai masks: %q has no model available yet", k))
		}
		vers[k] = ver
	}
	photos, err := e.deps.DB.GetPhotos(ctx, photoIDs)
	if err != nil {
		return nil, err
	}
	// Work = each photo's requested kinds whose map isn't on disk yet.
	type job struct {
		photo   store.Photo
		missing []edit.AIKind
	}
	var work []job
	for _, p := range photos {
		var missing []edit.AIKind
		for _, k := range kinds {
			if !e.deps.Cache.AIMaps.Has(p.CacheKey, k, vers[k]) {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			work = append(work, job{photo: p, missing: missing})
		}
	}
	if len(work) == 0 {
		return nil, nil // every map already exists — nothing to do
	}
	for _, k := range kinds {
		if spec, _ := aimask.SpecFor(k); !allowDownload && !e.deps.Infer.HasModel(spec) {
			return nil, aprot.ErrInvalidParams("ai masks: " + aiModelNotDownloadedMsg)
		}
	}
	total := len(work)

	meta := TaskMeta{Kind: "aimask", Folder: filepath.Base(work[0].photo.FolderPath), FolderPath: work[0].photo.FolderPath}
	tctx, task := tasks.StartTask[TaskMeta](
		context.WithoutCancel(ctx),
		fmt.Sprintf("AI masks — %d photo%s", total, plural(total)),
		tasks.Shared(),
	)
	task.SetMeta(meta)
	task.Progress(0, total)

	go func() {
		var done atomic.Int64
		g, gctx := errgroup.WithContext(tctx)
		// Same worker cap as AnalyzeSubjects: memory binds, not cores — every
		// in-flight frame pins a LibRaw handle the 3-entry HandleCache cannot
		// evict while held.
		g.SetLimit(min(3, max(1, runtime.NumCPU()-2)))
		for _, j := range work {
			g.Go(func() error {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				// Re-fetch: the preset apply persists the recipes concurrently
				// with this task, and generateAIMap invalidates renditions by
				// the photo's CURRENT edit hash — the snapshot from task start
				// may predate the save.
				p := j.photo
				if fresh, err := e.deps.DB.GetPhoto(gctx, j.photo.ID); err == nil {
					p = fresh
				}
				generated := false
				for _, k := range j.missing {
					res, _, err := e.generateAIMap(gctx, p, k, allowDownload, false, nil)
					if err != nil {
						if gctx.Err() != nil {
							return gctx.Err() // cancelled — not a per-frame failure
						}
						task.Output(p.FileName + ": " + err.Error())
						continue
					}
					generated = generated || res.Generated
				}
				if generated {
					e.deps.BroadcastAIMapsGenerated(p.ID)
				}
				task.Progress(int(done.Add(1)), total)
				return nil
			})
		}
		task.Err(g.Wait())
	}()
	return &tasks.TaskRef{TaskID: task.ID()}, nil
}

// SubjectBoundsResult is the subject's bounding box in fractions of the
// edit's oriented frame — the space the crop rectangle lives in. Found is
// false when the matte holds no confident subject.
type SubjectBoundsResult struct {
	Found bool    `json:"found"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
}

// SubjectBounds locates the photo's subject as a bounding box for the crop
// tool's auto crop. It ensures the subject matte exists first (same task and
// download-consent rules as GenerateAIMap — a missing model without
// allowDownload fails with aiModelNotDownloadedMsg), orients the matte into
// the edit's frame (params carries the client's rotate/flip), and boxes the
// confident pixels at the mask-default 0.5 threshold. Speckle rows/columns
// (fewer than a handful of confident pixels) don't stretch the box; a matte
// with no real subject reports Found=false, never an error.
func (e *Edits) SubjectBounds(ctx context.Context, photoID int64, params edit.Params, allowDownload bool) (*SubjectBoundsResult, error) {
	res, err := e.GenerateAIMap(ctx, photoID, edit.AISubject, allowDownload)
	if err != nil {
		return nil, err
	}
	photo, err := e.deps.DB.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	m := e.deps.Cache.AIMaps.LoadOriented(photo.CacheKey, edit.AISubject, res.MapVer, params.RotateTurns(), params.FlipH)
	if m == nil {
		return &SubjectBoundsResult{}, nil
	}

	// Marginal profiles of confident (≥0.5) matte pixels. Boxing off the
	// profiles instead of raw min/max keeps a stray speckle from dragging an
	// edge across the frame: an edge row/column must carry at least minRun
	// confident pixels to count.
	const confident = 128
	rows := make([]int, m.H)
	cols := make([]int, m.W)
	total := 0
	for y := range m.H {
		for x, v := range m.Pix[y*m.W : (y+1)*m.W] {
			if v >= confident {
				rows[y]++
				cols[x]++
				total++
			}
		}
	}
	minRun := max(2, max(m.W, m.H)/256)
	x0, x1 := profileSpan(cols, minRun)
	y0, y1 := profileSpan(rows, minRun)
	// A subject smaller than ~64 confident pixels (on the ~megapixel matte) is
	// noise, not something to crop to.
	if total < 64 || x1 <= x0 || y1 <= y0 {
		return &SubjectBoundsResult{}, nil
	}
	return &SubjectBoundsResult{
		Found: true,
		X:     float64(x0) / float64(m.W),
		Y:     float64(y0) / float64(m.H),
		W:     float64(x1-x0) / float64(m.W),
		H:     float64(y1-y0) / float64(m.H),
	}, nil
}

// profileSpan returns the [first, last+1) index range of profile entries at or
// above threshold, or (0, 0) when none qualify.
func profileSpan(profile []int, threshold int) (int, int) {
	first, last := -1, -1
	for i, n := range profile {
		if n >= threshold {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return 0, 0
	}
	return first, last + 1
}

// AnalyzeSubjects runs subject-matte inference across the given photos as one
// shared, cancellable background task, scoring each frame into
// subject_sharpness so the grid's focus badges re-evaluate subject-aware.
//
// It replaces the old client-side per-photo loop: the whole scan is a single
// aggregate task — one progress bar, one cancel, one "done" toast — instead of
// a per-photo "AI mask: <file>" task (and toast) for every frame. The pass
// rides on a cancel-free context so navigating away doesn't abort it; the user
// cancels from the task tray. Idempotent: already-scored frames are skipped and
// generateAIMap short-circuits on an on-disk matte, so re-running a processed
// folder is cheap.
//
// The first frame pays the one-time model download when allowDownload is set
// (the client passes it after the consent dialog); without it a missing model
// fails up front with aiModelNotDownloadedMsg so the client can ask. Returns a
// nil ref when every frame is already scored — nothing to do.
func (e *Edits) AnalyzeSubjects(ctx context.Context, photoIDs []int64, allowDownload bool) (*tasks.TaskRef, error) {
	if e.deps.Cache.AIMaps == nil || e.deps.Infer == nil {
		return nil, aprot.ErrInvalidParams("ai masks: inference is not configured")
	}
	photos, err := e.deps.DB.GetPhotos(ctx, photoIDs)
	if err != nil {
		return nil, err
	}
	// Work = frames without a subject-aware score yet. (An unscoreable frame
	// reads the same and re-runs, but that hits generateAIMap's on-disk fast
	// path, so it costs a decode-free round trip and no inference.)
	var work []store.Photo
	for _, p := range photos {
		if !p.SubjectSharpness.Valid {
			work = append(work, p)
		}
	}
	if len(work) == 0 {
		return nil, nil // nothing to do — the client just closes the dialog
	}
	if spec, _ := aimask.SpecFor(edit.AISubject); !allowDownload && !e.deps.Infer.HasModel(spec) {
		return nil, aprot.ErrInvalidParams("ai masks: " + aiModelNotDownloadedMsg)
	}
	total := len(work)

	// Light up the album this selection came from while the scan runs, the way
	// export does. A folder-wide scan's frames share one folder.
	meta := TaskMeta{Kind: "subjects", Folder: filepath.Base(work[0].FolderPath), FolderPath: work[0].FolderPath}
	tctx, task := tasks.StartTask[TaskMeta](
		context.WithoutCancel(ctx),
		fmt.Sprintf("Analyzing subjects — %d photo%s", total, plural(total)),
		tasks.Shared(),
	)
	task.SetMeta(meta)
	task.Progress(0, total)

	go func() {
		var done atomic.Int64
		g, gctx := errgroup.WithContext(tctx)
		// The binding resource is memory, not cores: every in-flight frame
		// pins its own unpacked LibRaw handle (~100–200 MB for a 42 MP file)
		// that the 3-entry HandleCache cannot evict while held, so a
		// core-count limit would stack multi-GB spikes on many-core machines.
		// Three workers match the handle cap, and ORT already parallelizes
		// each inference across cores internally — while still leaving the
		// interactive edit preview headroom. The one-time model download is
		// singleflighted, so concurrent workers share a single fetch.
		g.SetLimit(min(3, max(1, runtime.NumCPU()-2)))
		for _, p := range work {
			g.Go(func() error {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				if _, _, err := e.generateAIMap(gctx, p, edit.AISubject, allowDownload, false, nil); err != nil {
					if gctx.Err() != nil {
						return gctx.Err() // cancelled — not a per-frame failure
					}
					task.Output(p.FileName + ": " + err.Error())
				}
				task.Progress(int(done.Add(1)), total)
				return nil
			})
		}
		task.Err(g.Wait())
	}()
	return &tasks.TaskRef{TaskID: task.ID()}, nil
}
