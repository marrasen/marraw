package main

import (
	"fmt"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// addFolderView is the Add folder dialog: how to add, the drives down
// the left, the folder open and its folders at the right, and what
// adding it would bring.
type addFolderView struct {
	*widget.Dialog
	st      AddFolderState
	mode    *widget.Segmented
	about   *widget.Label
	subs    *widget.Switch
	subFold *widget.Fold
	drives  *widget.List
	back    *widget.IconButton
	crumb   *widget.Label
	items   *widget.Label
	list    *widget.List
	footer  *widget.Label
}

// The modes' words.
const (
	addShootAbout   = "Adds one folder as a single shoot. Its photos refresh while it is open. New folders beside it are not picked up: add their parent as a library folder for that."
	addLibraryAbout = "Adds a folder whose subfolders are each a shoot. marraw keeps watching it, so new shoots and new photos show up as they land on disk."
)

func newAddFolderView(s AddFolderState) *addFolderView {
	d := widget.NewDialog("Add a folder to the library")
	d.Width = 720
	v := &addFolderView{Dialog: d, st: s}
	v.mode = widget.NewSegmented("Single shoot", "Library folder of shoots")
	v.mode.KeepFocus = true
	v.mode.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		v.showMode(u)
		return AddFolderMode{Library: i == 1, Subfolders: v.subs.Checked()}
	}
	v.about = newSmallLabel(addShootAbout)
	v.about.Color, v.about.MaxLines = noteInk, 3
	v.subs = widget.NewSwitch("Include photos in subfolders")
	v.subs.KeepFocus = true
	v.subs.SetChecked(true, nil)
	v.subs.OnChange = func(on bool, _ *gunim.UI) gunim.Intent {
		return AddFolderMode{Library: v.mode.Selected() == 1, Subfolders: on}
	}
	v.subFold = widget.NewFold(widget.Column(spacer(6), v.subs), true)
	v.drives = widget.NewList()
	v.drives.SkipFocus, v.drives.ClickOnce = true, true
	v.drives.OnActivate = func(k widget.Key, _ *gunim.UI) gunim.Intent { return AddFolderNav{Path: string(k)} }
	v.back = widget.NewIconButton(icon.ChevronLeft, "Up a folder")
	v.back.KeepFocus = true
	v.back.OnClick = func(*gunim.UI) gunim.Intent {
		if p := parentDir(v.st.Path); p != v.st.Path && p != "" {
			return AddFolderNav{Path: dirOf(p)}
		}
		return nil
	}
	v.crumb = widget.NewLabel("")
	v.crumb.Face, v.crumb.MaxLines, v.crumb.Size = widget.MonoFont, 1, railCount
	v.items = newSmallLabel("")
	v.items.Color, v.items.Face = noteInk, widget.MonoFont
	v.list = widget.NewList()
	v.list.SkipFocus, v.list.ClickOnce = true, true
	v.list.OnActivate = func(k widget.Key, _ *gunim.UI) gunim.Intent { return AddFolderNav{Path: string(k)} }
	v.footer = newSmallLabel("")
	v.footer.Color, v.footer.MaxLines = noteInk, 2
	d.Body = &addFolderBody{v: v, top: widget.Column(glassSegmented(v.mode), spacer(6), v.about, v.subFold),
		drives: widget.NewScroll(v.drives), head: widget.Row(v.back, v.crumb), list: widget.NewScroll(v.list)}
	d.SetButtons("Add to library", "Cancel")
	d.AddAction("Choose with the system's dialog…", widget.Sends(AddFolderSystem{}))
	d.Check = func() string {
		switch {
		case v.st.Path == "":
			return "Open the folder to add"
		case v.st.Drive:
			return "Open a folder on the drive: a whole drive is never added"
		case v.st.Already:
			return "The library holds this folder already"
		case v.mode.Selected() == 0 && v.st.Count == 0:
			return "There are no RAW files here to add"
		}
		return ""
	}
	d.OnAccept = func(*gunim.UI) gunim.Intent {
		return AddFolderGo{OK: true, Library: v.mode.Selected() == 1, Subfolders: v.subs.Checked()}
	}
	d.OnDismiss = widget.Sends(AddFolderGo{})
	return v
}

// dirOf is path, a bare drive letter given back its slash.
func dirOf(path string) string {
	if strings.HasSuffix(path, ":") {
		return path + `\`
	}
	return path
}

// showMode shows the words and the switch of the mode chosen.
func (v *addFolderView) showMode(u *gunim.UI) {
	lib := v.mode.Selected() == 1
	v.about.Text = map[bool]string{false: addShootAbout, true: addLibraryAbout}[lib]
	v.subFold.SetOpen(!lib, u)
	v.SetButtons(map[bool]string{false: "Add to library", true: "Add as library folder"}[lib], "Cancel")
	v.showFooter()
	u.Invalidate()
}

// show takes s.
func (v *addFolderView) show(s AddFolderState, u *gunim.UI) {
	v.st = s
	widget.Sync(v.drives, u, s.Drives, func(d driveItem) widget.Key { return widget.Key(d.Path) },
		func(d driveItem) *pickRow {
			r := newPickRow()
			r.set(d.Name, "", icon.HardDrive, strings.EqualFold(d.Path, s.Path), true, u)
			return r
		},
		func(r *pickRow, d driveItem, u *gunim.UI) {
			r.set(d.Name, "", icon.HardDrive, strings.EqualFold(d.Path, s.Path), true, u)
		})
	showEntry := func(r *pickRow, e entryItem, u *gunim.UI) {
		note := "no RAW"
		switch {
		case e.RawCount > 0:
			note = fmt.Sprintf("%d RAW", e.RawCount)
		case e.HasSubdirs:
			note = "—"
		}
		r.set(e.Name, note, icon.Folder, false, e.RawCount > 0, u)
	}
	widget.Sync(v.list, u, s.Entries, func(e entryItem) widget.Key { return widget.Key(e.Path) },
		func(e entryItem) *pickRow {
			r := newPickRow()
			showEntry(r, e, u)
			return r
		}, showEntry)
	v.crumb.Text = s.Path
	switch {
	case s.Loading:
		v.items.Text = "…"
	default:
		v.items.Text = fmt.Sprintf("%d folder%s", len(s.Entries), map[bool]string{true: "", false: "s"}[len(s.Entries) == 1])
	}
	v.showFooter()
	u.Invalidate()
}

// driveItem and entryItem are the lists' items.
type (
	driveItem = marrawclient.DriveInfo
	entryItem = marrawclient.PickEntry
)

// showFooter says what adding the folder open would bring.
func (v *addFolderView) showFooter() {
	lib := v.mode.Selected() == 1
	switch {
	case v.st.Err != "":
		v.footer.Text = v.st.Err
	case v.st.Path == "":
		v.footer.Text = "Open the folder you want to add."
	case v.st.Drive:
		v.footer.Text = "A whole drive is never added. Open a folder on it."
	case v.st.Already:
		v.footer.Text = "The library holds this folder already."
	case v.st.Count < 0:
		v.footer.Text = "Counting the RAW files…"
	case v.st.Count == 0 && !lib:
		v.footer.Text = "No RAW files here."
	case lib:
		v.footer.Text = fmt.Sprintf("%s · %d RAW files in it now, and what lands later.", baseName(v.st.Path), v.st.Count)
	default:
		v.footer.Text = fmt.Sprintf("%s · %d RAW files to add.", baseName(v.st.Path), v.st.Count)
	}
}

// addFolderBody lays the dialog out: the mode at the top, the drives and
// the folders side by side, and the footer.
type addFolderBody struct {
	v                       *addFolderView
	top, drives, head, list gunim.Node
	// panes are where the two lists are, for their ground.
	panes [2]geom.Rect
}

// Children implements [gunim.Composite].
func (b *addFolderBody) Children() []gunim.Node {
	return []gunim.Node{b.top, b.drives, b.head, b.v.items, b.list, b.v.footer}
}

// Layout implements [gunim.Node].
func (b *addFolderBody) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const driveW, listH, gap = 170, 300, 14
	w := c.Max.W
	ts := kids.At(0).Layout(gunim.Loose(geom.Sz(w, 400)))
	kids.At(0).Place(geom.Point{})
	y := ts.H + gap
	kids.At(1).Layout(gunim.Tight(geom.Sz(driveW, listH)))
	kids.At(1).Place(geom.Pt(0, y))
	b.panes[0] = geom.Rc(0, y, driveW, listH)
	x := float32(driveW + gap)
	is := kids.At(3).Layout(gunim.Loose(geom.Sz(120, 30)))
	hs := kids.At(2).Layout(gunim.Loose(geom.Sz(w-x-is.W-8, 32)))
	kids.At(2).Place(geom.Pt(x, y))
	kids.At(3).Place(geom.Pt(w-is.W, y+(hs.H-is.H)/2))
	kids.At(4).Layout(gunim.Tight(geom.Sz(w-x, listH-hs.H-6)))
	kids.At(4).Place(geom.Pt(x, y+hs.H+6))
	b.panes[1] = geom.Rc(x, y+hs.H+6, w-x, listH-hs.H-6)
	fs := kids.At(5).Layout(gunim.Loose(geom.Sz(w, 40)))
	kids.At(5).Place(geom.Pt(0, y+listH+gap))
	return geom.Sz(w, y+listH+gap+fs.H)
}

// Paint implements [gunim.Node]: the panes on a darker ground.
func (b *addFolderBody) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for _, r := range b.panes {
		p.RRect(r.Inset(geom.Insets{Left: -4, Right: -4, Top: -4, Bottom: -4}), 8, paint.Solid(frost(0x08)))
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// pickRow is a row of the dialog's lists: an icon, a name, and a note at
// the right; lit when it is the one open, and dim when it holds no RAW.
type pickRow struct {
	anim.Group
	name, note *widget.Label
	ic         *icon.Icon
	on         bool
	hot, lit   *anim.Float
}

func newPickRow() *pickRow {
	r := &pickRow{name: widget.NewLabel(""), note: newSmallLabel(""), hot: anim.NewFloat(0), lit: anim.NewFloat(0)}
	r.name.Size, r.name.MaxLines = railSize, 1
	r.note.Face, r.note.Color, r.note.NoWrap, r.note.Align = widget.MonoFont, noteInk, true, text.AlignEnd
	r.Add(r.hot, r.lit)
	return r
}

// set shows name and note with ic, lit when on, dim unless full.
func (r *pickRow) set(name, note string, ic *icon.Icon, on, full bool, u *gunim.UI) {
	r.name.Text, r.note.Text, r.ic = name, note, ic
	r.name.Color = widget.Ink
	if !full {
		r.name.Color = noteInk
	}
	if on != r.on || u != nil {
		r.on = on
		r.lit.Animate(map[bool]float32{false: 0, true: 1}[on], widget.Quick.Get(u.Theme()))
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (r *pickRow) Children() []gunim.Node { return []gunim.Node{r.name, r.note} }

// Handle implements [gunim.Handler]: the row lights under the pointer.
func (r *pickRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e.(type) {
	case input.PointerEnter:
		r.hot.Animate(1, widget.Quick.Get(u.Theme()))
		u.Invalidate()
	case input.PointerLeave:
		r.hot.Animate(0, widget.Settle.Get(u.Theme()))
		u.Invalidate()
	}
	return false
}

// Layout implements [gunim.Node].
func (r *pickRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const h = 30
	w := c.Max.W
	ns := kids.At(1).Layout(gunim.Loose(geom.Sz(90, h)))
	kids.At(1).Place(geom.Pt(w-10-ns.W, (h-ns.H)/2))
	ms := kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, w-36-ns.W-16), h)))
	kids.At(0).Place(geom.Pt(32, (h-ms.H)/2))
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (r *pickRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	back := geom.Rc(2, 1, box.W-4, box.H-2)
	if h := r.hot.Value(); h > 0.01 {
		p.RRect(back, 6, paint.Solid(withAlpha(railHot, h)))
	}
	if k := r.lit.Value(); k > 0.01 {
		p.RRect(back, 6, paint.Solid(withAlpha(railPick, k)))
	}
	if r.ic != nil {
		p.Mask(icon.Stroke{Icon: r.ic, Width: 1.6, Progress: 1}, geom.Rc(10, box.H/2-7, 14, 14), noteInk.Get(f.Theme))
	}
	for k := range kids.All {
		k.Paint(p)
	}
}
