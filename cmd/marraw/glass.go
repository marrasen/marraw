package main

import (
	"image/color"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// The floating chrome's glass, as marraw's: a dark card, partly clear,
// that blurs what is behind it, with a hairline edge and a deep, soft
// shadow.
var (
	glassFill   = color.NRGBA{R: 12, G: 14, B: 18, A: 0x73}
	glassEdge   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x1f}
	glassShadow = paint.Shadow{Offset: geom.Pt(0, 24), Blur: 42, Spread: -18, Color: color.NRGBA{A: 0xb3}, Outside: true}
	glassBlur   = float32(18)
)

// glassShadowFor is the shadow under glass h tall: glassShadow's deep
// one under a card, a short, close one under a chip or a switch, so it
// stays by its own control rather than falling on the next.
func glassShadowFor(h float32) paint.Shadow {
	k := min(max((h-32)/(160-32), 0), 1)
	lerp := func(a, b float32) float32 { return a + (b-a)*k }
	s := glassShadow
	s.Offset = geom.Pt(0, lerp(2, glassShadow.Offset.Y))
	s.Blur = lerp(6, glassShadow.Blur)
	s.Spread = lerp(-1, glassShadow.Spread)
	s.Color.A = uint8(lerp(0x66, float32(glassShadow.Color.A)))
	return s
}

// paintGlass draws a card of glass in r, its corners rounded by radius.
func paintGlass(p *paint.Painter, r geom.Rect, radius float32) {
	p.ShadowRRect(r, radius, paint.Fill{}, glassShadowFor(r.Size().H))
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Backdrop: glassBlur, Clip: true, Radius: radius})()
		p.RRect(r, radius, paint.Solid(glassFill))
	}()
	p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: glassEdge})
}
