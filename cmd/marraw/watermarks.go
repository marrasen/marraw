package main

import (
	"context"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/aprot/client"
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/paint"
	xdraw "golang.org/x/image/draw"

	"github.com/marrasen/marraw/internal/marrawclient"
	"github.com/marrasen/marraw/internal/watermark"
)

type (
	// WatermarkState is what the watermark editor opens on: the
	// watermarks, and the one chosen.
	WatermarkState struct {
		List     []marrawclient.Watermark
		Selected string
		// Seq says the list is new to the editor, as when it opens; an
		// update with the Seq it has leaves its own copy be.
		Seq int
	}
	// WMPreview is the chosen watermark drawn over the photo, framed if
	// it frames.
	WMPreview struct{ Img *paint.Image }
	// WMAssetAdded is a picture the user chose for a logo, now kept by
	// the backend.
	WMAssetAdded struct {
		Info marrawclient.WatermarkAssetInfo
	}

	// WMChanged is the editor's list as the user leaves it, and the one
	// chosen; Now writes it at once instead of after a pause in typing
	// or dragging.
	WMChanged struct {
		List     []marrawclient.Watermark
		Selected string
		Now      bool
	}
	// WMAddImage asks for a picture to add as a logo.
	WMAddImage struct{}
	// WMDone closes the editor.
	WMDone struct{}
)

// wmPreviewBox is the most room the editor's preview takes.
var wmPreviewBox = image.Pt(430, 270)

// watermarker is the editor's state on the controller's side.
type watermarker struct {
	open     bool
	seq      int
	list     []marrawclient.Watermark
	selected string
	// base is the photo under the preview, fitted to wmPreviewBox.
	base *image.RGBA
	// busy says a preview is being drawn; again, that the list changed
	// while it was.
	busy, again bool
	// save is the write that waits for a pause.
	save *time.Timer
	// assets are the logos fetched for the preview, by name, in dir.
	assets map[string]string
	dir    string
}

// askWatermarks opens the watermark editor on the watermark selected,
// or the first.
func (cu *culler) askWatermarks(selected string) {
	w := &cu.wm
	if w.open {
		return
	}
	w.open = true
	w.seq++
	w.list = nil
	if cu.ui != nil {
		w.list = slices.Clone(cu.ui.Watermarks)
	}
	w.selected = selected
	st := WatermarkState{List: w.list, Selected: selected, Seq: w.seq}
	_ = cu.c.Mount(gunim.Root, "watermarks", "watermarks", st)
	_ = cu.c.Update("watermarks", st)
	w.base = nil
	cu.wmBase()
}

// wmBase fetches the photo the preview draws on: the one in hand, or
// the first, or a plain grey when there is none.
func (cu *culler) wmBase() {
	var p *marrawclient.Photo
	if at := cu.targets(); len(at) > 0 {
		p = &cu.photos[at[0]]
	} else if len(cu.exporting) > 0 {
		if i, ok := cu.index[cu.exporting[0]]; ok {
			p = &cu.photos[i]
		}
	} else if len(cu.photos) > 0 {
		p = &cu.photos[0]
	}
	if p == nil {
		cu.wm.base = wmPlain()
		cu.wmPreview()
		return
	}
	photo := *p
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		m, err := cu.im.rgba(ctx, photo, want{level: "512", stale: true, fast: true})
		base := wmPlain()
		if err == nil {
			base = fitRGBA(m, wmPreviewBox)
		}
		select {
		case cu.do <- func() {
			cu.wm.base = base
			cu.wmPreview()
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// wmPlain is a grey 3:2 frame, for the preview with no photo.
func wmPlain() *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, 420, 280))
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = 0x55, 0x5a, 0x63, 0xff
	}
	return m
}

// fitRGBA scales m down to fit box, keeping its shape.
func fitRGBA(m *image.RGBA, box image.Point) *image.RGBA {
	b := m.Bounds()
	k := min(float64(box.X)/float64(b.Dx()), float64(box.Y)/float64(b.Dy()), 1)
	w, h := max(1, int(float64(b.Dx())*k+0.5)), max(1, int(float64(b.Dy())*k+0.5))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(out, out.Bounds(), m, b, xdraw.Src, nil)
	return out
}

// wmChanged takes the editor's list: kept here and in the settings at
// once, written to the backend at once or after a pause, and drawn.
func (cu *culler) wmChanged(in WMChanged) {
	w := &cu.wm
	if !w.open {
		return
	}
	w.list, w.selected = in.List, in.Selected
	if cu.ui != nil {
		cu.ui.Watermarks = slices.Clone(in.List)
	}
	if w.save != nil {
		w.save.Stop()
		w.save = nil
	}
	if in.Now {
		cu.wmSave()
	} else {
		w.save = time.AfterFunc(400*time.Millisecond, func() {
			select {
			case cu.do <- func() { cu.wm.save = nil; cu.wmSave() }:
			case <-cu.ctx.Done():
			}
		})
	}
	cu.wmPreview()
}

// wmSave writes the list to the backend.
func (cu *culler) wmSave() {
	list := slices.Clone(cu.wm.list)
	if list == nil {
		list = []marrawclient.Watermark{}
	}
	cu.call("The watermarks could not be saved", func(ctx context.Context) error {
		return cu.api.Settings.SetWatermarks(ctx, list)
	}, nil)
}

// wmPreview draws the chosen watermark over the base, one drawing at a
// time, the latest list winning.
func (cu *culler) wmPreview() {
	w := &cu.wm
	if !w.open || w.base == nil {
		return
	}
	if w.busy {
		w.again = true
		return
	}
	var wm *marrawclient.Watermark
	for i := range w.list {
		if w.list[i].ID == w.selected {
			wm = &w.list[i]
		}
	}
	if wm == nil && len(w.list) > 0 {
		wm = &w.list[0]
	}
	var mark marrawclient.Watermark
	if wm != nil {
		mark = *wm
		mark.Elements = slices.Clone(wm.Elements)
	}
	w.busy = true
	base := w.base
	go func() {
		img := cu.wmDraw(base, mark)
		select {
		case cu.do <- func() {
			w.busy = false
			if w.open {
				_ = cu.c.Patch("watermarks", WMPreview{Img: img})
			}
			if w.again {
				w.again = false
				cu.wmPreview()
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// wmDraw is mark over base, as the exporter draws it: framed, then the
// elements, sized from the framed picture's short edge.
func (cu *culler) wmDraw(base *image.RGBA, mark marrawclient.Watermark) *paint.Image {
	spec := cu.wmSpec(mark)
	out := image.NewRGBA(base.Bounds())
	copy(out.Pix, base.Pix)
	if spec.Frame != nil {
		b := base.Bounds()
		l := spec.Frame.Layout(b.Dx(), b.Dy(), max(b.Dx(), b.Dy()))
		photo := out
		if l.PhotoW != b.Dx() || l.PhotoH != b.Dy() {
			photo = image.NewRGBA(image.Rect(0, 0, l.PhotoW, l.PhotoH))
			xdraw.CatmullRom.Scale(photo, photo.Bounds(), base, b, xdraw.Src, nil)
		}
		out = spec.Frame.Compose(photo, l)
	}
	_ = watermark.Apply(out, spec)
	return paint.NewImage(out)
}

// wmSpec is mark as internal/watermark takes it, as the backend's
// toWatermarkSpec makes it, its logos fetched to files of their own.
func (cu *culler) wmSpec(mark marrawclient.Watermark) watermark.Spec {
	var spec watermark.Spec
	for _, e := range mark.Elements {
		el := watermark.Element{Anchor: watermark.Anchor(e.Anchor), SizePct: e.SizePct, MarginPct: e.MarginPct, Opacity: e.Opacity}
		switch e.Type {
		case marrawclient.WatermarkElementTypeText:
			if strings.TrimSpace(e.Text) == "" {
				continue
			}
			el.Kind, el.Text, el.Font, el.Color = watermark.KindText, e.Text, watermark.FontID(e.Font), watermark.ParseHexColor(e.Color)
		case marrawclient.WatermarkElementTypeImage:
			path := cu.wmAsset(e.Asset)
			if path == "" {
				continue
			}
			el.Kind, el.AssetPath = watermark.KindImage, path
		case marrawclient.WatermarkElementTypeRect:
			el.Kind = watermark.KindRect
			el.Color, el.Color2 = watermark.ParseHexColor(e.Color), watermark.ParseHexColor(e.Color2)
			el.Gradient, el.Opacity2, el.GradientDir = e.Fill == marrawclient.WatermarkFillGradient, e.Opacity2, watermark.GradientDir(e.GradientDir)
			el.WidthPct, el.HeightPct = e.WidthPct, e.HeightPct
		default:
			continue
		}
		spec.Elements = append(spec.Elements, el)
	}
	if f := mark.Frame; f.Enabled {
		spec.Frame = &watermark.Frame{WidthPct: f.WidthPct, BottomPct: f.BottomPct, Color: watermark.ParseHexColor(f.Color)}
	}
	return spec
}

// wmAsset is the file holding the logo named name, fetched from the
// backend the first time, or "" when it cannot be had. It runs off the
// controller's goroutine, one drawing at a time, so the map needs no
// lock.
func (cu *culler) wmAsset(name string) string {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return ""
	}
	w := &cu.wm
	if p, ok := w.assets[name]; ok {
		return p
	}
	if w.dir == "" {
		d, err := os.MkdirTemp("", "marraw-wm-")
		if err != nil {
			return ""
		}
		w.dir = d
	}
	ctx, cancel := context.WithTimeout(cu.ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cu.im.base+"/wm/"+name+"?t="+cu.im.token, nil)
	if err != nil {
		return ""
	}
	resp, err := cu.im.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	p := filepath.Join(w.dir, name)
	if os.WriteFile(p, data, 0o600) != nil {
		return ""
	}
	if w.assets == nil {
		w.assets = map[string]string{}
	}
	w.assets[name] = p
	return p
}

// wmAddImage asks for a picture, hands it to the backend to keep, and
// gives it to the editor to add.
func (cu *culler) wmAddImage() {
	go func() {
		paths, err := cu.c.ChooseFiles(cu.ctx, driver.ChooseOptions{Title: "Choose a logo",
			Filters: []driver.FileFilter{{Name: "Pictures", Patterns: []string{"*.png", "*.jpg", "*.jpeg"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		var info *marrawclient.WatermarkAssetInfo
		if err == nil {
			kind := "image/png"
			if !strings.EqualFold(filepath.Ext(paths[0]), ".png") {
				kind = "image/jpeg"
			}
			ctx, cancel := context.WithTimeout(cu.ctx, time.Minute)
			info, err = cu.api.Settings.AddWatermarkAsset(ctx, client.Blob{ContentType: kind, Data: data})
			cancel()
		}
		select {
		case cu.do <- func() {
			switch {
			case err != nil:
				cu.fail("The logo could not be added", err)
			case info != nil && cu.wm.open:
				_ = cu.c.Patch("watermarks", WMAssetAdded{Info: *info})
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// wmDone closes the editor, writing what waits, and shows the export
// dialog the watermarks as they now are.
func (cu *culler) wmDone() {
	w := &cu.wm
	if !w.open {
		return
	}
	if w.save != nil {
		w.save.Stop()
		w.save = nil
		cu.wmSave()
	}
	w.open = false
	_ = cu.c.Unmount("watermarks")
	if cu.asking && cu.exporting != nil {
		cu.exportAsk.Watermarks = nil
		for _, m := range w.list {
			cu.exportAsk.Watermarks = append(cu.exportAsk.Watermarks, PresetChoice{ID: m.ID, Name: m.Name})
		}
		_ = cu.c.Update("export", cu.exportAsk)
	} else {
		cu.refocus()
	}
}

// wmName is a name for a new watermark that no other has.
func wmName(list []marrawclient.Watermark, base string) string {
	name := base
	for n := 2; slices.ContainsFunc(list, func(w marrawclient.Watermark) bool { return w.Name == name }); n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	return name
}
