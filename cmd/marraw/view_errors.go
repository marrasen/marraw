package main

import (
	"fmt"
	"image/color"
	"math"
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
const errCardW = 360

// errorTray is the errors not cleared yet, as cards stacked up from a
// corner, newest at the bottom, as marraw's toasts are; but they stay
// until the user clears them. It covers its parent and takes the
// pointer only on its cards.
type errorTray struct {
	anim.Group
	list  []ErrorNote
	cards map[int]*errCard
	// tasks are the background tasks under way, as chips above the
	// errors, each with its progress and a cross to cancel it.
	tasks []TaskNote
	chips map[string]*taskCard
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
	t := &errorTray{cards: map[int]*errCard{}, chips: map[string]*taskCard{}, clearAll: widget.NewButton("Clear all"), clearIn: anim.NewFloat(0)}
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

// setTasks shows the tasks in list, chips coming and going.
func (t *errorTray) setTasks(list []TaskNote, u *gunim.UI) {
	t.tasks = list
	// The chips showing glide to how far their tasks have got.
	for _, n := range list {
		if chip, ok := t.chips[n.ID]; ok {
			chip.show(n, u)
		}
	}
	u.Invalidate()
}

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
	if t.clearIn.Target() > 0.5 {
		y -= bs.H + 8
	}
	// The tasks, above the errors, the newest nearest them.
	wantTask := map[string]bool{}
	for _, n := range t.tasks {
		wantTask[n.ID] = true
	}
	for id, chip := range t.chips {
		if !wantTask[id] {
			kids.Drop(chip)
			delete(t.chips, id)
		}
	}
	for i := len(t.tasks) - 1; i >= 0; i-- {
		n := t.tasks[i]
		chip, ok := t.chips[n.ID]
		var k gunim.Child
		if !ok {
			chip = newTaskCard(n)
			t.chips[n.ID] = chip
			k = kids.Build(chip)
		} else {
			k = byNode[chip]
		}
		s := k.Layout(gunim.Tight(geom.Sz(errCardW, taskCardH)))
		y -= s.H
		at := geom.Pt(t.corner.X-errCardW, y)
		if !chip.placed {
			chip.y.Jump(at.Y)
			chip.placed = true
		} else if chip.y.Target() != at.Y {
			chip.y.Animate(at.Y, errCardIn)
		}
		k.Place(geom.Pt(at.X, chip.y.Value()))
		t.rects = append(t.rects, geom.Rc(at.X, chip.y.Value(), s.W, s.H))
		y -= 8
	}
	for k := range kids.All {
		if chip, ok := k.Node().(*taskCard); ok && !wantTask[chip.n.ID] {
			k.Layout(gunim.Tight(geom.Sz(errCardW, taskCardH)))
			k.Place(geom.Pt(t.corner.X-errCardW, chip.y.Value()))
		}
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

// taskCardH is a task chip's height.
const taskCardH = 46

// The task chips' inks, as marraw's.
var (
	spinnerInk = color.NRGBA{R: 0xaa, G: 0xb0, B: 0xff, A: 0xff}
	doneInk    = color.NRGBA{R: 0x34, G: 0xd3, B: 0x99, A: 0xff}
	chipText   = theme.Length("marraw.task.text", 11)
)

// taskCard is a background task as a chip: a spinner, its name and how
// far it has got, a bar, and a cross to cancel it. Done, it ticks.
type taskCard struct {
	anim.Group
	n            TaskNote
	title, count *widget.Label
	bar          *widget.ProgressBar
	cancel       *widget.IconButton
	in, y        *anim.Float
	placed       bool
	// turn is the spinner's turn, from 0 to 1, as the clock goes.
	turn    float32
	painted bool
	kids    []gunim.Node
}

func newTaskCard(n TaskNote) *taskCard {
	c := &taskCard{n: n, title: widget.NewLabel(n.Title), count: widget.NewLabel(""), bar: widget.NewProgressBar(),
		cancel: widget.NewIconButton(icon.X, "Cancel"), in: anim.NewFloat(0), y: anim.NewFloat(0)}
	c.title.Face, c.title.Size, c.title.MaxLines = widget.MonoFont, chipText, 1
	c.count.Face, c.count.Size, c.count.Color = widget.MonoFont, chipText, mutedInkTok
	c.cancel.KeepFocus = true
	id := n.ID
	c.cancel.OnClick = func(*gunim.UI) gunim.Intent { return TaskCancel{ID: id} }
	bar := marrawTheme().With(theme.Set(widget.ProgressHeight, 4), theme.Set(widget.Accent, primaryInk),
		theme.Set(widget.ProgressTrack, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x1f}))
	small := marrawTheme().With(theme.Set(widget.ButtonHeight, 22), theme.Set(widget.IconSize, 12))
	c.kids = []gunim.Node{c.title, c.count, widget.NewThemed(c.bar, bar), widget.NewThemed(c.cancel, small)}
	c.Add(c.in, c.y)
	c.show(n, nil)
	return c
}

// show takes n as the task moves on: the bar glides to it, and the count
// with the bar. With a nil u, as a chip is made, the bar starts there.
func (c *taskCard) show(n TaskNote, u *gunim.UI) {
	if n.Total != c.n.Total || n.Title != c.n.Title {
		// Another count, as a scan's photos after its download: the bar
		// starts it afresh.
		u = nil
	}
	c.n = n
	c.title.Text = n.Title
	if n.Done {
		c.title.Text = n.Title + " · Done"
	}
	c.bar.Indeterminate = n.Total <= 0 && !n.Done
	switch {
	case n.Done:
		c.bar.SetValue(1, u)
	case n.Total > 0:
		c.bar.SetValue(float32(n.Current)/float32(n.Total), u)
	default:
		c.bar.SetValue(0, nil)
	}
}

// countText is how far the task has got, counting up with the bar as it
// glides.
func (c *taskCard) countText() string {
	if c.n.Total <= 0 || c.n.Done {
		return ""
	}
	s := fmt.Sprintf("%d/%d", int(math.Round(float64(c.bar.Shown())*float64(c.n.Total))), c.n.Total)
	if c.n.Unit != "" {
		s += " " + c.n.Unit
	}
	return s
}

// Children implements [gunim.Composite].
func (c *taskCard) Children() []gunim.Node { return c.kids }

// Step implements [gunim.Animator]: the spinner turns while it shows.
func (c *taskCard) Step(dt time.Duration) bool {
	moving := c.painted && !c.n.Done
	if dt > 0 {
		c.painted = false
		c.turn = float32(math.Mod(float64(c.turn)+dt.Seconds()/0.9, 1))
	}
	return c.Group.Step(dt) || moving
}

// Transition implements [gunim.Transitioner], as an error card's.
func (c *taskCard) Transition(p gunim.Presence, _ gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		c.in.Animate(1, errCardIn)
	case gunim.Exiting:
		c.in.Animate(0, errCardOut)
	}
	return !c.in.Active()
}

// Layout implements [gunim.Node].
func (c *taskCard) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const padL, padR, spin = 14, 10, 15
	w, h := cs.Max.W, cs.Max.H
	x := float32(padL + spin + 11)
	bs := kids.At(3).Layout(gunim.Loose(geom.Sz(30, 30)))
	if c.n.Done {
		kids.At(3).Place(geom.Pt(-10000, 0))
		bs.W = 0
	} else {
		kids.At(3).Place(geom.Pt(w-padR-bs.W, (h-bs.H)/2))
	}
	right := w - padR - bs.W - 10
	c.count.Text = c.countText()
	ns := kids.At(1).Layout(gunim.Loose(geom.Sz(120, 20)))
	kids.At(1).Place(geom.Pt(right-ns.W, 11))
	kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, right-ns.W-12-x), 20)))
	kids.At(0).Place(geom.Pt(x, 11))
	kids.At(2).Layout(gunim.Tight(geom.Sz(max(0, right-x), 4)))
	kids.At(2).Place(geom.Pt(x, 29))
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (c *taskCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	k := min(max(c.in.Value(), 0), 1)
	if k < 0.01 {
		return
	}
	c.painted = true
	r := geom.Rect{Max: box.Point()}
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-40)), Opacity: k})()
	defer p.Push(paint.Translate(geom.Pt((1-k)*24, 0)))()
	paintGlass(p, r, 11)
	at := geom.Rc(14, (box.H-15)/2, 15, 15)
	if c.n.Done {
		widget.PaintIcon(p, f.Theme, icon.Check, at, doneInk)
	} else {
		// A faint ring, and a quarter of it turning.
		p.RRectStroke(at.Inset(geom.Uniform(1.5)), 6, paint.Fill{}, paint.Stroke{Width: 1.5, Color: withAlpha(spinnerInk, 0.3)})
		func() {
			defer p.Push(paint.Rotate(c.turn*2*math.Pi, at.Center()))()
			p.Mask(icon.Stroke{Icon: spinnerArc, Width: 1.5, Progress: 1}, at, spinnerInk)
		}()
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// spinnerArc is a quarter of a circle, the turning part of a spinner.
var spinnerArc = &icon.Icon{Name: "spinner-arc", Path: "M12 3a9 9 0 0 1 9 9"}
