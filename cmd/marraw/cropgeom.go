package main

import (
	"math"
	"strconv"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The crop's geometry, ported from marraw's crop.ts, which mirrors the
// backend: the crop rectangle is in fractions of the frame after the
// quarter turns and the mirror, before the straighten, which turns the
// frame about its middle.

// cropRect is a crop rectangle, in fractions of the frame.
type cropRect struct{ X, Y, W, H float64 }

// cropMin is the smallest a crop's side may be, a fraction of the frame's.
const cropMin = 0.05

// coverEps lets a corner lie a hair outside the turned frame: the fitting
// leaves corners right on its edge.
const coverEps = 1e-4

// rectOf is the crop of p, the whole frame where it has none.
func rectOf(p marrawclient.Params) cropRect {
	if p.CropW > 0 && p.CropH > 0 {
		return cropRect{p.CropX, p.CropY, p.CropW, p.CropH}
	}
	return cropRect{0, 0, 1, 1}
}

// setRect makes r p's crop.
func setRect(p *marrawclient.Params, r cropRect) {
	p.CropX, p.CropY, p.CropW, p.CropH = q4(r.X), q4(r.Y), q4(r.W), q4(r.H)
}

// q4 rounds a fraction to 1e-4, as marraw does, so float noise never
// changes an edit.
func q4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// turns is p's quarter turns clockwise, 0 to 3.
func turns(p marrawclient.Params) int { return ((p.Rotate % 4) + 4) % 4 }

// covered reports whether the point x, y of the frame still has the
// photo under it once the frame is turned by angle degrees; aspect is
// the frame's width over its height.
func covered(x, y, angle, aspect float64) bool {
	if angle == 0 {
		return true
	}
	rad := -angle * math.Pi / 180
	c, s := math.Cos(rad), math.Sin(rad)
	cx, cy := aspect/2, 0.5
	dx, dy := x*aspect-cx, y-cy
	sx, sy := cx+dx*c-dy*s, cy+dx*s+dy*c
	return sx >= -coverEps && sx <= aspect+coverEps && sy >= -coverEps && sy <= 1+coverEps
}

// cornersCovered reports whether all r is over the photo, turned.
func cornersCovered(r cropRect, angle, aspect float64) bool {
	return covered(r.X, r.Y, angle, aspect) && covered(r.X+r.W, r.Y, angle, aspect) &&
		covered(r.X, r.Y+r.H, angle, aspect) && covered(r.X+r.W, r.Y+r.H, angle, aspect)
}

// fitToTurn shrinks r about its middle, its shape kept, to the largest
// that is all over the photo once turned by angle.
func fitToTurn(r cropRect, angle, aspect float64) cropRect {
	if angle == 0 || cornersCovered(r, angle, aspect) {
		return r
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	if !covered(cx, cy, angle, aspect) {
		cx, cy = 0.5, 0.5
	}
	lo, hi := 0.0, 1.0
	for range 24 {
		f := (lo + hi) / 2
		w, h := r.W*f, r.H*f
		if cornersCovered(cropRect{cx - w/2, cy - h/2, w, h}, angle, aspect) {
			lo = f
		} else {
			hi = f
		}
	}
	w, h := r.W*lo, r.H*lo
	return cropRect{cx - w/2, cy - h/2, w, h}
}

// maxCovered is the largest t from 0 to 1 that make(t) is all over the
// photo for, make(0) being so.
func maxCovered(make func(t float64) cropRect, angle, aspect float64) float64 {
	if cornersCovered(make(1), angle, aspect) {
		return 1
	}
	lo, hi := 0.0, 1.0
	for range 20 {
		t := (lo + hi) / 2
		if cornersCovered(make(t), angle, aspect) {
			lo = t
		} else {
			hi = t
		}
	}
	return lo
}

// slideMove moves from toward to as far as each way allows in turn, so a
// move along a turned frame's edge slides along it.
func slideMove(from, to cropRect, angle, aspect float64) cropRect {
	if !cornersCovered(from, angle, aspect) {
		return fitToTurn(from, angle, aspect)
	}
	dx, dy := to.X-from.X, to.Y-from.Y
	tx := maxCovered(func(t float64) cropRect { r := from; r.X += dx * t; return r }, angle, aspect)
	after := from
	after.X += dx * tx
	ty := maxCovered(func(t float64) cropRect { r := after; r.Y += dy * t; return r }, angle, aspect)
	after.Y += dy * ty
	return after
}

func clamp01(v float64) float64 { return min(1, max(0, v)) }

// grip is the part of the crop a drag takes: an edge or a corner, by the
// compass, or "move" for the whole.
type grip string

// dragRect is start dragged by dx, dy as grip says, keeping its sides no
// smaller than cropMin, the shape ratio, in fractions, where ratio is
// set, and within the frame.
func dragRect(start cropRect, g grip, dx, dy, ratio float64) cropRect {
	r := start
	if g == "move" {
		r.X = clamp01(min(start.X+dx, 1-start.W))
		r.Y = clamp01(min(start.Y+dy, 1-start.H))
	} else {
		left, top, right, bottom := start.X, start.Y, start.X+start.W, start.Y+start.H
		has := func(c byte) bool {
			for i := range len(g) {
				if g[i] == c {
					return true
				}
			}
			return false
		}
		if has('w') {
			left = clamp01(min(start.X+dx, right-cropMin))
		}
		if has('e') {
			right = clamp01(max(right+dx, left+cropMin))
		}
		if has('n') {
			top = clamp01(min(start.Y+dy, bottom-cropMin))
		}
		if has('s') {
			bottom = clamp01(max(bottom+dy, top+cropMin))
		}
		r = cropRect{left, top, right - left, bottom - top}
		if ratio > 0 {
			// Width for a side grip, height for top and bottom, and the
			// edge across from the one dragged stays.
			anchorRight := g == "w" || g == "nw" || g == "sw"
			anchorBottom := g == "n" || g == "nw" || g == "ne"
			x, y, w, h := r.X, r.Y, r.W, r.H
			if g == "n" || g == "s" {
				nw := h * ratio
				x, w = clamp01(x+w/2-nw/2), nw
			} else {
				nh := w / ratio
				if anchorBottom {
					y = y + h - nh
				}
				h = nh
			}
			if anchorRight {
				x = r.X + r.W - w
			}
			r = cropRect{x, y, w, h}
		}
	}
	r.W = min(r.W, 1-max(0, r.X))
	r.H = min(r.H, 1-max(0, r.Y))
	r.X, r.Y = clamp01(r.X), clamp01(r.Y)
	return r
}

// dragCovered is dragRect kept all over the photo turned by angle: a move
// slides along the turned edge, a resize stops short of it.
func dragCovered(start cropRect, g grip, dx, dy, ratio, angle, aspect float64) cropRect {
	r := dragRect(start, g, dx, dy, ratio)
	if angle == 0 || cornersCovered(r, angle, aspect) {
		return r
	}
	if g == "move" {
		return slideMove(dragRect(start, g, 0, 0, ratio), r, angle, aspect)
	}
	t := maxCovered(func(k float64) cropRect { return dragRect(start, g, k*dx, k*dy, ratio) }, angle, aspect)
	return dragRect(start, g, t*dx, t*dy, ratio)
}

// turnCrop turns p's frame a quarter, clockwise or not, as it shows: under
// a mirror the stored turn runs the other way, and the crop follows the
// same pixels.
func turnCrop(p marrawclient.Params, cw bool) marrawclient.Params {
	step := 1
	if p.FlipH {
		step = 3
	}
	if !cw {
		step = 4 - step
	}
	out := p
	out.Rotate = (turns(p) + step) % 4
	if p.CropW > 0 && p.CropH > 0 {
		if cw {
			out.CropX, out.CropY = 1-(p.CropY+p.CropH), p.CropX
		} else {
			out.CropX, out.CropY = p.CropY, 1-(p.CropX+p.CropW)
		}
		out.CropW, out.CropH = p.CropH, p.CropW
	}
	// The masks keep to what they cover: a clockwise turn takes x, y to
	// 1-y, x.
	if cw {
		out.Masks = remapMasks(p.Masks, func(x, y float64) (float64, float64) { return 1 - y, x }, true)
	} else {
		out.Masks = remapMasks(p.Masks, func(x, y float64) (float64, float64) { return y, 1 - x }, true)
	}
	return out
}

// flipCrop mirrors p's frame across, or upside down: both toggle the
// mirror, upside down with a half turn, and the crop and the straighten
// mirror with it.
func flipCrop(p marrawclient.Params, vertical bool) marrawclient.Params {
	out := p
	out.FlipH = !p.FlipH
	if vertical {
		out.Rotate = (turns(p) + 2) % 4
	}
	if p.CropW > 0 && p.CropH > 0 {
		if vertical {
			out.CropY = 1 - (p.CropY + p.CropH)
		} else {
			out.CropX = 1 - (p.CropX + p.CropW)
		}
	}
	out.CropAngle = -p.CropAngle
	if vertical {
		out.Masks = remapMasks(p.Masks, func(x, y float64) (float64, float64) { return x, 1 - y }, false)
	} else {
		out.Masks = remapMasks(p.Masks, func(x, y float64) (float64, float64) { return 1 - x, y }, false)
	}
	return out
}

// aspectChoices are the crop's shapes, as marraw's bar offers them; nought
// is free, and -1 the frame's own.
var aspectChoices = []struct {
	label string
	ratio float64
}{{"Free", 0}, {"Original", -1}, {"1:1", 1}, {"3:2", 1.5}, {"4:3", 4.0 / 3}, {"16:9", 16.0 / 9}}

// ratioFrac is ratio, width over height as the photo shows, in fractions
// of a frame of aspect, or nought for free.
func ratioFrac(ratio, aspect float64) float64 {
	switch {
	case ratio == 0:
		return 0
	case ratio < 0:
		return 1
	}
	return ratio / aspect
}

// centredRect is the largest rectangle of ratio, in fractions, in the
// middle of the frame, fitted to the straighten.
func centredRect(rf, angle, aspect float64) cropRect {
	w, h := 1.0, 1.0
	if rf > 0 {
		if rf >= 1 {
			h = 1 / rf
		} else {
			w = rf
		}
	}
	return fitToTurn(cropRect{(1 - w) / 2, (1 - h) / 2, w, h}, angle, aspect)
}

// ratioName is a crop's shape as it reads: a familiar ratio where it is
// within a hundredth of one, or its value.
func ratioName(r float64) string {
	for _, n := range []struct {
		name string
		v    float64
	}{{"1:1", 1}, {"3:2", 1.5}, {"2:3", 2.0 / 3}, {"4:5", 0.8}, {"5:4", 1.25}, {"4:3", 4.0 / 3}, {"3:4", 0.75}, {"16:9", 16.0 / 9}, {"9:16", 9.0 / 16}} {
		if math.Abs(r-n.v)/n.v < 0.01 {
			return n.name
		}
	}
	return fmtFloat(r)
}

func fmtFloat(v float64) string { return trimZeros(math.Round(v*100) / 100) }

func trimZeros(v float64) string {
	s := []byte(fmtF(v))
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return string(s)
}

func fmtF(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
