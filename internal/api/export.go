package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"

	"github.com/marrasen/marraw/internal/export"
	"github.com/marrasen/marraw/internal/watermark"
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
	// ColorSpace picks the output primaries; empty = sRGB. Wide-space exports
	// get a matching ICC profile embedded, in both formats.
	ColorSpace ColorSpace `json:"colorSpace" validate:"omitempty,oneof=srgb adobergb prophoto"`
	// SharpenTarget applies output sharpening after the final resize; empty =
	// off.
	SharpenTarget SharpenTarget `json:"sharpenTarget" validate:"omitempty,oneof=off screen matte glossy"`
	// SharpenAmount scales the sharpening; empty = standard.
	SharpenAmount SharpenAmount `json:"sharpenAmount" validate:"omitempty,oneof=low standard high"`
	// FileNameTemplate names the output files; empty = "{name}". Tokens:
	// {name}, {seq}, {date}, {time} (see export.namer).
	FileNameTemplate string `json:"fileNameTemplate" validate:"omitempty,max=120"`
	// ExifMode selects the exported metadata; empty = all (full catalog set).
	ExifMode ExifMode `json:"exifMode" validate:"omitempty,oneof=all copyright none"`
	// RemoveLocation strips GPS while keeping the rest (meaningful with all).
	RemoveLocation bool `json:"removeLocation"`
	// Artist and Copyright are written as EXIF tags 315/33432 when non-empty.
	Artist    string `json:"artist" validate:"omitempty,max=120"`
	Copyright string `json:"copyright" validate:"omitempty,max=120"`
	// WatermarkID composites the named watermark onto the pixels; empty or
	// unknown (a since-deleted watermark) = none.
	WatermarkID string `json:"watermarkId" validate:"omitempty,max=64"`
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

	// Resolve the watermark up front, while the request context is live —
	// the stored list can change mid-batch without affecting this export.
	var wmSpec *watermark.Spec
	if wm := watermarkByID(ctx, x.deps.DB, req.WatermarkID); wm != nil {
		wmSpec = toWatermarkSpec(*wm, x.deps.WatermarkDir)
	}

	total := len(req.PhotoIDs)
	// Resolve the source album so the library rail can light up the folder
	// these photos come from while the export runs. A selection lives in one
	// open folder, so the first photo's folder is the album.
	meta := TaskMeta{Kind: "export", DestDir: req.DestDir}
	if photos, err := x.deps.DB.GetPhotos(ctx, req.PhotoIDs); err == nil && len(photos) > 0 {
		meta.FolderPath = photos[0].FolderPath
		meta.Folder = filepath.Base(photos[0].FolderPath)
	}
	tctx, task := tasks.StartTask[TaskMeta](
		context.WithoutCancel(ctx),
		fmt.Sprintf("Exporting %d photo%s", total, plural(total)),
		tasks.Shared(),
	)
	task.SetMeta(meta)
	task.Progress(0, total)

	go func() {
		var mu sync.Mutex
		done, failed := 0, 0
		err := export.Run(tctx, x.deps.DB, export.Request{
			PhotoIDs:         req.PhotoIDs,
			DestDir:          req.DestDir,
			Format:           format,
			JpegQuality:      req.JpegQuality,
			LongEdge:         req.LongEdge,
			ColorSpace:       string(req.ColorSpace),
			SharpenTarget:    string(req.SharpenTarget),
			SharpenAmount:    string(req.SharpenAmount),
			FileNameTemplate: req.FileNameTemplate,
			ExifMode:         string(req.ExifMode),
			RemoveLocation:   req.RemoveLocation,
			Artist:           strings.TrimSpace(req.Artist),
			Copyright:        strings.TrimSpace(req.Copyright),
			Watermark:        wmSpec,
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
