package watermark

import "image"

// anchorOrigin returns the top-left corner for a w×h box placed inside
// canvas at the given anchor, inset by marginPx on each anchored edge.
// Centered axes ignore the margin. layoutBox in
// client/src/lib/watermarks.ts is the TypeScript twin — same integer
// division, keep in lockstep.
func anchorOrigin(canvas image.Rectangle, w, h int, a Anchor, marginPx int) image.Point {
	var x, y int
	switch a {
	case AnchorTopLeft, AnchorLeft, AnchorBottomLeft:
		x = canvas.Min.X + marginPx
	case AnchorTopRight, AnchorRight, AnchorBottomRight:
		x = canvas.Max.X - marginPx - w
	default: // top, center, bottom
		x = canvas.Min.X + (canvas.Dx()-w)/2
	}
	switch a {
	case AnchorTopLeft, AnchorTop, AnchorTopRight:
		y = canvas.Min.Y + marginPx
	case AnchorBottomLeft, AnchorBottom, AnchorBottomRight:
		y = canvas.Max.Y - marginPx - h
	default: // left, center, right
		y = canvas.Min.Y + (canvas.Dy()-h)/2
	}
	return image.Point{X: x, Y: y}
}
