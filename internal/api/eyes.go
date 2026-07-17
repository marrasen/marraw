package api

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"
	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/eyes"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/store"
)

// Closed-eye detection: a cull-side soft signal, not an edit feature. The
// scoring itself (YuNet face detection + a per-eye open/closed classifier
// over the embedded thumb) lives in internal/eyes; this file wires it to the
// store, the granular patch channel, and the on-demand folder scan. Scoring
// runs only through AnalyzeEyes after the client's consent dialog, like
// AnalyzeSubjects — the one-time model download rides on that consent too;
// nothing scores eyes uninvited.

// scoreEyes measures photo's closed-eye probability from its embedded thumb,
// persists it (-1 = measured but no judgeable face/eyes, hidden from clients
// by nonNegativeFloat), and pushes a granular per-photo patch rather than a
// full folder-list refresh. Split out of AnalyzeEyes so the sentinel
// convention and store call live in one place.
func (d *Deps) scoreEyes(ctx context.Context, photo store.Photo, thumb image.Image) error {
	score, ok, err := eyes.Score(ctx, d.Infer, thumb, nil)
	if err != nil {
		return err
	}
	if !ok {
		score = -1 // measured, no judgeable face/eyes
	}
	if err := d.DB.SetEyesClosed(context.WithoutCancel(ctx), photo.ID, score); err != nil {
		return err
	}
	// Always flip the analyzed flag so the eye-scan indicator stops counting
	// this frame as pending — even when there was no face to judge (score < 0),
	// where re-analyzing would forever change nothing.
	analyzed := true
	patch := PhotoPatch{ID: photo.ID, EyesAnalyzed: &analyzed}
	if score >= 0 {
		s := score
		patch.EyesClosed = &s
	}
	d.patchFolderPhotos(photo.FolderID, []PhotoPatch{patch})
	return nil
}

// EyeModelStatus reports the download state of the closed-eye model pair —
// what the client's consent dialog shows before the first scan. Bytes counts
// only what is still missing.
func (l *Library) EyeModelStatus(ctx context.Context) (*AIModelInfo, error) {
	if l.deps.Infer == nil {
		return nil, fmt.Errorf("eyes: inference is not configured")
	}
	info := &AIModelInfo{Downloaded: true}
	for _, spec := range []struct{ has bool; bytes int64 }{
		{l.deps.Infer.HasModel(eyes.DetectSpec()), eyes.DetectSpec().Bytes},
		{l.deps.Infer.HasModel(eyes.StateSpec()), eyes.StateSpec().Bytes},
	} {
		if !spec.has {
			info.Downloaded = false
			info.Bytes += spec.bytes
		}
	}
	return info, nil
}

// AnalyzeEyes runs closed-eye detection across the given photos as one
// shared, cancellable background task, scoring each frame into eyes_closed
// so the grid's blink badges light up.
//
// Follows the AnalyzeSubjects shape exactly: one aggregate task on a
// cancel-free context (cancel lives in the task tray), idempotent (scored
// frames are skipped at selection), and the one-time model download happens
// only with allowDownload — the client sets it after the consent dialog;
// without it a missing model fails up front with aiModelNotDownloadedMsg so
// the client can ask. Unlike the subject scan this never runs RAW-domain
// inference — each frame costs an embedded-thumb read plus two tiny CPU
// models, so it parallelizes like the calibrate pass.
func (l *Library) AnalyzeEyes(ctx context.Context, photoIDs []int64, allowDownload bool) (*tasks.TaskRef, error) {
	if l.deps.Infer == nil {
		return nil, aprot.ErrInvalidParams("eyes: inference is not configured")
	}
	photos, err := l.deps.DB.GetPhotos(ctx, photoIDs)
	if err != nil {
		return nil, err
	}
	var work []store.Photo
	for _, p := range photos {
		if !p.EyesClosed.Valid {
			work = append(work, p)
		}
	}
	if len(work) == 0 {
		return nil, nil // nothing to do — the client just closes the dialog
	}
	installed := eyes.ModelsInstalled(l.deps.Infer)
	if !allowDownload && !installed {
		return nil, aprot.ErrInvalidParams("eyes: " + aiModelNotDownloadedMsg)
	}
	total := len(work)

	meta := TaskMeta{Kind: "eyes", Folder: filepath.Base(work[0].FolderPath), FolderPath: work[0].FolderPath}
	tctx, task := tasks.StartTask[TaskMeta](
		context.WithoutCancel(ctx),
		fmt.Sprintf("Detecting closed eyes — %d photo%s", total, plural(total)),
		tasks.Shared(),
	)
	task.SetMeta(meta)
	task.Progress(0, total)

	go func() {
		var done atomic.Int64
		g, gctx := errgroup.WithContext(tctx)
		// Same budget as the calibrate pass: the per-frame cost is the
		// embedded-thumb read and JPEG decode, not the inference (two
		// sub-MB CPU models). The download is singleflighted inside Session.
		g.SetLimit(max(1, runtime.NumCPU()-2))
		for _, p := range work {
			g.Go(func() error {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				err := l.deps.Pool.Do(gctx, p.CacheKey+"|eyes", decode.PriorityBackground,
					func(jctx context.Context, proc *libraw.Processor) error {
						if err := jctx.Err(); err != nil {
							return err
						}
						if err := proc.Open(p.Path()); err != nil {
							return err
						}
						thumb, err := proc.EmbeddedThumb()
						if err != nil {
							return err
						}
						img, err := jpeg.Decode(bytes.NewReader(thumb))
						if err != nil {
							return err
						}
						return l.deps.scoreEyes(jctx, p, img)
					})
				if err != nil {
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
		if !installed {
			l.deps.TriggerRefresh(modelsInfoKey) // Settings' model list is live
		}
	}()
	return &tasks.TaskRef{TaskID: task.ID()}, nil
}
