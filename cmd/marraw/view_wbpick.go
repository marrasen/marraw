package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The eyedropper's magnifier, as marraw's: how wide it is, how much it
// magnifies the photo as it shows, how far apart its grid's lines are,
// and how many of the sampled frame's pixels the backend averages for a
// pick.
const (
	loupeSize   = 138
	loupeZoom   = 3
	loupeGrid   = 14
	loupeSample = 7
	// pickFloor is the least a channel of the frame's 8-bit pixels needs
	// for a pick, about the backend's own floor in linear light.
	pickFloor = 13
)

// The readout's type, small and of even widths, and the warning's ink.
var (
	readoutSize = theme.Length("marraw.readout.size", 11)
	readoutInk  = theme.Color("marraw.readout", color.NRGBA{R: 0xc4, G: 0xc7, B: 0xcc, A: 0xff})
	warnInk     = theme.Color("marraw.warn", color.NRGBA{R: 0xff, G: 0x8a, B: 0x80, A: 0xff})
	hintSize    = theme.Length("marraw.hint.size", 11.5)
)

// newWBBar is the eyedropper's bar, as marraw's: the pipette and what to
// do, the white balances to have instead and a reset, and the ways out,
// Done the one filled. Its buttons are smaller than gunim's own.
func (v *cullView) newWBBar() gunim.Node {
	btn := func(label, act string, quiet bool) *widget.Button {
		b := widget.NewButton(label)
		b.KeepFocus, b.Ghost = true, act != "done"
		if quiet {
			b.Ink = noteInk
		}
		b.OnClick = widget.Sends(DevWBBar{Act: act})
		return b
	}
	done := btn("Done", "done", false)
	done.Kind = widget.ButtonPrimary
	pip := widget.NewIcon(icon.Pipette, "Eyedropper")
	pip.Size, pip.Color = theme.Length("marraw.bar.icon", 15), widget.Accent
	v.labelWB.Text = "Click a neutral grey"
	v.labelWB.Size, v.labelWB.Color = hintSize, noteInk
	hint := widget.Row(pip, v.labelWB)
	hint.Cross = widget.CrossCenter
	row := widget.Row(hint, &divider{}, btn("As shot", "asShot", true), btn("Auto", "auto", true),
		btn("Reset", "reset", true), &divider{}, btn("Cancel", "cancel", false), done)
	row.Cross = widget.CrossCenter
	th := marrawTheme().With(theme.Set(widget.ButtonHeight, 28), theme.Set(widget.ButtonPadding, 11),
		theme.Set(widget.ButtonRadius, 7), theme.Set(widget.TextSize, 12.5))
	return widget.NewThemed(row, th)
}

// divider is a hairline between a bar's groups.
type divider struct{ _ int }

// Layout implements [gunim.Node].
func (*divider) Layout(gunim.Constraints, gunim.Frame, gunim.Children) geom.Size {
	return geom.Sz(1, 26)
}

// Paint implements [gunim.Node].
func (*divider) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x26}))
}

// wbHover follows the pointer over the photo while the eyedropper is out,
// for the magnifier and what it reads.
func (v *cullView) wbHover(p geom.Point) {
	v.wbAt = p
	at, ok := v.photoPoint(p)
	v.wbOver = v.st.WBPick && ok && !v.stripRect.Contains(p) && !v.inPanel(p) && !v.wbBarRect.Contains(p)
	if !v.wbOver || v.st.WBPix == nil {
		v.wbRead.Text, v.wbWarn.Text = "", ""
		return
	}
	c := sampleAt(v.st.WBPix, at, loupeSample)
	v.wbRead.Text = fmt.Sprintf("R%d G%d B%d", c.R, c.G, c.B)
	v.wbWarn.Text = tooLittle(c)
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
	return "No " + strings.Join(none, " or ") + " light: can't be picked"
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
// and the magnifier in place of the pointer, as marraw's: the photo there
// three times as large, a faint grid, the target in the middle and the
// pipette at its edge, and a tag of what it reads beside it, warning when
// the spot cannot be picked.
func (v *cullView) paintWB(p *paint.Painter, th *theme.Live, bar, read, warn gunim.Child) {
	k := v.wbIn.Value()
	if k < 0.01 {
		return
	}
	func() {
		r := v.wbBarRect
		defer p.Push(paint.Translate(geom.Pt(0, (1-k)*24)))()
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-48)), Opacity: k})()
		paintGlass(p, r, 13)
		bar.Paint(p)
	}()
	if !v.wbOver || v.st.WBFrame == nil || v.st.WBPix == nil {
		return
	}
	at, _ := v.photoPoint(v.wbAt)
	b := v.st.WBPix.Bounds()
	// The frame's pixels across a pixel of the photo as it shows, so the
	// magnifier shows it loupeZoom times as large.
	shown := v.full().W * v.fit() * v.z.Value()
	per := float32(b.Dx()) / max(shown, 1)
	span := loupeSize / loupeZoom * per
	fx, fy := at.X*float32(b.Dx()-1), at.Y*float32(b.Dy()-1)
	src := geom.Rc(fx-span/2, fy-span/2, span, span)
	c := v.wbAt
	r := geom.Rc(c.X-loupeSize/2, c.Y-loupeSize/2, loupeSize, loupeSize)
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Insets{Left: -40, Top: -40, Bottom: -60, Right: -260}), Opacity: k})()
	p.ShadowRRect(r, loupeSize/2, paint.Solid(color.NRGBA{A: 0xff}),
		paint.Shadow{Offset: geom.Pt(0, 12), Blur: 26, Color: color.NRGBA{A: 0xb0}})
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: loupeSize / 2})()
		p.Image(v.st.WBFrame, r, paint.ImageOpts{Src: src, Opacity: 1})
		grid := color.NRGBA{A: 0x24}
		for d := float32(loupeGrid); d < loupeSize; d += loupeGrid {
			p.RRect(geom.Rc(r.Min.X+d, r.Min.Y, 1, loupeSize), 0, paint.Solid(grid))
			p.RRect(geom.Rc(r.Min.X, r.Min.Y+d, loupeSize, 1), 0, paint.Solid(grid))
		}
	}()
	p.RRectStroke(r, loupeSize/2, paint.Fill{}, paint.Stroke{Width: 2, Color: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}})
	sample := sampleAt(v.st.WBPix, at, loupeSample)
	why := tooLittle(sample)
	target := widget.Accent.Get(th)
	if why != "" {
		target = warnInk.Get(th)
	}
	t := geom.Rc(c.X-7, c.Y-7, 14, 14)
	p.RRectStroke(t.Inset(geom.Uniform(-1)), 1, paint.Fill{}, paint.Stroke{Width: 1, Color: color.NRGBA{A: 0xb0}})
	p.RRectStroke(t, 0, paint.Fill{}, paint.Stroke{Width: 1.5, Color: target})
	// The pipette at its lower right, on a shadow of itself.
	pr := geom.Rc(r.Min.X+loupeSize-20, r.Min.Y+loupeSize-22, 22, 22)
	p.Mask(icon.Stroke{Icon: icon.Pipette, Width: 1.6, Progress: 1}, pr.Add(geom.Pt(0, 2)), color.NRGBA{A: 0x99})
	p.Mask(icon.Stroke{Icon: icon.Pipette, Width: 1.6, Progress: 1}, pr, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	// The tag: the spot's colour and its channels, and why not, if not.
	rs, ws := read.Size(), warn.Size()
	w := 11 + 20 + 8 + rs.W + 11
	h := float32(34)
	if why != "" {
		w = max(w, 11+ws.W+11)
		h += ws.H + 4
	}
	tag := geom.Rc(r.Max.X+12, r.Min.Y+44, w, h)
	paintGlass(p, tag, 9)
	sw := geom.Rc(tag.Min.X+11, tag.Min.Y+7, 20, 20)
	edge := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x4c}
	if why != "" {
		edge = warnInk.Get(th)
	}
	p.RRectStroke(sw, 5, paint.Solid(sample), paint.Stroke{Width: 1, Color: edge})
	func() {
		defer p.Push(paint.Translate(geom.Pt(sw.Max.X+8, sw.Center().Y-rs.H/2)))()
		read.Paint(p)
	}()
	if why != "" {
		defer p.Push(paint.Translate(geom.Pt(tag.Min.X+11, tag.Min.Y+34)))()
		warn.Paint(p)
	}
}
