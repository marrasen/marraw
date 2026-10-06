package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// gridView is the library: the folder's photos as tiles, with how many are
// picked, rejected and rated above them. The tiles grow in one after
// another as the folder opens, and spring to new places as the window or
// Ctrl and the wheel change their size. Each tile's picture fades in as it
// arrives, its stars sweep to a new rating, its flag pops, and a rejected
// photo dims. Enter or a double click opens the cull view, the picture
// growing out of its tile.
type gridView struct {
	st     GridState
	grid   *widget.TileGrid
	head   *gridHead
	thumbs map[int]*paint.Image
	tiles  map[int]*photoTile
	box    geom.Size
	shown  bool
}

const (
	// gridHeadHeight is the band above the tiles.
	gridHeadHeight = 52
	// The tiles' widths, from Ctrl and the wheel.
	tileMin, tileMax = 110, 440
	// captionHeight is the band under a tile's picture, for its stars
	// and flag.
	captionHeight = 20
)

// cellSize is a tile's size for its width.
func cellSize(w float32) geom.Size { return geom.Sz(w, w*0.78+captionHeight) }

func newGridView(GridState) *gridView {
	v := &gridView{thumbs: map[int]*paint.Image{}, tiles: map[int]*photoTile{}, head: newGridHead()}
	v.grid = widget.NewTileGrid(cellSize(200))
	v.grid.Tile = v.newTile
	v.grid.OnView = func(first, count int) gunim.Intent { return NeedThumbs{First: first, Count: count} }
	v.grid.OnSelect = func(sel [][2]int, cursor int) gunim.Intent {
		n := 0
		for _, r := range sel {
			n += r[1] - r[0]
		}
		v.head.selected(n)
		return Selected{Runs: sel, Cursor: cursor}
	}
	v.grid.OnActivate = func(i int) gunim.Intent { return OpenCull{Index: i} }
	v.grid.OnZoom = func(notches float32, u *gunim.UI) {
		w := v.grid.Size.W * float32(math.Pow(1.15, float64(notches)))
		v.grid.Size = cellSize(max(tileMin, min(w, tileMax)))
		u.Invalidate()
	}
	return v
}

func (v *gridView) show(s GridState, u *gunim.UI) {
	v.st = s
	v.grid.SetLen(len(s.Photos), u)
	for i, t := range v.tiles {
		if i < len(s.Photos) {
			t.marks.set(s.Photos[i].Rating, s.Photos[i].Flag, u.Theme())
		}
	}
	v.head.set(s, v.shown, u.Theme())
	if !v.shown {
		// The folder opens: its tiles grow in, one after another.
		v.shown = true
		v.grid.Arrive(func(int) (geom.Rect, bool) { return geom.Rect{}, false }, u)
	}
}

// thumbIn takes a tile's small picture, or, nil, lets it go.
func (v *gridView) thumbIn(t ThumbIn, u *gunim.UI) {
	if t.Img == nil {
		// A tile built keeps what it shows; one built later asks again.
		delete(v.thumbs, t.Index)
		return
	}
	v.thumbs[t.Index] = t.Img
	if tl := v.tiles[t.Index]; tl != nil {
		tl.pic.set(t.Img)
	}
	u.Invalidate()
}

// photoMarked shows a photo's new rating and flag.
func (v *gridView) photoMarked(m PhotoMarked, u *gunim.UI) {
	if m.Index < 0 || m.Index >= len(v.st.Photos) {
		return
	}
	p := &v.st.Photos[m.Index]
	p.Rating, p.Flag = m.Rating, m.Flag
	if t := v.tiles[m.Index]; t != nil {
		t.marks.set(m.Rating, m.Flag, u.Theme())
	}
	v.head.set(v.st, true, u.Theme())
	u.Invalidate()
}

// gridAt puts the keyboard on photo i and, unseen under the cull view,
// its tile in the middle of the view, for the photo to fly back to.
func (v *gridView) gridAt(a GridAt, u *gunim.UI) {
	if a.Index < 0 || a.Index >= len(v.st.Photos) {
		return
	}
	v.grid.SetSelected([][2]int{{a.Index, a.Index + 1}}, a.Index, u)
	v.head.selected(1)
	if v.grid.Columns() > 0 {
		r := v.grid.TileRect(a.Index)
		room := v.box.H - gridHeadHeight
		if r.Min.Y < 0 || r.Max.Y > room {
			v.grid.JumpTo(r.Min.Y + v.grid.Offset() - (room-r.Size().H)/2)
		}
	}
	u.Invalidate()
}

func (v *gridView) newTile(i int) gunim.Node {
	p := v.st.Photos[i]
	t := &photoTile{aspect: p.Aspect, pic: newThumbPic(v.thumbs[i])}
	t.hero = widget.NewHero(heroTag(p.ID), t.pic)
	// The tile is where the cull view's picture flies from and back to.
	t.hero.Anchor = true
	t.marks = newMarks(&t.Group, p.Rating, p.Flag)
	v.tiles[i] = t
	return t
}

// Children implements [gunim.Composite].
func (v *gridView) Children() []gunim.Node { return []gunim.Node{v.head, v.grid} }

// Handle implements [gunim.Handler]: the keyboard goes on to the tiles,
// and the keys they leave rate and flag the photos selected.
func (v *gridView) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.FocusGained:
		u.After(0, func(u *gunim.UI) { u.Focus(v.grid) })
		return true
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) {
			return false
		}
		if in, ok := markKey(e.Key); ok {
			u.Send(v, in)
			return true
		}
	}
	return false
}

// Layout implements [gunim.Node].
func (v *gridView) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	v.box = box
	head, grid := kids.At(0), kids.At(1)
	head.Layout(gunim.Tight(geom.Sz(box.W, gridHeadHeight)))
	head.Place(geom.Point{})
	grid.Layout(gunim.Tight(geom.Sz(box.W, max(0, box.H-gridHeadHeight))))
	grid.Place(geom.Pt(0, gridHeadHeight))
	return box
}

// Paint implements [gunim.Node].
func (v *gridView) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(1).Paint(p)
	kids.At(0).Paint(p)
	p.RRect(geom.Rc(0, gridHeadHeight-1, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}))
}

// photoTile is one photo in the grid: its picture, fitted, as a hero, and
// its stars and flag beneath.
type photoTile struct {
	anim.Group
	aspect float32
	pic    *thumbPic
	hero   *widget.Hero
	marks  *marks
}

// Children implements [gunim.Composite].
func (t *photoTile) Children() []gunim.Node { return []gunim.Node{t.hero} }

// picRoom is where a tile's picture fits, in a tile of size box.
func picRoom(box geom.Size) geom.Rect {
	return geom.Rc(7, 7, max(0, box.W-14), max(0, box.H-14-captionHeight))
}

// Layout implements [gunim.Node].
func (t *photoTile) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	r := fitIn(picRoom(box), t.aspect)
	k := kids.At(0)
	k.Layout(gunim.Tight(r.Size()))
	k.Place(r.Min)
	return box
}

// Paint implements [gunim.Node].
func (t *photoTile) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	t.pic.dim = t.marks.dim.Value()
	kids.At(0).Paint(p)
	y := box.H - captionHeight/2 - 5
	t.marks.paintStars(p, geom.Pt(10, y), 6, 4, 0x48)
	t.marks.paintFlag(p, geom.Pt(box.W-14, y), 9)
}

// thumbPic is a tile's picture: its frame until the small picture comes,
// which fades and settles in.
type thumbPic struct {
	anim.Group
	img, old *paint.Image
	in       *anim.Float
	// dim is how far the picture is dimmed, for a rejected photo.
	dim float32
}

func newThumbPic(img *paint.Image) *thumbPic {
	q := &thumbPic{img: img, in: anim.NewFloat(1)}
	q.Add(q.in)
	return q
}

// set shows img, fading in over what showed.
func (q *thumbPic) set(img *paint.Image) {
	if img == q.img {
		return
	}
	q.old, q.img = q.img, img
	q.in.Jump(0)
	q.in.Animate(1, anim.Tween{Duration: 200 * time.Millisecond})
}

// Layout implements [gunim.Node].
func (q *thumbPic) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node].
func (q *thumbPic) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	op := 1 - 0.6*q.dim
	k := q.in.Value()
	if q.old != nil && k < 1 {
		p.Image(q.old, r, paint.ImageOpts{Opacity: op, Radius: 3})
	} else if q.img == nil || k < 1 {
		p.RRect(r, 3, paint.Solid(frameInk))
	}
	if q.img != nil {
		// It settles from a touch larger as it fades in.
		p.Image(q.img, scaleAbout(r, 1+0.04*(1-k)), paint.ImageOpts{Opacity: op * k, Radius: 3})
	}
}

// marks are a photo's stars and flag as they show, animated: the stars
// sweep from the old rating to the new, each swelling as the sweep
// passes, the flag pops in with a ring going out from it, and a rejected
// photo dims.
type marks struct {
	fill, badge, ring, dim *anim.Float
	ink                    *anim.Color
	rating                 int
	flag                   string
}

// sweep carries the stars to a new rating, straight on, so no star past it
// lights for a moment.
var sweep = anim.Spring{Response: 0.32, Damping: 1}

func flagged(f string) bool { return f == "pick" || f == "exclude" }

func flagInk(f string) color.NRGBA {
	if f == "exclude" {
		return rejectInk
	}
	return pickInk
}

func newMarks(g *anim.Group, rating int, flag string) *marks {
	m := &marks{fill: anim.NewFloat(0), badge: anim.NewFloat(0), ring: anim.NewFloat(1), dim: anim.NewFloat(0),
		ink: anim.NewColor(pickInk)}
	m.jump(rating, flag)
	g.Add(m.fill, m.badge, m.ring, m.dim, m.ink)
	return m
}

// jump shows rating and flag at once, for another photo.
func (m *marks) jump(rating int, flag string) {
	m.rating, m.flag = rating, flag
	m.fill.Jump(float32(rating))
	m.ink.Jump(flagInk(flag))
	m.badge.Jump(map[bool]float32{false: 0, true: 1}[flagged(flag)])
	m.dim.Jump(map[bool]float32{false: 0, true: 1}[flag == "exclude"])
	m.ring.Jump(1)
}

// set animates to rating and flag.
func (m *marks) set(rating int, flag string, th *theme.Live) {
	if rating != m.rating {
		m.rating = rating
		m.fill.Animate(float32(rating), sweep)
	}
	if flag == m.flag {
		return
	}
	was := m.flag
	m.flag = flag
	if flagged(flag) {
		if flagged(was) {
			m.ink.Animate(flagInk(flag), widget.Quick.Get(th))
			m.badge.Jump(0.6)
		} else {
			m.ink.Jump(flagInk(flag))
			m.badge.Jump(0)
		}
		m.badge.Animate(1, widget.Bounce.Get(th))
		m.ring.Jump(0)
		m.ring.Animate(1, anim.Tween{Duration: 450 * time.Millisecond})
	} else {
		m.badge.Animate(0, widget.Quick.Get(th))
	}
	m.dim.Animate(map[bool]float32{false: 0, true: 1}[flag == "exclude"], widget.Settle.Get(th))
}

// paintStars draws the five stars as dots of size d, gap apart, from the
// left middle at; offAlpha is how strongly the unlit ones show.
func (m *marks) paintStars(p *paint.Painter, at geom.Point, d, gap float32, offAlpha uint8) {
	fill := m.fill.Value()
	off := starOff
	off.A = offAlpha
	for k := range 5 {
		t := min(max(fill-float32(k), 0), 1)
		swell := 1 + 0.5*float32(math.Sin(math.Pi*float64(t)))
		c := anim.Mix(anim.ColorCodec, off, starInk, t)
		s := d * swell
		cx := at.X + float32(k)*(d+gap) + d/2
		p.RRect(geom.Rc(cx-s/2, at.Y-s/2, s, s), s/2, paint.Solid(c))
	}
}

// paintFlag draws the flag as a dot of size d about at, and the ring
// going out from it as it is set.
func (m *marks) paintFlag(p *paint.Painter, at geom.Point, d float32) {
	ink := m.ink.Value()
	if r := m.ring.Value(); r < 0.999 {
		s := d * (1 + 1.6*r)
		c := ink
		c.A = uint8(float32(c.A) * (1 - r) * 0.8)
		p.RRectStroke(geom.Rc(at.X-s/2, at.Y-s/2, s, s), s/2, paint.Fill{}, paint.Stroke{Width: 1.5, Color: c})
	}
	if b := m.badge.Value(); b > 0.01 {
		s := d * b
		p.RRect(geom.Rc(at.X-s/2, at.Y-s/2, s, s), s/2, paint.Solid(ink))
	}
}

// gridHead is the band above the tiles: the folder's name, how many
// photos it holds or are selected, and the counts of picks, rejects and
// rated photos, each popping as it changes.
type gridHead struct {
	title, sub *widget.Label
	pills      [3]*countPill
	total, sel int
}

var (
	headTitleSize = theme.Length("marraw.head.size", 17)
	ratedInk      = starInk
)

func newGridHead() *gridHead {
	h := &gridHead{title: widget.NewLabel(""), sub: widget.NewLabel("")}
	h.title.Size = headTitleSize
	h.sub.Color, h.sub.Size = noteInk, noteSize
	h.pills = [3]*countPill{newCountPill(pickInk, "picked"), newCountPill(rejectInk, "rejected"), newCountPill(ratedInk, "rated")}
	return h
}

// set shows s's counts, popping those that changed when animate is set.
func (h *gridHead) set(s GridState, animate bool, th *theme.Live) {
	h.title.SetText(s.Folder)
	h.total = len(s.Photos)
	h.subText()
	var picks, rejects, rated int
	for _, p := range s.Photos {
		switch p.Flag {
		case "pick":
			picks++
		case "exclude":
			rejects++
		}
		if p.Rating > 0 {
			rated++
		}
	}
	for i, n := range []int{picks, rejects, rated} {
		h.pills[i].set(n, animate, th)
	}
}

// selected shows how many photos are selected.
func (h *gridHead) selected(n int) {
	h.sel = n
	h.subText()
}

func (h *gridHead) subText() {
	s := fmt.Sprintf("%d photos", h.total)
	if h.sel > 1 {
		s += fmt.Sprintf(" · %d selected", h.sel)
	}
	h.sub.SetText(s)
}

// Children implements [gunim.Composite].
func (h *gridHead) Children() []gunim.Node {
	return []gunim.Node{h.title, h.sub, h.pills[0], h.pills[1], h.pills[2]}
}

// Layout implements [gunim.Node]: the name and the count on the left, the
// pills on the right, all on the band's middle line.
func (h *gridHead) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	mid := box.H / 2
	x := float32(20)
	for i := range 2 {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(box.W/3, box.H)))
		k.Place(geom.Pt(x, mid-s.H/2))
		x += s.W + 14
	}
	right := box.W - 16
	for i := 4; i >= 2; i-- {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(box.W/4, box.H)))
		right -= s.W
		k.Place(geom.Pt(right, mid-s.H/2))
		right -= 8
	}
	return box
}

// Paint implements [gunim.Node].
func (h *gridHead) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// countPill is a count with a coloured dot, such as the picks: it pops as
// the count changes, and fades back while it is nought.
type countPill struct {
	anim.Group
	label   *widget.Label
	what    string
	n       int
	pop, on *anim.Float
	ink     color.NRGBA
}

func newCountPill(ink color.NRGBA, what string) *countPill {
	c := &countPill{label: widget.NewLabel("0 " + what), what: what, ink: ink, pop: anim.NewFloat(1), on: anim.NewFloat(0.4)}
	c.label.Size = noteSize
	c.Add(c.pop, c.on)
	return c
}

func (c *countPill) set(n int, animate bool, th *theme.Live) {
	on := map[bool]float32{false: 0.4, true: 1}[n > 0]
	if !animate {
		c.n = n
		c.label.SetText(fmt.Sprintf("%d %s", n, c.what))
		c.on.Jump(on)
		return
	}
	if n == c.n {
		return
	}
	c.n = n
	c.label.SetText(fmt.Sprintf("%d %s", n, c.what))
	c.pop.Jump(1.18)
	c.pop.Animate(1, widget.Bounce.Get(th))
	c.on.Animate(on, widget.Quick.Get(th))
}

// Children implements [gunim.Composite].
func (c *countPill) Children() []gunim.Node { return []gunim.Node{c.label} }

// Layout implements [gunim.Node].
func (c *countPill) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Loose(cs.Max))
	h := max(s.H+10, 26)
	k.Place(geom.Pt(26, (h-s.H)/2))
	return geom.Sz(26+s.W+12, h)
}

// Paint implements [gunim.Node].
func (c *countPill) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	defer p.Push(paint.Scale(c.pop.Value(), r.Center()))()
	if on := c.on.Value(); on < 0.999 {
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-4)), Opacity: on})()
	}
	p.RRect(r, box.H/2, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
	p.RRect(geom.Rc(11, box.H/2-4, 8, 8), 4, paint.Solid(c.ink))
	kids.At(0).Paint(p)
}
