package main

import (
	"context"
	"errors"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// exportCopy takes Copy to clipboard from the export dialog: it closes
// the dialog, keeps its choices for next time, and copies the photo.
func (cu *culler) exportCopy(in ExportCopy) {
	cu.asking = false
	_ = cu.c.Unmount("export")
	cu.refocus()
	ids := cu.exporting
	cu.exporting = nil
	o := normalExportOptions(in.Options)
	if cu.ui != nil {
		cu.ui.ExportOptions = o
	}
	if len(ids) == 1 {
		cu.copyPhoto(ids[0], o)
	}
}

// copyImage is Ctrl+Shift+C: the one photo in hand onto the clipboard, as
// the export last chose to size, sharpen and mark it.
func (cu *culler) copyImage() {
	at := cu.targets()
	if len(at) != 1 {
		cu.notify("Choose one photo to copy")
		return
	}
	o := defaultExportOptions
	if cu.ui != nil && cu.ui.ExportOptions.Format != "" {
		o = normalExportOptions(cu.ui.ExportOptions)
	}
	cu.copyPhoto(cu.photos[at[0]].ID, o)
}

// copyPhoto renders id for the clipboard and puts it there, stopping a
// copy still under way first. The picture is sRGB with no metadata, so a
// paste never carries where it was taken.
func (cu *culler) copyPhoto(id int64, o marrawclient.ExportOptions) {
	if cu.stopCopy != nil {
		cu.stopCopy()
	}
	ctx, cancel := context.WithTimeout(cu.ctx, 2*time.Minute)
	cu.stopCopy = cancel
	req := marrawclient.ClipboardRenderRequest{PhotoID: id, SharpenTarget: o.SharpenTarget,
		SharpenAmount: o.SharpenAmount, WatermarkID: o.WatermarkID}
	if o.ResizeMode == "edge" {
		req.LongEdge = o.EdgePx
	}
	cu.notify("Rendering for the clipboard…")
	go func() {
		defer cancel()
		err := func() error {
			b, err := cu.api.Export.RenderClipboard(ctx, req)
			if err != nil {
				return err
			}
			return cu.c.SetClipboardImage(b.Data)
		}()
		select {
		case cu.do <- func() {
			switch {
			case errors.Is(err, context.Canceled):
			case err != nil:
				cu.fail("The photo could not be copied", err)
			default:
				cu.notify("Copied — ready to paste")
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}
