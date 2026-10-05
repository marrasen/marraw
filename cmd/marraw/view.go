package main

import (
	"context"
	"fmt"
	"image/color"
	"image/png"
	"os"

	"github.com/marrasen/gunim"
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

// cullView shows one photo fitted to the window, on black, with where it
// is in the folder and which rendition shows, and steps through the folder
// with the keys.
type cullView struct {
	img    *paint.Image
	aspect float32
	name   *widget.Label
	note   *widget.Label
	hud    gunim.Node
}

func newCullView(Cull) *cullView {
	v := &cullView{name: widget.NewLabel(""), note: widget.NewLabel(""), aspect: 1.5}
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
	v.img, v.aspect = s.Img, s.Aspect
	v.name.SetText(fmt.Sprintf("%s   %d / %d", s.Name, s.Index+1, s.Total))
	v.note.SetText(s.Note)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (v *cullView) Children() []gunim.Node { return []gunim.Node{v.hud} }

// Focusable implements [gunim.Focusable]: the keys step through the folder.
func (v *cullView) Focusable() bool { return true }

// Handle implements [gunim.Handler].
func (v *cullView) Handle(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !ok {
		return false
	}
	switch k.Key {
	case input.KeyRight, input.KeyDown, input.KeySpace:
		u.Send(v, Step{By: 1})
	case input.KeyLeft, input.KeyUp, input.KeyBackspace:
		u.Send(v, Step{By: -1})
	case input.KeyHome:
		u.Send(v, Jump{To: 0})
	case input.KeyEnd:
		u.Send(v, Jump{To: -1})
	case input.KeyEscape:
		u.Send(v, Quit{})
	default:
		return false
	}
	return true
}

// Layout implements [gunim.Node]: the readout in the lower left corner.
func (v *cullView) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	k := kids.At(0)
	sz := k.Layout(gunim.Loose(box))
	k.Place(geom.Pt(0, box.H-sz.H))
	return box
}

// Paint implements [gunim.Node]: the photo, as large as fits, centred, or
// its frame until pixels come.
func (v *cullView) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(color.NRGBA{R: 0x0b, G: 0x0c, B: 0x0f, A: 0xff}))
	aspect := v.aspect
	if v.img != nil {
		w, h := v.img.Size()
		if h > 0 {
			aspect = float32(w) / float32(h)
		}
	}
	room := geom.Rect{Max: box.Point()}.Inset(geom.Uniform(16))
	rs := room.Size()
	w, h := rs.W, rs.W/aspect
	if h > rs.H {
		w, h = rs.H*aspect, rs.H
	}
	r := geom.Rc(room.Min.X+(rs.W-w)/2, room.Min.Y+(rs.H-h)/2, w, h)
	if v.img == nil {
		p.RRect(r, 2, paint.Solid(color.NRGBA{R: 0x1a, G: 0x1c, B: 0x22, A: 0xff}))
	} else {
		p.Image(v.img, r, paint.ImageOpts{Opacity: 1})
	}
	hud := kids.At(0)
	hs := hud.Size()
	p.RRect(geom.Rc(0, box.H-hs.H, hs.W, hs.H), 0, paint.Solid(color.NRGBA{A: 0xa0}))
	hud.Paint(p)
}

// keyRight is the Right key pressed, for the script's steps.
func keyRight() input.Event { return input.KeyPress{Key: input.KeyRight} }

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
