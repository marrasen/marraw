package main

import (
	"fmt"
	"image/color"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The error cards' look.
var (
	errorInk      = color.NRGBA{R: 0xf2, G: 0x8c, B: 0x8c, A: 0xff}
	errTitleSize  = theme.Length("marraw.error.title.size", 12.5)
	errDetailSize = theme.Length("marraw.error.detail.size", 11)
	errCardIn     = anim.Spring{Response: 0.32, Damping: 0.86}
	errCardOut    = anim.Tween{Duration: 160 * time.Millisecond}
)

// errCardW is an error card's width.
const errCardW = 340

// errorTray is the errors not cleared yet, as cards stacked up from a
// corner, newest at the bottom, as marraw's toasts are; but they stay
// until the user clears them. It covers its parent and takes the
// pointer only on its cards.
type errorTray struct {
	anim.Group
	list  []ErrorNote
	cards map[int]*errCard
	// clearAll is a button over the stack while there are two or more.
	clearAll *widget.Button
	clearIn  *anim.Float
	clearBox geom.Rect
	// corner is where the stack's bottom right corner is, set by the
	// parent before it lays the tray out.
	corner geom.Point
	rects  []geom.Rect
	kids   []gunim.Node
}

func newErrorTray() *errorTray {
	t := &errorTray{cards: map[int]*errCard{}, clearAll: widget.NewButton("Clear all"), clearIn: anim.NewFloat(0)}
	t.clearAll.KeepFocus = true
	t.clearAll.OnClick = widget.Sends(ErrClear{All: true})
	th := marrawTheme().With(theme.Set(widget.ButtonHeight, 26), theme.Set(widget.ButtonPadding, 10),
		theme.Set(widget.ButtonRadius, 13), theme.Set(widget.TextSize, 12))
	t.kids = []gunim.Node{widget.NewThemed(t.clearAll, th)}
	t.Add(t.clearIn)
	return t
}

// set shows list, cards coming and going.
func (t *errorTray) set(list []ErrorNote, u *gunim.UI) {
	t.list = list
	t.clearIn.Animate(on(len(list) > 1), widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (t *errorTray) Children() []gunim.Node { return t.kids }

// Covers implements [gunim.Shaped]: the cards and the button take the
// pointer, and what lies between them is the parent's.
func (t *errorTray) Covers(p geom.Point) bool {
	if t.clearIn.Value() > 0.5 && t.clearBox.Contains(p) {
		return true
	}
	return slices.ContainsFunc(t.rects, func(r geom.Rect) bool { return r.Contains(p) })
}

// Layout implements [gunim.Node]: the cards stacked up from the corner,
// built as errors come and dropped, fading, as they are cleared.
func (t *errorTray) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	want := map[int]bool{}
	for _, e := range t.list {
		want[e.ID] = true
	}
	for id, card := range t.cards {
		if !want[id] {
			kids.Drop(card)
			delete(t.cards, id)
		}
	}
	byNode := map[gunim.Node]gunim.Child{}
	for k := range kids.All {
		byNode[k.Node()] = k
	}
	y := t.corner.Y
	t.rects = t.rects[:0]
	for i := len(t.list) - 1; i >= 0; i-- {
		e := t.list[i]
		card, ok := t.cards[e.ID]
		var k gunim.Child
		if !ok {
			card = newErrCard(e)
			t.cards[e.ID] = card
			k = kids.Build(card)
		} else {
			k = byNode[card]
		}
		card.show(e)
		s := k.Layout(gunim.Tight(geom.Sz(errCardW, card.height())))
		y -= s.H
		at := geom.Pt(t.corner.X-errCardW, y)
		if !card.placed {
			card.y.Jump(at.Y)
			card.placed = true
		} else if card.y.Target() != at.Y {
			card.y.Animate(at.Y, errCardIn)
		}
		k.Place(geom.Pt(at.X, card.y.Value()))
		t.rects = append(t.rects, geom.Rc(at.X, card.y.Value(), s.W, s.H))
		y -= 8
	}
	// The cards leaving keep where they were as they fade.
	for k := range kids.All {
		if card, ok := k.Node().(*errCard); ok && !want[card.e.ID] {
			k.Layout(gunim.Tight(geom.Sz(errCardW, card.height())))
			k.Place(geom.Pt(t.corner.X-errCardW, card.y.Value()))
		}
	}
	b := kids.At(0)
	bs := b.Layout(gunim.Loose(geom.Sz(200, 40)))
	t.clearBox = geom.Rc(t.corner.X-bs.W, y-bs.H, bs.W, bs.H)
	if t.clearIn.Value() > 0.01 {
		b.Place(t.clearBox.Min)
	} else {
		b.Place(geom.Pt(-10000, -10000))
	}
	return c.Max
}

// Paint implements [gunim.Node].
func (t *errorTray) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	if k := t.clearIn.Value(); k > 0.01 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: t.clearBox.Inset(geom.Uniform(-30)), Opacity: min(k, 1)})()
			paintGlass(p, t.clearBox, t.clearBox.Size().H/2)
			kids.At(0).Paint(p)
		}()
	}
	for i := 1; i < kids.Len(); i++ {
		kids.At(i).Paint(p)
	}
}

// errCard is one error: a mark, what failed, the error under it in small
// type, how many times it came, and a cross to clear it.
type errCard struct {
	anim.Group
	e             ErrorNote
	title, detail *widget.Label
	count         *widget.Label
	close         *widget.IconButton
	in, y         *anim.Float
	placed        bool
	h             float32
	kids          []gunim.Node
}

func newErrCard(e ErrorNote) *errCard {
	c := &errCard{e: e, title: widget.NewLabel(e.Text), detail: widget.NewLabel(e.Detail), count: widget.NewLabel(""),
		close: widget.NewIconButton(icon.X, "Clear"), in: anim.NewFloat(0), y: anim.NewFloat(0)}
	c.title.Size, c.title.MaxLines = errTitleSize, 2
	c.detail.Face, c.detail.Size, c.detail.Color, c.detail.MaxLines = widget.MonoFont, errDetailSize, mutedInkTok, 3
	c.count.Face, c.count.Size, c.count.Color = widget.MonoFont, errDetailSize, mutedInkTok
	c.close.KeepFocus = true
	id := e.ID
	c.close.OnClick = func(*gunim.UI) gunim.Intent { return ErrClear{ID: id} }
	small := marrawTheme().With(theme.Set(widget.ButtonHeight, 22), theme.Set(widget.IconSize, 13))
	c.kids = []gunim.Node{c.title, c.detail, c.count, widget.NewThemed(c.close, small)}
	c.Add(c.in, c.y)
	return c
}

// show takes e, as it counts up.
func (c *errCard) show(e ErrorNote) {
	c.e = e
	c.title.Text, c.detail.Text = e.Text, e.Detail
	c.count.Text = ""
	if e.Count > 1 {
		c.count.Text = fmt.Sprintf("×%d", e.Count)
	}
}

// height is the card's height at its width, from its last layout.
func (c *errCard) height() float32 { return max(c.h, 52) }

// Children implements [gunim.Composite].
func (c *errCard) Children() []gunim.Node { return c.kids }

// Transition implements [gunim.Transitioner]: a card slides in from the
// side and fades out where it is.
func (c *errCard) Transition(p gunim.Presence, _ gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		c.in.Animate(1, errCardIn)
	case gunim.Exiting:
		c.in.Animate(0, errCardOut)
	}
	return !c.in.Active()
}

// Layout implements [gunim.Node].
func (c *errCard) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const pad, mark = 12, 16
	w := cs.Max.W
	x := float32(pad + mark + 10)
	bs := kids.At(3).Layout(gunim.Loose(geom.Sz(30, 30)))
	kids.At(3).Place(geom.Pt(w-pad+4-bs.W, pad-4))
	ns := kids.At(2).Layout(gunim.Loose(geom.Sz(60, 20)))
	text := max(0, w-x-pad-bs.W-ns.W-4)
	ts := kids.At(0).Layout(gunim.Loose(geom.Sz(text, 60)))
	kids.At(0).Place(geom.Pt(x, pad))
	kids.At(2).Place(geom.Pt(x+ts.W+6, pad+ts.H-ns.H))
	ds := kids.At(1).Layout(gunim.Loose(geom.Sz(w-x-pad, 80)))
	kids.At(1).Place(geom.Pt(x, pad+ts.H+4))
	c.h = pad + ts.H + 4 + ds.H + pad
	return geom.Sz(w, cs.Max.H)
}

// Paint implements [gunim.Node].
func (c *errCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	k := min(max(c.in.Value(), 0), 1)
	if k < 0.01 {
		return
	}
	r := geom.Rect{Max: box.Point()}
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-40)), Opacity: k})()
	defer p.Push(paint.Translate(geom.Pt((1-k)*24, 0)))()
	paintGlass(p, r, 11)
	widget.PaintIcon(p, f.Theme, icon.CircleAlert, geom.Rc(12, 12, 16, 16), errorInk)
	for k := range kids.All {
		k.Paint(p)
	}
}
