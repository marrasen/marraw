package main

import (
	"fmt"
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// railWidth is the library sidebar's width beside the grid.
const railWidth = 270

var (
	railFill  = color.NRGBA{R: 0x13, G: 0x15, B: 0x1a, A: 0xff}
	railHot   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0c}
	railPick  = color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30}
	railMark  = color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff}
	railSize  = theme.Length("marraw.rail.size", 13)
	railCount = theme.Length("marraw.rail.count", 11.5)
)

// railView is the library sidebar: each library folder with its shoots
// under it, and the shoot showing marked. Rows come and go as the library
// changes, the mark fades from the shoot left to the one chosen, and a
// row lights under the pointer.
type railView struct {
	title *widget.Label
	empty *widget.Label
	list  *widget.List
	body  gunim.Node
	st    RailState
}

func newRailView() *railView {
	v := &railView{title: widget.NewLabel("Library"), empty: widget.NewLabel(""), list: widget.NewList()}
	v.title.Color, v.title.Size = headingInk, headingSize
	v.empty.Color, v.empty.Size = noteInk, noteSize
	v.list.SkipFocus, v.list.ClickOnce = true, true
	v.list.OnActivate = func(k widget.Key, _ *gunim.UI) gunim.Intent { return OpenShoot{Path: string(k)} }
	v.body = widget.NewScroll(v.list)
	return v
}

// railEntry is a row's item, and whether it is the shoot showing.
type railEntry struct {
	RailItem
	current bool
}

func (v *railView) show(s RailState, u *gunim.UI) {
	v.st = s
	entries := make([]railEntry, len(s.Items))
	for i, it := range s.Items {
		entries[i] = railEntry{RailItem: it, current: !it.Group && it.Path == s.Current}
	}
	widget.Sync(v.list, u, entries,
		func(e railEntry) widget.Key { return widget.Key(e.Path) },
		newRailRow, (*railRow).set)
	switch {
	case s.Loaded && len(s.Items) == 0:
		v.empty.Text = "No library folders yet. Add them in marraw, or open one with -folder."
	default:
		v.empty.Text = ""
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (v *railView) Children() []gunim.Node { return []gunim.Node{v.title, v.empty, v.body} }

// Layout implements [gunim.Node]: the title, then the rows, scrolling.
func (v *railView) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	title, empty, body := kids.At(0), kids.At(1), kids.At(2)
	ts := title.Layout(gunim.Loose(geom.Sz(box.W-32, 40)))
	title.Place(geom.Pt(16, (gridHeadHeight-ts.H)/2))
	es := empty.Layout(gunim.Loose(geom.Sz(box.W-32, 200)))
	empty.Place(geom.Pt(16, gridHeadHeight+8))
	_ = es
	body.Layout(gunim.Tight(geom.Sz(box.W, max(0, box.H-gridHeadHeight))))
	body.Place(geom.Pt(0, gridHeadHeight))
	return box
}

// Paint implements [gunim.Node].
func (v *railView) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(railFill))
	p.RRect(geom.Rc(box.W-1, 0, 1, box.H), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
	p.RRect(geom.Rc(0, gridHeadHeight-1, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}))
	for k := range kids.All {
		k.Paint(p)
	}
}

// railRow is one row of the sidebar: a shoot, its name and how many
// photos it holds, or a library folder's name over its shoots.
type railRow struct {
	anim.Group
	item       railEntry
	name, n    *widget.Label
	pick, hot  *anim.Float
	pickTarget float32
}

func newRailRow(e railEntry) *railRow {
	r := &railRow{name: widget.NewLabel(""), n: widget.NewLabel(""), pick: anim.NewFloat(0), hot: anim.NewFloat(0)}
	// One line, cut with an ellipsis where a name is long.
	r.name.Size, r.name.MaxLines = railSize, 1
	r.n.Size, r.n.Color, r.n.NoWrap, r.n.Align = railCount, noteInk, true, text.AlignEnd
	r.Add(r.pick, r.hot)
	r.set(e, nil)
	return r
}

// set shows e: the mark fades in on the shoot showing and out of the one
// left.
func (r *railRow) set(e railEntry, u *gunim.UI) {
	r.item = e
	r.name.Text = e.Name
	if e.Group {
		r.name.Color, r.name.Size = headingInk, headingSize
		r.n.Text = ""
	} else {
		r.name.Color, r.name.Size = widget.Ink, railSize
		r.n.Text = fmt.Sprint(e.Count)
	}
	to := map[bool]float32{false: 0, true: 1}[e.current]
	if u == nil {
		r.pick.Jump(to)
		return
	}
	if e.current && r.pick.Value() < 0.01 {
		// Chosen: it fades in from a touch narrower, as a press lands.
		r.pick.Jump(0)
	}
	r.pick.Animate(to, widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (r *railRow) Children() []gunim.Node { return []gunim.Node{r.name, r.n} }

// Handle implements [gunim.Handler]: the row lights under the pointer.
func (r *railRow) Handle(e input.Event, u *gunim.UI) bool {
	if r.item.Group {
		return false
	}
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
func (r *railRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	if w <= 0 {
		w = railWidth
	}
	h := float32(30)
	if r.item.Group {
		h = 34
	}
	indent := float32(16 + 14*r.item.Depth)
	name, n := kids.At(0), kids.At(1)
	ns := n.Layout(gunim.Loose(geom.Sz(60, h)))
	n.Place(geom.Pt(w-14-ns.W, (h-ns.H)/2))
	ms := name.Layout(gunim.Loose(geom.Sz(max(0, w-indent-ns.W-26), h)))
	y := (h - ms.H) / 2
	if r.item.Group {
		y = h - ms.H - 4
	}
	name.Place(geom.Pt(indent, y))
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (r *railRow) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	back := geom.Rc(6, 1, box.W-12, box.H-2)
	if h := r.hot.Value(); h > 0.01 {
		p.RRect(back, 6, paint.Solid(withAlpha(railHot, h)))
	}
	if k := r.pick.Value(); k > 0.01 {
		p.RRect(scaleAbout(back, 0.96+0.04*k), 6, paint.Solid(withAlpha(railPick, k)))
		bar := geom.Rc(back.Min.X+3, back.Min.Y+back.Size().H*(0.5-0.3*k), 3, back.Size().H*0.6*k)
		p.RRect(bar, 1.5, paint.Solid(withAlpha(railMark, k)))
	}
	for k := range kids.All {
		k.Paint(p)
	}
}
