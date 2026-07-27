package watermark

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// drawRect composites one rect element: an anchored box filled with a solid
// color or a two-stop linear gradient. Cannot fail — geometry outside the
// canvas simply clips away.
func drawRect(dst *image.RGBA, el Element, shortEdge int) {
	b := dst.Bounds()
	w := max(1, int(math.Round(el.WidthPct/100*float64(b.Dx()))))
	h := sizePx(el.HeightPct, shortEdge)
	origin := anchorOrigin(b, w, h, el.Anchor, marginPx(el.MarginPct, shortEdge))
	rect := image.Rectangle{Min: origin, Max: origin.Add(image.Point{X: w, Y: h})}.Intersect(b)
	if rect.Empty() {
		return
	}
	if !el.Gradient {
		fill := color.NRGBA{R: el.Color.R, G: el.Color.G, B: el.Color.B, A: alpha8(el.Opacity)}
		draw.Draw(dst, rect, image.NewUniform(fill), image.Point{}, draw.Over)
		return
	}
	// Gradient: 1-px uniform strips perpendicular to the direction, each
	// lerped in straight (non-premultiplied) space between (Color, Opacity)
	// and (Color2, Opacity2) so a fade-to-transparent keeps its hue.
	vertical := el.GradientDir == GradientDown || el.GradientDir == GradientUp
	n := rect.Dy()
	if !vertical {
		n = rect.Dx()
	}
	for i := 0; i < n; i++ {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		if el.GradientDir == GradientUp || el.GradientDir == GradientLeft {
			t = 1 - t
		}
		strip := image.Rect(rect.Min.X+i, rect.Min.Y, rect.Min.X+i+1, rect.Max.Y)
		if vertical {
			strip = image.Rect(rect.Min.X, rect.Min.Y+i, rect.Max.X, rect.Min.Y+i+1)
		}
		fill := color.NRGBA{
			R: lerp8(el.Color.R, el.Color2.R, t),
			G: lerp8(el.Color.G, el.Color2.G, t),
			B: lerp8(el.Color.B, el.Color2.B, t),
			A: alpha8(clamp01(el.Opacity) + (clamp01(el.Opacity2)-clamp01(el.Opacity))*t),
		}
		draw.Draw(dst, strip, image.NewUniform(fill), image.Point{}, draw.Over)
	}
}

func lerp8(a, b uint8, t float64) uint8 {
	return uint8(math.Round(float64(a) + (float64(b)-float64(a))*t))
}
