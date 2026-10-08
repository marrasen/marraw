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
	glassFill   = color.NRGBA{R: 12, G: 14, B: 18, A: 0xae}
	glassEdge   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x1f}
	glassShadow = paint.Shadow{Offset: geom.Pt(0, 14), Blur: 28, Color: color.NRGBA{A: 0x90}}
	glassBlur   = float32(18)
)

// paintGlass draws a card of glass in r, its corners rounded by radius.
func paintGlass(p *paint.Painter, r geom.Rect, radius float32) {
	p.ShadowRRect(r, radius, paint.Fill{}, glassShadow)
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Backdrop: glassBlur, Clip: true, Radius: radius})()
		p.RRect(r, radius, paint.Solid(glassFill))
	}()
	p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: glassEdge})
}
