package main

import (
	"fmt"
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// cropZoom is how far the whole frame shows while cropping, of the room,
// so the crop's handles keep clear of the window's edges.
const cropZoom = 0.8

// cropUI is the cull view's crop: the bar at the foot, and a drag of the
// crop under way, from start at from, as grip, showing local.
type cropUI struct {
	bar      gunim.Node
	barRect  geom.Rect
	aspects  *widget.Segmented
	angle    *widget.Slider
	degrees  *widget.Label
	info     *widget.Label
	dragging bool
	g        grip
	start    cropRect
	from     geom.Point
	local    cropRect
	hasLocal bool
}

// newCropBar is the crop's bar, as marraw's: the shapes, the straighten,
// the quarter turns and mirrors, a reset, and Done.
func (v *cullView) newCropBar() gunim.Node {
	c := &v.crop
	var labels []string
	for _, a := range aspectChoices {
		labels = append(labels, a.label)
	}
	c.aspects = widget.NewSegmented(labels...)
	c.aspects.KeepFocus = true
	c.aspects.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return CropAspect{Index: i} }
	c.angle = widget.NewSlider(-15, 15)
	c.angle.Tooltip = "Straighten"
	c.angle.KeepFocus, c.angle.Snap, c.angle.HasRest = true, 0.1, true
	c.angle.OnChange = func(x float32, _ *gunim.UI) gunim.Intent { return CropAngle{Angle: float64(x)} }
	c.angle.OnCommit = func(x float32, _ *gunim.UI) gunim.Intent { return CropAngle{Angle: float64(x), Commit: true} }
	c.degrees = widget.NewLabel("0.0°")
	c.degrees.Face, c.degrees.Size, c.degrees.Color = widget.MonoFont, readoutSize, readoutInk
	c.info = widget.NewLabel("")
	c.info.Face, c.info.Size, c.info.Color = widget.MonoFont, readoutSize, readoutInk
	icon := func(ic *icon.Icon, tip string, in gunim.Intent) *widget.IconButton {
		b := widget.NewIconButton(ic, tip)
		b.KeepFocus, b.OnClick = true, widget.Sends(in)
		return b
	}
	btn := func(label string, in gunim.Intent, primary bool) *widget.Button {
		b := widget.NewButton(label)
		b.KeepFocus, b.Ghost, b.OnClick = true, !primary, widget.Sends(in)
		if primary {
			b.Kind = widget.ButtonPrimary
		}
		return b
	}
	row := widget.Row(c.aspects, &divider{}, &fixedWidth{w: 120, child: c.angle}, c.degrees, &divider{},
		icon(iconRotateCcw, "Turn left", CropTurn{}), icon(iconRotateCw, "Turn right", CropTurn{CW: true}),
		icon(iconFlipH, "Mirror across", CropFlip{}), icon(iconFlipV, "Mirror upside down", CropFlip{Vertical: true}),
		&divider{}, btn("Reset", CropReset{}, false), btn("Done", CropDone{}, true))
	row.Cross = widget.CrossCenter
	th := marrawTheme().With(theme.Set(widget.ButtonHeight, 28), theme.Set(widget.ButtonPadding, 11),
		theme.Set(widget.ButtonRadius, 7), theme.Set(widget.TextSize, 12.5), theme.Set(widget.SegmentedHeight, 28))
	return widget.NewThemed(row, th)
}

// The crop bar's icons.
var (
	iconRotateCcw = icon.RotateCcw
	iconRotateCw  = icon.RotateCw
	iconFlipH     = icon.FlipHorizontal2
	iconFlipV     = icon.FlipVertical2
)

// cropShow takes the crop of s: the bar follows it, and the frame zooms
// out to show whole as cropping starts, and back as it ends.
func (v *cullView) cropShow(s, prev Cull, u *gunim.UI) {
	c := &v.crop
	was := prev.Crop != nil && prev.Crop.Ready
	now := s.Crop != nil && s.Crop.Ready
	th := u.Theme()
	if now && !was {
		v.flinging = false
		v.z.Animate(cropZoom, widget.Settle.Get(th))
		v.c.Animate(geom.Pt(0.5, 0.5), widget.Settle.Get(th))
	}
	if !now && was {
		v.z.Animate(1, widget.Settle.Get(th))
		v.c.Animate(geom.Pt(0.5, 0.5), widget.Settle.Get(th))
	}
	v.cropIn.Animate(on(now), widget.Quick.Get(th))
	if s.Crop == nil {
		c.hasLocal, c.dragging = false, false
		return
	}
	if !c.dragging {
		c.hasLocal = false
	}
	c.aspects.SetSelected(s.Crop.Aspect, u)
	if !c.angle.Held() {
		c.angle.SetValue(float32(s.Crop.Angle), u)
	}
	c.degrees.Text = fmt.Sprintf("%+.1f°", s.Crop.Angle)
}

// cropping reports whether the crop shows, its frame's pixels come.
func (v *cullView) cropping() bool { return v.st.Crop != nil && v.st.Crop.Ready }

// photoRect is where the photo shows on screen now.
func (v *cullView) photoRect() geom.Rect {
	z := v.z.Value()
	f, s := v.full(), v.fit()*z
	o := v.origin(z, v.c.Value())
	return geom.Rc(o.X, o.Y, f.W*s, f.H*s)
}

// cropShown is the crop as it shows: the one dragged, or the edit's.
func (v *cullView) cropShown() cropRect {
	if v.crop.hasLocal {
		return v.crop.local
	}
	return v.st.Crop.Rect
}

// cropScreen is crop r on screen.
func (v *cullView) cropScreen(r cropRect) geom.Rect {
	pr := v.photoRect()
	s := pr.Size()
	return geom.Rc(pr.Min.X+float32(r.X)*s.W, pr.Min.Y+float32(r.Y)*s.H, float32(r.W)*s.W, float32(r.H)*s.H)
}

// gripAt is the part of the crop at p: a corner within 12 of it, an edge
// within 6, inside, or "" outside.
func (v *cullView) gripAt(p geom.Point) grip {
	r := v.cropScreen(v.cropShown())
	near := func(a, b float32, d float32) bool { return float32(math.Abs(float64(a-b))) <= d }
	l, t, rr, b := near(p.X, r.Min.X, 12), near(p.Y, r.Min.Y, 12), near(p.X, r.Max.X, 12), near(p.Y, r.Max.Y, 12)
	switch {
	case l && t:
		return "nw"
	case rr && t:
		return "ne"
	case l && b:
		return "sw"
	case rr && b:
		return "se"
	}
	in := func(x, lo, hi float32) bool { return x >= lo && x <= hi }
	switch {
	case near(p.X, r.Min.X, 6) && in(p.Y, r.Min.Y, r.Max.Y):
		return "w"
	case near(p.X, r.Max.X, 6) && in(p.Y, r.Min.Y, r.Max.Y):
		return "e"
	case near(p.Y, r.Min.Y, 6) && in(p.X, r.Min.X, r.Max.X):
		return "n"
	case near(p.Y, r.Max.Y, 6) && in(p.X, r.Min.X, r.Max.X):
		return "s"
	case r.Contains(p):
		return "move"
	}
	return ""
}

// gripCursor is the pointer over grip g.
func gripCursor(g grip) input.Cursor {
	switch g {
	case "nw", "se":
		return input.CursorResizeNWSE
	case "ne", "sw":
		return input.CursorResizeNESW
	case "n", "s":
		return input.CursorResizeV
	case "e", "w":
		return input.CursorResizeH
	case "move":
		return input.CursorMove
	}
	return input.CursorArrow
}

// cropPoint is p in fractions of the photo as it shows.
func (v *cullView) cropPoint(p geom.Point) (float64, float64) {
	pr := v.photoRect()
	s := pr.Size()
	return float64((p.X - pr.Min.X) / max(s.W, 1)), float64((p.Y - pr.Min.Y) / max(s.H, 1))
}

// cropHandle takes the pointer while cropping: a drag of a handle or of
// the crop moves it, kept on the photo as straightened, and its end sets
// it. It reports whether it took e.
func (v *cullView) cropHandle(e input.Event, u *gunim.UI) bool {
	c := &v.crop
	cv := v.st.Crop
	asp := float64(v.full().W / max(v.full().H, 1))
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || v.inPanel(e.Pos) || e.Pos.Y >= v.box.H-stripHeight || c.barRect.Contains(e.Pos) {
			return false
		}
		g := v.gripAt(e.Pos)
		if g == "" {
			return true
		}
		c.dragging, c.g, c.start, c.local, c.hasLocal = true, g, v.cropShown(), v.cropShown(), true
		x, y := v.cropPoint(e.Pos)
		c.from = geom.Pt(float32(x), float32(y))
		return true
	case input.PointerMove:
		if !c.dragging {
			return false
		}
		x, y := v.cropPoint(e.Pos)
		c.local = dragCovered(c.start, c.g, x-float64(c.from.X), y-float64(c.from.Y), cv.Ratio, cv.Angle, asp)
		u.Invalidate()
		return true
	case input.PointerUp:
		if !c.dragging {
			return false
		}
		c.dragging = false
		u.Send(v, CropSet{Rect: c.local})
		return true
	}
	return false
}

// paintCrop draws the crop over the whole frame, as marraw's: the rest of
// the photo dimmed, the crop's edge, its thirds, its handles at the
// corners and the middles of its sides, and its shape and size in a pill
// in its middle; and the bar at the foot.
func (v *cullView) paintCrop(p *paint.Painter, box geom.Size, bar, info gunim.Child) {
	k := v.cropIn.Value()
	if k < 0.01 || v.st.Crop == nil {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(k, 1)})()
	cr := v.cropScreen(v.cropShown())
	room := geom.Rc(0, 0, box.W-panelWidth*v.side.Value(), box.H-stripHeight)
	dim := paint.Solid(color.NRGBA{R: 4, G: 6, B: 9, A: 0x9e})
	p.RRect(geom.Rc(room.Min.X, room.Min.Y, room.Size().W, max(0, cr.Min.Y-room.Min.Y)), 0, dim)
	p.RRect(geom.Rc(room.Min.X, cr.Max.Y, room.Size().W, max(0, room.Max.Y-cr.Max.Y)), 0, dim)
	p.RRect(geom.Rc(room.Min.X, cr.Min.Y, max(0, cr.Min.X-room.Min.X), cr.Size().H), 0, dim)
	p.RRect(geom.Rc(cr.Max.X, cr.Min.Y, max(0, room.Max.X-cr.Max.X), cr.Size().H), 0, dim)
	white := func(a uint8) paint.Fill { return paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: a}) }
	for i := 1; i <= 2; i++ {
		x := cr.Min.X + cr.Size().W*float32(i)/3
		y := cr.Min.Y + cr.Size().H*float32(i)/3
		p.RRect(geom.Rc(x, cr.Min.Y, 1, cr.Size().H), 0, white(0x48))
		p.RRect(geom.Rc(cr.Min.X, y, cr.Size().W, 1), 0, white(0x48))
	}
	edge := uint8(0xd9)
	if v.crop.dragging {
		edge = 0xff
	}
	p.RRectStroke(cr, 0, paint.Fill{}, paint.Stroke{Width: 1, Color: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: edge}})
	// The corners' marks, 22 long and 3 thick, and the sides' bars.
	const arm, th = 22, 3
	for _, c := range []struct {
		at     geom.Point
		sx, sy float32
	}{{cr.Min, 1, 1}, {geom.Pt(cr.Max.X, cr.Min.Y), -1, 1}, {geom.Pt(cr.Min.X, cr.Max.Y), 1, -1}, {cr.Max, -1, -1}} {
		hx := geom.Rc(c.at.X, c.at.Y-th/2, arm*c.sx, th).Normalized()
		hy := geom.Rc(c.at.X-th/2, c.at.Y, th, arm*c.sy).Normalized()
		p.RRect(hx, 1, white(0xff))
		p.RRect(hy, 1, white(0xff))
	}
	mid := cr.Center()
	p.RRect(geom.Rc(mid.X-15, cr.Min.Y-2, 30, 4), 2, white(0xff))
	p.RRect(geom.Rc(mid.X-15, cr.Max.Y-2, 30, 4), 2, white(0xff))
	p.RRect(geom.Rc(cr.Min.X-2, mid.Y-15, 4, 30), 2, white(0xff))
	p.RRect(geom.Rc(cr.Max.X-2, mid.Y-15, 4, 30), 2, white(0xff))
	// The crop's shape and size, in its middle.
	is := info.Size()
	if cr.Size().W > is.W+40 && cr.Size().H > is.H+24 {
		pill := geom.Rc(mid.X-is.W/2-10, mid.Y-is.H/2-5, is.W+20, is.H+10)
		paintGlass(p, pill, pill.Size().H/2)
		func() {
			defer p.Push(paint.Translate(geom.Pt(pill.Min.X+10, pill.Min.Y+5)))()
			info.Paint(p)
		}()
	}
	func() {
		r := v.crop.barRect
		defer p.Push(paint.Translate(geom.Pt(0, (1-k)*24)))()
		paintGlass(p, r, 13)
		bar.Paint(p)
	}()
}

// cropInfo is the crop's shape and size as its pill says them.
func (v *cullView) cropInfo() string {
	cv := v.st.Crop
	if cv == nil {
		return ""
	}
	r := v.cropShown()
	w, h := r.W*float64(cv.Frame.X), r.H*float64(cv.Frame.Y)
	if w <= 0 || h <= 0 {
		return ""
	}
	return fmt.Sprintf("%s  %d × %d", ratioName(w/h), int(math.Round(w)), int(math.Round(h)))
}
