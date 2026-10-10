package main

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The export dialog's choices, as marraw's offers them.
var (
	exportFormats     = []string{"JPEG", "TIFF", "PNG", "RAW + XMP"}
	exportFormatKeys  = []marrawclient.ExportFormat{"jpeg", "tiff8", "png", "rawXmp"}
	exportSpaces      = []string{"sRGB", "Adobe RGB", "ProPhoto"}
	exportSpaceKeys   = []marrawclient.ColorSpace{"srgb", "adobergb", "prophoto"}
	exportSharpens    = []string{"Off", "Screen", "Matte", "Glossy"}
	exportSharpenKeys = []marrawclient.SharpenTarget{"off", "screen", "matte", "glossy"}
	exportAmounts     = []string{"Low", "Standard", "High"}
	exportAmountKeys  = []marrawclient.SharpenAmount{"low", "standard", "high"}
	exportExifs       = []string{"All metadata", "Copyright only", "None"}
	exportExifKeys    = []marrawclient.ExifMode{"all", "copyright", "none"}
)

// defaultExportOptions are an export's choices before any are made, as
// marraw's.
var defaultExportOptions = marrawclient.ExportOptions{Format: "jpeg", JpegQuality: 90, ResizeMode: "full", EdgePx: 2160,
	ColorSpace: "srgb", SharpenTarget: "off", SharpenAmount: "standard", ExifMode: "all"}

// normalExportOptions is o with every choice one the backend takes, as it
// would make them.
func normalExportOptions(o marrawclient.ExportOptions) marrawclient.ExportOptions {
	d := defaultExportOptions
	if indexOf(exportFormatKeys, o.Format) < 0 {
		o.Format = d.Format
	}
	if o.JpegQuality < 1 || o.JpegQuality > 100 {
		o.JpegQuality = d.JpegQuality
	}
	if o.ResizeMode != "edge" {
		o.ResizeMode = "full"
	}
	if o.EdgePx < 16 || o.EdgePx > 65536 {
		o.EdgePx = d.EdgePx
	}
	if indexOf(exportSpaceKeys, o.ColorSpace) < 0 {
		o.ColorSpace = d.ColorSpace
	}
	if indexOf(exportSharpenKeys, o.SharpenTarget) < 0 {
		o.SharpenTarget = d.SharpenTarget
	}
	if indexOf(exportAmountKeys, o.SharpenAmount) < 0 {
		o.SharpenAmount = d.SharpenAmount
	}
	if indexOf(exportExifKeys, o.ExifMode) < 0 {
		o.ExifMode = d.ExifMode
	}
	return o
}

func indexOf[T comparable](s []T, v T) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// exportView is the export dialog, as marraw's: a preset, where the files
// go and how they are named, then how they are made, the rows that do not
// apply folding away; and under them what the export will be, in a line.
type exportView struct {
	*widget.Dialog
	ask       ExportAsk
	active    string
	preset    *widget.Dropdown
	presetIDs []string
	save      *widget.MenuButton
	naming    *widget.Fold
	nameField *widget.TextField
	nameOp    string
	dest      *widget.TextField
	lastDest  string
	useHere   *widget.Fold
	template  *widget.TextField
	example   *widget.Label
	format    *widget.Segmented
	quality   *widget.Slider
	qualityN  *widget.Label
	qualityOn *widget.Fold
	resize    *widget.Segmented
	edge      *widget.NumberField
	edgeOn    *widget.Fold
	pixels    *widget.Fold
	space     *widget.Segmented
	sharpen   *widget.Segmented
	amount    *widget.Segmented
	amountOn  *widget.Fold
	mark      *widget.Dropdown
	markIDs   []string
	exif      *widget.Segmented
	location  *widget.Switch
	locOn     *widget.Fold
	artist    *widget.TextField
	copyright *widget.TextField
	creditOn  *widget.Fold
	summary   *widget.Label
	inPlace   *widget.Fold
}

func newExportView(s ExportAsk) *exportView {
	what := "this photo"
	if s.Count > 1 {
		what = fmt.Sprintf("%d photos", s.Count)
	}
	v := &exportView{Dialog: widget.NewDialog("Export " + what), ask: s, active: s.Active}
	v.Width = 680
	v.SetButtons("Export", "Cancel")
	o := normalExportOptions(s.Options)

	// Preset.
	v.preset = widget.NewDropdown(nil)
	v.preset.KeepFocus = true
	v.preset.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		if i <= 0 || i >= len(v.presetIDs) {
			v.active = ""
		} else {
			v.active = v.presetIDs[i]
			for _, p := range v.ask.Presets {
				if p.ID == v.active {
					v.setOptions(normalExportOptions(p.Options), u)
				}
			}
		}
		v.changed(u)
		return nil
	}
	v.save = widget.NewMenuButton("Save…", nil)
	v.save.OnPick = func(i int, u *gunim.UI) gunim.Intent { return v.presetMenu(i, u) }
	v.nameField = widget.NewTextField()
	v.nameField.Placeholder = "Preset name, as Web JPEG"
	nameOK := widget.NewButton("Save")
	nameOK.KeepFocus = true
	nameOK.OnClick = func(u *gunim.UI) gunim.Intent { return v.nameDone(u) }
	nameCancel := widget.NewButton("Cancel")
	nameCancel.Ghost, nameCancel.KeepFocus = true, true
	nameCancel.OnClick = func(u *gunim.UI) gunim.Intent {
		v.naming.SetOpen(false, u)
		return nil
	}
	v.nameField.OnCommit = func(_ string, u *gunim.UI) gunim.Intent { return v.nameDone(u) }
	v.naming = widget.NewFold(exportRow("", widget.Row(&fixedWidth{w: 260, child: v.nameField}, nameOK, nameCancel)), false)

	// Where.
	v.dest = widget.NewTextField()
	v.dest.Face = widget.MonoFont
	v.dest.Placeholder = "Destination folder"
	v.dest.SetText(s.Dest, nil)
	v.lastDest = s.Dest
	v.dest.OnChange = func(string, *gunim.UI) gunim.Intent { v.changed(nil); return nil }
	choose := widget.NewButton("Choose…")
	choose.KeepFocus, choose.OnClick = true, widget.Sends(ExportChooseDir{})
	here := widget.NewButton("Use the photos' folder")
	here.KeepFocus = true
	here.OnClick = func(u *gunim.UI) gunim.Intent {
		v.dest.SetText(v.ask.Folder, u)
		v.changed(u)
		return nil
	}
	v.useHere = widget.NewFold(exportRow("", here), false)
	inPlace := newSmallLabel("The destination is the photos' own folder: XMP sidecars are written beside the originals, and nothing is copied.")
	inPlace.Color, inPlace.MaxLines = noteInk, 3
	v.inPlace = widget.NewFold(exportRow("", inPlace), false)
	v.template = widget.NewTextField()
	v.template.Face = widget.MonoFont
	v.template.Placeholder = "{name}"
	v.template.SetText(o.FileNameTemplate, nil)
	v.template.OnChange = func(string, *gunim.UI) gunim.Intent { v.changed(nil); return nil }
	v.example = newSmallLabel("")
	v.example.Color, v.example.MaxLines = noteInk, 3

	// How.
	seg := func(labels []string, i int) *widget.Segmented {
		s := widget.NewSegmented(labels...)
		s.KeepFocus = true
		s.SetSelected(i, nil)
		s.OnChange = func(int, *gunim.UI) gunim.Intent { v.changed(nil); return nil }
		return s
	}
	v.format = seg(exportFormats, indexOf(exportFormatKeys, o.Format))
	v.quality = widget.NewSlider(1, 100)
	v.quality.Snap, v.quality.KeepFocus = 1, true
	v.quality.SetValue(float32(o.JpegQuality), nil)
	v.quality.OnChange = func(float32, *gunim.UI) gunim.Intent { v.changed(nil); return nil }
	v.qualityN = widget.NewLabel("")
	v.qualityN.Face = widget.MonoFont
	qrow := widget.Row(&fixedWidth{w: 300, child: v.quality}, v.qualityN)
	qrow.Cross = widget.CrossCenter
	v.qualityOn = widget.NewFold(exportRow("Quality", qrow), false)
	v.resize = seg([]string{"Full res", "Long edge"}, map[bool]int{false: 0, true: 1}[o.ResizeMode == "edge"])
	v.edge = widget.NewNumberField(16, 65536)
	v.edge.Suffix = " px"
	v.edge.SetValue(float64(o.EdgePx), nil)
	v.edge.OnChange = func(float64, *gunim.UI) gunim.Intent { v.changed(nil); return nil }
	v.edgeOn = widget.NewFold(&fixedWidth{w: 120, child: v.edge}, false)
	v.space = seg(exportSpaces, indexOf(exportSpaceKeys, o.ColorSpace))
	v.sharpen = seg(exportSharpens, indexOf(exportSharpenKeys, o.SharpenTarget))
	v.amount = seg(exportAmounts, indexOf(exportAmountKeys, o.SharpenAmount))
	v.amountOn = widget.NewFold(exportRow("Amount", glassSegmented(v.amount)), false)
	v.mark = widget.NewDropdown(nil)
	v.mark.KeepFocus = true
	v.mark.OnChange = func(int, *gunim.UI) gunim.Intent { v.changed(nil); return nil }
	editMarks := widget.NewButton("Edit…")
	editMarks.KeepFocus, editMarks.OnClick = true, widget.Sends(AskWatermarks{})
	v.exif = seg(exportExifs, indexOf(exportExifKeys, o.ExifMode))
	v.location = widget.NewSwitch("Remove location info")
	v.location.KeepFocus = true
	v.location.SetChecked(o.RemoveLocation, nil)
	v.locOn = widget.NewFold(exportRow("", v.location), false)
	v.artist, v.copyright = widget.NewTextField(), widget.NewTextField()
	v.artist.Placeholder, v.copyright.Placeholder = "Artist, as Jane Doe", "Copyright, as © 2026 Jane Doe"
	v.artist.SetText(o.Artist, nil)
	v.copyright.SetText(o.Copyright, nil)
	v.artist.OnChange = func(string, *gunim.UI) gunim.Intent { v.changed(nil); return nil }
	v.copyright.OnChange = v.artist.OnChange
	v.creditOn = widget.NewFold(exportRow("Credit", widget.Row(&fixedWidth{w: 230, child: v.artist}, &fixedWidth{w: 260, child: v.copyright})), false)
	v.pixels = widget.NewFold(widget.Column(
		exportRow("Resize", widget.Row(glassSegmented(v.resize), v.edgeOn)),
		exportRow("Colour space", glassSegmented(v.space)),
		exportRow("Sharpen for", glassSegmented(v.sharpen)),
		v.amountOn,
		exportRow("Watermark", widget.Row(&fixedWidth{w: 220, child: v.mark}, editMarks)),
		exportRow("Metadata", glassSegmented(v.exif)),
		v.locOn, v.creditOn), true)
	v.summary = newSmallLabel("")
	v.summary.Face, v.summary.Color, v.summary.MaxLines = widget.MonoFont, noteInk, 2

	v.Body = widget.Column(
		exportRow("Preset", widget.Row(&fixedWidth{w: 260, child: v.preset}, v.save)), v.naming,
		exportRow("Destination", widget.Row(&fixedWidth{w: 380, child: v.dest}, choose)), v.useHere, v.inPlace,
		exportRow("File name", widget.Column(&fixedWidth{w: 380, child: v.template}, v.example)),
		exportRow("Format", glassSegmented(v.format)), v.qualityOn,
		v.pixels, spacer(6), v.summary)
	if s.Single {
		v.AddAction("Copy to clipboard", func(*gunim.UI) gunim.Intent { return ExportCopy{Options: v.options()} })
	}
	v.Check = func() string {
		if strings.TrimSpace(v.dest.Text()) == "" {
			return "Where should the photos go?"
		}
		return ""
	}
	v.OnAccept = func(*gunim.UI) gunim.Intent {
		return ExportGo{OK: true, Dest: strings.TrimSpace(v.dest.Text()), Options: v.options()}
	}
	v.OnDismiss = widget.Sends(ExportGo{})
	v.showLists(s, nil)
	v.changed(nil)
	return v
}

// show takes s: the presets and watermarks as they are, and a folder
// chosen.
func (v *exportView) show(s ExportAsk, u *gunim.UI) {
	v.ask = s
	if s.Dest != v.lastDest {
		v.lastDest = s.Dest
		v.dest.SetText(s.Dest, u)
	}
	if s.Active != "" {
		v.active = s.Active
	}
	v.showLists(s, u)
	v.changed(u)
}

// showLists shows s's presets and watermarks in their drop-downs.
func (v *exportView) showLists(s ExportAsk, u *gunim.UI) {
	marks := []widget.MenuItem{{Label: "None"}}
	v.markIDs = []string{""}
	for _, m := range s.Watermarks {
		marks = append(marks, widget.MenuItem{Label: m.Name})
		v.markIDs = append(v.markIDs, m.ID)
	}
	cur := ""
	if i := v.mark.Selected(); i > 0 && i < len(v.markIDs) {
		cur = v.markIDs[i]
	} else if v.mark.Selected() < 0 {
		cur = s.Options.WatermarkID
	}
	v.mark.SetItems(marks)
	v.mark.SetSelected(max(0, indexOf(v.markIDs, cur)), u)
}

// options are the dialog's choices as they stand.
func (v *exportView) options() marrawclient.ExportOptions {
	o := marrawclient.ExportOptions{
		Format:           exportFormatKeys[max(0, v.format.Selected())],
		JpegQuality:      int(v.quality.Value()),
		ResizeMode:       map[int]string{0: "full", 1: "edge"}[v.resize.Selected()],
		EdgePx:           int(v.edge.Value()),
		ColorSpace:       exportSpaceKeys[max(0, v.space.Selected())],
		SharpenTarget:    exportSharpenKeys[max(0, v.sharpen.Selected())],
		SharpenAmount:    exportAmountKeys[max(0, v.amount.Selected())],
		FileNameTemplate: strings.TrimSpace(v.template.Text()),
		ExifMode:         exportExifKeys[max(0, v.exif.Selected())],
		RemoveLocation:   v.location.Checked(),
		Artist:           strings.TrimSpace(v.artist.Text()),
		Copyright:        strings.TrimSpace(v.copyright.Text()),
	}
	if i := v.mark.Selected(); i > 0 && i < len(v.markIDs) {
		o.WatermarkID = v.markIDs[i]
	}
	return normalExportOptions(o)
}

// setOptions shows o, as a preset chosen gives them.
func (v *exportView) setOptions(o marrawclient.ExportOptions, u *gunim.UI) {
	v.format.SetSelected(indexOf(exportFormatKeys, o.Format), u)
	v.quality.SetValue(float32(o.JpegQuality), u)
	v.resize.SetSelected(map[bool]int{false: 0, true: 1}[o.ResizeMode == "edge"], u)
	v.edge.SetValue(float64(o.EdgePx), u)
	v.space.SetSelected(indexOf(exportSpaceKeys, o.ColorSpace), u)
	v.sharpen.SetSelected(indexOf(exportSharpenKeys, o.SharpenTarget), u)
	v.amount.SetSelected(indexOf(exportAmountKeys, o.SharpenAmount), u)
	v.template.SetText(o.FileNameTemplate, u)
	v.exif.SetSelected(indexOf(exportExifKeys, o.ExifMode), u)
	v.location.SetChecked(o.RemoveLocation, u)
	v.artist.SetText(o.Artist, u)
	v.copyright.SetText(o.Copyright, u)
	v.mark.SetSelected(max(0, indexOf(v.markIDs, o.WatermarkID)), u)
}

// changed follows a choice made: the rows that apply, the example name,
// the line saying what the export will be, and the preset's name, marked
// as changed where the choices are no longer its.
func (v *exportView) changed(u *gunim.UI) {
	o := v.options()
	raw := o.Format == "rawXmp"
	v.qualityN.Text = fmt.Sprint(o.JpegQuality)
	set := func(f *widget.Fold, open bool) {
		if u == nil {
			if f.Open() != open {
				f.SetOpen(open, nil)
			}
			return
		}
		f.SetOpen(open, u)
	}
	dest := strings.TrimSpace(v.dest.Text())
	inPlace := raw && v.ask.Folder != "" && samePath(dest, v.ask.Folder)
	set(v.qualityOn, o.Format == "jpeg")
	set(v.pixels, !raw)
	set(v.edgeOn, o.ResizeMode == "edge")
	set(v.amountOn, o.SharpenTarget != "off")
	set(v.locOn, o.ExifMode == "all")
	set(v.creditOn, o.ExifMode != "none")
	set(v.useHere, raw && !inPlace && v.ask.Folder != "")
	set(v.inPlace, inPlace)
	hint := "{name} the file's name · {seq} a number · {date} {time} when taken — as " + exampleFileName(o, v.ask)
	if raw {
		hint += " · not used when exporting into the photos' own folder"
	}
	v.example.Text = hint
	v.summary.Text = exportSummary(o, v.ask.Count, inPlace, v.ask.WatermarkName(o.WatermarkID))
	// The presets, the one chosen marked where its choices have changed.
	items := []widget.MenuItem{{Label: "None"}}
	v.presetIDs = []string{""}
	sel := 0
	for _, p := range v.ask.Presets {
		label := p.Name
		if p.ID == v.active {
			sel = len(items)
			if normalExportOptions(p.Options) != o {
				label += " (changed)"
			}
		}
		items = append(items, widget.MenuItem{Label: label})
		v.presetIDs = append(v.presetIDs, p.ID)
	}
	v.preset.SetItems(items)
	v.preset.SetSelected(sel, u)
	menu := []widget.MenuItem{{Label: "Save as a new preset…"}}
	if v.active != "" {
		name := ""
		for _, p := range v.ask.Presets {
			if p.ID == v.active {
				name = p.Name
			}
		}
		menu = append(menu, widget.MenuItem{Label: "Update “" + name + "”"}, widget.MenuItem{Label: "Rename…"},
			widget.MenuItem{Label: "Delete", Break: true})
	}
	v.save.SetItems(menu)
}

// presetMenu takes a pick from the preset's menu: save as, update,
// rename or delete.
func (v *exportView) presetMenu(i int, u *gunim.UI) gunim.Intent {
	switch i {
	case 0:
		v.nameOp = "save"
		v.nameField.SetText("", u)
	case 1:
		return ExportPresetOp{Op: "update", ID: v.active, Options: v.options()}
	case 2:
		v.nameOp = "rename"
		for _, p := range v.ask.Presets {
			if p.ID == v.active {
				v.nameField.SetText(p.Name, u)
			}
		}
	case 3:
		id := v.active
		v.active = ""
		return ExportPresetOp{Op: "delete", ID: id}
	default:
		return nil
	}
	v.naming.SetOpen(true, u)
	u.Focus(v.nameField)
	return nil
}

// nameDone takes the name typed for a preset saved or renamed.
func (v *exportView) nameDone(u *gunim.UI) gunim.Intent {
	name := strings.TrimSpace(v.nameField.Text())
	if name == "" {
		return nil
	}
	v.naming.SetOpen(false, u)
	if v.nameOp == "rename" {
		return ExportPresetOp{Op: "rename", ID: v.active, Name: name}
	}
	return ExportPresetOp{Op: "save", Name: name, Options: v.options()}
}

// exportRow is a row of the export dialog: its label in a column of its
// own, and its control.
func exportRow(label string, control gunim.Node) gunim.Node {
	l := newSmallLabel(label)
	l.Color = noteInk
	return &labelledLine{label: l, child: control}
}

// labelledLine is a label beside a control, the labels a column 110
// wide.
type labelledLine struct {
	label *widget.Label
	child gunim.Node
}

// Children implements [gunim.Composite].
func (r *labelledLine) Children() []gunim.Node { return []gunim.Node{r.label, r.child} }

// Layout implements [gunim.Node].
func (r *labelledLine) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const lw, pad = 110, 6
	cs := kids.At(1).Layout(gunim.Loose(geom.Sz(max(0, c.Max.W-lw), 400)))
	h := max(cs.H, 28) + 2*pad
	ls := kids.At(0).Layout(gunim.Loose(geom.Sz(lw-8, h)))
	kids.At(0).Place(geom.Pt(0, pad+(min(cs.H, 34)-ls.H)/2))
	kids.At(1).Place(geom.Pt(lw, pad))
	return geom.Sz(c.Max.W, h)
}

// Paint implements [gunim.Node].
func (r *labelledLine) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	kids.At(1).Paint(p)
}

// samePath reports whether a and b name one folder, either spelling of
// its separators, and its case aside where the path is Windows'.
func samePath(a, b string) bool {
	clean := func(s string) string {
		s = strings.TrimRight(strings.ReplaceAll(s, "\\", "/"), "/")
		if len(s) > 1 && s[1] == ':' {
			s = strings.ToLower(s)
		}
		return s
	}
	return a != "" && clean(a) == clean(b)
}

// exampleFileName is what the first photo's file would be called, as
// marraw's example names it: the backend has the last word.
func exampleFileName(o marrawclient.ExportOptions, s ExportAsk) string {
	name := s.ExampleName
	if name == "" {
		name = "DSC00001"
	}
	ext := path.Ext(strings.ReplaceAll(name, "\\", "/"))
	base := strings.TrimSuffix(name, ext)
	digits := max(3, len(fmt.Sprint(s.Count)))
	t := o.FileNameTemplate
	if t == "" {
		t = "{name}"
	}
	date, clock := "", ""
	if s.ExampleTaken > 0 {
		tt := time.Unix(s.ExampleTaken, 0)
		date, clock = tt.Format("20060102"), tt.Format("150405")
	}
	out := strings.NewReplacer("{name}", base, "{seq}", fmt.Sprintf("%0*d", digits, 1), "{date}", date, "{time}", clock).Replace(t)
	out = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 0x20 {
			return '-'
		}
		return r
	}, out)
	out = strings.TrimRight(out, ". ")
	switch o.Format {
	case "tiff8":
		ext = ".tif"
	case "png":
		ext = ".png"
	case "rawXmp":
		if ext == "" {
			ext = ".ARW"
		}
	default:
		ext = ".jpg"
	}
	return out + ext
}

// exportSummary is what the export will be, in a line, as marraw says it.
func exportSummary(o marrawclient.ExportOptions, n int, inPlace bool, mark string) string {
	parts := []string{fmt.Sprintf("%d file%s", n, map[bool]string{false: "s", true: ""}[n == 1])}
	if o.Format == "rawXmp" {
		if inPlace {
			parts = append(parts, "XMP sidecars beside the originals")
		} else {
			parts = append(parts, "RAW copies + XMP sidecars")
		}
	} else {
		switch o.Format {
		case "jpeg":
			parts = append(parts, fmt.Sprintf("JPEG q%d", o.JpegQuality))
		case "png":
			parts = append(parts, "PNG lossless")
		default:
			parts = append(parts, "TIFF lossless")
		}
		if o.ResizeMode == "edge" {
			parts = append(parts, fmt.Sprintf("%dpx", o.EdgePx))
		} else {
			parts = append(parts, "full res")
		}
		if o.ColorSpace != "srgb" {
			parts = append(parts, exportSpaces[indexOf(exportSpaceKeys, o.ColorSpace)])
		}
		if o.SharpenTarget != "off" {
			parts = append(parts, "sharpen "+string(o.SharpenTarget))
		}
		if mark != "" {
			parts = append(parts, "wm "+mark)
		}
		switch {
		case o.ExifMode == "none":
			parts = append(parts, "no metadata")
		case o.ExifMode == "copyright":
			parts = append(parts, "© only")
		case o.RemoveLocation:
			parts = append(parts, "no location")
		}
	}
	return strings.Join(append(parts, "runs in the background"), " · ")
}
