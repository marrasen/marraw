package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// cullView shows one photo, fitted to the window or zoomed in, over the
// grid, with where it is in the folder and which rendition shows. It opens
// with the picture growing out of its tile as the backdrop fades in and the
// filmstrip slides up, and closes the other way. A step slides the next
// photo in over the last; sharper pixels fade in over blurrier ones; the
// filmstrip glides to the photo showing; the stars and the flag animate as
// in the grid.
//
// The keys step through the folder. The wheel zooms about the pointer, a
// drag pans, and Z or a double click goes between fit and one image pixel a
// screen pixel; the zoom and the pan stay as the photo changes, to compare
// a burst. Past what the 2048 shows sharp, it asks for the full-resolution
// tiles in view.
type cullView struct {
	anim.Group
	st   Cull
	name *widget.Label
	note *widget.Label
	hud  gunim.Node
	pic  *cullPic
	hero *widget.Hero

	// in is how far the view has come in, from 0 to 1, and leaving says it
	// is on its way out.
	in      *anim.Float
	leaving bool
	// z is the zoom, 1 at fit, and c the image point at the middle of the
	// view, from 0 to 1 across the image.
	z *anim.Float
	c *anim.Point
	// box is the view's size and scale its device pixels a logical one,
	// from the last layout.
	box   geom.Size
	scale float32
	// dragging pans from last.
	dragging bool
	last     geom.Point
	// asked is the last ask for tiles, not to repeat it.
	asked WantTiles
	// strip is the filmstrip's place, in photos, gliding after the one
	// showing, and ring the width of the ring around it.
	strip, ring *anim.Float
	marks       *marks
}

// The cull view's motions: in, out, a step's slide and a sharper
// rendition's fade.
var (
	cullIn  = anim.Spring{Response: 0.32, Damping: 1}
	cullOut = anim.Spring{Response: 0.22, Damping: 1}
	stepIn  = anim.Tween{Duration: 140 * time.Millisecond}
	sharpen = anim.Tween{Duration: 180 * time.Millisecond}
)

func newCullView(s Cull) *cullView {
	v := &cullView{name: widget.NewLabel(""), note: widget.NewLabel(""), z: anim.NewFloat(1), c: anim.NewPoint(geom.Pt(0.5, 0.5)),
		scale: 1, in: anim.NewFloat(0), strip: anim.NewFloat(float32(s.Index)), ring: anim.NewFloat(0)}
	v.Add(v.z, v.c, v.in, v.strip, v.ring)
	v.note.Color = noteInk
	v.note.Size = noteSize
	v.hud = widget.NewPad(widget.Column(v.name, v.note))
	v.pic = newCullPic(v, s)
	v.hero = widget.NewHero(heroTag(s.ID), v.pic)
	// It flies in under the readout and the marks, which this view draws
	// over it, not above the whole window.
	v.hero.InPlace = true
	v.marks = newMarks(&v.Group, s.Rating, s.Flag)
	v.st = s
	return v
}

func (v *cullView) show(s Cull, u *gunim.UI) {
	prev := v.st
	v.st = s
	th := u.Theme()
	v.hero.Tag = heroTag(s.ID)
	switch {
	case s.ID != prev.ID:
		// A step: the next photo slides in, at fit, or fades in, zoomed,
		// so a burst compares in place.
		dir := float32(1)
		if s.Index < prev.Index {
			dir = -1
		}
		slide := float32(0)
		if v.z.Target() < 1.01 {
			slide = 28 * dir
		}
		v.pic.step(s, prev, slide)
		v.strip.Animate(float32(s.Index), widget.Quick.Get(th))
		v.marks.jump(s.Rating, s.Flag)
		v.askTiles(u)
	default:
		v.pic.sharpen(s)
		v.marks.set(s.Rating, s.Flag, th)
	}
	v.readout(v.z.Target())
	note := s.Note
	if s.TileNote != "" {
		note += "\n" + s.TileNote + fmt.Sprintf(" (%d in memory)", len(s.Tiles))
	}
	v.note.SetText(note)
	u.Invalidate()
}

func (v *cullView) readout(z float32) {
	v.name.SetText(fmt.Sprintf("%s   %d / %d   %.0f%%", v.st.Name, v.st.Index+1, v.st.Total, v.percent(z)))
}

// Transition implements [gunim.Transitioner]: the backdrop fades and the
// filmstrip slides, in and out, while the picture flies.
func (v *cullView) Transition(p gunim.Presence, _ gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		// Mounted again mid-exit, the view is brought back, not built anew.
		v.leaving = false
		v.in.Animate(1, cullIn)
	case gunim.Exiting:
		v.leaving = true
		v.in.Animate(0, cullOut)
	case gunim.Present:
	}
	return !v.in.Active()
}

// full is the photo's full resolution, or a guess from its frame.
func (v *cullView) full() geom.Size {
	if v.st.Full.X > 0 && v.st.Full.Y > 0 {
		return geom.Sz(float32(v.st.Full.X), float32(v.st.Full.Y))
	}
	return geom.Sz(3000*v.st.Aspect, 3000)
}

// stripHeight is the filmstrip's band along the bottom, and thumbHeight its
// pictures'.
const (
	stripHeight = 92
	thumbHeight = 64
	thumbGap    = 6
)

// room is where the photo goes at fit: above the filmstrip.
func (v *cullView) room() geom.Rect {
	return geom.Rc(0, 0, v.box.W, max(0, v.box.H-stripHeight)).Inset(geom.Uniform(16))
}

// fitRect is where the photo shows at fit.
func (v *cullView) fitRect() geom.Rect {
	f, s := v.full(), v.fit()
	o := v.origin(1, geom.Pt(0.5, 0.5))
	return geom.Rc(o.X, o.Y, f.W*s, f.H*s)
}

// stripThumb is one filmstrip picture's place.
type stripThumb struct {
	index int
	r     geom.Rect
	th    Thumb
}

func thumbWidth(t Thumb) float32 { return thumbHeight * max(0.5, min(t.Aspect, 2)) }

// stripRects lays the filmstrip out: the photo showing in the middle, its
// neighbours either side, each as wide as its shape asks, the whole
// shifted by as far as the strip has still to glide.
func (v *cullView) stripRects() []stripThumb {
	if len(v.st.Strip) == 0 {
		return nil
	}
	y := v.box.H - stripHeight + (stripHeight-thumbHeight)/2
	var cur int
	for i, t := range v.st.Strip {
		if t.Index == v.st.Index {
			cur = i
		}
	}
	out := make([]stripThumb, len(v.st.Strip))
	shift := (float32(v.st.Index) - v.strip.Value()) * (thumbHeight*1.5 + thumbGap)
	x := v.box.W/2 - thumbWidth(v.st.Strip[cur])/2 + shift
	out[cur] = stripThumb{index: v.st.Strip[cur].Index, r: geom.Rc(x, y, thumbWidth(v.st.Strip[cur]), thumbHeight), th: v.st.Strip[cur]}
	right := x + thumbWidth(v.st.Strip[cur]) + thumbGap
	for i := cur + 1; i < len(v.st.Strip); i++ {
		t := v.st.Strip[i]
		out[i] = stripThumb{index: t.Index, r: geom.Rc(right, y, thumbWidth(t), thumbHeight), th: t}
		right += thumbWidth(t) + thumbGap
	}
	left := x - thumbGap
	for i := cur - 1; i >= 0; i-- {
		t := v.st.Strip[i]
		left -= thumbWidth(t)
		out[i] = stripThumb{index: t.Index, r: geom.Rc(left, y, thumbWidth(t), thumbHeight), th: t}
		left -= thumbGap
	}
	return out
}

// fit is the scale, logical pixels an image pixel, at which the photo fits.
func (v *cullView) fit() float32 {
	rs, f := v.room().Size(), v.full()
	return min(rs.W/f.W, rs.H/f.H)
}

// oneToOne is the zoom at which an image pixel is a screen pixel.
func (v *cullView) oneToOne() float32 { return max(1, 1/v.scale/v.fit()) }

// percent is zoom z as a percentage of one image pixel a screen pixel.
func (v *cullView) percent(z float32) float32 { return 100 * v.fit() * z * v.scale }

// origin is where the image's top left corner is, at zoom z with c in the
// middle.
func (v *cullView) origin(z float32, c geom.Point) geom.Point {
	f, s := v.full(), v.fit()*z
	mid := v.room().Center()
	return geom.Pt(mid.X-c.X*f.W*s, mid.Y-c.Y*f.H*s)
}

// clampCentre keeps the image over the view at zoom z: centred while it is
// smaller, and with no gap at an edge once it is larger.
func (v *cullView) clampCentre(z float32, c geom.Point) geom.Point {
	f, s, rs := v.full(), v.fit()*z, v.room().Size()
	axis := func(c, length, room float32) float32 {
		w := length * s
		if w <= room {
			return 0.5
		}
		half := room / 2 / w
		return max(half, min(c, 1-half))
	}
	return geom.Pt(axis(c.X, f.W, rs.W), axis(c.Y, f.H, rs.H))
}

// zoomTo animates to zoom z, keeping the image point under at where it is.
func (v *cullView) zoomTo(z float32, at geom.Point, u *gunim.UI) {
	z = max(1, min(z, v.oneToOne()*4))
	f, s0 := v.full(), v.fit()*v.z.Value()
	o := v.origin(v.z.Value(), v.c.Value())
	ip := geom.Pt((at.X-o.X)/(f.W*s0), (at.Y-o.Y)/(f.H*s0))
	s1 := v.fit() * z
	mid := v.room().Center()
	c := geom.Pt((mid.X-at.X)/(f.W*s1)+ip.X, (mid.Y-at.Y)/(f.H*s1)+ip.Y)
	v.z.Animate(z, widget.Quick.Get(u.Theme()))
	v.c.Animate(v.clampCentre(z, c), widget.Quick.Get(u.Theme()))
	v.readout(z)
	v.askTiles(u)
}

// toggle goes between fit and one to one, about at.
func (v *cullView) toggle(at geom.Point, u *gunim.UI) {
	if v.z.Target() > 1.01 {
		v.zoomTo(1, v.room().Center(), u)
		return
	}
	v.zoomTo(v.oneToOne(), at, u)
}

// askTiles asks for the tiles the view will show once the zoom and pan
// settle, or for none where the 2048 is sharp enough.
func (v *cullView) askTiles(u *gunim.UI) {
	w := WantTiles{Index: v.st.Index}
	z, c := v.z.Target(), v.c.Target()
	f, s := v.full(), v.fit()*z
	baseDensity := float32(2048) / max(f.W, f.H)
	if s*v.scale > baseDensity*1.05 {
		o := v.origin(z, c)
		r := v.room()
		x0, y0 := (r.Min.X-o.X)/s, (r.Min.Y-o.Y)/s
		x1, y1 := (r.Max.X-o.X)/s, (r.Max.Y-o.Y)/s
		cols, rows := int(math.Ceil(float64(f.W)/tileSize)), int(math.Ceil(float64(f.H)/tileSize))
		w.Range = image.Rect(
			max(0, int(x0/tileSize)), max(0, int(y0/tileSize)),
			min(cols, int(math.Ceil(float64(x1/tileSize)))), min(rows, int(math.Ceil(float64(y1/tileSize)))))
	}
	if w != v.asked {
		v.asked = w
		u.Send(v, w)
	}
}

// Children implements [gunim.Composite].
func (v *cullView) Children() []gunim.Node { return []gunim.Node{v.hero, v.hud} }

// Focusable implements [gunim.Focusable]: the keys step through the folder.
func (v *cullView) Focusable() bool { return true }

// Handle implements [gunim.Handler].
func (v *cullView) Handle(e input.Event, u *gunim.UI) bool {
	if v.leaving {
		return false
	}
	switch e := e.(type) {
	case input.KeyPress:
		if in, ok := markKey(e.Key); ok {
			u.Send(v, in)
			return true
		}
		switch e.Key {
		case input.KeyRight, input.KeyDown:
			u.Send(v, Step{By: 1})
		case input.KeyLeft, input.KeyUp:
			u.Send(v, Step{By: -1})
		case input.KeyHome:
			u.Send(v, Jump{To: 0})
		case input.KeyEnd:
			u.Send(v, Jump{To: -1})
		case input.KeyZ, input.KeySpace:
			v.toggle(v.room().Center(), u)
		case input.KeyEqual:
			v.zoomTo(v.z.Target()*1.25, v.room().Center(), u)
		case input.KeyMinus:
			v.zoomTo(v.z.Target()*0.8, v.room().Center(), u)
		case input.KeyEscape, input.KeyEnter, input.KeyG:
			u.Send(v, LeaveCull{})
		default:
			return false
		}
		return true
	case input.Scroll:
		d := e.Notches.Y
		if d == 0 {
			d = -e.Delta.Y / 40
		}
		if d != 0 {
			v.zoomTo(v.z.Target()*float32(math.Pow(1.25, float64(d))), e.Pos, u)
		}
		return true
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		for _, t := range v.stripRects() {
			if t.r.Contains(e.Pos) {
				u.Send(v, Jump{To: t.index})
				return true
			}
		}
		if e.Clicks == 2 {
			v.toggle(e.Pos, u)
			return true
		}
		v.dragging, v.last = true, e.Pos
		return true
	case input.PointerMove:
		if !v.dragging {
			return false
		}
		f, s := v.full(), v.fit()*v.z.Value()
		c := v.c.Value()
		c = geom.Pt(c.X-(e.Pos.X-v.last.X)/(f.W*s), c.Y-(e.Pos.Y-v.last.Y)/(f.H*s))
		v.c.Jump(v.clampCentre(v.z.Value(), c))
		v.last = e.Pos
		u.Invalidate()
		return true
	case input.PointerUp:
		if v.dragging {
			v.dragging = false
			v.askTiles(u)
			return true
		}
	}
	return false
}

// Layout implements [gunim.Node]: the picture at fit, which the zoom
// scales as it paints, and the readout in the lower left corner.
func (v *cullView) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	v.box, v.scale = box, max(f.Scale, 0.1)
	r := v.fitRect()
	hero := kids.At(0)
	hero.Layout(gunim.Tight(r.Size()))
	hero.Place(r.Min)
	hud := kids.At(1)
	sz := hud.Layout(gunim.Loose(box))
	hud.Place(geom.Pt(0, max(0, box.H-stripHeight-sz.H)))
	if cur := v.current(); cur != nil {
		v.ring.Animate(thumbWidth(*cur), widget.Quick.Get(f.Theme))
		if v.ring.Value() == 0 {
			v.ring.Jump(thumbWidth(*cur))
		}
	}
	return box
}

// current is the filmstrip's picture of the photo showing.
func (v *cullView) current() *Thumb {
	for i := range v.st.Strip {
		if v.st.Strip[i].Index == v.st.Index {
			return &v.st.Strip[i]
		}
	}
	return nil
}

// Paint implements [gunim.Node]: the backdrop, the picture at its zoom,
// and the filmstrip, the marks and the readout over it.
func (v *cullView) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(v.in.Value(), 0), 1)
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(withAlpha(backdrop, in)))
	func() {
		// The picture shows above the filmstrip, whose top edge it follows
		// as the strip slides in, so a flight from a tile low in the grid
		// starts whole and a zoom never covers the strip.
		bottom := box.H - stripHeight*in
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, box.W, max(0, bottom)), Opacity: 1, Clip: true})()
		if v.leaving && in < 0.999 {
			// No tile to fly back to: the picture fades with the rest.
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: in})()
		}
		z, r := v.z.Value(), v.fitRect()
		o := v.origin(z, v.c.Value())
		defer p.Push(paint.Translate(geom.Pt(o.X-r.Min.X, o.Y-r.Min.Y)))()
		defer p.Push(paint.Scale(z, r.Min))()
		kids.At(0).Paint(p)
	}()
	if in < 0.001 {
		return
	}
	// The chrome comes up from below as the view comes in.
	defer p.Push(paint.Translate(geom.Pt(0, (1-in)*stripHeight)))()
	if in < 0.999 {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: in})()
	}
	v.paintStrip(p, box)
	r := v.room()
	v.marks.paintStars(p, geom.Pt(r.Max.X-12-5*16, r.Min.Y+17), 11, 5, 0xc0)
	v.marks.paintFlag(p, geom.Pt(r.Max.X-12-5*16-20, r.Min.Y+17), 17)
	hud := kids.At(1)
	hs := hud.Size()
	p.RRect(geom.Rc(0, box.H-stripHeight-hs.H, hs.W, hs.H), 0, paint.Solid(color.NRGBA{A: 0xa0}))
	hud.Paint(p)
}

func withAlpha(c color.NRGBA, a float32) color.NRGBA {
	c.A = uint8(float32(c.A) * min(max(a, 0), 1))
	return c
}

// paintStrip draws the filmstrip: each picture, a rejected one dimmed, and
// the flags and stars along the bottom edge, gliding under a ring that
// stays in the middle.
func (v *cullView) paintStrip(p *paint.Painter, box geom.Size) {
	p.RRect(geom.Rc(0, box.H-stripHeight, box.W, stripHeight), 0, paint.Solid(color.NRGBA{R: 0x13, G: 0x15, B: 0x1a, A: 0xff}))
	for _, t := range v.stripRects() {
		if t.r.Max.X < 0 || t.r.Min.X > box.W {
			continue
		}
		opacity := float32(1)
		if t.th.Flag == "exclude" {
			opacity = 0.35
		}
		if t.th.Img != nil {
			p.Image(t.th.Img, t.r, paint.ImageOpts{Opacity: opacity, Radius: 3})
		} else {
			p.RRect(t.r, 3, paint.Solid(color.NRGBA{R: 0x22, G: 0x25, B: 0x2d, A: 0xff}))
		}
		switch t.th.Flag {
		case "pick":
			p.RRect(geom.Rc(t.r.Min.X+4, t.r.Min.Y+4, 8, 8), 4, paint.Solid(pickInk))
		case "exclude":
			p.RRect(geom.Rc(t.r.Min.X+4, t.r.Min.Y+4, 8, 8), 4, paint.Solid(rejectInk))
		}
		for i := range t.th.Rating {
			p.RRect(geom.Rc(t.r.Min.X+4+float32(i)*7, t.r.Max.Y-9, 5, 5), 2.5, paint.Solid(starInk))
		}
	}
	if w := v.ring.Value(); w > 0 {
		y := box.H - stripHeight + (stripHeight-thumbHeight)/2
		r := geom.Rc(box.W/2-w/2, y, w, thumbHeight).Inset(geom.Uniform(-3))
		p.RRectStroke(r, 5, paint.Fill{}, paint.Stroke{Width: 2, Color: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xe0}})
	}
}

// cullPic is the photo showing: its best pixels so far, or its small
// picture, and the full-resolution tiles fetched over them. A step slides
// the next photo in over the last, and sharper pixels fade in over
// blurrier ones.
type cullPic struct {
	anim.Group
	v        *cullView
	img, old *paint.Image
	// oldRect is where the old picture shows, at fit, in the view, and
	// slide how far the new one comes in from, sideways.
	oldRect geom.Rect
	slide   float32
	mix     *anim.Float
	tiles   map[image.Point]*paint.Image
	full    image.Point
}

func newCullPic(v *cullView, s Cull) *cullPic {
	q := &cullPic{v: v, img: best(s), mix: anim.NewFloat(1), tiles: s.Tiles, full: s.Full}
	q.Add(q.mix)
	return q
}

// best is the best picture of the photo in s so far.
func best(s Cull) *paint.Image {
	if s.Img != nil {
		return s.Img
	}
	return s.Thumb
}

// step shows the photo in s, come from prev, sliding in from slide.
func (q *cullPic) step(s, prev Cull, slide float32) {
	q.old, q.oldRect = q.img, q.v.fitRect()
	q.img, q.tiles, q.full, q.slide = best(s), s.Tiles, s.Full, slide
	q.mix.Jump(0)
	q.mix.Animate(1, stepIn)
}

// sharpen shows the photo's better pixels, fading in over the last.
func (q *cullPic) sharpen(s Cull) {
	q.tiles, q.full = s.Tiles, s.Full
	img := best(s)
	if img == q.img {
		return
	}
	if q.img == nil || q.mix.Value() < 1 {
		// Mid-step, or nothing to fade from: the new pixels take the place.
		q.img = img
		return
	}
	q.old, q.oldRect, q.img, q.slide = q.img, q.v.fitRect(), img, 0
	q.mix.Jump(0)
	q.mix.Animate(1, sharpen)
}

// Layout implements [gunim.Node].
func (q *cullPic) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node].
func (q *cullPic) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	k := min(max(q.mix.Value(), 0), 1)
	if q.old != nil && k < 1 {
		// The last picture, where it was, sliding away.
		at := q.v.fitRect().Min
		o := q.oldRect.Add(geom.Pt(-at.X-q.slide*k, -at.Y))
		p.Image(q.old, o, paint.ImageOpts{Opacity: 1 - k*k})
	}
	if q.img == nil {
		p.RRect(r.Add(geom.Pt(q.slide*(1-k), 0)), 2, paint.Solid(withAlpha(frameInk, k)))
		return
	}
	in := min(1, k*1.6)
	p.Image(q.img, r.Add(geom.Pt(q.slide*(1-k), 0)), paint.ImageOpts{Opacity: in})
	if len(q.tiles) == 0 || q.full.X <= 0 {
		return
	}
	// The tiles in the window, over the picture, scaled with it.
	s := box.W / float32(q.full.X)
	t := p.Transform()
	win := geom.Rect{Max: q.v.box.Point()}
	for at, img := range q.tiles {
		w, h := img.Size()
		tr := geom.Rc(float32(at.X*tileSize)*s, float32(at.Y*tileSize)*s, float32(w)*s, float32(h)*s)
		on := geom.Rect{Min: t.Apply(tr.Min), Max: t.Apply(tr.Max)}.Normalized()
		if on.Max.X < win.Min.X || on.Max.Y < win.Min.Y || on.Min.X > win.Max.X || on.Min.Y > win.Max.Y {
			continue
		}
		p.Image(img, tr, paint.ImageOpts{Opacity: in})
	}
}
