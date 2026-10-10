package main

import (
	"fmt"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// ShowShortcuts opens the overlay of the keys, as ? does in marraw.
type ShowShortcuts struct{}

// paletteFor says where the palette opens: in the cull view, with the
// develop panel open, or in the grid with the keyboard on a photo.
type paletteFor struct {
	culling, panel bool
	cursor         int
}

// The grid's state and the library as the window last had them, for the
// palette to offer views and shoots wherever it opens.
var (
	lastGrid GridState
	lastRail RailState
)

// paletteEntry is one thing the palette offers, and the intent it sends.
type paletteEntry struct {
	item widget.PaletteItem
	do   gunim.Intent
}

// paletteEntries are what the palette offers where it opens.
func paletteEntries(at paletteFor) []paletteEntry {
	var out []paletteEntry
	add := func(title, hint string, do gunim.Intent, also ...string) {
		out = append(out, paletteEntry{widget.PaletteItem{Title: title, Hint: hint, Also: also}, do})
	}
	if at.culling {
		add("Back to the grid", "Esc", LeaveCull{}, "library", "grid")
		add("Develop panel", "D", ToggleDevelop{}, "edit", "adjust")
		add("Develop tab", "Tab", DevTab{Index: tabDevelop, Open: true}, "panel")
		add("Curve tab", "", DevTab{Index: tabCurve, Open: true}, "panel", "tone curve")
		add("Local tab (masks)", "", DevTab{Index: tabLocal, Open: true}, "panel", "mask")
		add("Presets tab", "", DevTab{Index: tabPresets, Open: true}, "panel")
		add("Info tab", "", DevTab{Index: tabInfo, Open: true}, "panel", "metadata", "exif")
	} else if at.cursor >= 0 {
		add("Open in the cull view", "Enter", OpenCull{Index: at.cursor}, "loupe", "cull")
	}
	add("Export…", "Ctrl+E", AskExport{}, "save", "jpeg")
	add("Undo", "Ctrl+Z", DevUndo{})
	add("Redo", "Ctrl+Shift+Z", DevUndo{Redo: true})
	add("Copy edit settings", "Ctrl+C", EditCopy{})
	add("Paste edit settings", "Ctrl+V", EditPaste{})
	add("Reset edit", "Ctrl+0", DevReset{}, "clear")
	add("Move to the Recycle Bin…", "Delete", AskDelete{}, "delete", "remove", "trash")
	if at.culling && at.panel {
		add("Auto tone", "Ctrl+U", DevAuto{Sections: []string{"tone"}})
		add("Auto white balance and colour", "Ctrl+Shift+U", DevAuto{Sections: []string{"wb", "color"}}, "color")
		add("Auto everything", "Ctrl+Alt+U", DevAuto{Sections: []string{"all"}})
		add("White balance eyedropper", "W", DevWBPick{On: true}, "pick", "neutral")
		add("Crop and straighten", "R", ToggleCrop{}, "rotate", "straighten", "flip")
		add("Add a linear gradient mask", "", MaskAdd{Kind: "linear"}, "local", "mask")
		add("Add a radial mask", "", MaskAdd{Kind: "radial"}, "local", "mask")
		add("Add a brush mask", "", MaskAdd{Kind: "brush"}, "local", "mask", "paint")
		add("Add a range mask", "", MaskAdd{Kind: "range"}, "local", "mask", "colour", "luminance")
		add("Add an AI subject mask", "", MaskAI{Kind: "subject"}, "local", "mask")
		add("Add an AI background mask", "", MaskAI{Kind: "background"}, "local", "mask")
		add("Add an AI depth mask", "", MaskAI{Kind: "depth"}, "local", "mask", "distance")
		add("Tilt shift", "", MaskAI{Kind: "tilt"}, "local", "mask", "blur", "miniature")
		add("Pick a scene region as a mask", "", MaskAI{Kind: "scene"}, "local", "mask", "sky", "foliage")
		add("Pick a person as a mask", "", MaskAI{Kind: "people"}, "local", "mask", "people")
		add("Heal spots", "Q", HealToggle{}, "retouch", "clone", "fill", "spot", "dust")
		add("Auto crop around the subject", "", CropAuto{}, "crop", "subject")
		add("Keep this burst frame: pick it, reject the rest", "Shift+P", BurstKeep{Pick: true}, "burst", "sharpest")
		add("Save the edit as a preset", "", AskPreset{}, "look")
	}
	add("Keyboard shortcuts", "?", ShowShortcuts{}, "keys", "help")
	add("Judge the bursts: pick the sharpest of each, reject the rest", "", JudgeBursts{}, "burst", "auto")
	add("Look for closed eyes", "", CheckEyes{}, "blink", "eyes", "analyse")
	add("Find the subjects, to judge their sharpness", "", CheckSubjects{}, "focus", "sharp", "analyse")
	if lastGrid.FolderID != 0 {
		v := lastGrid.View
		sortNames := []string{"capture time, oldest first", "capture time, newest first", "file name, A to Z", "file name, Z to A"}
		sortKeys := []string{"captureAsc", "captureDesc", "nameAsc", "nameDesc"}
		ratingKeys := []string{"any rating", "★ and up", "★★ and up", "★★★ and up", "★★★★ and up", "★★★★★"}
		for i, name := range sortNames {
			nv := v
			nv.Sort = sortKeys[i]
			add("Sort by "+name, "", SetLibView{View: nv}, "order")
		}
		for i, name := range flagNames {
			nv := v
			nv.Flag = flagKeys[i]
			add("Show "+name, "", SetLibView{View: nv}, "filter", "flag")
		}
		for _, t := range []struct {
			name string
			on   bool
			flip func(*LibView)
		}{
			{"soft photos", v.Soft, func(v *LibView) { v.Soft = !v.Soft }},
			{"photos with closed eyes", v.Blinks, func(v *LibView) { v.Blinks = !v.Blinks }},
			{"the sharpest frame of each burst", v.Collapse, func(v *LibView) { v.Collapse = !v.Collapse }},
		} {
			nv := v
			t.flip(&nv)
			title := "Show " + t.name + " only"
			if t.on {
				title = "Show all, not only " + t.name
			}
			add(title, "", SetLibView{View: nv}, "filter", "aid")
		}
		for r, name := range ratingKeys {
			nv := v
			nv.MinRating = r
			add("Show "+name, "", SetLibView{View: nv}, "filter", "stars", "rating")
		}
	}
	for _, it := range lastRail.Items {
		if it.Group {
			continue
		}
		e := paletteEntry{widget.PaletteItem{Title: "Open " + it.Name, Detail: fmt.Sprintf("%d photos", it.Count),
			Also: []string{"shoot", "folder"}}, OpenShoot{Path: it.Path}}
		out = append(out, e)
	}
	return out
}

// openPalette opens the command palette over opener: what can be done
// where it opens, found by typing part of its name, as Ctrl+K does in
// marraw.
func openPalette(opener gunim.Node, room geom.Rect, u *gunim.UI, at paletteFor) {
	entries := paletteEntries(at)
	p := &widget.Palette{Placeholder: "Jump to anything…"}
	for _, e := range entries {
		p.Items = append(p.Items, e.item)
	}
	p.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		if i >= 0 && i < len(entries) {
			u.Send(opener, entries[i].do)
		}
		return nil
	}
	p.Open(opener, geom.Rc(room.Center().X-1, room.Min.Y+40, 2, 2), u)
}

// shortcutGroups are the keys the overlay lists, as this build has them.
var shortcutGroups = []struct {
	title string
	keys  [][2]string
}{
	{"Everywhere", [][2]string{
		{"0–5", "Rate"}, {"P / X / U", "Pick / reject / clear, P and X again take it off"},
		{"Shift+P / Shift+X", "Pick this burst frame and reject the rest / only reject the rest"},
		{"Ctrl+Z / Ctrl+Shift+Z", "Undo / redo"}, {"Ctrl+C / Ctrl+V", "Copy / paste edit settings"},
		{"Ctrl+0", "Reset edit"}, {"Ctrl+E", "Export"}, {"Delete", "Move to the Recycle Bin"},
		{"Ctrl+K", "Command palette"}, {"?", "These keys"},
	}},
	{"Grid", [][2]string{
		{"Arrows, click, Ctrl, Shift", "Select"}, {"Enter, double click", "Open in the cull view"},
		{"Ctrl+wheel", "Tile size"},
	}},
	{"Cull view", [][2]string{
		{"Left / Right", "Previous / next photo"}, {"Home / End", "First / last"},
		{"Z, Space, double click", "Fit or 100%"}, {"+ / −, wheel", "Zoom"}, {"Shift+arrows, drag, flick", "Pan"},
		{"Backspace (hold)", "The original, before any edit"}, {"D", "Develop panel"}, {"Esc, Enter", "Back to the grid"},
	}},
	{"Develop panel", [][2]string{
		{"Up / Down", "Choose a control"}, {"+ / − (Shift)", "Step it (further)"},
		{"E B T I K G S C A V O H N M", "Jump to a control"}, {"W", "White balance eyedropper"},
		{"Ctrl+U (Shift, Alt)", "Auto tone (colour, everything)"}, {"Esc", "Let the control go"},
		{"Tab / Shift+Tab", "Next / previous panel tab"},
		{"R", "Crop and straighten; Enter, Esc or R again to finish"},
		{"Ctrl+1–9 / Ctrl+Shift+1–9", "Creative preset / your own preset, by its place"},
	}},
	{"Local tab", [][2]string{
		{"Up / Down", "Walk the masks' controls"}, {"+ / − (Shift)", "Step the control (further)"},
		{"Esc", "Let the mask go; put Scene or People picking away"},
		{"Q", "Heal spots, on and off"}, {"Delete (a spot chosen)", "Delete the spot"},
		{"1–9 / 0 (a spot chosen)", "The spot's opacity, 10–90% / whole"},
	}},
}

// newShortcutsDialog is the overlay of the keys.
func newShortcutsDialog(struct{}) *widget.Dialog {
	d := widget.NewDialog("Keyboard shortcuts")
	var rows []gunim.Node
	for _, g := range shortcutGroups {
		h := widget.NewLabel(g.title)
		h.Color, h.Size = headingInk, headingSize
		rows = append(rows, widget.NewPad(h))
		for _, k := range g.keys {
			key := newSmallLabel(k[0])
			key.Color = autoInk
			what := newSmallLabel(k[1])
			rows = append(rows, &keyRow{key: key, what: what})
		}
	}
	d.Body = widget.Column(rows...)
	d.Width = 560
	d.SetButtons("Close", "")
	d.OnAccept = widget.Sends(Confirmed{Kind: "shortcuts"})
	d.OnDismiss = widget.Sends(Confirmed{Kind: "shortcuts"})
	return d
}

// showShortcuts opens the overlay of the keys.
func (cu *culler) showShortcuts() {
	if cu.asking {
		return
	}
	cu.asking = true
	_ = cu.c.Mount(gunim.Root, "confirm", "shortcuts", struct{}{})
}

// keyRow is a shortcut in the list of them: its keys, and what they do.
type keyRow struct{ key, what *widget.Label }

// keyColumn is how wide the keys' column is.
const keyColumn = 190

// Children implements [gunim.Composite].
func (r *keyRow) Children() []gunim.Node { return []gunim.Node{r.key, r.what} }

// Layout implements [gunim.Node].
func (r *keyRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	ks := kids.At(0).Layout(gunim.Loose(geom.Sz(keyColumn-12, c.Max.H)))
	ws := kids.At(1).Layout(gunim.Loose(geom.Sz(max(0, c.Max.W-keyColumn), c.Max.H)))
	h := max(ks.H, ws.H) + 8
	kids.At(0).Place(geom.Pt(0, (h-ks.H)/2))
	kids.At(1).Place(geom.Pt(keyColumn, (h-ws.H)/2))
	return geom.Sz(c.Max.W, h)
}

// Paint implements [gunim.Node].
func (r *keyRow) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	kids.At(1).Paint(p)
}
