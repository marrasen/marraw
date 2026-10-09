package main

import (
	"image/color"
	"math"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// maskTintInk is a mask's tint over the photo, as marraw's.
var maskTintInk = color.NRGBA{R: 240, G: 64, B: 64, A: 0x66}

// maskUI is the cull view's mask editing: a drag of a handle under way,
// from the mask as it was, showing the mask as it goes; and the brush's
// stroke being painted.
type maskUI struct {
	dragging bool
	grip     string
	start    marrawclient.Mask
	from     geom.Point
	live     marrawclient.Mask
	hasLive  bool
	pointer  geom.Point
	over     bool
}

// maskOn reports whether a mask is being worked on, over the photo.
func (v *cullView) maskOn() bool {
	ms := v.st.Masks
	return ms != nil && ms.Selected >= 0 && ms.Selected < len(ms.Params.Masks) && v.st.Crop == nil && !v.st.WBPick
}

// theMask is the mask being worked on, as it shows: dragged, or the edit's.
func (v *cullView) theMask() marrawclient.Mask {
	if v.mask.hasLive {
		return v.mask.live
	}
	return v.st.Masks.Params.Masks[v.st.Masks.Selected]
}

// frameScale is how many screen pixels a pixel of the oriented frame
// takes, as the photo shows now.
func (v *cullView) frameScale() float64 {
	ms := v.st.Masks
	w := ms.Frame[0]
	if p := ms.Params; p.CropW > 0 {
		w *= p.CropW
	}
	if w <= 0 {
		return 1
	}
	return float64(v.photoRect().Size().W) / w
}

// toScreen is the point fx, fy of the oriented frame on screen.
func (v *cullView) toScreen(fx, fy float64) geom.Point {
	ms := v.st.Masks
	bx, by := shownFromFrame(fx, fy, ms.Params, ms.Frame[0], ms.Frame[1])
	pr := v.photoRect()
	return geom.Pt(pr.Min.X+float32(bx)*pr.Size().W, pr.Min.Y+float32(by)*pr.Size().H)
}

// toFrame is screen point p in the oriented frame.
func (v *cullView) toFrame(p geom.Point) (float64, float64) {
	ms := v.st.Masks
	pr := v.photoRect()
	bx := float64((p.X - pr.Min.X) / max(pr.Size().W, 1))
	by := float64((p.Y - pr.Min.Y) / max(pr.Size().H, 1))
	return frameFromShown(bx, by, ms.Params, ms.Frame[0], ms.Frame[1])
}

// radialAxes are a radial mask's half-axes on screen, and its turn there,
// in radians.
func (v *cullView) radialAxes(m marrawclient.Mask) (float32, float32, float64) {
	k := v.frameScale()
	ms := v.st.Masks
	rx, ry := m.RX*ms.Frame[0]*k, m.RY*ms.Frame[1]*k
	return float32(rx), float32(ry), (m.Angle + ms.Params.CropAngle) * math.Pi / 180
}

// radialHandles are a radial mask's handles on screen: its middle, the end
// of its width and its height, and the turn's, above it.
func (v *cullView) radialHandles(m marrawclient.Mask) (c, east, south, turn geom.Point) {
	c = v.toScreen(m.CX, m.CY)
	rx, ry, a := v.radialAxes(m)
	cs, sn := float32(math.Cos(a)), float32(math.Sin(a))
	east = geom.Pt(c.X+rx*cs, c.Y+rx*sn)
	south = geom.Pt(c.X-ry*sn, c.Y+ry*cs)
	turn = geom.Pt(c.X+(ry+24)*sn, c.Y-(ry+24)*cs)
	return
}

// maskGripAt is the handle of mask m at p, or "".
func (v *cullView) maskGripAt(m marrawclient.Mask, p geom.Point) string {
	near := func(q geom.Point, d float32) bool {
		return math.Hypot(float64(p.X-q.X), float64(p.Y-q.Y)) <= float64(d)
	}
	switch m.Type {
	case "linear":
		a, b := v.toScreen(m.X0, m.Y0), v.toScreen(m.X1, m.Y1)
		switch {
		case near(a, 12):
			return "a"
		case near(b, 12):
			return "b"
		case segDist(p, a, b) <= 7:
			return "line"
		}
	case "radial":
		c, e, s, t := v.radialHandles(m)
		switch {
		case near(t, 12):
			return "turn"
		case near(e, 12):
			return "east"
		case near(s, 12):
			return "south"
		case near(c, 14):
			return "move"
		}
	}
	return ""
}

// segDist is how far p is from the segment a to b.
func segDist(p, a, b geom.Point) float32 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := dx*dx + dy*dy
	t := float32(0)
	if l > 0 {
		t = min(1, max(0, ((p.X-a.X)*dx+(p.Y-a.Y)*dy)/l))
	}
	q := geom.Pt(a.X+t*dx, a.Y+t*dy)
	return float32(math.Hypot(float64(p.X-q.X), float64(p.Y-q.Y)))
}

// maskHandle takes the pointer while a mask is worked on: a drag of a
// handle reshapes it, a brush paints or erases, and the range picker picks.
// It reports whether it took e.
func (v *cullView) maskHandle(e input.Event, u *gunim.UI) bool {
	ms := v.st.Masks
	mu := &v.mask
	inPhoto := func(p geom.Point) bool {
		return !v.inPanel(p) && !v.stripRect.Contains(p) && !v.marksRect().Contains(p)
	}
	switch e := e.(type) {
	case input.PointerMove:
		mu.pointer, mu.over = e.Pos, inPhoto(e.Pos)
		if !mu.dragging {
			if ms.Brush.Painting {
				u.Invalidate()
			}
			return false
		}
		mu.live = v.dragMask(e.Pos)
		mu.hasLive = true
		u.Send(v, MaskGeom{Index: ms.Selected, Mask: mu.live})
		u.Invalidate()
		return true
	case input.PointerLeave:
		mu.over = false
		u.Invalidate()
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || !inPhoto(e.Pos) {
			return false
		}
		m := v.theMask()
		if ms.RangePick {
			if at, ok := v.photoPoint(e.Pos); ok {
				u.Send(v, RangeAt{X: float64(at.X), Y: float64(at.Y)})
			}
			return true
		}
		g := v.maskGripAt(m, e.Pos)
		if m.Type == "brush" && ms.Brush.Painting {
			g = "paint"
		}
		if g == "" {
			return false
		}
		mu.dragging, mu.grip, mu.start, mu.from = true, g, m, e.Pos
		mu.live, mu.hasLive = v.dragMask(e.Pos), true
		if g == "paint" {
			u.Send(v, MaskGeom{Index: ms.Selected, Mask: mu.live})
		}
		u.Invalidate()
		return true
	case input.PointerUp:
		if !mu.dragging {
			return false
		}
		mu.dragging = false
		u.Send(v, MaskGeom{Index: ms.Selected, Mask: mu.live, Commit: true})
		return true
	}
	return false
}

// dragMask is the mask being dragged, the pointer at p.
func (v *cullView) dragMask(p geom.Point) marrawclient.Mask {
	mu := &v.mask
	ms := v.st.Masks
	m := mu.start
	fx, fy := v.toFrame(p)
	ox, oy := v.toFrame(mu.from)
	dx, dy := fx-ox, fy-oy
	switch mu.grip {
	case "a":
		m.X0, m.Y0 = q4(mu.start.X0+dx), q4(mu.start.Y0+dy)
	case "b":
		m.X1, m.Y1 = q4(mu.start.X1+dx), q4(mu.start.Y1+dy)
	case "line":
		m.X0, m.Y0, m.X1, m.Y1 = q4(m.X0+dx), q4(m.Y0+dy), q4(m.X1+dx), q4(m.Y1+dy)
	case "move":
		m.CX, m.CY = q4(m.CX+dx), q4(m.CY+dy)
	case "east", "south", "turn":
		c := v.toScreen(m.CX, m.CY)
		_, _, a := v.radialAxes(m)
		k := v.frameScale()
		vx, vy := float64(p.X-c.X), float64(p.Y-c.Y)
		switch mu.grip {
		case "east":
			m.RX = q4(max(0.01, (vx*math.Cos(a)+vy*math.Sin(a))/(ms.Frame[0]*k)))
		case "south":
			m.RY = q4(max(0.01, (-vx*math.Sin(a)+vy*math.Cos(a))/(ms.Frame[1]*k)))
		case "turn":
			deg := math.Atan2(vy, vx)*180/math.Pi + 90 - ms.Params.CropAngle
			m.Angle = q4(math.Mod(math.Mod(deg, 360)+360, 360))
		}
	case "paint":
		// The stroke so far, and the point; one too near the last is let
		// go, as marraw spaces a stroke's points.
		long := max(ms.Frame[0], ms.Frame[1])
		live := mu.live
		if !mu.hasLive || len(live.Strokes) == len(mu.start.Strokes) {
			live = mu.start
			live.Strokes = append(slices.Clone(mu.start.Strokes), marrawclient.Stroke{Erase: ms.Brush.Erase,
				Radius: ms.Brush.Radius, Feather: ms.Brush.Feather, Flow: ms.Brush.Flow})
		} else {
			live.Strokes = slices.Clone(live.Strokes)
		}
		s := &live.Strokes[len(live.Strokes)-1]
		if n := len(s.Pts); n >= 2 {
			lx, ly := s.Pts[n-2], s.Pts[n-1]
			d := math.Hypot((fx-lx)*ms.Frame[0], (fy-ly)*ms.Frame[1]) / long
			if d < s.Radius/4 {
				return live
			}
		}
		s.Pts = append(slices.Clone(s.Pts), q4(fx), q4(fy))
		return live
	}
	return m
}

// paintMask draws the mask worked on over the photo: its tint while it is
// dragged or painted, the backend's tint of a mask under the pointer in
// the panel, and its handles, or the brush.
func (v *cullView) paintMask(p *paint.Painter) {
	ms := v.st.Masks
	if ms == nil {
		return
	}
	pr := v.photoRect()
	if k := v.maskTintIn.Value(); k > 0.01 && v.tintImg != nil {
		p.Image(v.tintImg, pr, paint.ImageOpts{Opacity: min(k, 1)})
	}
	if !v.maskOn() {
		return
	}
	m := v.theMask()
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: v.box.Point()}, Opacity: 1, Clip: true})()
	if v.mask.dragging || ms.Brush.Painting && m.Type == "brush" {
		v.paintClientTint(p, m, pr)
	}
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xf0}
	shade := color.NRGBA{A: 0x90}
	dot := func(c geom.Point, r float32) {
		p.RRect(geom.Rc(c.X-r-1.5, c.Y-r-1.5, 2*r+3, 2*r+3), r+1.5, paint.Solid(shade))
		p.RRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Solid(white))
	}
	line := func(a, b geom.Point, w float32, c color.NRGBA) {
		if b.X < a.X {
			a, b = b, a
		}
		dx, dy := b.X-a.X, b.Y-a.Y
		l := float32(math.Hypot(float64(dx), float64(dy)))
		if l < 0.5 {
			return
		}
		ang := float32(math.Atan2(float64(dy), float64(dx)))
		defer p.Push(paint.Rotate(ang, a))()
		p.RRect(geom.Rc(a.X, a.Y-w/2, l, w), w/2, paint.Solid(c))
	}
	switch m.Type {
	case "linear":
		a, b := v.toScreen(m.X0, m.Y0), v.toScreen(m.X1, m.Y1)
		dx, dy := b.X-a.X, b.Y-a.Y
		l := float32(math.Hypot(float64(dx), float64(dy)))
		if l > 0 {
			// The guides across the gradient, through each end.
			nx, ny := -dy/l*3000, dx/l*3000
			line(geom.Pt(a.X-nx, a.Y-ny), geom.Pt(a.X+nx, a.Y+ny), 1.5, withAlpha(white, 0.8))
			line(geom.Pt(b.X-nx, b.Y-ny), geom.Pt(b.X+nx, b.Y+ny), 1, withAlpha(white, 0.5))
		}
		line(a, b, 3.5, shade)
		line(a, b, 1.5, white)
		dot(a, 6)
		dot(b, 5)
	case "radial":
		c, e, s, t := v.radialHandles(m)
		rx, ry, a := v.radialAxes(m)
		func() {
			defer p.Push(paint.Rotate(float32(a), c))()
			ellipse(p, c, rx, ry, paint.Stroke{Width: 3.5, Color: shade})
			ellipse(p, c, rx, ry, paint.Stroke{Width: 1.5, Color: white})
			if f := float32(1 - m.Feather); f > 0.02 && f < 0.999 {
				ellipse(p, c, rx*f, ry*f, paint.Stroke{Width: 1, Color: withAlpha(white, 0.55)})
			}
		}()
		line(c, t, 1, withAlpha(white, 0.6))
		dot(c, 6)
		dot(e, 5)
		dot(s, 5)
		dot(t, 5)
	case "brush":
		if ms.Brush.Painting && v.mask.over {
			// The brush, its size on the photo, and its feather within.
			r := float32(ms.Brush.Radius * max(ms.Frame[0], ms.Frame[1]) * v.frameScale())
			c := v.mask.pointer
			p.RRectStroke(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Fill{}, paint.Stroke{Width: 2.5, Color: shade})
			p.RRectStroke(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Fill{}, paint.Stroke{Width: 1.2, Color: white})
			if f := r * float32(1-ms.Brush.Feather); f > 2 && f < r-1 {
				p.RRectStroke(geom.Rc(c.X-f, c.Y-f, 2*f, 2*f), f, paint.Fill{}, paint.Stroke{Width: 1, Color: withAlpha(white, 0.45)})
			}
		}
	}
}

// ellipse strokes an ellipse of half-axes rx, ry about c: a circle under a
// scale, its stroke kept even.
func ellipse(p *paint.Painter, c geom.Point, rx, ry float32, s paint.Stroke) {
	if rx <= 0 || ry <= 0 {
		return
	}
	k := ry / rx
	defer p.Push(paint.Transform{A: 1, B: 0, C: 0, D: 0, E: k, F: c.Y * (1 - k)})()
	p.RRectStroke(geom.Rc(c.X-rx, c.Y-rx, 2*rx, 2*rx), rx, paint.Fill{}, s)
}

// paintClientTint draws mask m's tint as it is dragged or painted, worked
// out here, the backend's coming once it is let go: a gradient's or a
// radial's ramp, or the brush's strokes.
func (v *cullView) paintClientTint(p *paint.Painter, m marrawclient.Mask, pr geom.Rect) {
	defer p.Layer(paint.LayerOpts{Bounds: pr, Opacity: 1, Clip: true})()
	red, none := maskTintInk, color.NRGBA{R: maskTintInk.R, G: maskTintInk.G, B: maskTintInk.B}
	if m.Invert {
		red, none = none, red
	}
	switch m.Type {
	case "linear":
		a, b := v.toScreen(m.X0, m.Y0), v.toScreen(m.X1, m.Y1)
		p.RRect(pr, 0, paint.Fill{Gradient: &paint.Gradient{From: a, To: b, Start: red, End: none}})
	case "radial":
		c := v.toScreen(m.CX, m.CY)
		rx, ry, a := v.radialAxes(m)
		inner := float32(1 - m.Feather)
		func() {
			defer p.Push(paint.Rotate(float32(a), c))()
			k := ry / max(rx, 0.5)
			defer p.Push(paint.Transform{A: 1, E: k, F: c.Y * (1 - k)})()
			// Past the edge the gradient keeps its end: clear, or, inverted,
			// all tint.
			g := &paint.Gradient{From: c, To: geom.Pt(c.X+rx, c.Y), Radial: true, Start: red, End: none,
				Stops: []paint.Stop{{At: inner, Color: red}}}
			big := float32(20000)
			p.RRect(geom.Rc(c.X-big, c.Y-big, 2*big, 2*big), 0, paint.Fill{Gradient: g})
		}()
	case "brush":
		long := max(v.st.Masks.Frame[0], v.st.Masks.Frame[1])
		k := v.frameScale()
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: pr, Opacity: 0.4})()
			for _, s := range m.Strokes {
				r := float32(s.Radius * long * k)
				ink := color.NRGBA{R: 240, G: 64, B: 64, A: 0xff}
				if s.Erase {
					ink = color.NRGBA{R: 60, G: 60, B: 70, A: 0xff}
				}
				for i := 0; i+1 < len(s.Pts); i += 2 {
					c := v.toScreen(s.Pts[i], s.Pts[i+1])
					p.RRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Solid(ink))
				}
			}
		}()
	}
}

// maskShow takes the masks of s: the backend's tint fades in or out, and a
// drag let go shows the edit's mask again.
func (v *cullView) maskShow(s Cull, u *gunim.UI) {
	if !v.mask.dragging {
		v.mask.hasLive = false
	}
	if s.Masks != nil && s.Masks.Tint != nil {
		v.tintImg = s.Masks.Tint
		v.maskTintIn.Animate(1, anim.Tween{Duration: 300 * time.Millisecond})
		return
	}
	v.maskTintIn.Animate(0, anim.Tween{Duration: 300 * time.Millisecond})
}
