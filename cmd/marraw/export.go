package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The vocabulary of exporting.
type (
	// AskExport opens the export dialog for the photos selected, or the
	// one showing, or all the grid shows.
	AskExport struct{}
	// ExportAsk is what the export dialog starts from.
	ExportAsk struct {
		Count    int
		Dest     string
		Format   int
		Quality  int
		Edge     int
		Template string
	}
	// ExportGo is the dialog's answer: export with these, or not.
	ExportGo struct {
		OK       bool
		Dest     string
		Format   int
		Quality  int
		Edge     int
		Template string
	}
)

// The export dialog's choices, as marraw's export dialog offers them.
var (
	exportFormats     = []string{"JPEG", "TIFF", "PNG", "RAW + XMP"}
	exportFormatKeys  = []marrawclient.ExportFormat{"jpeg", "tiff8", "png", "rawXmp"}
	exportEdges       = []string{"Full size", "4000 px", "3000 px", "2048 px", "1600 px"}
	exportEdgeValues  = []int{0, 4000, 3000, 2048, 1600}
	exportDefaultName = "{name}"
)

// newExportDialog is the export dialog, starting from s.
func newExportDialog(s ExportAsk) *widget.Dialog {
	what := "this photo"
	if s.Count > 1 {
		what = fmt.Sprintf("%d photos", s.Count)
	}
	d := widget.NewDialog("Export " + what)
	dest := widget.NewTextField()
	dest.SetText(s.Dest, nil)
	format := widget.NewDropdown(menuItems(exportFormats...))
	format.SetSelected(s.Format, nil)
	quality := widget.NewNumberField(50, 100)
	quality.SetValue(float64(s.Quality), nil)
	edge := widget.NewDropdown(menuItems(exportEdges...))
	edge.SetSelected(s.Edge, nil)
	name := widget.NewTextField()
	name.SetText(s.Template, nil)
	hint := widget.NewLabel("{name} the file's name, {seq} a number, {date} and {time} when it was taken")
	hint.Color, hint.Size, hint.MaxLines = widget.PaletteHint, noteSize, 2
	form := widget.NewForm().Add("Destination", dest).Add("Format", format).Add("JPEG quality", quality).
		Add("Size", edge).Add("File names", name)
	d.Body = widget.Column(form, hint)
	d.Width = 520
	d.SetButtons("Export", "Cancel")
	d.Check = func() string {
		if strings.TrimSpace(dest.Text()) == "" {
			return "Where should the photos go?"
		}
		return ""
	}
	d.OnAccept = func(*gunim.UI) gunim.Intent {
		return ExportGo{OK: true, Dest: strings.TrimSpace(dest.Text()), Format: format.Selected(),
			Quality: int(quality.Value()), Edge: edge.Selected(), Template: strings.TrimSpace(name.Text())}
	}
	d.OnDismiss = widget.Sends(ExportGo{})
	return d
}

// exportTargets are the photos an export takes: the selection, or the one
// showing in the cull view, or all the grid shows.
func (cu *culler) exportTargets() []int64 {
	var ids []int64
	if cu.culling || len(cu.sel) > 0 {
		for _, i := range cu.targets() {
			ids = append(ids, cu.photos[i].ID)
		}
	}
	if len(ids) == 0 {
		for _, p := range cu.photos {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// askExport opens the export dialog, starting from the choices last used.
func (cu *culler) askExport() {
	ids := cu.exportTargets()
	if len(ids) == 0 || cu.asking {
		return
	}
	cu.asking, cu.exporting = true, ids
	ask := ExportAsk{Count: len(ids), Dest: filepath.Join(cu.folderPath, "Exports"), Quality: 90, Template: exportDefaultName}
	if ui := cu.ui; ui != nil {
		o := ui.ExportOptions
		for i, f := range exportFormatKeys {
			if f == o.Format {
				ask.Format = i
			}
		}
		if o.JpegQuality > 0 {
			ask.Quality = o.JpegQuality
		}
		for i, e := range exportEdgeValues {
			if o.ResizeMode != "" && o.ResizeMode != "full" && e == o.EdgePx {
				ask.Edge = i
			}
		}
		if o.FileNameTemplate != "" {
			ask.Template = o.FileNameTemplate
		}
		if ui.ExportDir != "" && filepath.IsAbs(ui.ExportDir) {
			ask.Dest = ui.ExportDir
		}
	}
	_ = cu.c.Mount(gunim.Root, "export", "export", ask)
}

// exportGo takes the export dialog's answer, and starts the export: the
// note over the photos says how it goes.
func (cu *culler) exportGo(a ExportGo) {
	cu.asking = false
	_ = cu.c.Unmount("export")
	cu.refocus()
	ids := cu.exporting
	cu.exporting = nil
	if !a.OK || len(ids) == 0 {
		return
	}
	req := marrawclient.ExportRequest{PhotoIDs: ids, DestDir: a.Dest, Format: exportFormatKeys[a.Format],
		JpegQuality: a.Quality, LongEdge: exportEdgeValues[a.Edge], ColorSpace: "srgb", SharpenTarget: "off",
		SharpenAmount: "standard", FileNameTemplate: a.Template, ExifMode: "all", CreateDir: true}
	if req.FileNameTemplate == "" {
		req.FileNameTemplate = exportDefaultName
	}
	opts := marrawclient.ExportOptions{Format: req.Format, JpegQuality: req.JpegQuality, ResizeMode: "full",
		EdgePx: req.LongEdge, ColorSpace: req.ColorSpace, SharpenTarget: req.SharpenTarget,
		SharpenAmount: req.SharpenAmount, FileNameTemplate: req.FileNameTemplate, ExifMode: req.ExifMode}
	if req.LongEdge > 0 {
		opts.ResizeMode = "long"
	}
	if cu.ui != nil {
		cu.ui.ExportOptions, cu.ui.ExportDir = opts, a.Dest
	}
	cu.notify(fmt.Sprintf("Exporting %d %s…", len(ids), map[bool]string{false: "photos", true: "photo"}[len(ids) == 1]))
	go func() {
		_ = cu.api.Settings.SetExportOptions(cu.ctx, opts)
		_ = cu.api.Settings.SetExportDir(cu.ctx, a.Dest)
		ref, err := cu.api.Export.StartExport(cu.ctx, req)
		select {
		case cu.do <- func() {
			if err != nil || ref == nil {
				cu.fail("The export could not start", orNoAnswer(err))
				return
			}
			cu.exports[ref.TaskID] = exportRun{dest: a.Dest, count: len(ids)}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// exportRun is an export under way, for the notes of how it goes.
type exportRun struct {
	dest  string
	count int
}

// tasksChanged takes the backend's tasks as they change, and says how the
// exports under way go: every tenth of the way, and when they are done.
func (cu *culler) tasksChanged(ts []marrawclient.SharedTaskState) {
	cu.trackTasks(ts)
	for _, t := range ts {
		if cu.scanState(t) {
			continue
		}
		run, ok := cu.exports[t.ID]
		if !ok {
			continue
		}
		switch t.Status {
		case "completed":
			delete(cu.exports, t.ID)
			cu.notify(fmt.Sprintf("Exported %d %s to %s", run.count, map[bool]string{false: "photos", true: "photo"}[run.count == 1], run.dest))
		case "failed":
			delete(cu.exports, t.ID)
			why := t.Error
			if why == "" {
				why = "no reason given"
			}
			cu.fail("The export failed", errors.New(why))
		}
	}
}

// taskProgress takes how far a task has got: the tray shows it.
func (cu *culler) taskProgress(id string, current, total int) {
	cu.taskMoved(id, current, total)
}

// menuItems are labels as a drop-down's items.
func menuItems(labels ...string) []widget.MenuItem {
	out := make([]widget.MenuItem, len(labels))
	for i, l := range labels {
		out[i] = widget.MenuItem{Label: l}
	}
	return out
}
