package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// The eyedropper's magnifier: how wide it is on screen, how many of the
// sampled frame's pixels it shows across, and how many the backend
// averages for a pick, the square in its middle.
const (
	loupeSize   = 132
	loupeSpan   = 21
	loupeSample = 7
	// pickFloor is the least a channel of the frame's 8-bit pixels needs
	// for a pick, about the backend's own floor in linear light.
	pickFloor = 13
)

// newWBBar is the eyedropper's bar: what to do, the modes to have instead,
// a reset, and the ways out, keeping the pick or not.
func (v *cullView) newWBBar() gunim.Node {
	btn := func(label, act string) *widget.Button {
		b := widget.NewButton(label)
		b.KeepFocus = true
		b.OnClick = widget.Sends(DevWBBar{Act: act})
		return b
	}
	done := btn("Done", "done")
	done.Kind = widget.ButtonPrimary
	row := widget.Row(v.labelWB, btn("As shot", "asShot"), btn("Auto", "auto"), btn("Reset", "reset"),
		btn("Cancel", "cancel"), done)
	row.Cross = widget.CrossCenter
	return row
}

// wbHover follows the pointer over the photo while the eyedropper is out,
// for the magnifier and what it reads.
func (v *cullView) wbHover(p geom.Point) {
	v.wbAt = p
	at, ok := v.photoPoint(p)
	v.wbOver = v.st.WBPick && ok && p.Y < v.box.H-stripHeight && !v.inPanel(p) && !v.wbBarRect.Contains(p)
	if !v.wbOver || v.st.WBPix == nil {
		v.wbRead.Text = ""
		return
	}
	c := sampleAt(v.st.WBPix, at, loupeSample)
	text := fmt.Sprintf("R %d   G %d   B %d", c.R, c.G, c.B)
	if why := tooLittle(c); why != "" {
		text += "\n" + why
	}
	v.wbRead.Text = text
}

// tooLittle says what a spot of colour c lacks for a pick, or "".
func tooLittle(c color.NRGBA) string {
	var none []string
	for i, x := range []uint8{c.R, c.G, c.B} {
		if x < pickFloor {
			none = append(none, [3]string{"red", "green", "blue"}[i])
		}
	}
	switch {
	case len(none) == 0:
		return ""
	case len(none) == 3:
		return "Too dark to pick"
	}
	return "No " + strings.Join(none, " or ") + " here: no grey to find"
}

// sampleAt is the mean of the n by n pixels of m about at, 0 to 1 across
// it, as the backend samples a pick.
func sampleAt(m *image.RGBA, at geom.Point, n int) color.NRGBA {
	b := m.Bounds()
	cx := b.Min.X + int(at.X*float32(b.Dx()-1))
	cy := b.Min.Y + int(at.Y*float32(b.Dy()-1))
	var r, g, bl, k int
	for y := cy - n/2; y <= cy+n/2; y++ {
		for x := cx - n/2; x <= cx+n/2; x++ {
			if !(image.Point{x, y}).In(b) {
				continue
			}
			o := m.PixOffset(x, y)
			r, g, bl, k = r+int(m.Pix[o]), g+int(m.Pix[o+1]), bl+int(m.Pix[o+2]), k+1
		}
	}
	if k == 0 {
		return color.NRGBA{A: 0xff}
	}
	return color.NRGBA{R: uint8(r / k), G: uint8(g / k), B: uint8(bl / k), A: 0xff}
}

// paintWB draws the eyedropper's bar, rising in at the foot of the photo,
// and the magnifier under the pointer: the sampled frame's pixels there,
// large, the square the pick averages, and what it reads.
func (v *cullView) paintWB(p *paint.Painter, bar, readout gunim.Child) {
	k := v.wbIn.Value()
	if k < 0.01 {
		return
	}
	func() {
		r := v.wbBarRect
		defer p.Push(paint.Translate(geom.Pt(0, (1-k)*24)))()
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-24)), Opacity: k})()
		p.ShadowRRect(r, r.Size().H/2, paint.Solid(color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xf0}),
			paint.Shadow{Offset: geom.Pt(0, 3), Blur: 14, Color: color.NRGBA{A: 0x80}})
		bar.Paint(p)
	}()
	if !v.wbOver || v.st.WBFrame == nil || v.st.WBPix == nil {
		return
	}
	at, _ := v.photoPoint(v.wbAt)
	b := v.st.WBPix.Bounds()
	fx, fy := at.X*float32(b.Dx()-1), at.Y*float32(b.Dy()-1)
	src := geom.Rc(fx-loupeSpan/2, fy-loupeSpan/2, loupeSpan, loupeSpan)
	c := v.wbAt
	r := geom.Rc(c.X-loupeSize/2, c.Y-loupeSize/2, loupeSize, loupeSize)
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-120)), Opacity: k})()
	p.ShadowRRect(r, loupeSize/2, paint.Solid(color.NRGBA{A: 0xff}), paint.Shadow{Offset: geom.Pt(0, 4), Blur: 16, Color: color.NRGBA{A: 0x90}})
	p.Image(v.st.WBFrame, r, paint.ImageOpts{Src: src, Radius: loupeSize / 2, Opacity: 1})
	sq := float32(loupeSize) * loupeSample / loupeSpan
	sample := sampleAt(v.st.WBPix, at, loupeSample)
	ring := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xe0}
	if tooLittle(sample) != "" {
		ring = rejectInk
	}
	p.RRectStroke(geom.Rc(c.X-sq/2, c.Y-sq/2, sq, sq), 3, paint.Fill{}, paint.Stroke{Width: 1.5, Color: ring})
	p.RRectStroke(r, loupeSize/2, paint.Fill{}, paint.Stroke{Width: 2, Color: ring})
	// What it reads, beside it: the spot's colour and its channels.
	rs := readout.Size()
	pill := geom.Rc(r.Max.X+10, c.Y-rs.H/2-8, rs.W+46, rs.H+16)
	p.RRect(pill, 10, paint.Solid(color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xf0}))
	p.RRectStroke(geom.Rc(pill.Min.X+10, c.Y-9, 18, 18), 4, paint.Solid(sample), paint.Stroke{Width: 1, Color: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x60}})
	func() {
		defer p.Push(paint.Translate(geom.Pt(pill.Min.X+36, pill.Min.Y+8)))()
		readout.Paint(p)
	}()
}
