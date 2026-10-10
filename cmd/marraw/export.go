package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The vocabulary of exporting.
type (
	// AskExport opens the export dialog for the photos selected, or the
	// one showing, or all the grid shows.
	AskExport struct{}
	// ExportAsk is what the export dialog shows: how many photos, from
	// what folder, where they go, the choices to start from, the presets
	// and watermarks to choose among, the preset chosen, and the first
	// photo's name and when it was taken, for an example file name.
	ExportAsk struct {
		Count        int
		Folder       string
		Dest         string
		Options      marrawclient.ExportOptions
		Presets      []marrawclient.ExportPreset
		Watermarks   []PresetChoice
		Active       string
		ExampleName  string
		ExampleTaken int64
		Single       bool
	}
	// ExportGo is the dialog's answer: export with these, or not; Create
	// makes the destination where it is not there yet.
	ExportGo struct {
		OK      bool
		Dest    string
		Options marrawclient.ExportOptions
		Create  bool
	}
	// ExportPresetOp saves the export's choices as a preset named Name,
	// updates the preset ID with them, renames it, or deletes it, as Op
	// says: "save", "update", "rename" or "delete".
	ExportPresetOp struct {
		Op, ID, Name string
		Options      marrawclient.ExportOptions
	}
	// ExportChooseDir asks for the destination with the system's dialog.
	ExportChooseDir struct{}
	// ExportCopy renders the photo with Options and puts it on the
	// clipboard.
	ExportCopy struct{ Options marrawclient.ExportOptions }
	// AskWatermarks opens the watermark editor on the watermark
	// Selected.
	AskWatermarks struct{ Selected string }
	// CopyImage puts the photo in hand on the clipboard as a picture,
	// rendered as the export last chose.
	CopyImage struct{}
)

// WatermarkName is the name of the watermark id, or "".
func (a ExportAsk) WatermarkName(id string) string {
	for _, w := range a.Watermarks {
		if w.ID == id {
			return w.Name
		}
	}
	return ""
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
	cu.exportAsk = cu.newExportAsk(ids)
	_ = cu.c.Mount(gunim.Root, "export", "export", cu.exportAsk)
}

// newExportAsk is what the export dialog starts from, for ids.
func (cu *culler) newExportAsk(ids []int64) ExportAsk {
	sep := "\\"
	if strings.Contains(cu.folderPath, "/") {
		sep = "/"
	}
	ask := ExportAsk{Count: len(ids), Folder: cu.folderPath, Dest: cu.folderPath + sep + "Exports",
		Options: defaultExportOptions, Single: len(ids) == 1}
	if i, ok := cu.index[ids[0]]; ok {
		ask.ExampleName, ask.ExampleTaken = cu.photos[i].FileName, cu.photos[i].TakenAt
	}
	if ui := cu.ui; ui != nil {
		if ui.ExportOptions.Format != "" {
			ask.Options = normalExportOptions(ui.ExportOptions)
		}
		if ui.ExportDir != "" {
			ask.Dest = ui.ExportDir
		}
		ask.Presets = ui.ExportPresets
		for _, w := range ui.Watermarks {
			ask.Watermarks = append(ask.Watermarks, PresetChoice{ID: w.ID, Name: w.Name})
		}
		if ask.WatermarkName(ask.Options.WatermarkID) == "" {
			ask.Options.WatermarkID = ""
		}
		for _, p := range ui.ExportPresets {
			if normalExportOptions(p.Options) == ask.Options {
				ask.Active = p.ID
				break
			}
		}
	}
	return ask
}

// exportGo takes the export dialog's answer: the destination checked,
// and asked about where it is not there yet, then the export started; the
// chip at the corner says how it goes.
func (cu *culler) exportGo(a ExportGo) {
	if !a.Create {
		cu.asking = false
		_ = cu.c.Unmount("export")
		cu.refocus()
	}
	ids := cu.exporting
	if !a.OK || len(ids) == 0 {
		cu.exporting = nil
		return
	}
	o := normalExportOptions(a.Options)
	if cu.ui != nil {
		cu.ui.ExportOptions, cu.ui.ExportDir = o, a.Dest
	}
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		if !a.Create {
			if d, err := cu.api.Export.CheckDest(ctx, a.Dest); err == nil && d != nil && !d.Exists {
				select {
				case cu.do <- func() {
					cu.pendingExport = &a
					cu.asking = true
					_ = cu.c.Mount(gunim.Root, "confirm", "confirm", ConfirmAsk{Kind: "exportCreate", Title: "Create the folder?",
						Body: "The folder " + a.Dest + " is not there yet. Create it, and export into it?", OK: "Create and export"})
				}:
				case <-cu.ctx.Done():
				}
				return
			}
		}
		_ = cu.api.Settings.SetExportOptions(cu.ctx, o)
		_ = cu.api.Settings.SetExportDir(cu.ctx, a.Dest)
		removeLocation := o.ExifMode == "all" && o.RemoveLocation
		mark := o.WatermarkID
		if o.Format == "rawXmp" {
			mark = ""
		}
		edge := 0
		if o.ResizeMode == "edge" {
			edge = o.EdgePx
		}
		req := marrawclient.ExportRequest{PhotoIDs: ids, DestDir: a.Dest, Format: o.Format, JpegQuality: o.JpegQuality,
			LongEdge: edge, ColorSpace: o.ColorSpace, SharpenTarget: o.SharpenTarget, SharpenAmount: o.SharpenAmount,
			FileNameTemplate: o.FileNameTemplate, ExifMode: o.ExifMode, RemoveLocation: removeLocation,
			Artist: o.Artist, Copyright: o.Copyright, WatermarkID: mark, CreateDir: a.Create}
		ref, err := cu.api.Export.StartExport(cu.ctx, req)
		select {
		case cu.do <- func() {
			cu.exporting = nil
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

// exportCreate takes the answer to whether to make the destination.
func (cu *culler) exportCreate(ok bool) {
	a := cu.pendingExport
	cu.pendingExport = nil
	if a == nil {
		return
	}
	if !ok {
		cu.exporting = nil
		return
	}
	a.Create = true
	cu.exportGo(*a)
}

// exportChooseDir asks for the destination with the system's dialog, and
// shows it in the export dialog.
func (cu *culler) exportChooseDir() {
	go func() {
		paths, err := cu.c.ChooseFiles(cu.ctx, chooseFolder("Choose where the photos go"))
		if err != nil || len(paths) == 0 {
			return
		}
		select {
		case cu.do <- func() {
			cu.exportAsk.Dest = paths[0]
			_ = cu.c.Update("export", cu.exportAsk)
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// exportPresetOp saves, updates, renames or deletes an export preset,
// and shows the presets anew in the dialog.
func (cu *culler) exportPresetOp(in ExportPresetOp) {
	if cu.ui == nil {
		cu.ui = &marrawclient.UISettings{}
	}
	ps := slices.Clone(cu.ui.ExportPresets)
	at := slices.IndexFunc(ps, func(p marrawclient.ExportPreset) bool { return p.ID == in.ID })
	note := ""
	switch in.Op {
	case "save":
		name := uniqueName(in.Name, ps)
		p := marrawclient.ExportPreset{ID: newPresetID(), Name: name, Options: normalExportOptions(in.Options)}
		ps = append(ps, p)
		cu.exportAsk.Active = p.ID
		note = "Saved the export preset “" + name + "”"
	case "update":
		if at < 0 {
			return
		}
		ps[at].Options = normalExportOptions(in.Options)
		note = "“" + ps[at].Name + "” has the choices as they are now"
	case "rename":
		if at < 0 || strings.TrimSpace(in.Name) == "" {
			return
		}
		ps[at].Name = strings.TrimSpace(in.Name)
	case "delete":
		if at < 0 {
			return
		}
		note = "Deleted the export preset “" + ps[at].Name + "”"
		ps = slices.Delete(ps, at, at+1)
		cu.exportAsk.Active = ""
	}
	cu.ui.ExportPresets = ps
	cu.exportAsk.Presets = ps
	_ = cu.c.Update("export", cu.exportAsk)
	if note != "" {
		cu.notify(note)
	}
	cu.call("The export presets could not be saved", func(ctx context.Context) error {
		return cu.api.Settings.SetExportPresets(ctx, ps)
	}, nil)
}

// uniqueName is name, or with (2), (3) and so on after it, so no preset
// in ps has it.
func uniqueName(name string, ps []marrawclient.ExportPreset) string {
	name = strings.TrimSpace(name)
	if len(name) > 80 {
		name = name[:80]
	}
	taken := func(n string) bool {
		return slices.ContainsFunc(ps, func(p marrawclient.ExportPreset) bool { return p.Name == n })
	}
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s (%d)", name, i); !taken(n) {
			return n
		}
	}
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
