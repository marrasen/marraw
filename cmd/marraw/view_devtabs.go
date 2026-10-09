package main

import (
	"image/color"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// panelHeadH is the room at the top of the panel for the photo's header:
// its name, flags, stars and camera, which the cull view draws there, as
// marraw's drawer has them.
const panelHeadH = 98

// The panel's colours, as marraw's dark theme has them.
var (
	primaryInk = color.NRGBA{R: 0x7c, G: 0x83, B: 0xff, A: 0xff}
	accentInk  = color.NRGBA{R: 0xc3, G: 0xc7, B: 0xff, A: 0xff}
	mutedInk   = color.NRGBA{R: 0x8b, G: 0x8f, B: 0x96, A: 0xff}
	panelLine  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x17}

	sectionLabelInk  = theme.Color("marraw.section.label", mutedInk)
	sectionLabelSize = theme.Length("marraw.section.label.size", 10)
	tabTitleSize     = theme.Length("marraw.tab.title.size", 13)
	sectionLabelPad  = theme.Insets("marraw.section.label.pad", geom.Insets{Top: 10, Bottom: 6})
	mutedInkTok      = theme.Color("marraw.muted", mutedInk)
	infoNameSize     = theme.Length("marraw.info.name.size", 12)
	infoValueSize    = theme.Length("marraw.info.value.size", 11.5)
	headNameSize     = theme.Length("marraw.head.name.size", 12.5)
	headExifSize     = theme.Length("marraw.head.exif.size", 10.5)
)

// tabsTheme is the theme of the panel's tabs: marraw's small segmented
// control, the chosen tab a soft violet with light violet text.
func tabsTheme() theme.Theme {
	return marrawTheme().With(
		theme.Set(widget.Accent, withAlpha(primaryInk, 0.25)),
		theme.Set(widget.ButtonStrongInk, accentInk),
		theme.Set(widget.Placeholder, mutedInk),
		theme.Set(widget.FieldFill, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0d}),
		theme.Set(widget.SegmentedHeight, 26),
		theme.Set(widget.SegmentedPadding, 7),
		theme.Set(widget.TextSize, 11.5))
}

// devPager is the panel's tabs: a segmented row of their names, and the
// page of the one chosen under it, the last fading as the next slides in
// a little from the side it lies on.
type devPager struct {
	anim.Group
	seg   *widget.Segmented
	bar   gunim.Node
	pages []gunim.Node
	// selected is the page showing, prev the one leaving or -1, from the
	// side the new one comes from, and slide how far it has come.
	selected, prev int
	from           float32
	slide          *anim.Float
	// dot shows on the Curve tab while a curve is set.
	dot    *anim.Float
	barH   float32
	segBox geom.Rect
}

func newDevPager(titles []string, pages []gunim.Node, onChange func(i int, u *gunim.UI) gunim.Intent) *devPager {
	g := &devPager{seg: widget.NewSegmented(titles...), pages: pages, prev: -1, slide: anim.NewFloat(1), dot: anim.NewFloat(0)}
	g.seg.KeepFocus = true
	g.seg.OnChange = onChange
	g.bar = widget.NewThemed(g.seg, tabsTheme())
	g.Add(g.slide, g.dot)
	return g
}

// Selected is the tab showing.
func (g *devPager) Selected() int { return g.selected }

// SetSelected shows tab i, the page sliding in.
func (g *devPager) SetSelected(i int, u *gunim.UI) {
	if i < 0 || i >= len(g.pages) || i == g.selected {
		return
	}
	g.prev, g.from = g.selected, 1
	if i < g.selected {
		g.from = -1
	}
	g.selected = i
	g.seg.SetSelected(i, u)
	g.slide.Jump(0)
	g.slide.Animate(1, anim.Tween{Duration: tabSlide})
	u.Invalidate()
}

// tabSlide is how long a page takes to come in.
const tabSlide = 180 * time.Millisecond

// setDot shows the Curve tab's dot, or takes it off.
func (g *devPager) setDot(on bool, u *gunim.UI) {
	to := float32(0)
	if on {
		to = 1
	}
	if g.dot.Target() != to {
		g.dot.Animate(to, widget.Quick.Get(u.Theme()))
	}
}

// Children implements [gunim.Composite].
func (g *devPager) Children() []gunim.Node { return append([]gunim.Node{g.bar}, g.pages...) }

// Layout implements [gunim.Node]: the row of tabs, then the page showing,
// and the one leaving, in the room under it. The other pages stay
// mounted, unlaid, and keep their scroll.
func (g *devPager) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const padX, padTop, padBottom = 16, 11, 6
	bar := kids.At(0)
	bs := bar.Layout(gunim.Loose(geom.Sz(c.Max.W-2*padX, 40)))
	bar.Place(geom.Pt(padX, padTop))
	g.segBox = geom.Rc(padX, padTop, bs.W, bs.H)
	g.barH = padTop + bs.H + padBottom
	page := geom.Sz(c.Max.W, max(0, c.Max.H-g.barH))
	for i := 1; i < kids.Len(); i++ {
		if i-1 != g.selected && i-1 != g.prev {
			continue
		}
		k := kids.At(i)
		k.Layout(gunim.Tight(page))
		k.Place(geom.Pt(0, g.barH))
	}
	return c.Max
}

// Paint implements [gunim.Node].
func (g *devPager) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	if d := g.dot.Value(); d > 0.01 && tabCurve < len(g.pages) {
		// The dot sits after the Curve tab's name, at its top right.
		w := g.segBox.Size().W / float32(len(g.pages))
		at := geom.Pt(g.segBox.Min.X+w*float32(tabCurve+1)-7, g.segBox.Min.Y+7)
		s := 5 * d
		p.RRect(geom.Rc(at.X-s/2, at.Y-s/2, s, s), s/2, paint.Solid(withAlpha(primaryInk, d)))
	}
	body := geom.Rect{Min: geom.Pt(0, g.barH), Max: box.Point()}
	defer p.Layer(paint.LayerOpts{Bounds: body, Opacity: 1, Clip: true})()
	s := min(max(g.slide.Value(), 0), 1)
	if g.prev >= 0 && s < 1 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: body, Opacity: 1 - s})()
			kids.At(g.prev + 1).Paint(p)
		}()
	}
	if s >= 1 {
		g.prev = -1
	}
	if s < 1 {
		defer p.Layer(paint.LayerOpts{Bounds: body, Opacity: s})()
		defer p.Push(paint.Translate(geom.Pt(g.from*(1-s)*24, 0)))()
	}
	kids.At(g.selected + 1).Paint(p)
}

// titleRow is a tab's title, and Undo and Redo at its right, as marraw's
// panel heads its Develop, Curve and Local tabs.
type titleRow struct {
	title      *widget.Label
	undo, redo *widget.IconButton
	kids       []gunim.Node
}

func newTitleRow(title string) *titleRow {
	t := &titleRow{title: widget.NewLabel(title),
		undo: widget.NewIconButton(icon.Undo2, "Undo (Ctrl+Z)"), redo: widget.NewIconButton(icon.Redo2, "Redo (Ctrl+Shift+Z)")}
	t.title.Size = tabTitleSize
	t.undo.KeepFocus, t.redo.KeepFocus = true, true
	t.undo.OnClick = func(*gunim.UI) gunim.Intent { return DevUndo{} }
	t.redo.OnClick = func(*gunim.UI) gunim.Intent { return DevUndo{Redo: true} }
	small := marrawTheme().With(theme.Set(widget.ButtonHeight, 24), theme.Set(widget.IconSize, 14))
	t.kids = []gunim.Node{t.title, widget.NewThemed(t.undo, small), widget.NewThemed(t.redo, small)}
	return t
}

// Children implements [gunim.Composite].
func (t *titleRow) Children() []gunim.Node { return t.kids }

// Layout implements [gunim.Node].
func (t *titleRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const h = 30
	w := c.Max.W
	ts := kids.At(0).Layout(gunim.Loose(geom.Sz(w, h)))
	kids.At(0).Place(geom.Pt(0, (h-ts.H)/2))
	x := w
	for i := 2; i >= 1; i-- {
		s := kids.At(i).Layout(gunim.Loose(geom.Sz(40, h)))
		x -= s.W
		kids.At(i).Place(geom.Pt(x, (h-s.H)/2))
		x -= 2
	}
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (t *titleRow) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// sectionLabel is a heading that does not fold, as marraw's Presets and
// Info tabs have them: small, in capitals, muted.
func sectionLabel(s string) gunim.Node {
	l := widget.NewLabel(strings.ToUpper(s))
	l.Size, l.Color = sectionLabelSize, sectionLabelInk
	pad := widget.NewPad(l)
	pad.Padding = sectionLabelPad
	return pad
}

// smallButtons are buttons in a row that wraps, in marraw's small outlined
// look.
func smallButtons(bs ...*widget.Button) gunim.Node {
	nodes := make([]gunim.Node, len(bs))
	for i, b := range bs {
		b.KeepFocus = true
		nodes[i] = b
	}
	th := marrawTheme().With(theme.Set(widget.ButtonHeight, 28), theme.Set(widget.ButtonPadding, 10),
		theme.Set(widget.ButtonRadius, 7), theme.Set(widget.TextSize, 12))
	return widget.NewThemed(widget.NewWrap(nodes...), th)
}

// InfoLocate shows the photo's file in the system's file manager, and
// Notify says Text over the photo.
type (
	InfoLocate struct{}
	Notify     struct{ Text string }
)
