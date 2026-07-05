package api

import (
	"context"
	"iter"
	"os"

	"github.com/marrasen/aprot"

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
}

type ExportItem struct {
	PhotoID  int64  `json:"photoId"`
	FileName string `json:"fileName"`
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
}

// ExportPhotos renders the requested photos to req.DestDir using every CPU
// core, streaming one item per finished file. Cancel via AbortController.
func (x *Export) ExportPhotos(ctx context.Context, req ExportRequest) (iter.Seq[ExportItem], error) {
	if st, err := os.Stat(req.DestDir); err != nil || !st.IsDir() {
		return nil, aprot.ErrInvalidParams("destination is not a directory: " + req.DestDir)
	}
	format := string(req.Format)
	if format == "" {
		format = string(ExportJPEG)
	}

	return func(yield func(ExportItem) bool) {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		items := make(chan export.Item, len(req.PhotoIDs))
		go func() {
			defer close(items)
			export.Run(runCtx, x.deps.DB, export.Request{
				PhotoIDs:    req.PhotoIDs,
				DestDir:     req.DestDir,
				Format:      format,
				JpegQuality: req.JpegQuality,
				LongEdge:    req.LongEdge,
			}, func(it export.Item) { items <- it })
		}()

		total := len(req.PhotoIDs)
		done := 0
		for it := range items {
			done++
			aprot.Progress(ctx).Update(done, total, it.FileName)
			out := ExportItem{PhotoID: it.PhotoID, FileName: it.FileName, OK: it.Err == nil}
			if it.Err != nil {
				out.Error = it.Err.Error()
			}
			if !yield(out) {
				cancel()
				for range items {
				} // drain so the exporter can finish
				return
			}
		}
	}, nil
}
