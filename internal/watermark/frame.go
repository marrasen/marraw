package watermark

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// Frame is a border added around the photo — the canvas grows, no photo
// pixels are covered. Widths are % of the framed canvas short edge;
// BottomPct adds extra height below the photo (the polaroid chin).
type Frame struct {
	WidthPct  float64
	BottomPct float64
	Color     color.NRGBA
}

// FrameLayout is a solved frame geometry: scale the photo to PhotoW×PhotoH
// and compose it at (Border, Border) on a CanvasW×CanvasH canvas.
type FrameLayout struct {
	PhotoW, PhotoH   int
	CanvasW, CanvasH int
	Border, Chin     int
}

// Layout solves the framed geometry. With longEdge > 0 the framed canvas
// long edge equals longEdge exactly and the photo box shrinks to fit inside
// the borders; the photo is never upscaled — when longEdge is 0 or would
// need upscaling, the photo keeps its native dims and the canvas grows
// around it. Borders are fractions of the framed short edge, which depends
// on the borders themselves, so both cases solve in reals first and
// integerize after.
func (f Frame) Layout(photoW, photoH, longEdge int) FrameLayout {
	// Mirror the API bounds defensively: they keep every denominator below
	// (1-2b-c >= 0.40) safely positive.
	b := math.Min(0.15, math.Max(0, f.WidthPct/100))
	c := math.Min(0.30, math.Max(0, f.BottomPct/100))
	pw, ph := float64(photoW), float64(photoH)

	if longEdge > 0 && photoW > 0 && photoH > 0 {
		a := pw / ph
		L := float64(longEdge)
		// Landscape framed canvas: Wf = L is the long edge, solve Hf from the
		// photo-aspect constraint; if Hf comes out taller than L the framed
		// canvas is really portrait — solve Wf with Hf = L instead.
		if hf := L / (a*(1-2*b-c) + 2*b); hf <= L {
			sf := int(math.Round(hf))
			bd, ch := int(math.Round(b*float64(sf))), int(math.Round(c*float64(sf)))
			l := FrameLayout{
				PhotoW: longEdge - 2*bd, PhotoH: sf - 2*bd - ch,
				CanvasW: longEdge, CanvasH: sf, Border: bd, Chin: ch,
			}
			if l.PhotoW > 0 && l.PhotoH > 0 && l.PhotoW <= photoW && l.PhotoH <= photoH {
				return l
			}
		} else {
			wf := a * L / (1 - 2*b + a*(2*b+c))
			sf := int(math.Round(wf))
			bd, ch := int(math.Round(b*float64(sf))), int(math.Round(c*float64(sf)))
			l := FrameLayout{
				PhotoW: sf - 2*bd, PhotoH: longEdge - 2*bd - ch,
				CanvasW: sf, CanvasH: longEdge, Border: bd, Chin: ch,
			}
			if l.PhotoW > 0 && l.PhotoH > 0 && l.PhotoW <= photoW && l.PhotoH <= photoH {
				return l
			}
		}
		// Solved box would not shrink the photo — never upscale, grow instead.
	}

	// Full resolution: the photo keeps its native dims and the canvas grows.
	// Assume the framed height is the short edge; fall through when the
	// integerized result contradicts that.
	hf := ph / (1 - 2*b - c)
	bd, ch := int(math.Round(b*hf)), int(math.Round(c*hf))
	if photoH+2*bd+ch > photoW+2*bd {
		wf := pw / (1 - 2*b)
		bd, ch = int(math.Round(b*wf)), int(math.Round(c*wf))
	}
	return FrameLayout{
		PhotoW: photoW, PhotoH: photoH,
		CanvasW: photoW + 2*bd, CanvasH: photoH + 2*bd + ch,
		Border: bd, Chin: ch,
	}
}

// Compose returns a new canvas filled with the frame color, with the photo
// (already scaled to l.PhotoW×l.PhotoH) drawn inside the border.
func (f Frame) Compose(photo *image.RGBA, l FrameLayout) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, l.CanvasW, l.CanvasH))
	fill := color.NRGBA{R: f.Color.R, G: f.Color.G, B: f.Color.B, A: 0xff}
	draw.Draw(dst, dst.Bounds(), image.NewUniform(fill), image.Point{}, draw.Src)
	rect := image.Rect(l.Border, l.Border, l.Border+l.PhotoW, l.Border+l.PhotoH)
	draw.Draw(dst, rect, photo, photo.Bounds().Min, draw.Src)
	return dst
}
