package watermark

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Text below this em size is unreadable noise; both renderers clamp so a 4%
// element on a tiny export never becomes a zero-size face.
const minTextPx = 4

// drawText composites one single-line text element. Placement uses the ink
// bounding box (font.BoundString), matching the preview's
// measureText(actualBoundingBox*) — the visible glyphs, not the em box, sit
// flush against the anchored margins.
func drawText(dst *image.RGBA, el Element, shortEdge int) error {
	if !hasText(el) {
		return nil
	}
	f, err := Font(el.Font)
	if err != nil {
		return err
	}
	px := math.Max(minTextPx, math.Round(el.SizePct/100*float64(shortEdge)))
	// Faces are not safe for concurrent use and export runs a worker per
	// core — a fresh Face per element is the cheap, correct choice (the
	// expensive parse is cached in Font).
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    px,
		DPI:     72, // Size is then in px, same unit as CSS font-size
		Hinting: font.HintingNone,
	})
	if err != nil {
		return err
	}
	defer face.Close()

	bounds, _ := font.BoundString(face, el.Text)
	w := (bounds.Max.X - bounds.Min.X).Ceil()
	h := (bounds.Max.Y - bounds.Min.Y).Ceil()
	if w <= 0 || h <= 0 {
		return nil
	}
	origin := anchorOrigin(dst.Bounds(), w, h, el.Anchor, sizePx(el.MarginPct, shortEdge))

	c := el.Color
	c.A = alpha8(el.Opacity)
	d := font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(c),
		Face: face,
		// Shift the pen so the ink box's top-left lands exactly on origin.
		Dot: fixed.Point26_6{
			X: fixed.I(origin.X) - bounds.Min.X,
			Y: fixed.I(origin.Y) - bounds.Min.Y,
		},
	}
	d.DrawString(el.Text)
	return nil
}

// ParseHexColor reads "#rrggbb" (the only form the client writes); anything
// else falls back to white, the sensible default over photographs.
func ParseHexColor(s string) color.NRGBA {
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	if len(s) != 7 || s[0] != '#' {
		return white
	}
	var v [3]uint8
	for i := 0; i < 3; i++ {
		hi, okHi := hexNibble(s[1+2*i])
		lo, okLo := hexNibble(s[2+2*i])
		if !okHi || !okLo {
			return white
		}
		v[i] = hi<<4 | lo
	}
	return color.NRGBA{R: v[0], G: v[1], B: v[2], A: 0xff}
}

func hexNibble(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
