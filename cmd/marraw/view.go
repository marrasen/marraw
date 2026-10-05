package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// registerViews is the window half: the cull view.
func registerViews(w *gunim.Window) {
	w.RegisterTheme(widget.Dark())
	gunim.RegisterView(w, "cull", newCullView, (*cullView).show)
}

// cullView shows one photo, fitted to the window or zoomed in, on black,
// with where it is in the folder and which rendition shows. The keys step
// through the folder. The wheel zooms about the pointer, a drag pans, and Z
// or a double click goes between fit and one image pixel a screen pixel;
// the zoom and the pan stay as the photo changes, to compare a burst. Past
// what the 2048 shows sharp, it asks for the full-resolution tiles in view.
type cullView struct {
	anim.Group
	st   Cull
	name *widget.Label
	note *widget.Label
	hud  gunim.Node

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
}

func newCullView(Cull) *cullView {
	v := &cullView{name: widget.NewLabel(""), note: widget.NewLabel(""), z: anim.NewFloat(1), c: anim.NewPoint(geom.Pt(0.5, 0.5)), scale: 1}
	v.Add(v.z, v.c)
	v.note.Color = noteInk
	v.note.Size = noteSize
	col := widget.Column(v.name, v.note)
	v.hud = widget.NewPad(col)
	return v
}

var (
	noteInk  = theme.Color("marraw.note", color.NRGBA{R: 0xa4, G: 0xab, B: 0xbb, A: 0xff})
	noteSize = theme.Length("marraw.note.size", 12.5)
)

func (v *cullView) show(s Cull, u *gunim.UI) {
	moved := s.Index != v.st.Index
	v.st = s
	v.name.SetText(fmt.Sprintf("%s   %d / %d   %.0f%%", s.Name, s.Index+1, s.Total, v.percent(v.z.Target())))
	note := s.Note
	if s.TileNote != "" {
		note += "\n" + s.TileNote + fmt.Sprintf(" (%d in memory)", len(s.Tiles))
	}
	v.note.SetText(note)
	if moved {
		v.askTiles(u)
	}
	u.Invalidate()
}

// full is the photo's full resolution, or a guess from its frame.
func (v *cullView) full() geom.Size {
	if v.st.Full.X > 0 && v.st.Full.Y > 0 {
		return geom.Sz(float32(v.st.Full.X), float32(v.st.Full.Y))
	}
	return geom.Sz(3000*v.st.Aspect, 3000)
}

// room is where the photo goes at fit.
func (v *cullView) room() geom.Rect {
	return geom.Rect{Max: v.box.Point()}.Inset(geom.Uniform(16))
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
	v.name.SetText(fmt.Sprintf("%s   %d / %d   %.0f%%", v.st.Name, v.st.Index+1, v.st.Total, v.percent(z)))
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
func (v *cullView) Children() []gunim.Node { return []gunim.Node{v.hud} }

// Focusable implements [gunim.Focusable]: the keys step through the folder.
func (v *cullView) Focusable() bool { return true }

// Handle implements [gunim.Handler].
func (v *cullView) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.KeyPress:
		switch e.Key {
		case input.KeyRight, input.KeyDown, input.KeySpace:
			u.Send(v, Step{By: 1})
		case input.KeyLeft, input.KeyUp, input.KeyBackspace:
			u.Send(v, Step{By: -1})
		case input.KeyHome:
			u.Send(v, Jump{To: 0})
		case input.KeyEnd:
			u.Send(v, Jump{To: -1})
		case input.KeyZ:
			v.toggle(v.room().Center(), u)
		case input.Key0:
			v.zoomTo(1, v.room().Center(), u)
		case input.KeyEscape:
			u.Send(v, Quit{})
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

// Layout implements [gunim.Node]: the readout in the lower left corner.
func (v *cullView) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	v.box, v.scale = box, max(f.Scale, 0.1)
	k := kids.At(0)
	sz := k.Layout(gunim.Loose(box))
	k.Place(geom.Pt(0, box.H-sz.H))
	return box
}

// Paint implements [gunim.Node]: the photo at its zoom, the tiles fetched
// over it, or its frame until pixels come.
func (v *cullView) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(color.NRGBA{R: 0x0b, G: 0x0c, B: 0x0f, A: 0xff}))
	z, c := v.z.Value(), v.c.Value()
	full, s := v.full(), v.fit()*z
	o := v.origin(z, c)
	r := geom.Rc(o.X, o.Y, full.W*s, full.H*s)
	if v.st.Img == nil {
		p.RRect(r, 2, paint.Solid(color.NRGBA{R: 0x1a, G: 0x1c, B: 0x22, A: 0xff}))
	} else {
		p.Image(v.st.Img, r, paint.ImageOpts{Opacity: 1})
	}
	for t, img := range v.st.Tiles {
		w, h := img.Size()
		tr := geom.Rc(o.X+float32(t.X*tileSize)*s, o.Y+float32(t.Y*tileSize)*s, float32(w)*s, float32(h)*s)
		if tr.Max.X < 0 || tr.Max.Y < 0 || tr.Min.X > box.W || tr.Min.Y > box.H {
			continue
		}
		p.Image(img, tr, paint.ImageOpts{Opacity: 1})
	}
	hud := kids.At(0)
	hs := hud.Size()
	p.RRect(geom.Rc(0, box.H-hs.H, hs.W, hs.H), 0, paint.Solid(color.NRGBA{A: 0xa0}))
	hud.Paint(p)
}

// keyRight is the Right key pressed, for the script's steps, and keyZ the
// zoom key.
func keyRight() input.Event { return input.KeyPress{Key: input.KeyRight} }
func keyZ() input.Event     { return input.KeyPress{Key: input.KeyZ} }

// writeShot writes what the window shows to a PNG file.
func writeShot(ctx context.Context, c gunim.Client, path string) error {
	img, err := c.Shot(ctx)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
