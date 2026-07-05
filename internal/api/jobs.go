package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/marrasen/aprot/tasks"
	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// TaskMeta is the typed metadata attached to shared background tasks so the
// client task tray can render kind-specific UI (reveal export folder, …).
type TaskMeta struct {
	Kind    string `json:"kind"` // "scan" | "calibrate" | "prerender" | "export"
	Folder  string `json:"folder,omitempty"`
	DestDir string `json:"destDir,omitempty"`
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

	name := filepath.Base(path)
	go func() {
		l.metaPass(ctx, folderID, name)
		l.calibratePass(ctx, folderID, name)
		l.prerenderPass(ctx, folderID, name)
	}()
}

// calibratePass measures the base look's auto-brighten lift (as an exposure
// EV) for photos that don't have one yet. The value seeds the exposure dial
// so the camera-mimic compensation is visible in the develop values instead
// of silently vanishing on the first edit. Two demosaic-free half-size
// decodes per photo — much cheaper than the pre-render pass that follows.
func (l *Library) calibratePass(ctx context.Context, folderID int64, name string) {
	photos, err := l.deps.DB.ListPhotos(ctx, folderID)
	if err != nil {
		return
	}
	var work []store.Photo
	for _, p := range photos {
		if !p.BaseExpEV.Valid {
			work = append(work, p)
		}
	}
	if len(work) == 0 {
		return
	}

	tctx, task := tasks.StartTask[TaskMeta](ctx, "Calibrating "+name, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "calibrate", Folder: name})
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
					ev, err := pyramid.MeasureAutoBrightEV(proc)
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
}

// metaPass backfills missing photo metadata under a shared task.
func (l *Library) metaPass(ctx context.Context, folderID int64, name string) {
	n, err := l.deps.Scanner.MetaCount(ctx, folderID)
	if err != nil || n == 0 {
		return
	}
	tctx, task := tasks.StartTask[TaskMeta](ctx, "Scanning "+name, tasks.Shared())
	task.SetMeta(TaskMeta{Kind: "scan", Folder: name})
	task.Progress(0, n)
	task.Err(l.deps.Scanner.Backfill(tctx, folderID, task.Progress))
}

// prerenderPass renders the loupe-ready 2048 rendition (which also yields
// every smaller level) for photos that don't have one yet, under a shared
// cancellable task. Runs at background priority so visible/interactive
// requests always preempt it in the decode pool.
func (l *Library) prerenderPass(ctx context.Context, folderID int64, name string) {
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
	task.SetMeta(TaskMeta{Kind: "prerender", Folder: name})
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
