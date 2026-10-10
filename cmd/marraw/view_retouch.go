package main

import (
	"image/color"
	"math"
	"reflect"
	"slices"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// healUI is the cull view's heal tool: a spot being placed or dragged,
// by what, from where, and where the pointer is.
type healUI struct {
	drag    string
	idx     int
	live    marrawclient.Spot
	start   marrawclient.Spot
	from    geom.Point
	pointer geom.Point
	over    bool
}

// healOn is the heal tool, while it is on and the photo free for it.
func (v *cullView) healOn() *HealView {
	ms := v.st.Masks
	if ms == nil || ms.Heal == nil || !ms.Heal.On || v.st.Crop != nil || v.st.WBPick {
		return nil
	}
	return ms.Heal
}

// healKey takes the heal tool's keys, as marraw's: Q turns it on and
// off; with a spot chosen, Delete deletes it and 1 to 9 and 0 set its
// opacity, 10% to 90% and whole.
func (v *cullView) healKey(e input.KeyPress, u *gunim.UI) bool {
	if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) {
		return false
	}
	if e.Key == input.KeyQ && v.st.Panel && v.st.Crop == nil && !v.st.WBPick {
		u.Send(v, HealToggle{})
		return true
	}
	hv := v.healOn()
	if hv == nil {
		return false
	}
	spots := v.st.Masks.Params.Spots
	if hv.Sel < 0 || hv.Sel >= len(spots) {
		return false
	}
	switch {
	case e.Key == input.KeyDelete:
		u.Send(v, SpotDelete{Index: hv.Sel})
		return true
	case e.Key >= input.Key0 && e.Key <= input.Key9:
		s := spots[hv.Sel]
		s.Opacity = float64(e.Key-input.Key0) / 10
		if e.Key == input.Key0 {
			// Whole, as the edit spells it.
			s.Opacity = 0
		}
		u.Send(v, SpotSet{Index: hv.Sel, Spot: s, Commit: true, Label: "Spot opacity"})
		return true
	}
	return false
}

// frameLong is the oriented frame's long edge, in its pixels.
func (v *cullView) frameLong() float64 {
	f := v.st.Masks.Frame
	return max(f[0], f[1], 1)
}

// spotScreenR is a spot's radius r, a fraction of the frame's long edge,
// on screen.
func (v *cullView) spotScreenR(r float64) float32 {
	return float32(r * v.frameLong() * v.frameScale())
}

// defaultSpotR is a new spot's radius, as marraw makes it: some twenty
// pixels on screen at the zoom showing.
func (v *cullView) defaultSpotR() float64 {
	return min(max(20/(v.frameLong()*v.frameScale()), 0.003), 0.05)
}

// interimSource is a heal's source until the backend chooses one, as
// marraw places it: from the spot toward the frame's middle, two and a
// half radii on.
func (v *cullView) interimSource(cx, cy, r float64) (float64, float64) {
	f := v.st.Masks.Frame
	dx, dy := 0.5-cx, 0.5-cy
	mag := math.Hypot(dx*f[0], dy*f[1])
	if mag == 0 {
		mag = 1
	}
	off := 2.5 * r * v.frameLong() / mag
	return min(max(cx+dx*off, 0), 1), min(max(cy+dy*off, 0), 1)
}

// strokeCentre is the middle of a brushed spot's stamps, and the radius
// round it that holds them, as fractions of the frame and its long edge.
func (v *cullView) strokeCentre(s marrawclient.Spot) (float64, float64, float64) {
	f := v.st.Masks.Frame
	lo, hi := [2]float64{math.Inf(1), math.Inf(1)}, [2]float64{math.Inf(-1), math.Inf(-1)}
	var r float64
	for _, st := range s.Strokes {
		r = max(r, st.Radius)
		for i := 0; i+1 < len(st.Pts); i += 2 {
			lo = [2]float64{min(lo[0], st.Pts[i]), min(lo[1], st.Pts[i+1])}
			hi = [2]float64{max(hi[0], st.Pts[i]), max(hi[1], st.Pts[i+1])}
		}
	}
	if math.IsInf(lo[0], 1) {
		return s.CX, s.CY, 0
	}
	cx, cy := (lo[0]+hi[0])/2, (lo[1]+hi[1])/2
	half := math.Hypot((hi[0]-lo[0])*f[0], (hi[1]-lo[1])*f[1]) / 2 / v.frameLong()
	return cx, cy, half + r
}

// spotHandles are the chosen spot's handles on screen: its middle, its
// source's, and its size's at its right edge, a circle's alone.
func (v *cullView) spotHandles(s marrawclient.Spot) (dest, src, size geom.Point, hasSize bool) {
	dest, src = v.toScreen(s.CX, s.CY), v.toScreen(s.SX, s.SY)
	if s.Kind != "stroke" {
		size, hasSize = dest.Add(geom.Pt(v.spotScreenR(s.Radius), 0)), true
	}
	return
}

// spotAt is the spot under p, the chosen one's first, or -1.
func (v *cullView) spotAt(p geom.Point) int {
	spots := v.st.Masks.Params.Spots
	for i := len(spots) - 1; i >= 0; i-- {
		s := spots[i]
		if s.Kind == "stroke" {
			for _, st := range s.Strokes {
				r := v.spotScreenR(st.Radius)
				for k := 0; k+1 < len(st.Pts); k += 2 {
					if dist(v.toScreen(st.Pts[k], st.Pts[k+1]), p) <= r {
						return i
					}
				}
			}
			continue
		}
		if dist(v.toScreen(s.CX, s.CY), p) <= max(v.spotScreenR(s.Radius), 8) {
			return i
		}
	}
	return -1
}

func dist(a, b geom.Point) float32 {
	d := a.Sub(b)
	return float32(math.Hypot(float64(d.X), float64(d.Y)))
}

// healHandle places and drags spots: a press on the chosen spot's handle
// drags it, on another spot chooses it and drags it, and elsewhere on the
// photo places a new one, grown by the drag, or brushed. It reports
// whether it took e.
func (v *cullView) healHandle(e input.Event, u *gunim.UI) bool {
	hv := v.healOn()
	if hv == nil {
		v.heal.drag = ""
		return false
	}
	h := &v.heal
	spots := v.st.Masks.Params.Spots
	switch e := e.(type) {
	case input.PointerMove:
		h.pointer, h.over = e.Pos, v.inPhotoArea(e.Pos) && v.photoRect().Contains(e.Pos)
		if h.drag == "" {
			u.Invalidate()
			return false
		}
		v.healDrag(e.Pos, hv)
		u.Invalidate()
		return true
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || !v.inPhotoArea(e.Pos) {
			return false
		}
		h.from, h.pointer = e.Pos, e.Pos
		if sel := hv.Sel; sel >= 0 && sel < len(spots) {
			s := spots[sel]
			dest, src, size, hasSize := v.spotHandles(s)
			switch {
			case hasSize && dist(size, e.Pos) <= 10:
				h.drag = "size"
			case s.Mode != "fill" && dist(src, e.Pos) <= 10:
				h.drag = "source"
			case dist(dest, e.Pos) <= 10:
				h.drag = "dest"
			}
			if h.drag != "" {
				h.idx, h.live, h.start = sel, s, s
				u.Invalidate()
				return true
			}
		}
		if i := v.spotAt(e.Pos); i >= 0 {
			u.Send(v, SpotSelect{Index: i})
			h.drag, h.idx, h.live, h.start = "dest", i, spots[i], spots[i]
			u.Invalidate()
			return true
		}
		if !v.photoRect().Contains(e.Pos) {
			return false
		}
		fx, fy := v.toFrame(e.Pos)
		h.idx = -1
		if hv.Tool == "brush" {
			h.drag = "stroke"
			h.live = marrawclient.Spot{Kind: "stroke", CX: fx, CY: fy,
				Strokes: []marrawclient.Stroke{{Radius: hv.Radius, Feather: hv.Feather, Flow: 1, Pts: []float64{fx, fy}}}}
		} else {
			h.drag = "new"
			r := v.defaultSpotR()
			sx, sy := v.interimSource(fx, fy, r)
			h.live = marrawclient.Spot{CX: fx, CY: fy, Radius: r, SX: sx, SY: sy}
		}
		u.Invalidate()
		return true
	case input.PointerUp:
		if h.drag == "" {
			return false
		}
		drag := h.drag
		h.drag = ""
		s := h.live
		switch drag {
		case "new":
			u.Send(v, SpotAdd{Spot: s})
		case "stroke":
			cx, cy, r := v.strokeCentre(s)
			s.CX, s.CY = cx, cy
			s.SX, s.SY = v.interimSource(cx, cy, r)
			u.Send(v, SpotAdd{Spot: s})
		default:
			if !reflect.DeepEqual(s, h.start) {
				u.Send(v, SpotSet{Index: h.idx, Spot: s, Commit: true})
			}
		}
		u.Invalidate()
		return true
	}
	return false
}

// healDrag carries the spot dragged to the pointer at p.
func (v *cullView) healDrag(p geom.Point, hv *HealView) {
	h := &v.heal
	fx, fy := v.toFrame(p)
	L, k := v.frameLong(), v.frameScale()
	switch h.drag {
	case "new":
		r := max(v.defaultSpotR(), float64(dist(p, h.from))/k/L)
		r = min(r, 0.5)
		h.live.Radius = r
		h.live.SX, h.live.SY = v.interimSource(h.live.CX, h.live.CY, r)
	case "stroke":
		st := &h.live.Strokes[0]
		n := len(st.Pts)
		last := v.toScreen(st.Pts[n-2], st.Pts[n-1])
		if float64(dist(last, p)) >= st.Radius*L*k/4 {
			st.Pts = append(st.Pts, fx, fy)
		}
	case "dest":
		ox, oy := v.toFrame(h.from)
		dx, dy := fx-ox, fy-oy
		s := h.start
		s.CX, s.CY = s.CX+dx, s.CY+dy
		if s.Kind == "stroke" {
			s.Strokes = slices.Clone(s.Strokes)
			for i := range s.Strokes {
				pts := slices.Clone(s.Strokes[i].Pts)
				for j := 0; j+1 < len(pts); j += 2 {
					pts[j], pts[j+1] = pts[j]+dx, pts[j+1]+dy
				}
				s.Strokes[i].Pts = pts
			}
			s.SX, s.SY = s.SX+dx, s.SY+dy
		}
		h.live = s
	case "source":
		h.live.SX, h.live.SY = fx, fy
	case "size":
		c := v.toScreen(h.live.CX, h.live.CY)
		h.live.Radius = min(max(float64(dist(p, c))/k/L, 0.003), 0.5)
	}
}

// The spots' inks, as marraw's.
var (
	spotFill  = color.NRGBA{R: 120, G: 180, B: 255, A: 0x1f}
	spotWhite = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
)

// paintHeal draws the spots while the heal tool is on: each a ring, the
// chosen one with its source, the line between them and its handles, and
// where a new one would go at the pointer.
func (v *cullView) paintHeal(p *paint.Painter) {
	hv := v.healOn()
	if hv == nil {
		return
	}
	h := &v.heal
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: v.box.Point()}, Opacity: 1, Clip: true})()
	spots := v.st.Masks.Params.Spots
	for i, s := range spots {
		if h.drag != "" && h.idx == i {
			s = h.live
		}
		a := float32(1)
		if s.Disabled {
			a = 0.35
		}
		if i == hv.Sel {
			v.paintChosenSpot(p, s, a, h.drag)
		} else {
			v.paintSpotRing(p, s, withAlpha(spotWhite, 0.55*a), 1.25)
		}
	}
	if h.drag == "new" || h.drag == "stroke" {
		v.paintChosenSpot(p, h.live, 1, h.drag)
		return
	}
	if h.drag == "" && h.over {
		// Where a new spot would go.
		r := v.spotScreenR(v.defaultSpotR())
		if hv.Tool == "brush" {
			r = v.spotScreenR(hv.Radius)
		}
		ringAt(p, h.pointer, r, withAlpha(spotWhite, 0.6), 1)
	}
}

// paintSpotRing draws spot s's outline: its ring, or its brushed stamps'.
func (v *cullView) paintSpotRing(p *paint.Painter, s marrawclient.Spot, c color.NRGBA, w float32) {
	if s.Kind == "stroke" {
		for _, st := range s.Strokes {
			r := v.spotScreenR(st.Radius)
			for k := 0; k+1 < len(st.Pts); k += 2 {
				ringAt(p, v.toScreen(st.Pts[k], st.Pts[k+1]), r, withAlpha(c, 0.5), w)
			}
		}
		return
	}
	ringAt(p, v.toScreen(s.CX, s.CY), v.spotScreenR(s.Radius), c, w)
}

// paintChosenSpot draws the chosen spot s: its place filled faintly and
// ringed, its source's ring dashed, the line from one to the other
// dashed, and its handles; while its source is dragged, its place goes.
func (v *cullView) paintChosenSpot(p *paint.Painter, s marrawclient.Spot, a float32, drag string) {
	dest, src, size, hasSize := v.spotHandles(s)
	fill, ring := withAlpha(spotFill, a), withAlpha(spotWhite, 0.95*a)
	showDest := drag != "source"
	if s.Kind == "stroke" {
		dx, dy := s.SX-s.CX, s.SY-s.CY
		for _, st := range s.Strokes {
			r := v.spotScreenR(st.Radius)
			for k := 0; k+1 < len(st.Pts); k += 2 {
				c := v.toScreen(st.Pts[k], st.Pts[k+1])
				if showDest {
					p.RRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Solid(fill))
				}
				if s.Mode != "fill" {
					ringAt(p, v.toScreen(st.Pts[k]+dx, st.Pts[k+1]+dy), r, withAlpha(spotWhite, 0.3*a), 1)
				}
			}
		}
	} else {
		r := v.spotScreenR(s.Radius)
		if showDest {
			p.RRect(geom.Rc(dest.X-r, dest.Y-r, 2*r, 2*r), r, paint.Solid(fill))
			ringAt(p, dest, r, ring, 1.75)
		}
		if s.Mode != "fill" {
			dashedRing(p, src, r, withAlpha(spotWhite, 0.85*a), 1.5)
		}
	}
	if s.Mode != "fill" && drag != "new" && drag != "stroke" {
		dashedLine(p, dest, src, withAlpha(spotWhite, 0.7*a), 1.25)
		handleDot(p, src)
	}
	if showDest && drag != "new" && drag != "stroke" {
		handleDot(p, dest)
		if hasSize {
			handleDot(p, size)
		}
	}
}

// ringAt draws a ring round c, r across, w wide.
func ringAt(p *paint.Painter, c geom.Point, r float32, ink color.NRGBA, w float32) {
	p.RRectStroke(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Fill{}, paint.Stroke{Width: w, Color: ink})
}

// handleDot is a handle: a dark dot ringed white, as marraw's.
func handleDot(p *paint.Painter, c geom.Point) {
	const r = 8
	p.RRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Solid(color.NRGBA{A: 0x66}))
	p.RRectStroke(geom.Rc(c.X-r+1, c.Y-r+1, 2*r-2, 2*r-2), r-1, paint.Fill{}, paint.Stroke{Width: 2, Color: spotWhite})
}

// segment draws a straight stroke from a to b, w wide.
func segment(p *paint.Painter, a, b geom.Point, w float32, ink color.NRGBA) {
	d := b.Sub(a)
	l := float32(math.Hypot(float64(d.X), float64(d.Y)))
	if l < 0.5 {
		return
	}
	mid := geom.Pt((a.X+b.X)/2, (a.Y+b.Y)/2)
	defer p.Push(paint.Rotate(float32(math.Atan2(float64(d.Y), float64(d.X))), mid))()
	p.RRect(geom.Rc(mid.X-l/2, mid.Y-w/2, l, w), w/2, paint.Solid(ink))
}

// dashedLine draws a line from a to b in dashes, as marraw's 4 and 3.
func dashedLine(p *paint.Painter, a, b geom.Point, ink color.NRGBA, w float32) {
	d := b.Sub(a)
	l := float32(math.Hypot(float64(d.X), float64(d.Y)))
	const dash, gap = 4, 3
	for t := float32(0); t < l; t += dash + gap {
		e := min(t+dash, l)
		segment(p, a.Add(geom.Pt(d.X*t/l, d.Y*t/l)), a.Add(geom.Pt(d.X*e/l, d.Y*e/l)), w, ink)
	}
}

// dashedRing draws a ring round c, r across, in dashes, as marraw's 5
// and 3.
func dashedRing(p *paint.Painter, c geom.Point, r float32, ink color.NRGBA, w float32) {
	circ := 2 * math.Pi * float64(r)
	n := max(8, int(circ/8))
	for k := 0; k < n; k++ {
		a0 := 2 * math.Pi * float64(k) / float64(n)
		a1 := a0 + 2*math.Pi/float64(n)*5/8
		pa := c.Add(geom.Pt(r*float32(math.Cos(a0)), r*float32(math.Sin(a0))))
		pb := c.Add(geom.Pt(r*float32(math.Cos(a1)), r*float32(math.Sin(a1))))
		segment(p, pa, pb, w, ink)
	}
}
