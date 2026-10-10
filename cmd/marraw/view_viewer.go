package main

import (
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

// viewerView is the pop-out viewer: the photo the main window has in
// hand, fitted, with its own zoom and pan kept from photo to photo, and
// a pin and full screen that come up as the pointer moves.
type viewerView struct {
	anim.Group
	st        ViewerState
	img, prev *paint.Image
	mix       *anim.Float
	// z is the zoom, one fitting the photo, and c the point of the photo
	// at the window's middle, as fractions of it.
	z      *anim.Float
	c      *anim.Point
	box    geom.Size
	scale  float32
	top    float32
	chrome *anim.Float
	hide   func()
	pin    *widget.IconButton
	full   *widget.IconButton
	name   *widget.Label
	ctrls  gunim.Node
	// drag is where a press to pan began, and from the centre then.
	dragging bool
	dragAt   geom.Point
	dragC    geom.Point
	pinned   bool
}

func newViewerView(s ViewerState) *viewerView {
	v := &viewerView{st: s, mix: anim.NewFloat(1), z: anim.NewFloat(1), c: anim.NewPoint(geom.Pt(0.5, 0.5)),
		chrome: anim.NewFloat(0), name: newSmallLabel(""), scale: 1}
	v.Add(v.mix, v.z, v.c, v.chrome)
	v.name.Face, v.name.Color = widget.MonoFont, noteInk
	v.pin = widget.NewIconButton(icon.Pin, "Keep over other windows (P)")
	v.pin.KeepFocus = true
	v.pin.OnClick = func(u *gunim.UI) gunim.Intent { return v.setPinned(!v.pinned, u) }
	v.full = widget.NewIconButton(icon.Maximize2, "Full screen (F11)")
	v.full.KeepFocus = true
	v.full.OnClick = func(u *gunim.UI) gunim.Intent {
		u.SetFullScreen(!u.FullScreen())
		return nil
	}
	v.ctrls = &hflow{items: []gunim.Node{v.pin, v.full}, gap: 4, pad: 6}
	return v
}

// show takes s: a new picture crossfades in, and the pin follows.
func (v *viewerView) show(s ViewerState, u *gunim.UI) {
	if s.Img != v.img {
		if v.img != nil && s.ID != v.st.ID {
			v.prev = v.img
			v.mix.Jump(0)
			v.mix.Animate(1, widget.Crossfade.Get(u.Theme()))
		}
		v.img = s.Img
	}
	v.st = s
	v.name.Text = s.Name
	if s.Pinned != v.pinned || !u.Pinned() && s.Pinned {
		v.setPinned(s.Pinned, u)
	}
	u.Invalidate()
}

// setPinned keeps the window over others, or not.
func (v *viewerView) setPinned(on bool, u *gunim.UI) gunim.Intent {
	v.pinned = on
	_ = u.SetPinned(on)
	v.pin.Active = on
	v.pin.Icon = map[bool]*icon.Icon{false: icon.PinOff, true: icon.Pin}[on]
	u.Invalidate()
	return ViewerPin{On: on}
}

// Children implements [gunim.Composite].
func (v *viewerView) Children() []gunim.Node { return []gunim.Node{v.ctrls, v.name} }

// Layout implements [gunim.Node].
func (v *viewerView) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	v.box, v.scale, v.top = c.Max, max(f.Scale, 0.1), f.Safe.Top
	cs := kids.At(0).Layout(gunim.Loose(c.Max))
	kids.At(0).Place(geom.Pt(c.Max.W-cs.W-14, v.top+10))
	ns := kids.At(1).Layout(gunim.Loose(geom.Sz(c.Max.W-28, 30)))
	kids.At(1).Place(geom.Pt(14, c.Max.H-ns.H-12))
	return c.Max
}

// fitRect is where the picture fits whole.
func (v *viewerView) fitRect(m *paint.Image) geom.Rect {
	if m == nil {
		return geom.Rect{}
	}
	w, h := m.Size()
	if w == 0 || h == 0 {
		return geom.Rect{}
	}
	k := min(v.box.W/float32(w), v.box.H/float32(h))
	fw, fh := float32(w)*k, float32(h)*k
	return geom.Rc((v.box.W-fw)/2, (v.box.H-fh)/2, fw, fh)
}

// shown is where the picture is drawn at zoom z about centre c.
func (v *viewerView) shown(m *paint.Image, z float32, c geom.Point) geom.Rect {
	r := v.fitRect(m)
	w, h := r.Size().W*z, r.Size().H*z
	return geom.Rc(v.box.W/2-c.X*w, v.box.H/2-c.Y*h, w, h)
}

// clampC keeps the picture over the window where it is larger than it,
// and centred where it is not.
func (v *viewerView) clampC(c geom.Point, z float32) geom.Point {
	r := v.fitRect(v.img)
	w, h := r.Size().W*z, r.Size().H*z
	cl := func(x, size, room float32) float32 {
		if size <= room {
			return 0.5
		}
		half := room / 2 / size
		return min(max(x, half), 1-half)
	}
	return geom.Pt(cl(c.X, w, v.box.W), cl(c.Y, h, v.box.H))
}

// oneToOne is the zoom where a pixel of the picture is a pixel of the
// screen.
func (v *viewerView) oneToOne() float32 {
	r := v.fitRect(v.img)
	if v.img == nil || r.Size().W == 0 {
		return 1
	}
	w, _ := v.img.Size()
	return max(1, float32(w)/(r.Size().W*v.scale))
}

// zoomTo glides to zoom z, keeping the photo's point under at where it is.
func (v *viewerView) zoomTo(z float32, at geom.Point, u *gunim.UI) {
	z = max(1, min(z, 16))
	old := v.z.Target()
	c := v.c.Target()
	if r := v.shown(v.img, old, c); r.Size().W > 0 {
		// The photo's point under at, which stays there.
		px := (at.X - r.Min.X) / r.Size().W
		py := (at.Y - r.Min.Y) / r.Size().H
		f := v.fitRect(v.img)
		w, h := f.Size().W*z, f.Size().H*z
		c = geom.Pt(px-(at.X-v.box.W/2)/w, py-(at.Y-v.box.H/2)/h)
	}
	v.z.Animate(z, widget.Quick.Get(u.Theme()))
	v.c.Animate(v.clampC(c, z), widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// wake brings the controls up, and lets them fade after a moment.
func (v *viewerView) wake(u *gunim.UI) {
	v.chrome.Animate(1, widget.Quick.Get(u.Theme()))
	if v.hide != nil {
		v.hide()
	}
	v.hide = u.After(1800*time.Millisecond, func(u *gunim.UI) {
		v.hide = nil
		v.chrome.Animate(0, widget.Settle.Get(u.Theme()))
		u.Invalidate()
	})
	u.Invalidate()
}

// Focusable implements [gunim.Focuser]: the viewer takes the keys.
func (v *viewerView) Focusable() bool { return true }

// Handle implements [gunim.Handler]: the wheel and + and - zoom, a drag
// pans, Z, Space and a double click go between fit and 1:1, F11 fills
// the screen, P pins, and Ctrl+N closes, as it opened.
func (v *viewerView) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		v.wake(u)
		if v.dragging {
			z := v.z.Value()
			r := v.fitRect(v.img)
			w, h := r.Size().W*z, r.Size().H*z
			if w > 0 && h > 0 {
				c := geom.Pt(v.dragC.X-(e.Pos.X-v.dragAt.X)/w, v.dragC.Y-(e.Pos.Y-v.dragAt.Y)/h)
				v.c.Jump(v.clampC(c, z))
				u.Invalidate()
			}
			return true
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		u.Focus(v)
		if e.Clicks%2 == 0 {
			v.toggleFit(e.Pos, u)
			return true
		}
		v.dragging, v.dragAt, v.dragC = true, e.Pos, v.c.Value()
		return true
	case input.PointerUp:
		if v.dragging {
			v.dragging = false
			return true
		}
	case input.Scroll:
		dy := e.Delta.Y
		if e.Notches.Y != 0 {
			dy = e.Notches.Y
		} else {
			dy /= 40
		}
		if dy != 0 {
			v.zoomTo(v.z.Target()*float32(math.Pow(1.15, float64(dy))), e.Pos, u)
			return true
		}
	case input.KeyPress:
		mid := geom.Pt(v.box.W/2, v.box.H/2)
		if e.Mods.Has(input.ModControl) {
			if e.Key == input.KeyN {
				u.Send(v, ViewerClose{})
				return true
			}
			return false
		}
		if d := plusMinus(e); d != 0 {
			k := float32(1.25)
			if d < 0 {
				k = 0.8
			}
			v.zoomTo(v.z.Target()*k, mid, u)
			return true
		}
		switch e.Key {
		case input.KeyF11:
			u.SetFullScreen(!u.FullScreen())
			return true
		case input.KeyEscape:
			if u.FullScreen() {
				u.SetFullScreen(false)
				return true
			}
		case input.KeyZ, input.KeySpace:
			v.toggleFit(mid, u)
			return true
		case input.KeyP:
			u.Send(v, v.setPinned(!v.pinned, u))
			return true
		}
	}
	return false
}

// toggleFit goes between fitting the photo and 1:1 about at.
func (v *viewerView) toggleFit(at geom.Point, u *gunim.UI) {
	if v.z.Target() > 1.01 {
		v.z.Animate(1, widget.Quick.Get(u.Theme()))
		v.c.Animate(geom.Pt(0.5, 0.5), widget.Quick.Get(u.Theme()))
		u.Invalidate()
		return
	}
	v.zoomTo(v.oneToOne(), at, u)
}

// Paint implements [gunim.Node].
func (v *viewerView) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(backdrop))
	z, c := v.z.Value(), v.c.Value()
	t := min(max(v.mix.Value(), 0), 1)
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})()
		if v.prev != nil && t < 1 {
			p.Image(v.prev, v.shown(v.prev, z, c), paint.ImageOpts{Opacity: 1 - t*t*t})
		}
		if v.img != nil {
			p.Image(v.img, v.shown(v.img, z, c), paint.ImageOpts{Opacity: t})
		}
	}()
	if t >= 1 {
		v.prev = nil
	}
	if k := v.chrome.Value(); k > 0.01 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(k, 1)})()
			ctrls := kids.At(0)
			r := geom.Rect{Min: geom.Pt(box.W-ctrls.Size().W-14, v.top+10)}
			r.Max = r.Min.Add(ctrls.Size().Point())
			paintGlass(p, r, 10)
			ctrls.Paint(p)
			kids.At(1).Paint(p)
		}()
	}
}
