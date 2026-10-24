package main

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The render chip's inks: its bar's track, and its fill in the accent.
var (
	renderTrack = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x1a}
	renderFill  = color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff}
	renderTitle = theme.Length("marraw.render.title", 13.5)
	renderSub   = theme.Length("marraw.render.sub", 11)
)

// renderChip says the photo showing is being rendered at full resolution,
// and how far it has got, as marraw's chip at the foot of the photo.
type renderChip struct {
	spin       *widget.Icon
	title, sub *widget.Label
	// frac is how far the render has got, below nought before the backend
	// says: the bar waits empty. textX is where the words start, and the
	// bar under them.
	frac  float64
	textX float32
}

func newRenderChip() *renderChip {
	c := &renderChip{spin: widget.NewIcon(icon.LoaderCircle, "Rendering"), title: widget.NewLabel("Rendering full resolution"),
		sub: widget.NewLabel(""), frac: -1}
	c.spin.Spin = true
	c.title.Size, c.title.MaxLines = renderTitle, 1
	c.sub.Face, c.sub.Size, c.sub.Color, c.sub.MaxLines = widget.MonoFont, renderSub, noteInk, 1
	c.set(-1)
	return c
}

// set shows how far the render has got.
func (c *renderChip) set(frac float64) {
	c.frac = frac
	c.sub.Text = "1:1 tile · " + renderStage(frac)
}

// renderStage names what the backend does at frac of a render, as its
// budget goes: the RAW's decode to 0.70, the look and masks to 0.90, the
// tiles' writing after. Before it says, the decode.
func renderStage(frac float64) string {
	switch {
	case frac < 0.72:
		return "decoding RAW"
	case frac < 0.92:
		return "developing"
	}
	return "writing"
}

// Children implements [gunim.Composite].
func (c *renderChip) Children() []gunim.Node { return []gunim.Node{c.spin, c.title, c.sub} }

// renderChipW is the chip's width, as marraw's.
const renderChipW = 252

// Layout implements [gunim.Node]: the spinner at the left, the title over
// the stage, and the bar beneath them.
func (c *renderChip) Layout(_ gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const padX, padY, gap = 18, 13, 12
	ss := kids.At(0).Layout(gunim.Loose(geom.Sz(19, 19)))
	tw := float32(renderChipW - 2*padX - ss.W - gap)
	ts := kids.At(1).Layout(gunim.Loose(geom.Sz(tw, 40)))
	us := kids.At(2).Layout(gunim.Loose(geom.Sz(tw, 40)))
	h := padY + ts.H + 4 + us.H + 4 + 3 + padY
	kids.At(0).Place(geom.Pt(padX, (h-ss.H)/2))
	x := padX + ss.W + gap
	c.textX = x
	kids.At(1).Place(geom.Pt(x, padY))
	kids.At(2).Place(geom.Pt(x, padY+ts.H+4))
	return geom.Sz(renderChipW, h)
}

// Paint implements [gunim.Node].
func (c *renderChip) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	paintGlass(p, r, 12)
	for i := range 3 {
		kids.At(i).Paint(p)
	}
	x := c.textX
	bar := geom.Rc(x, box.H-13-3, box.W-18-x, 3)
	p.RRect(bar, 1.5, paint.Solid(renderTrack))
	if c.frac >= 0 {
		p.RRect(geom.Rc(bar.Min.X, bar.Min.Y, bar.Size().W*float32(min(c.frac, 1)), 3), 1.5, paint.Solid(renderFill))
	}
}
