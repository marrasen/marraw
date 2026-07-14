package api

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"
	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/aimask"
	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// TaskMeta is the typed metadata attached to shared background tasks so the
// client task tray can render kind-specific UI (reveal export folder, …).
type TaskMeta struct {
	Kind string `json:"kind"` // "scan" | "calibrate" | "prerender" | "fullres" | "export"
	// Folder is the album's display name (base of FolderPath), shown in the
	// tray sub-label.
	Folder string `json:"folder,omitempty"`
	// FolderPath is the album's full path on disk, so the library rail can
	// light up the matching root while this task runs. Empty for tasks not
	// tied to a single album.
	FolderPath string `json:"folderPath,omitempty"`
	DestDir    string `json:"destDir,omitempty"`
}

// startFolderJobs cancels the previous folder's background work and starts
// the metadata backfill followed by the pre-render pass for the newly opened
// folder. Both surface as cancellable shared tasks. reqCtx must be a live
// request context — task delivery and cancellation ride on its values, which
// context.WithoutCancel preserves.
func (l *Library) startFolderJobs(reqCtx context.Context, folderID int64, path string) {
	d := l.deps
	d.jobMu.Lock()
	if d.folderJobsCancel != nil {
		d.folderJobsCancel()
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(reqCtx))
	d.folderJobsCancel = cancel
	d.jobMu.Unlock()

	go l.folderPasses(ctx, folderID, path)
}

// folderPasses brings a folder's photos up to date: metadata, the camera-mimic
// calibration, the loupe-ready rendition, and optionally the 1:1 tiles. Every
// pass filters to the photos that still need it and returns without opening a
// task when there are none, so running this over an already-processed folder
// costs a few queries and stat calls.
//
// Shared by the open path (through startFolderJobs, which owns the cancel slot)
// and the watcher's ingest path (which must not touch that slot).
func (l *Library) folderPasses(ctx context.Context, folderID int64, path string) {
	l.metaPass(ctx, folderID, path)
	l.calibratePass(ctx, folderID, path)
	l.prerenderPass(ctx, folderID, path)
	// Opt-in: 1:1 full-resolution tiles, the most expensive pass, runs
	// last so it never delays the loupe-ready renditions above.
	if raw, _ := l.deps.DB.GetSetting(ctx, settingUIPrerenderFull); raw == "true" {
		l.fullresPass(ctx, folderID, path)
	}
}

// RenderFolderFullres pre-renders 1:1 full-resolution tiles for every photo in
// the folder at path, on demand (the folder context menu's "Render 1:1"). It
// runs regardless of the PrerenderFullres setting and surfaces as a shared,
// cancellable task; the pass rides on a cancel-free context so navigating away
// doesn't abort it — the user cancels from the task tray.
func (l *Library) RenderFolderFullres(ctx context.Context, path string) (*FolderInfo, error) {
	folderID, count, err := l.deps.Scanner.OpenFolder(ctx, path, l.scanRecursionFor(ctx, path))
	if err != nil {
		return nil, aprot.ErrInvalidParams(err.Error())
	}
	go l.fullresPass(context.WithoutCancel(ctx), folderID, path)
	return &FolderInfo{FolderID: folderID, Path: path, PhotoCount: count}, nil
}

// fullresPass renders the full-resolution tile set (which also yields every
// smaller level) for photos that don't have one yet, under a shared
// cancellable task. A single decode per photo renders all of its tiles, so
// EnsureTile at 0,0 is enough to materialize the whole grid. Background
// priority keeps interactive edits and visible loads ahead of it.
func (l *Library) fullresPass(ctx context.Context, folderID int64, path string) {
	name := filepath.Base(path)
	photos, err := l.deps.DB.ListPhotos(ctx, folderID)
	if err != nil {
		return
	}
	var work []store.Photo
	for _, p := range photos {
		// Tile 0,0 exists iff the folder's full render has been done for this
		// edit state — the cheap stand-in for the whole grid.
		if _, err := os.Stat(l.deps.Cache.PathForTile(p.CacheKey, 0, 0, currentHash(p))); err != nil {
			work = append(work, p)
		}
	}
	if len(work) == 0 {
		return
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, "Rendering 1:1 "+name, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "fullres", Folder: name, FolderPath: path})
	total := len(work)
	task.Progress(0, total)

	var done atomic.Int64
	g, gctx := errgroup.WithContext(tctx)
	// Same headroom as the pre-render pass: leave a couple of pool workers
	// free so an interactive edit preview never waits out a full decode.
	g.SetLimit(max(1, runtime.NumCPU()-2))
	for _, p := range work {
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			if _, err := l.deps.Cache.EnsureTile(gctx, p, 0, 0, currentHash(p), decode.PriorityBackground); err != nil {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				task.Output(p.FileName + ": " + err.Error())
			}
			task.Progress(int(done.Add(1)), total)
			return nil
		})
	}
	task.Err(g.Wait())
}

// calibratePass measures per-photo derived values that are missing: the base
// look's auto-brighten lift (as an exposure EV, seeding the exposure dial so
// the camera-mimic compensation is visible in the develop values), the
// sharpness score (Laplacian variance of the embedded thumb, the grid's
// soft-photo badge), and — only for photos whose AI subject matte is already
// on disk — the subject-weighted sharpness score. The pass never runs
// inference itself; a matte appears when the user first makes a subject
// mask, and GenerateAIMap scores it immediately. Two demosaic-free half-size
// decodes plus a thumb read per photo — much cheaper than the pre-render
// pass that follows.
func (l *Library) calibratePass(ctx context.Context, folderID int64, path string) {
	name := filepath.Base(path)
	photos, err := l.deps.DB.ListPhotos(ctx, folderID)
	if err != nil {
		return
	}
	subjVer, _ := aimask.MapVerFor(edit.AISubject)
	needSubject := func(p store.Photo) bool {
		return subjVer != "" && !p.SubjectSharpness.Valid &&
			l.deps.Cache.AIMaps.Has(p.CacheKey, edit.AISubject, subjVer)
	}
	var work []store.Photo
	for _, p := range photos {
		if !p.BaseExpEV.Valid || !p.Sharpness.Valid || needSubject(p) {
			work = append(work, p)
		}
	}
	if len(work) == 0 {
		return
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, "Calibrating "+name, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "calibrate", Folder: name, FolderPath: path})
	total := len(work)
	task.Progress(0, total)

	var done atomic.Int64
	g, gctx := errgroup.WithContext(tctx)
	g.SetLimit(max(1, runtime.NumCPU()-2))
	for _, p := range work {
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			// The DB write lives inside the pool job so a deduplicated
			// concurrent caller can't race in a zero measurement.
			err := l.deps.Pool.Do(gctx, p.CacheKey+"|calibrate", decode.PriorityBackground,
				func(jctx context.Context, proc *libraw.Processor) error {
					if err := jctx.Err(); err != nil {
						return err
					}
					if err := proc.Open(p.Path()); err != nil {
						return err
					}
					if !p.Sharpness.Valid || needSubject(p) {
						if thumb, err := proc.EmbeddedThumb(); err == nil {
							if img, err := jpeg.Decode(bytes.NewReader(thumb)); err == nil {
								if !p.Sharpness.Valid {
									score := pyramid.SharpnessScore(img)
									if err := l.deps.DB.SetSharpness(context.WithoutCancel(jctx), p.ID, score); err != nil {
										return err
									}
								}
								if needSubject(p) {
									if matte := l.deps.Cache.AIMaps.Load(p.CacheKey, edit.AISubject, subjVer); matte != nil {
										score, ok := pyramid.SubjectSharpnessScore(img, matte, p.Orientation)
										if !ok {
											score = -1 // measured, no scoreable subject
										}
										if err := l.deps.DB.SetSubjectSharpness(context.WithoutCancel(jctx), p.ID, score); err != nil {
											return err
										}
									}
								}
							}
						}
					}
					if p.BaseExpEV.Valid {
						return nil // only the thumb-based backfills were missing
					}
					ev, err := pyramid.MeasureAutoBrightEV(jctx, proc)
					if err != nil {
						return err
					}
					return l.deps.DB.SetBaseExpEV(context.WithoutCancel(jctx), p.ID, ev)
				})
			if err != nil && gctx.Err() == nil {
				task.Output(p.FileName + ": " + err.Error())
			}
			task.Progress(int(done.Add(1)), total)
			return nil
		})
	}
	task.Err(g.Wait())
	// The measurements landed in the DB but the folder list clients already
	// hold predates them; re-list so photo.baseExpEV (the exposure dial's
	// neutral) matches the seed GetEditParams now returns.
	l.deps.TriggerRefresh(photosKey(folderID))
}

// metaPass backfills missing photo metadata under a shared task.
func (l *Library) metaPass(ctx context.Context, folderID int64, path string) {
	name := filepath.Base(path)
	n, err := l.deps.Scanner.MetaCount(ctx, folderID)
	if err != nil || n == 0 {
		return
	}
	tctx, task := tasks.StartTask[TaskMeta](ctx, "Scanning "+name, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "scan", Folder: name, FolderPath: path})
	task.Progress(0, n)
	task.Err(l.deps.Scanner.Backfill(tctx, folderID, task.Progress))
	// The backfill just wrote taken_at values the rail's shoot list was built
	// without; re-list so Shoot.earliestTakenAt (the date sort/grouping key)
	// reflects them. A shoot lives in its parent's listing; a managed parent's
	// own loose-RAW row lives in its own.
	if parent := filepath.Dir(filepath.Clean(path)); l.isParentRoot(ctx, parent) {
		l.deps.TriggerRefresh(shootsKey(parent))
	}
	if l.isParentRoot(ctx, path) {
		l.deps.TriggerRefresh(shootsKey(path))
	}
}

// prerenderPass renders the loupe-ready 2048 rendition (which also yields
// every smaller level) for photos that don't have one yet, under a shared
// cancellable task. Runs at background priority so visible/interactive
// requests always preempt it in the decode pool.
func (l *Library) prerenderPass(ctx context.Context, folderID int64, path string) {
	name := filepath.Base(path)
	photos, err := l.deps.DB.ListPhotos(ctx, folderID)
	if err != nil {
		return
	}
	var work []store.Photo
	for _, p := range photos {
		if _, err := os.Stat(l.deps.Cache.PathFor(p.CacheKey, "2048", currentHash(p))); err != nil {
			work = append(work, p)
		}
	}
	if len(work) == 0 {
		return
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, fmt.Sprintf("Pre-rendering %s", name), tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "prerender", Folder: name, FolderPath: path})
	total := len(work)
	task.Progress(0, total)

	var done atomic.Int64
	g, gctx := errgroup.WithContext(tctx)
	// Leave a couple of pool workers free so an interactive edit preview
	// never has to wait out a full background decode.
	g.SetLimit(max(1, runtime.NumCPU()-2))
	for _, p := range work {
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			if _, err := l.deps.Cache.Ensure(gctx, p, "2048", currentHash(p), decode.PriorityBackground); err != nil {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				task.Output(p.FileName + ": " + err.Error())
			}
			task.Progress(int(done.Add(1)), total)
			return nil
		})
	}
	task.Err(g.Wait())
}
