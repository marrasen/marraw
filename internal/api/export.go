package api

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"

	"github.com/marrasen/marraw/internal/export"
)

// Export handles rendering photos out to disk.
type Export struct {
	deps *Deps
}

type ExportRequest struct {
	PhotoIDs    []int64      `json:"photoIds" validate:"required,min=1"`
	DestDir     string       `json:"destDir" validate:"required"`
	Format      ExportFormat `json:"format"`
	JpegQuality int          `json:"jpegQuality" validate:"gte=0,lte=100"`
	LongEdge    int          `json:"longEdge" validate:"gte=0,lte=65536"`
	// ColorSpace picks the output primaries; empty = sRGB. JPEGs in wide
	// spaces get a matching ICC profile embedded.
	ColorSpace ColorSpace `json:"colorSpace" validate:"omitempty,oneof=srgb adobergb prophoto"`
	// SharpenTarget applies output sharpening after the final resize; empty =
	// off. JPEG only — TIFF16 stays a neutral flat master.
	SharpenTarget SharpenTarget `json:"sharpenTarget" validate:"omitempty,oneof=off screen matte glossy"`
	// SharpenAmount scales the sharpening; empty = standard.
	SharpenAmount SharpenAmount `json:"sharpenAmount" validate:"omitempty,oneof=low standard high"`
	// CreateDir creates DestDir if missing (the client asks the user first).
	CreateDir bool `json:"createDir"`
}

type DestInfo struct {
	Exists bool `json:"exists"`
}

// CheckDest reports whether the destination directory exists, so the client
// can offer to create it before starting the export.
func (x *Export) CheckDest(ctx context.Context, path string) (*DestInfo, error) {
	st, err := os.Stat(path)
	return &DestInfo{Exists: err == nil && st.IsDir()}, nil
}

// StartExport renders the requested photos to req.DestDir as a background
// shared task using every spare CPU core. Progress and per-file failures
// stream through the task system; cancel via the task's cancel action.
func (x *Export) StartExport(ctx context.Context, req ExportRequest) (*tasks.TaskRef, error) {
	if st, err := os.Stat(req.DestDir); err == nil {
		if !st.IsDir() {
			return nil, aprot.ErrInvalidParams("destination is not a directory: " + req.DestDir)
		}
	} else if !req.CreateDir {
		return nil, aprot.ErrInvalidParams("destination does not exist: " + req.DestDir)
	} else if err := os.MkdirAll(req.DestDir, 0o755); err != nil {
		return nil, aprot.ErrInvalidParams("cannot create destination: " + err.Error())
	}
	format := string(req.Format)
	if format == "" {
		format = string(ExportJPEG)
	}

	total := len(req.PhotoIDs)
	tctx, task := tasks.StartTask[TaskMeta](
		context.WithoutCancel(ctx),
		fmt.Sprintf("Exporting %d photo%s", total, plural(total)),
		tasks.Shared(),
	)
	task.SetMeta(TaskMeta{Kind: "export", DestDir: req.DestDir})
	task.Progress(0, total)

	go func() {
		var mu sync.Mutex
		done, failed := 0, 0
		err := export.Run(tctx, x.deps.DB, export.Request{
			PhotoIDs:      req.PhotoIDs,
			DestDir:       req.DestDir,
			Format:        format,
			JpegQuality:   req.JpegQuality,
			LongEdge:      req.LongEdge,
			ColorSpace:    string(req.ColorSpace),
			SharpenTarget: string(req.SharpenTarget),
			SharpenAmount: string(req.SharpenAmount),
		}, func(it export.Item) {
			mu.Lock()
			done++
			d := done
			if it.Err != nil {
				failed++
				task.Output(fmt.Sprintf("%s: %v", it.FileName, it.Err))
			}
			mu.Unlock()
			task.Progress(d, total)
		})
		switch {
		case err != nil:
			task.Fail(err.Error())
		case failed > 0:
			task.Fail(fmt.Sprintf("%d of %d exports failed", failed, total))
		default:
			task.Close()
		}
	}()
	return &tasks.TaskRef{TaskID: task.ID()}, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
