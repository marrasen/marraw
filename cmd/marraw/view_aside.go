package main

import (
	"fmt"
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// asideW is the library's info panel's width.
const asideW = 300

type (
	// LibInfo is the photo the library's info panel shows: the one the
	// grid's keyboard is on.
	LibInfo struct {
		Folder  int64
		ID      int64
		Name    string
		Exif    string
		Rating  int
		Flag    string
		Info    PhotoInfo
		Hist    [3][256]uint32
		HasHist bool
	}
	// AsideToggle puts the library's info panel away, or brings it back.
	AsideToggle struct{}
	// LibLocate shows the photo the info panel shows in the file manager.
	LibLocate struct{ ID int64 }
)

// libAside is the library's info panel at the right of the grid, as
// marraw's: the photo the keyboard is on, its marks, histogram and
// details; with several selected, the batch card in their place; with
// none, a word to choose one.
type libAside struct {
	info  *libInfo
	batch *batchCard
	none  *widget.Label
	// many says several photos are selected, and has that a photo is
	// shown.
	many, has bool
}

func newLibAside(batch *batchCard) *libAside {
	a := &libAside{info: newLibInfo(), batch: batch, none: widget.NewLabel("Select a photo to develop.")}
	a.none.Color = noteInk
	batch.bare = true
	return a
}

// Children implements [gunim.Composite].
func (a *libAside) Children() []gunim.Node { return []gunim.Node{a.info, a.batch, a.none} }

// Layout implements [gunim.Node].
func (a *libAside) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := geom.Sz(asideW, c.Max.H)
	kids.At(0).Layout(gunim.Tight(box))
	kids.At(0).Place(geom.Point{})
	kids.At(1).Layout(gunim.Loose(box))
	kids.At(1).Place(geom.Point{})
	ns := kids.At(2).Layout(gunim.Loose(box))
	kids.At(2).Place(geom.Pt((box.W-ns.W)/2, box.H/2-ns.H/2+30))
	return box
}

// Paint implements [gunim.Node]: the panel's ground and edge, and what it
// holds now.
func (a *libAside) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(railFill))
	p.RRect(geom.Rc(0, 0, 1, box.H), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
	switch {
	case a.many:
		kids.At(1).Paint(p)
	case a.has:
		kids.At(0).Paint(p)
	default:
		r := geom.Rc(box.W/2-22, box.H/2-44, 44, 44)
		p.RRectStroke(r, 10, paint.Solid(frost(0x08)), paint.Stroke{Width: 1, Color: frost(0x18)})
		widget.PaintIcon(p, f.Theme, icon.Image, r.Inset(geom.Uniform(12)), noteInk.Get(f.Theme))
		kids.At(2).Paint(p)
	}
}

// Covers implements [gunim.Shaped]: only what it shows takes the
// pointer.
func (a *libAside) Covers(geom.Point) bool { return true }

// libInfo is one photo in the info panel.
type libInfo struct {
	st    LibInfo
	name  *widget.Label
	exif  *widget.Label
	pick  *widget.Button
	rej   *widget.Button
	stars []*widget.IconButton
	hist  *widget.Histogram
	rows  *infoRows
	body  gunim.Node
}

func newLibInfo() *libInfo {
	l := &libInfo{name: widget.NewLabel(""), exif: newSmallLabel(""), hist: widget.NewHistogram(), rows: newInfoRows()}
	l.name.Face, l.name.MaxLines = widget.MonoFont, 1
	l.exif.Face, l.exif.Color, l.exif.MaxLines = widget.MonoFont, noteInk, 2
	l.pick, l.rej = widget.NewButton("P"), widget.NewButton("X")
	l.pick.Tooltip, l.rej.Tooltip = "Pick (P)", "Reject (X)"
	l.pick.KeepFocus, l.rej.KeepFocus = true, true
	l.pick.OnClick = func(*gunim.UI) gunim.Intent { return Mark{Flag: "pick", ID: l.st.ID} }
	l.rej.OnClick = func(*gunim.UI) gunim.Intent { return Mark{Flag: "exclude", ID: l.st.ID} }
	var stars []gunim.Node
	for n := 1; n <= 5; n++ {
		s := widget.NewIconButton(icon.Star, fmt.Sprintf("Rate %d", n))
		s.KeepFocus = true
		s.OnClick = func(*gunim.UI) gunim.Intent { return Rate{Stars: n, ID: l.st.ID, Toggle: true} }
		l.stars = append(l.stars, s)
		stars = append(stars, s)
	}
	small := marrawTheme().With(theme.Set(widget.ButtonHeight, 24), theme.Set(widget.ButtonPadding, 8), theme.Set(widget.TextSize, 11))
	flags := widget.NewThemed(&hflow{items: []gunim.Node{&edged{child: l.pick}, &edged{child: l.rej}}, gap: 6}, glassTheme(small))
	starRow := widget.NewThemed(&hflow{items: stars, gap: 0}, small.With(theme.Set(widget.IconSize, 15)))
	head := &asideHead{name: l.name, flags: flags, stars: starRow, exif: l.exif}
	locate, copyAll := widget.NewButton("Locate on disk"), widget.NewButton("Copy")
	copyDir, copyName := widget.NewButton("Copy folder path"), widget.NewButton("Copy filename")
	locate.Icon, copyAll.Icon, copyDir.Icon, copyName.Icon = icon.ExternalLink, icon.Copy, icon.Folder, icon.FileText
	locate.OnClick = func(*gunim.UI) gunim.Intent { return LibLocate{ID: l.st.ID} }
	copyAll.OnClick = func(u *gunim.UI) gunim.Intent {
		u.SetClipboard(l.rows.text())
		return Notify{Text: "Info copied"}
	}
	copyDir.OnClick = func(u *gunim.UI) gunim.Intent {
		u.SetClipboard(l.st.Info.Folder)
		return Notify{Text: "Folder path copied"}
	}
	copyName.OnClick = func(u *gunim.UI) gunim.Intent {
		u.SetClipboard(l.st.Info.File)
		return Notify{Text: "File name copied"}
	}
	body := widget.Column(sectionLabel("Histogram"),
		widget.NewThemed(l.hist, marrawTheme().With(theme.Set(widget.HistogramHeight, 120), theme.Set(widget.HistogramFill, histGlass))),
		sectionLabel("Info"), l.rows, spacer(8), smallButtons(locate, copyAll, copyDir, copyName))
	pad := widget.NewPad(body)
	pad.Padding = theme.Insets("marraw.aside.pad", geom.Insets{Left: 16, Right: 16, Top: 4, Bottom: 16})
	l.body = widget.Column(head, widget.NewScroll(pad))
	return l
}

// show takes the photo s.
func (l *libInfo) show(s LibInfo, u *gunim.UI) {
	l.st = s
	l.name.Text, l.exif.Text = s.Name, s.Exif
	l.pick.Active, l.rej.Active = s.Flag == "pick", s.Flag == "exclude"
	for i, st := range l.stars {
		lit := i < s.Rating
		st.Icon = map[bool]*icon.Icon{false: icon.Star, true: starLit}[lit]
		st.Ink = map[bool]theme.Token[color.NRGBA]{false: noteInk, true: selStarInk}[lit]
	}
	l.rows.show(s.Info)
	if s.HasHist {
		l.hist.SetCounts(s.Hist, u)
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (l *libInfo) Children() []gunim.Node { return []gunim.Node{l.body} }

// Layout implements [gunim.Node].
func (l *libInfo) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(c.Max))
	kids.At(0).Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (l *libInfo) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// asideHead is the photo's header in the info panel, as the develop
// panel's: its name with its flags at the right, its stars, its camera.
type asideHead struct {
	name, exif   *widget.Label
	flags, stars gunim.Node
}

// Children implements [gunim.Composite].
func (h *asideHead) Children() []gunim.Node { return []gunim.Node{h.name, h.flags, h.stars, h.exif} }

// Layout implements [gunim.Node].
func (h *asideHead) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	fs := kids.At(1).Layout(gunim.Loose(geom.Sz(w, 40)))
	kids.At(1).Place(geom.Pt(w-16-fs.W, 14))
	ns := kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, w-32-fs.W-8), 30)))
	kids.At(0).Place(geom.Pt(16, 14+(fs.H-ns.H)/2))
	y := 14 + max(fs.H, ns.H) + 6
	ss := kids.At(2).Layout(gunim.Loose(geom.Sz(w, 30)))
	kids.At(2).Place(geom.Pt(10, y))
	y += ss.H + 4
	es := kids.At(3).Layout(gunim.Loose(geom.Sz(w-32, 40)))
	kids.At(3).Place(geom.Pt(16, y))
	return geom.Sz(w, y+es.H+12)
}

// Paint implements [gunim.Node]: the header, and a line under it.
func (h *asideHead) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
}
