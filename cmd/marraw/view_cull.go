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
	"github.com/marrasen/gunim/icon"
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
	// pendingKey is a press waiting for the text it types, as
	// heldForText says.
	pendingKey *input.KeyPress
	// wbBar is the eyedropper's bar, and wbRead what the magnifier
	// reads; wbAt is where the pointer is over the photo, as wbOver says.
	wbBar  gunim.Node
	wbRead *widget.Label
	wbWarn *widget.Label
	wbAt   geom.Point
	wbOver bool
	st     Cull
	name   *widget.Label
	note   *widget.Label
	hud    gunim.Node
	pic    *cullPic
	hero   *widget.Hero

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
	// slot holds the develop panel, and side is how far it has come in
	// beside the photo, which makes room for it.
	slot *gunim.Box
	side *anim.Float
	// shape is the photo's full size, gliding to its true shape as its
	// pixels tell it.
	shape *anim.Size
	// notice is the note over the photo, noticeIn how far it has come in,
	// and noticeSeq the note it is, to show each anew.
	notice    *widget.Label
	noticeIn  *anim.Float
	noticeSeq int
	noteRect  geom.Rect
	// navDrag says a press in the navigator moves the view, and origIn
	// and wbIn bring the labels for the original and the eyedropper in.
	navDrag      bool
	wbBarRect    geom.Rect
	origIn, wbIn *anim.Float
	labelOrig    *widget.Label
	labelWB      *widget.Label
	// trail follows a drag, for a flick's speed; fling is the glide's
	// speed on screen, in pixels a second, while flinging, and glided says
	// a glide came to rest and the tiles there are to be asked for.
	trail    anim.Trail
	fling    geom.Point
	flinging bool
	glided   bool
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
		scale: 1, in: anim.NewFloat(0), strip: anim.NewFloat(float32(s.Index)), ring: anim.NewFloat(0),
		slot: &gunim.Box{}, side: anim.NewFloat(0), shape: anim.NewSize(fullOf(s)),
		notice: widget.NewLabel(""), noticeIn: anim.NewFloat(0), noticeSeq: s.NoticeSeq,
		origIn: anim.NewFloat(0), wbIn: anim.NewFloat(0),
		labelOrig: widget.NewLabel("Original"), labelWB: widget.NewLabel("Click something neutral grey or white"),
		wbRead: widget.NewLabel(""), wbWarn: widget.NewLabel("")}
	v.labelOrig.Size, v.labelWB.Size = noteSize, noteSize
	v.wbRead.Face, v.wbRead.Size, v.wbRead.Color = widget.MonoFont, readoutSize, readoutInk
	v.wbWarn.Size, v.wbWarn.Color = readoutSize, warnInk
	v.labelWB.Color = noteInk
	v.wbBar = v.newWBBar()
	v.notice.Size = noteSize
	if s.Panel {
		v.side.Jump(1)
	}
	v.Add(v.z, v.c, v.in, v.strip, v.ring, v.side, v.shape, v.noticeIn, v.origIn, v.wbIn)
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
	if s.ID != prev.ID {
		v.flinging = false
		v.shape.Jump(fullOf(s))
	} else {
		// The same photo in a shape learned from its pixels: it glides.
		v.shape.Animate(fullOf(s), widget.Quick.Get(th))
	}
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
	v.side.Animate(map[bool]float32{false: 0, true: 1}[s.Panel], panelSlide)
	v.origIn.Animate(map[bool]float32{false: 0, true: 1}[s.Original], widget.Quick.Get(th))
	v.wbIn.Animate(map[bool]float32{false: 0, true: 1}[s.WBPick], widget.Quick.Get(th))
	if s.NoticeSeq != v.noticeSeq {
		v.showNotice(s.Notice, s.NoticeSeq, u)
	}
	v.readout(v.z.Target())
	note := s.Note
	if s.TileNote != "" {
		note += "\n" + s.TileNote + fmt.Sprintf(" (%d in memory)", len(s.Tiles))
	}
	v.note.Text = note
	u.Invalidate()
}

func (v *cullView) readout(z float32) {
	v.name.Text = fmt.Sprintf("%s   %d / %d   %.0f%%", v.st.Name, v.st.Index+1, v.st.Total, v.percent(z))
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
func (v *cullView) full() geom.Size { return v.shape.Value() }

// fullOf is the full resolution s gives, or a guess from its shape.
func fullOf(s Cull) geom.Size {
	if s.Full.X > 0 && s.Full.Y > 0 {
		return geom.Sz(float32(s.Full.X), float32(s.Full.Y))
	}
	return geom.Sz(3000*s.Aspect, 3000)
}

// stripHeight is the filmstrip's band along the bottom, and thumbHeight its
// pictures'.
const (
	stripHeight = 92
	thumbHeight = 64
	thumbGap    = 6
)

// room is where the photo goes at fit: above the filmstrip, and beside
// the develop panel as far as it has come in.
func (v *cullView) room() geom.Rect {
	return geom.Rc(0, 0, max(0, v.box.W-panelWidth*v.side.Value()), max(0, v.box.H-stripHeight)).Inset(geom.Uniform(16))
}

// inPanel reports whether p is over the develop panel.
func (v *cullView) inPanel(p geom.Point) bool {
	return v.side.Value() > 0.01 && p.X >= v.box.W-panelWidth*v.side.Value() && p.Y < v.box.H-stripHeight
}

// Slot implements [gunim.Slotted]: the develop panel mounts here.
func (v *cullView) Slot() gunim.Node { return v.slot }

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

// panSlack is how far past the view's edge, as a share of the view, the
// photo may be pushed, as in marraw's loupe: anywhere from edge to edge
// while it is smaller than the view, and this far beyond, so it can always
// be moved clear of what lies over it.
const panSlack = 0.4

// clampCentre keeps the photo at zoom z where it may be pushed: its edges
// no further past the view's than panSlack of the view, whatever its size.
// Anywhere inside, it stays where it is put.
func (v *cullView) clampCentre(z float32, c geom.Point) geom.Point {
	f, s, rs := v.full(), v.fit()*z, v.room().Size()
	axis := func(c, length, view float32) float32 {
		w := length * s
		if w <= 0 {
			return 0.5
		}
		// The photo's near edge, from the view's, at most slack past it,
		// as React's scroll slack has it.
		slack := view*panSlack + max(0, view-w)
		lo, hi := view-w-slack, slack
		// c puts the photo's near edge at view/2 - c*w.
		return max((view/2-hi)/w, min(c, (view/2-lo)/w))
	}
	return geom.Pt(axis(c.X, f.W, rs.W), axis(c.Y, f.H, rs.H))
}

// zoomTo animates to zoom z, keeping the image point under at where it is.
func (v *cullView) zoomTo(z float32, at geom.Point, u *gunim.UI) {
	v.flinging = false
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
		// Back to fit, in the middle.
		v.flinging = false
		v.z.Animate(1, widget.Quick.Get(u.Theme()))
		v.c.Animate(geom.Pt(0.5, 0.5), widget.Quick.Get(u.Theme()))
		v.readout(1)
		v.askTiles(u)
		return
	}
	v.zoomTo(v.oneToOne(), at, u)
}

// askTiles asks for the tiles the view will show once the zoom and pan
// settle, or for none where the 2048 is sharp enough.
func (v *cullView) askTiles(u *gunim.UI) {
	v.askTilesBy(func(n gunim.Node, in gunim.Intent) { u.Send(n, in) })
}

// askTilesBy is askTiles sending by send, from a frame or a UI.
func (v *cullView) askTilesBy(send func(gunim.Node, gunim.Intent)) {
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
		send(v, w)
	}
}

// Children implements [gunim.Composite].
func (v *cullView) Children() []gunim.Node {
	return []gunim.Node{v.hero, v.hud, v.slot, v.notice, v.labelOrig, v.wbBar, v.wbRead, v.wbWarn}
}

// Cursor implements [gunim.CursorShaper]: a crosshair while the
// eyedropper is on.
func (v *cullView) Cursor(p geom.Point) input.Cursor {
	if v.st.WBPick && p.Y < v.box.H-stripHeight && !v.inPanel(p) && !v.wbBarRect.Contains(p) {
		// The magnifier takes the pointer's place once it has a frame.
		if _, ok := v.photoPoint(p); ok && v.st.WBFrame != nil {
			return input.CursorNone
		}
		return input.CursorCrosshair
	}
	return input.CursorArrow
}

// photoPoint is where p falls on the photo as it shows, 0 to 1 across and
// down, and whether it falls on it.
func (v *cullView) photoPoint(p geom.Point) (geom.Point, bool) {
	z := v.z.Value()
	f, s := v.full(), v.fit()*z
	o := v.origin(z, v.c.Value())
	at := geom.Pt((p.X-o.X)/(f.W*s), (p.Y-o.Y)/(f.H*s))
	return at, at.X >= 0 && at.X <= 1 && at.Y >= 0 && at.Y <= 1
}

// navOn reports whether the navigator shows: while zoomed past fit.
func (v *cullView) navOn() bool { return v.z.Value() > 1.02 }

// navRect is the navigator, in the photo's room's lower right corner, in
// the photo's shape.
func (v *cullView) navRect() geom.Rect {
	r := v.room()
	f := v.full()
	w := float32(190)
	h := w * f.H / max(f.W, 1)
	if h > 150 {
		h, w = 150, 150*f.W/max(f.H, 1)
	}
	return geom.Rc(r.Max.X-w-6, r.Max.Y-h-6, w, h)
}

// navTo moves the view's middle to where p falls in the navigator.
func (v *cullView) navTo(p geom.Point) {
	n := v.navRect()
	c := geom.Pt((p.X-n.Min.X)/n.Size().W, (p.Y-n.Min.Y)/n.Size().H)
	v.flinging = false
	v.c.Jump(v.clampCentre(v.z.Value(), c))
}

// paintNav draws the navigator: the photo small, and the part showing
// framed; it fades in as the zoom passes fit.
func (v *cullView) paintNav(p *paint.Painter) {
	k := min(max((v.z.Value()-1.02)*6, 0), 1)
	if k < 0.01 || v.pic.img == nil {
		return
	}
	n := v.navRect()
	defer p.Layer(paint.LayerOpts{Bounds: n.Inset(geom.Uniform(-48)), Opacity: k})()
	paintGlass(p, n.Inset(geom.Uniform(-4)), 8)
	p.Image(v.pic.img, pixelFit(n, v.pic.img), paint.ImageOpts{Opacity: 0.85, Radius: 3})
	// The part of the photo the room shows, as fractions of it.
	z := v.z.Value()
	f, s := v.full(), v.fit()*z
	o, r := v.origin(z, v.c.Value()), v.room()
	x0, y0 := max(0, (r.Min.X-o.X)/(f.W*s)), max(0, (r.Min.Y-o.Y)/(f.H*s))
	x1, y1 := min(1, (r.Max.X-o.X)/(f.W*s)), min(1, (r.Max.Y-o.Y)/(f.H*s))
	if x1 <= x0 || y1 <= y0 {
		return
	}
	frame := geom.Rect{Min: geom.Pt(n.Min.X+x0*n.Size().W, n.Min.Y+y0*n.Size().H), Max: geom.Pt(n.Min.X+x1*n.Size().W, n.Min.Y+y1*n.Size().H)}
	p.RRectStroke(frame, 2, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x18}), paint.Stroke{Width: 1.5, Color: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xe0}})
}

// Focusable implements [gunim.Focusable]: the keys step through the folder.
func (v *cullView) Focusable() bool { return true }

// Handle implements [gunim.Handler].
func (v *cullView) Handle(e input.Event, u *gunim.UI) bool {
	if v.leaving {
		return false
	}
	switch e := e.(type) {
	case input.KeyPress:
		if heldForText(e) {
			v.pendingKey = &e
			return true
		}
		// Ctrl and Z undoes the edit, with Shift or as Ctrl and Y redoes.
		if e.Mods.Has(input.ModControl) {
			switch e.Key {
			case input.KeyZ:
				u.Send(v, DevUndo{Redo: e.Mods.Has(input.ModShift)})
				return true
			case input.KeyY:
				u.Send(v, DevUndo{Redo: true})
				return true
			case input.KeyC:
				u.Send(v, EditCopy{})
				return true
			case input.KeyV:
				u.Send(v, EditPaste{})
				return true
			case input.Key0:
				u.Send(v, DevReset{})
				return true
			case input.KeyE:
				u.Send(v, AskExport{})
				return true
			case input.KeyK:
				openPalette(v, v.room(), u, paletteFor{culling: true, panel: v.st.Panel})
				return true
			case input.KeyU:
				// Auto tone; with Shift white balance and colour; with Alt
				// everything, as marraw's keys have it.
				secs := []string{"tone"}
				switch {
				case e.Mods.Has(input.ModAlt):
					secs = []string{"all"}
				case e.Mods.Has(input.ModShift):
					secs = []string{"wb", "color"}
				}
				u.Send(v, DevAuto{Sections: secs})
				return true
			}
			return false
		}
		// Backspace held shows the photo before any edit.
		if e.Key == input.KeyBackspace {
			if !e.Repeat {
				u.Send(v, ShowOriginal{On: true})
			}
			return true
		}
		if e.Key == input.KeyDelete {
			u.Send(v, AskDelete{})
			return true
		}
		// W turns the white-balance eyedropper on and off, and Escape off.
		if v.st.WBPick && (e.Key == input.KeyEscape || e.Key == input.KeyEnter) {
			u.Send(v, DevWBBar{Act: map[bool]string{false: "cancel", true: "done"}[e.Key == input.KeyEnter]})
			return true
		}
		if v.st.Panel && !e.Mods.Has(input.ModShift) && e.Key == input.KeyW {
			u.Send(v, DevWBPick{On: !v.st.WBPick})
			return true
		}
		if e.Mods.Has(input.ModShift) && v.panKey(e.Key, u) {
			return true
		}
		// With the develop panel open, Up and Down walk its controls, + and
		// - step the one chosen, a letter chooses one, and Escape lets it
		// go, as marraw's keys do.
		if v.st.Panel && v.developKey(e, u) {
			return true
		}
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
		case input.KeyEqual, input.KeyMinus, input.KeyKPAdd, input.KeyKPSubtract:
			// By what the key types, so + is + on any keyboard.
			z := map[bool]float32{false: 0.8, true: 1.25}[plusMinus(e) > 0]
			v.zoomTo(v.z.Target()*z, v.room().Center(), u)
		case input.KeyEscape, input.KeyEnter, input.KeyG:
			u.Send(v, LeaveCull{})
		case input.KeyD:
			u.Send(v, ToggleDevelop{})
		default:
			return false
		}
		return true
	case input.Scroll:
		if v.inPanel(e.Pos) {
			return false
		}
		d := e.Notches.Y
		if d == 0 {
			d = -e.Delta.Y / 40
		}
		if d != 0 {
			v.zoomTo(v.z.Target()*float32(math.Pow(1.25, float64(d))), e.Pos, u)
		}
		return true
	case input.TextInput:
		held := v.pendingKey
		v.pendingKey = nil
		if e.Text == "?" {
			u.Send(v, ShowShortcuts{})
			return true
		}
		if held != nil {
			k := *held
			k.Typed = false
			return v.Handle(k, u)
		}
		return false
	case input.KeyRelease:
		if e.Key == input.KeyBackspace && v.st.Original {
			u.Send(v, ShowOriginal{})
			return true
		}
		return false
	case input.WindowFocusLost:
		if v.st.Original {
			u.Send(v, ShowOriginal{})
		}
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || v.inPanel(e.Pos) {
			return false
		}
		// A click on a star rates the photo, again takes the rating off,
		// and one on a flag sets it, again takes it off.
		if v.marksRect().Contains(e.Pos) {
			mp := v.markPlace()
			if k := mp.starAt(e.Pos); k > 0 {
				u.Send(v, Rate{Stars: k, Toggle: true})
			} else if f := mp.flagAt(e.Pos); f != "" {
				u.Send(v, Mark{Flag: f})
			}
			return true
		}
		// The navigator: a press there moves the view to it.
		if v.navOn() && v.navRect().Contains(e.Pos) {
			v.navDrag = true
			v.navTo(e.Pos)
			return true
		}
		// The eyedropper: a click picks where it lands on the photo.
		if v.st.WBPick && e.Pos.Y < v.box.H-stripHeight && !v.wbBarRect.Contains(e.Pos) {
			if at, ok := v.photoPoint(e.Pos); ok {
				u.Send(v, DevWBAt{X: float64(at.X), Y: float64(at.Y)})
			}
			return true
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
		// A press catches a photo still gliding.
		v.flinging = false
		v.c.Jump(v.clampCentre(v.z.Value(), v.c.Value()))
		v.dragging, v.last = true, e.Pos
		v.trail.Reset()
		v.trail.Add(e.Pos, e.Time)
		return true
	case input.PointerMove:
		if !v.dragging {
			v.marks.hover(e.Pos, v.marksRect().Contains(e.Pos), v.markPlace(), u.Theme())
			v.wbHover(e.Pos)
			u.Invalidate()
		}
		if v.navDrag {
			v.navTo(e.Pos)
			u.Invalidate()
			return true
		}
		if !v.dragging {
			return false
		}
		v.trail.Add(e.Pos, e.Time)
		f, s := v.full(), v.fit()*v.z.Value()
		c := v.c.Value()
		c = geom.Pt(c.X-(e.Pos.X-v.last.X)/(f.W*s), c.Y-(e.Pos.Y-v.last.Y)/(f.H*s))
		v.c.Jump(v.clampCentre(v.z.Value(), c))
		v.last = e.Pos
		u.Invalidate()
		return true
	case input.PointerLeave:
		v.marks.hover(geom.Point{}, false, v.markPlace(), u.Theme())
		v.wbOver = false
		u.Invalidate()
		return false
	case input.PointerUp:
		if v.navDrag {
			v.navDrag = false
			v.askTiles(u)
			return true
		}
		if v.dragging {
			v.dragging = false
			// A flick sends the photo gliding on, slowing to a stop.
			if fl := v.trail.Velocity(e.Time); math.Hypot(float64(fl.X), float64(fl.Y)) > 60 {
				v.fling, v.flinging = fl, true
				u.Invalidate()
				return true
			}
			v.askTiles(u)
			return true
		}
	}
	return false
}

// heldForText reports whether press e waits for the text it types to say
// what it means: Shift with the key of + or -, which types ? on a Swedish
// keyboard, where a US one types + or _.
func heldForText(e input.KeyPress) bool {
	return e.Typed && e.Mods.Has(input.ModShift) && !e.Mods.Has(input.ModControl) && !e.Mods.Has(input.ModAlt) &&
		plusMinus(e) != 0
}

// plusMinus is +1 for a key that types + or =, -1 for one that types -,
// and 0 for another: by the character, so + is + on a Swedish keyboard,
// where the key sits where a US one has -.
func plusMinus(e input.KeyPress) int {
	switch e.Char {
	case '+', '=':
		return 1
	case '-':
		return -1
	case 0:
		switch e.Key {
		case input.KeyEqual, input.KeyKPAdd:
			return 1
		case input.KeyMinus, input.KeyKPSubtract:
			return -1
		}
	}
	switch e.Key {
	case input.KeyKPAdd:
		return 1
	case input.KeyKPSubtract:
		return -1
	}
	return 0
}

// developKey takes a key for the develop panel, and reports whether it
// was one.
func (v *cullView) developKey(e input.KeyPress, u *gunim.UI) bool {
	if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) {
		return false
	}
	switch e.Key {
	case input.KeyUp:
		u.Send(v, DevWalk{By: -1})
		return true
	case input.KeyDown:
		u.Send(v, DevWalk{By: 1})
		return true
	case input.KeyEscape:
		if v.st.Active != "" {
			u.Send(v, DevPick{})
			return true
		}
		return false
	}
	if d := plusMinus(e); d != 0 && v.st.Active != "" {
		u.Send(v, DevNudge{Dir: d, Big: e.Mods.Has(input.ModShift)})
		return true
	}
	ch := e.Char
	if ch == 0 && e.Key >= input.KeyA && e.Key <= input.KeyZ {
		ch = rune('a' + int(e.Key-input.KeyA))
	}
	if key, ok := controlKeys[ch]; ok && !e.Mods.Has(input.ModShift) {
		u.Send(v, DevPick{Key: key})
		return true
	}
	return false
}

// noticeFor is how long a short note stays over the photo.
const noticeFor = 1400 * time.Millisecond

// noticeTime is how long note stays: a longer one, as why a spot cannot
// be picked, stays long enough to read.
func noticeTime(note string) time.Duration {
	return noticeFor + time.Duration(max(0, len(note)-30))*45*time.Millisecond
}

// showNotice pops note in over the photo, and lets it fade after a moment;
// a note on the heels of the last pops again.
func (v *cullView) showNotice(note string, seq int, u *gunim.UI) {
	v.noticeSeq = seq
	v.notice.Text = note
	v.noticeIn.Jump(min(v.noticeIn.Value(), 0.6))
	v.noticeIn.Animate(1, widget.Bounce.Get(u.Theme()))
	u.After(noticeTime(note), func(u *gunim.UI) {
		if v.noticeSeq == seq {
			v.noticeIn.Animate(0, widget.Settle.Get(u.Theme()))
		}
	})
	u.Invalidate()
}

// panStep is how far Shift and an arrow pan, as a share of the view, as
// in marraw's loupe; panEase carries the photo there. A held key's
// repeats push the destination on ahead of the photo, so it glides.
const panStep = 0.1

var panEase = anim.Tween{Duration: 160 * time.Millisecond}

// panKey pans the photo for Shift and an arrow, and reports whether k is
// one.
func (v *cullView) panKey(k input.Key, u *gunim.UI) bool {
	var d geom.Point
	switch k {
	case input.KeyLeft:
		d.X = -1
	case input.KeyRight:
		d.X = 1
	case input.KeyUp:
		d.Y = -1
	case input.KeyDown:
		d.Y = 1
	default:
		return false
	}
	v.flinging = false
	z := v.z.Target()
	f, s, view := v.full(), v.fit()*z, v.room().Size()
	c := v.c.Target()
	c = geom.Pt(c.X+d.X*panStep*view.W/(f.W*s), c.Y+d.Y*panStep*view.H/(f.H*s))
	v.c.Animate(v.clampCentre(z, c), panEase)
	v.askTiles(u)
	u.Invalidate()
	return true
}

// glideTau is how a flick's glide slows: its speed falls to a third in a
// third of a second, as a fling does in gunim's scroll views.
const glideTau = 0.33

// Step implements [gunim.Animator]: the springs, and a flick's glide.
func (v *cullView) Step(dt time.Duration) bool {
	busy := v.Group.Step(dt)
	if !v.flinging {
		return busy
	}
	sec := dt.Seconds()
	e := float32(math.Exp(-sec / glideTau))
	move := geom.Pt(v.fling.X*glideTau*(1-e), v.fling.Y*glideTau*(1-e))
	v.fling = geom.Pt(v.fling.X*e, v.fling.Y*e)
	z := v.z.Value()
	f, s := v.full(), v.fit()*z
	c := v.c.Value()
	c = geom.Pt(c.X-move.X/(f.W*s), c.Y-move.Y/(f.H*s))
	// At the furthest it may go, it stops that way, and stays.
	in := v.clampCentre(z, c)
	if in.X != c.X {
		v.fling.X = 0
	}
	if in.Y != c.Y {
		v.fling.Y = 0
	}
	v.c.Jump(in)
	if math.Hypot(float64(v.fling.X), float64(v.fling.Y)) < 20 {
		// At rest: the tiles of where it came to.
		v.flinging, v.glided = false, true
	}
	return true
}

// Layout implements [gunim.Node]: the picture at fit, which the zoom
// scales as it paints, and the readout in the lower left corner.
func (v *cullView) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	v.box, v.scale = box, max(f.Scale, 0.1)
	if v.glided {
		v.glided = false
		v.askTilesBy(f.Send)
	}
	r := v.fitRect()
	hero := kids.At(0)
	hero.Layout(gunim.Tight(r.Size()))
	hero.Place(r.Min)
	hud := kids.At(1)
	sz := hud.Layout(gunim.Loose(box))
	hud.Place(geom.Pt(12, max(0, box.H-stripHeight-sz.H-12)))
	slot := kids.At(2)
	slot.Layout(gunim.Tight(geom.Sz(panelWidth, max(0, box.H-stripHeight))))
	slot.Place(geom.Pt(box.W-panelWidth, 0))
	// The note, in the middle at the top of the photo's room.
	note := kids.At(3)
	room := v.room()
	// It keeps clear of the marks at the top right, wrapping if need be.
	nw := max(200, min(box.W/2, room.Size().W-2*(v.marksRect().Size().W+40)))
	ns := note.Layout(gunim.Loose(geom.Sz(nw, 80)))
	v.noteRect = geom.Rc(room.Center().X-ns.W/2, room.Min.Y+14, ns.W, ns.H)
	note.Place(v.noteRect.Min)
	orig := kids.At(4)
	orig.Layout(gunim.Loose(geom.Sz(box.W/2, 40)))
	orig.Place(geom.Pt(room.Min.X+16, room.Min.Y+14))
	// The eyedropper's bar, at the foot of the photo while it is out,
	// and nowhere for the pointer otherwise.
	bar := kids.At(5)
	bs := bar.Layout(gunim.Loose(geom.Sz(room.Size().W, 60)))
	v.wbBarRect = geom.Rc(room.Center().X-bs.W/2-16, box.H-stripHeight-bs.H-20-16, bs.W+32, bs.H+20)
	if v.wbBarRect.Min.X < 12+sz.W+12 {
		// Over the readout's corner: above it instead.
		v.wbBarRect = v.wbBarRect.Add(geom.Pt(0, -(sz.H + 4)))
	}
	if v.wbIn.Value() > 0.01 {
		bar.Place(v.wbBarRect.Min.Add(geom.Pt(16, 10)))
	} else {
		bar.Place(geom.Pt(-10000, -10000))
	}
	for i := 6; i <= 7; i++ {
		kids.At(i).Layout(gunim.Loose(geom.Sz(300, 40)))
	}
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
func (v *cullView) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(v.in.Value(), 0), 1)
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(withAlpha(backdrop, in)))
	func() {
		// The picture shows above the filmstrip, whose top edge it follows
		// as the strip slides in, so a flight from a tile low in the grid
		// starts whole and a zoom never covers the strip.
		bottom := box.H - stripHeight*in
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, box.W, max(0, bottom)), Opacity: 1, Clip: true})()
		if v.leaving && in < 0.999 && !v.hero.Flying() {
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
	mp := v.markPlace()
	paintGlass(p, v.marksRect(), 18)
	v.marks.paint(p, f.Theme, mp, 0xc0, true)
	hud := kids.At(1)
	hs := hud.Size()
	paintGlass(p, geom.Rc(12, box.H-stripHeight-hs.H-12, hs.W, hs.H), 10)
	hud.Paint(p)
	v.paintNav(p)
	kids.At(2).Paint(p)
	v.paintNotice(p, kids.At(3))
	// The label for the original, in the corner.
	l, rm := kids.At(4), v.room()
	paintNote(p, l, geom.Rc(rm.Min.X+16, rm.Min.Y+14, l.Size().W, l.Size().H), v.origIn.Value())
	v.paintWB(p, f.Theme, kids.At(5), kids.At(6), kids.At(7))
}

// markPlace is where the photo's marks are: its flags and stars, at the
// top right of the room for the photo, to be clicked.
func (v *cullView) markPlace() markPlace {
	r := v.room()
	x, y := r.Max.X-16-5*18-4*5, r.Min.Y+20
	return markPlace{stars: geom.Pt(x, y), star: 18, gap: 5, reject: geom.Pt(x-24, y), pick: geom.Pt(x-56, y), flag: 20}
}

// marksRect is the pill behind the marks.
func (v *cullView) marksRect() geom.Rect {
	mp, r := v.markPlace(), v.room()
	return geom.Rc(mp.pick.X-20, mp.stars.Y-18, r.Max.X-4-(mp.pick.X-20), 36)
}

// paintNotice draws the note in a pill, popping in and fading out.
func (v *cullView) paintNotice(p *paint.Painter, note gunim.Child) {
	paintNote(p, note, v.noteRect, v.noticeIn.Value())
}

// paintNote draws note, placed at at, in a pill, k of the way in.
func paintNote(p *paint.Painter, note gunim.Child, at geom.Rect, k float32) {
	if k < 0.01 {
		return
	}
	r := at.Inset(geom.Insets{Top: -7, Bottom: -7, Left: -14, Right: -14})
	defer p.Push(paint.Scale(0.9+0.1*k, r.Center()))()
	if k < 0.999 {
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-48)), Opacity: min(k, 1)})()
	}
	paintGlass(p, r, min(r.Size().H/2, 16))
	note.Paint(p)
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
		// The flag and the stars as small icons, on a shade to read on
		// any picture.
		if flagged(t.th.Flag) {
			ic, w := flagSet, float32(2)
			if t.th.Flag == "exclude" {
				ic, w = rejectMark, 3
			}
			fr := geom.Rc(t.r.Min.X+3, t.r.Min.Y+3, 13, 13)
			p.RRect(fr.Inset(geom.Uniform(-2)), 4, paint.Solid(color.NRGBA{A: 0x90}))
			p.Mask(icon.Stroke{Icon: ic, Width: w, Progress: 1}, fr, flagInk(t.th.Flag))
		}
		if n := t.th.Rating; n > 0 {
			p.RRect(geom.Rc(t.r.Min.X+2, t.r.Max.Y-12, float32(n)*9+3, 11), 3, paint.Solid(color.NRGBA{A: 0x90}))
			for i := range n {
				p.Mask(icon.Stroke{Icon: starLit, Width: 2, Progress: 1}, geom.Rc(t.r.Min.X+4+float32(i)*9, t.r.Max.Y-11, 9, 9), starInk)
			}
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
	if s.Live {
		// A live preview of an edit under way swaps in at once, so the photo
		// keeps up with the slider.
		q.img, q.old = img, nil
		q.mix.Jump(1)
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
		// Sliding, it fades as it goes; fading to sharper pixels in place,
		// it stays whole under them, so nothing behind shows through.
		op := float32(1)
		if q.slide != 0 {
			op = 1 - k*k
		}
		p.Image(q.old, pixelFit(o, q.old), paint.ImageOpts{Opacity: op})
	}
	if q.img == nil {
		p.RRect(r.Add(geom.Pt(q.slide*(1-k), 0)), 2, paint.Solid(withAlpha(frameInk, k)))
		return
	}
	in := min(1, k*1.6)
	p.Image(q.img, pixelFit(r, q.img).Add(geom.Pt(q.slide*(1-k), 0)), paint.ImageOpts{Opacity: in})
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
