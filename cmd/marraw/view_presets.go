package main

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// PresetCard is a preset as the panel lists it: its name, a mark of what
// kind it is, and its place in its list, user or creative auto. Key names
// it, for its small picture.
type PresetCard struct {
	Key   string
	Name  string
	Badge string
	Auto  bool
	Index int
}

// PresetThumb is a preset's small picture over photo Photo's edit.
type PresetThumb struct {
	Photo int64
	Key   string
	Img   *paint.Image
}

// presetGrid is the panel's presets, as marraw's Presets tab has them:
// the user's own and the creative autos as cards in two columns, each
// with its small picture of the photo as the preset makes it. The pointer
// over a card shows the preset on the photo; a click keeps it.
type presetGrid struct {
	v              *developView
	mine, creative *widget.Label
	none           *widget.Label
	save           *widget.Button
	cards          map[string]*presetCard
	thumbs         map[string]*paint.Image
	photo          int64
	list           []PresetCard
}

func newPresetGrid(v *developView) *presetGrid {
	g := &presetGrid{v: v, mine: newSmallLabel("My presets"), creative: newSmallLabel("Creative"),
		none: newSmallLabel("No presets of your own yet: save an edit as one."), cards: map[string]*presetCard{},
		thumbs: map[string]*paint.Image{}}
	g.mine.Color, g.creative.Color, g.none.Color = headingInk, headingInk, noteInk
	g.none.MaxLines = 2
	g.save = widget.NewButton("Save the edit as a preset…")
	g.save.Ghost, g.save.KeepFocus, g.save.OnClick = true, true, widget.Sends(AskPreset{})
	return g
}

// show takes the presets of s, the thumbnails going with another photo.
func (g *presetGrid) show(s DevelopState, u *gunim.UI) {
	if s.ID != g.photo {
		g.photo = s.ID
		clear(g.thumbs)
		for _, c := range g.cards {
			c.img = nil
		}
	}
	g.list = s.Presets
	u.Invalidate()
}

// thumbIn takes a preset's small picture.
func (g *presetGrid) thumbIn(t PresetThumb, u *gunim.UI) {
	if t.Photo != g.photo {
		return
	}
	g.thumbs[t.Key] = t.Img
	if c := g.cards[t.Key]; c != nil {
		c.setImg(t.Img, u)
	}
	u.Invalidate()
}

// Children implements [gunim.Composite]: the cards are built as they come.
func (g *presetGrid) Children() []gunim.Node {
	return []gunim.Node{g.mine, g.none, g.save, g.creative}
}

// presetGap is the room between cards.
const presetGap = 8

// Layout implements [gunim.Node].
func (g *presetGrid) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	cw := (w - presetGap) / 2
	ch := cw*2/3 + 22
	// The cards wanted, built where new, and dropped where gone.
	want := map[string]bool{}
	for _, p := range g.list {
		want[p.Key] = true
	}
	children := map[*presetCard]gunim.Child{}
	for kid := range kids.All {
		if pc, ok := kid.Node().(*presetCard); ok {
			children[pc] = kid
		}
	}
	for k, pc := range g.cards {
		if !want[k] {
			kids.Drop(pc)
			delete(g.cards, k)
			delete(children, pc)
		}
	}
	for _, p := range g.list {
		pc, ok := g.cards[p.Key]
		if !ok {
			pc = newPresetCard(p)
			pc.img = g.thumbs[p.Key]
			g.cards[p.Key] = pc
			children[pc] = kids.Build(pc)
		}
		pc.card = p
		pc.name.Text = p.Name
		if p.Badge != "" {
			pc.name.Text += "  " + p.Badge
		}
	}
	y := float32(0)
	place := func(k gunim.Child, h float32) {
		s := k.Layout(gunim.Loose(geom.Sz(w, h)))
		k.Place(geom.Pt(0, y))
		y += s.H + 8
	}
	grid := func(auto bool) {
		col := 0
		for _, p := range g.list {
			if p.Auto != auto {
				continue
			}
			k := children[g.cards[p.Key]]
			k.Layout(gunim.Tight(geom.Sz(cw, ch)))
			k.Place(geom.Pt(float32(col)*(cw+presetGap), y))
			if col++; col == 2 {
				col, y = 0, y+ch+presetGap
			}
		}
		if col > 0 {
			y += ch + presetGap
		}
	}
	mine := 0
	for _, p := range g.list {
		if !p.Auto {
			mine++
		}
	}
	place(kids.At(0), 30)
	if mine == 0 {
		place(kids.At(1), 60)
	} else {
		kids.At(1).Layout(gunim.Tight(geom.Size{}))
		kids.At(1).Place(geom.Pt(-10000, 0))
	}
	grid(false)
	place(kids.At(2), 40)
	place(kids.At(3), 30)
	grid(true)
	return geom.Sz(w, y)
}

// Paint implements [gunim.Node].
func (g *presetGrid) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// presetCard is a preset's card: its small picture, which lifts and rings
// under the pointer, and its name. A user preset's card shows a cross to
// delete it under the pointer.
type presetCard struct {
	anim.Group
	card     PresetCard
	name     *widget.Label
	img, old *paint.Image
	hot, in  *anim.Float
	box      geom.Size
	overDel  bool
}

func newPresetCard(p PresetCard) *presetCard {
	c := &presetCard{card: p, name: newSmallLabel(p.Name), hot: anim.NewFloat(0), in: anim.NewFloat(1)}
	c.name.MaxLines = 1
	c.Add(c.hot, c.in)
	return c
}

// setImg shows img, fading in over what showed.
func (c *presetCard) setImg(img *paint.Image, u *gunim.UI) {
	c.old, c.img = c.img, img
	c.in.Jump(0)
	c.in.Animate(1, widget.Settle.Get(u.Theme()))
}

// Children implements [gunim.Composite].
func (c *presetCard) Children() []gunim.Node { return []gunim.Node{c.name} }

// picRect is the card's picture.
func (c *presetCard) picRect() geom.Rect { return geom.Rc(0, 0, c.box.W, c.box.W*2/3) }

// delRect is the cross that deletes a user preset.
func (c *presetCard) delRect() geom.Rect { return geom.Rc(c.box.W-22, 4, 18, 18) }

// Layout implements [gunim.Node].
func (c *presetCard) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	c.box = cs.Max
	k := kids.At(0)
	k.Layout(gunim.Loose(geom.Sz(c.box.W, 20)))
	k.Place(geom.Pt(2, c.box.W*2/3+4))
	return c.box
}

// Handle implements [gunim.Handler].
func (c *presetCard) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerEnter:
		c.hot.Animate(1, widget.Quick.Get(th))
		u.Send(c, PresetHover{Auto: c.card.Auto, Index: c.card.Index})
	case input.PointerMove:
		c.overDel = !c.card.Auto && c.delRect().Contains(e.Pos)
	case input.PointerLeave:
		c.hot.Animate(0, widget.Quick.Get(th))
		c.overDel = false
		u.Send(c, PresetHover{Index: -1})
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if !c.card.Auto && c.delRect().Contains(e.Pos) {
			u.Send(c, PresetHover{Index: -1})
			u.Send(c, PresetDelete{Index: c.card.Index})
			return true
		}
		u.Send(c, PresetApply{Auto: c.card.Auto, Index: c.card.Index})
		return true
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Cursor implements [gunim.CursorShaper].
func (c *presetCard) Cursor(geom.Point) input.Cursor { return input.CursorHand }

// Paint implements [gunim.Node].
func (c *presetCard) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	hot := c.hot.Value()
	r := c.picRect()
	func() {
		defer p.Push(paint.Scale(1+0.03*hot, r.Center()))()
		p.RRect(r, 6, paint.Solid(frameInk))
		k := c.in.Value()
		if c.old != nil && k < 1 {
			p.Image(c.old, r, paint.ImageOpts{Src: coverSrc(c.old, r), Radius: 6, Opacity: 1})
		}
		if c.img != nil {
			p.Image(c.img, r, paint.ImageOpts{Src: coverSrc(c.img, r), Radius: 6, Opacity: k})
		}
		if hot > 0.01 {
			ring := widget.Accent.Get(f.Theme)
			p.RRectStroke(r, 6, paint.Fill{}, paint.Stroke{Width: 2, Color: withAlpha(ring, hot)})
		}
		if !c.card.Auto && hot > 0.01 {
			d := c.delRect()
			bg := color.NRGBA{A: 0xa0}
			if c.overDel {
				bg = rejectInk
			}
			p.RRect(d, 9, paint.Solid(withAlpha(bg, hot)))
			p.Mask(icon.Stroke{Icon: icon.X, Width: 2.5, Progress: 1}, d.Inset(geom.Uniform(4)), withAlpha(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, hot))
		}
	}()
	kids.At(0).Paint(p)
}

// coverSrc is the part of img that fills r, its middle, cut to r's shape.
func coverSrc(img *paint.Image, r geom.Rect) geom.Rect {
	w, h := img.Size()
	if w <= 0 || h <= 0 || r.Size().H <= 0 {
		return geom.Rect{}
	}
	want := r.Size().W / r.Size().H
	fw, fh := float32(w), float32(h)
	if fw/fh > want {
		cw := fh * want
		return geom.Rc((fw-cw)/2, 0, cw, fh)
	}
	chh := fw / want
	return geom.Rc(0, (fh-chh)/2, fw, chh)
}
