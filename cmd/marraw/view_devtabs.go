package main

import (
	"image/color"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
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
	// histGlass is the histogram's ground: a darker pane of the glass,
	// dark enough for its channels to read.
	histGlass = color.NRGBA{R: 6, G: 7, B: 9, A: 0x80}

	sectionLabelInk  = theme.Color("marraw.section.label", mutedInk)
	sectionLabelSize = theme.Length("marraw.section.label.size", 10)
	tabTitleSize     = theme.Length("marraw.tab.title.size", 13)
	tabLabelSize     = theme.Length("marraw.tab.label.size", 11)
	tabLabelInk      = theme.Color("marraw.tab.label", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	sectionLabelPad  = theme.Insets("marraw.section.label.pad", geom.Insets{Top: 10, Bottom: 6})
	mutedInkTok      = theme.Color("marraw.muted", mutedInk)
	infoNameSize     = theme.Length("marraw.info.name.size", 12)
	infoValueSize    = theme.Length("marraw.info.value.size", 11.5)
	headNameSize     = theme.Length("marraw.head.name.size", 12.5)
	headExifSize     = theme.Length("marraw.head.exif.size", 10.5)
)

// devPager is the panel's tabs: a bar of them across the panel, and the
// page of the one chosen under it, the last fading as the next slides in
// a little from the side it lies on.
type devPager struct {
	anim.Group
	bar   *devTabBar
	pages []gunim.Node
	// selected is the page showing, prev the one leaving or -1, from the
	// side the new one comes from, and slide how far it has come.
	selected, prev int
	from           float32
	slide          *anim.Float
	barH           float32
}

func newDevPager(titles []string, pages []gunim.Node, onChange func(i int, u *gunim.UI) gunim.Intent) *devPager {
	g := &devPager{bar: newDevTabBar(titles, devTabIcons, onChange), pages: pages, prev: -1, slide: anim.NewFloat(1)}
	g.Add(g.slide)
	return g
}

// devTabIcons are the tabs' icons, by place.
var devTabIcons = []*icon.Icon{icon.SlidersHorizontal, icon.Spline, icon.Brush, icon.SwatchBook, icon.Info}

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
	g.bar.choose(i, u)
	g.slide.Jump(0)
	g.slide.Animate(1, anim.Tween{Duration: tabSlide})
	u.Invalidate()
}

// tabSlide is how long a page takes to come in.
const tabSlide = 180 * time.Millisecond

// setDot shows the Curve tab's dot, or takes it off.
func (g *devPager) setDot(on bool, u *gunim.UI) {
	if g.bar.dot.Target() != on2(on) {
		g.bar.dot.Animate(on2(on), widget.Quick.Get(u.Theme()))
	}
}

func on2(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// Children implements [gunim.Composite].
func (g *devPager) Children() []gunim.Node { return append([]gunim.Node{g.bar}, g.pages...) }

// Layout implements [gunim.Node]: the bar of tabs, then the page showing,
// and the one leaving, in the room under it. The other pages stay
// mounted, unlaid, and keep their scroll.
func (g *devPager) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	bar := kids.At(0)
	bs := bar.Layout(gunim.Tight(geom.Sz(c.Max.W, devTabBarH)))
	bar.Place(geom.Point{})
	g.barH = bs.H
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

// devTabBarH is the tab bar's height.
const devTabBarH = 60

// The tab bar's motions: the line's leading edge runs ahead and its
// trailing edge follows, so it stretches as it goes and draws together as
// it lands; the pane behind the tab glides after them.
var (
	tabLead  = anim.Spring{Response: 0.22, Damping: 0.9}
	tabTrail = anim.Spring{Response: 0.36, Damping: 0.95}
	tabPane  = anim.Spring{Response: 0.3, Damping: 0.86}
)

// devTabBar is the panel's tabs as a bar across the panel: each tab its
// icon over its name, in equal columns. The chosen one stands on a pane
// of brighter glass, a short glowing line under it; changing tabs, the
// line runs to the next, stretching as it goes, and the names and icons
// brighten as it reaches them.
type devTabBar struct {
	anim.Group
	titles   []string
	icons    []*icon.Icon
	labels   []*widget.Label
	onChange func(i int, u *gunim.UI) gunim.Intent
	selected int
	// left and right are the line's ends and pane the pane's middle, in
	// tabs from the first, at their middles; hot is how far the pointer
	// is over each tab, and dot the Curve tab's dot.
	left, right, pane *anim.Float
	hot               []*anim.Float
	dot               *anim.Float
	over              int
	kids              []gunim.Node
	col               float32
}

func newDevTabBar(titles []string, icons []*icon.Icon, onChange func(i int, u *gunim.UI) gunim.Intent) *devTabBar {
	b := &devTabBar{titles: titles, icons: icons, onChange: onChange, left: anim.NewFloat(0), right: anim.NewFloat(0),
		pane: anim.NewFloat(0), dot: anim.NewFloat(0), over: -1}
	b.Add(b.left, b.right, b.pane, b.dot)
	for _, t := range titles {
		l := widget.NewLabel(t)
		l.Size, l.Color = tabLabelSize, tabLabelInk
		b.labels = append(b.labels, l)
		b.kids = append(b.kids, l)
		h := anim.NewFloat(0)
		b.hot = append(b.hot, h)
		b.Add(h)
	}
	return b
}

// choose moves the bar to tab i.
func (b *devTabBar) choose(i int, u *gunim.UI) {
	if i == b.selected {
		return
	}
	// The edge on the side it goes leads, the other trails.
	to := float32(i)
	if i > b.selected {
		b.right.Animate(to, tabLead)
		b.left.Animate(to, tabTrail)
	} else {
		b.left.Animate(to, tabLead)
		b.right.Animate(to, tabTrail)
	}
	b.pane.Animate(to, tabPane)
	b.selected = i
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (b *devTabBar) Children() []gunim.Node { return b.kids }

// Handle implements [gunim.Handler]: a click chooses a tab, and the
// pointer lights the tab it is over.
func (b *devTabBar) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerMove:
		b.hover(b.at(e.Pos), th)
		return true
	case input.PointerLeave:
		b.hover(-1, th)
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if i := b.at(e.Pos); i >= 0 && i != b.selected {
			if b.onChange != nil {
				u.Send(b, b.onChange(i, u))
			}
		}
		return true
	}
	return false
}

// hover lights tab i, -1 for none.
func (b *devTabBar) hover(i int, th *theme.Live) {
	if i == b.over {
		return
	}
	b.over = i
	for k, h := range b.hot {
		h.Animate(on2(k == i), widget.Quick.Get(th))
	}
}

// at is the tab at p, or -1.
func (b *devTabBar) at(p geom.Point) int {
	if b.col <= 0 || p.Y < 0 || p.Y > devTabBarH {
		return -1
	}
	i := int((p.X - devTabPadX) / b.col)
	if i < 0 || i >= len(b.titles) {
		return -1
	}
	return i
}

// devTabPadX is the room either side of the tabs.
const devTabPadX = 10

// Layout implements [gunim.Node].
func (b *devTabBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	b.col = (w - 2*devTabPadX) / float32(len(b.titles))
	for i := range b.titles {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(b.col, 20)))
		k.Place(geom.Pt(devTabPadX+b.col*(float32(i)+0.5)-s.W/2, 33))
	}
	return geom.Sz(w, devTabBarH)
}

// Paint implements [gunim.Node].
func (b *devTabBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	mid := func(at float32) float32 { return devTabPadX + b.col*(at+0.5) }
	// The pane behind the chosen tab.
	pw := min(b.col-6, 62)
	pane := geom.Rc(mid(b.pane.Value())-pw/2, 7, pw, 46)
	p.RRect(pane, 11, paint.Solid(frost(0x14)))
	p.RRectStroke(pane.Inset(geom.Uniform(0.5)), 10.5, paint.Fill{}, paint.Stroke{Width: 1, Color: frost(0x1c)})
	// The tabs: lit as the line nears them, and under the pointer; white,
	// faded as far as they are not.
	lit := (b.left.Value() + b.right.Value()) / 2
	for i := range b.labels {
		near := 1 - min(abs32(lit-float32(i)), 1)
		a := 0.55 + 0.45*max(near, b.hot[i].Value()*0.6)
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(devTabPadX+b.col*float32(i), 0, b.col, box.H), Opacity: a})()
			if i < len(b.icons) && b.icons[i] != nil {
				c := geom.Pt(mid(float32(i)), 21)
				s := float32(17)
				ink := anim.Mix(anim.ColorCodec, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, accentInk, near*0.5)
				widget.PaintIcon(p, th, b.icons[i], geom.Rc(c.X-s/2, c.Y-s/2, s, s), ink)
			}
			kids.At(i).Paint(p)
		}()
	}
	// The Curve tab's dot, at its icon's top right.
	if d := b.dot.Value(); d > 0.01 && tabCurve < len(b.titles) {
		at := geom.Pt(mid(tabCurve)+12, 13)
		s := 5 * d
		p.RRect(geom.Rc(at.X-s/2, at.Y-s/2, s, s), s/2, paint.Solid(withAlpha(primaryInk, d)))
	}
	// The line under the chosen tab, stretching as it runs, glowing.
	const lineW = 22
	x0, x1 := mid(b.left.Value())-lineW/2, mid(b.right.Value())+lineW/2
	line := geom.Rc(x0, 50, x1-x0, 2.5)
	p.ShadowRRect(line, 1.25, paint.Solid(primaryInk), paint.Shadow{Blur: 6, Color: withAlpha(primaryInk, 0.7)})
	// The panel's hairline under the bar.
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(panelLine))
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

// smallButtons are buttons of glass in a row that wraps.
func smallButtons(bs ...*widget.Button) gunim.Node {
	nodes := make([]gunim.Node, len(bs))
	for i, b := range bs {
		b.KeepFocus = true
		nodes[i] = &edged{child: b}
	}
	th := glassTheme(marrawTheme().With(theme.Set(widget.ButtonHeight, 28), theme.Set(widget.ButtonPadding, 10),
		theme.Set(widget.ButtonRadius, 7), theme.Set(widget.TextSize, 12)))
	return widget.NewThemed(widget.NewWrap(nodes...), th)
}

// glassSegments is th with its segmented controls of frosted glass: a
// faint pane, the chosen option a brighter one in white.
func glassSegments(th theme.Theme) theme.Theme {
	return th.With(
		theme.Set(widget.Accent, frost(0x2e)),
		theme.Set(widget.ButtonStrongInk, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		theme.Set(widget.Placeholder, mutedInk),
		theme.Set(widget.FieldFill, frost(0x0f)))
}

// glassSegmented is seg in glass, its edge drawn.
func glassSegmented(seg gunim.Node) gunim.Node {
	return widget.NewThemed(&edged{child: seg, round: true}, glassSegments(marrawTheme()))
}

// frost is white at alpha a: over the panel's frosted glass, a paler,
// clearer pane of it.
func frost(a uint8) color.NRGBA { return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: a} }

// glassTheme is th with its buttons of frosted glass: a pale pane over
// the panel's glass, paler under the pointer.
func glassTheme(th theme.Theme) theme.Theme {
	return th.With(theme.Set(widget.ButtonFill, frost(0x16)), theme.Set(widget.ButtonHover, frost(0x2c)))
}

// glassButton is b in glass, its edge drawn, as a lone button in the
// panel.
func glassButton(b *widget.Button) gunim.Node {
	return widget.NewThemed(&edged{child: b}, glassTheme(marrawTheme()))
}

// edged draws glass's hairline edge round its child, a button or a
// segmented control: its corners a button's, or round with round set.
type edged struct {
	child gunim.Node
	round bool
}

// Children implements [gunim.Composite].
func (e *edged) Children() []gunim.Node { return []gunim.Node{e.child} }

// Layout implements [gunim.Node].
func (e *edged) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s := kids.At(0).Layout(c)
	kids.At(0).Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (e *edged) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	radius := min(widget.ButtonRadius.Get(f.Theme), box.H/2)
	if e.round {
		radius = box.H / 2
	}
	r := geom.Rect{Max: box.Point()}.Inset(geom.Uniform(0.5))
	p.RRectStroke(r, max(0, radius-0.5), paint.Fill{}, paint.Stroke{Width: 1, Color: frost(0x24)})
}

// InfoLocate shows the photo's file in the system's file manager, and
// Notify says Text over the photo.
type (
	InfoLocate struct{}
	Notify     struct{ Text string }
)
