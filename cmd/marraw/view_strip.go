package main

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// StripGroup is a time-gap group the filmstrip shows of: where it starts
// in the folder, how many it holds, when it was taken, and the gap
// before it, or "".
type StripGroup struct {
	Start, Count int
	Label, Gap   string
}

// The filmstrip's measures, as marraw's: its thumbnails 40 high, 4 apart,
// groups 8 apart, the photo showing 1.22 times as large; the strip 16
// from the window's foot, its padding 14 across and 10 down.
const (
	stripThumbH  = 40
	stripGap     = 4
	stripGroupSp = 8
	stripPadX    = 14
	stripPadY    = 10
	stripHeadH   = 15
	stripBottom  = 16
	stripCurrent = 1.22
	stripPillW   = 16
	// stripBoxH is the strip's height.
	stripBoxH = stripPadY*2 + stripHeadH + 5 + stripThumbH
)

// The filmstrip's inks, as marraw's.
var (
	stripFill    = color.NRGBA{R: 10, G: 12, B: 16, A: 0x99}
	stripEdge    = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x1f}
	stripRing    = color.NRGBA{R: 0x7c, G: 0x83, B: 0xff, A: 0xff}
	stripMono    = theme.Length("marraw.strip.mono", 10)
	stripTiny    = theme.Length("marraw.strip.tiny", 9)
	stripCountSz = theme.Length("marraw.strip.count", 13)
	accentText   = theme.Color("marraw.accent.text", color.NRGBA{R: 0xc3, G: 0xc7, B: 0xff, A: 0xff})
	faintInk     = theme.Color("marraw.faint", color.NRGBA{R: 0x8b, G: 0x8f, B: 0x96, A: 0xff})
)

// filmstrip is the cull view's filmstrip, as marraw's: a strip of glass at
// the foot of the window, as wide as its photos need up to the room there
// is, the photos 40 high as wide as their shapes, grouped under when they
// were taken with the gap between groups marked, the photo showing grown
// and ringed in the middle, the strip easing to keep it there.
type filmstrip struct {
	anim.Group
	v      *cullView
	st     Cull
	groups *widget.Label
	count  *widget.Label
	heads  map[int]*stripHead
	// focus is the photo the strip is centred on, gliding to the one
	// showing; scroll is how far the wheel has taken it from there.
	focus  *anim.Float
	scroll float32
	// grow is how far each photo near the middle has grown, by its place.
	grow map[int]*anim.Float
	// laid is where each photo lies, by its place, and box the strip's
	// size, from the last layout; left is where the photos begin.
	laid       map[int]geom.Rect
	box        geom.Size
	left, view float32
	hot        int
	// offsetNow is the scroll as last drawn, for the pointer; gapPills are
	// where the gaps' pills are, and gapAt the middle of each one's text.
	offsetNow float32
	gapPills  []float32
	gapAt     map[*widget.Label]geom.Point
}

// stripHead is a group's heading in the filmstrip: its times and count,
// and the gap before it, written on its side.
type stripHead struct {
	when, n, gap *widget.Label
}

func newFilmstrip(v *cullView) *filmstrip {
	s := &filmstrip{v: v, groups: widget.NewLabel("GROUPS"), count: widget.NewLabel(""), heads: map[int]*stripHead{},
		focus: anim.NewFloat(0), grow: map[int]*anim.Float{}, laid: map[int]geom.Rect{}, hot: -1,
		gapAt: map[*widget.Label]geom.Point{}}
	s.groups.Size, s.groups.Color = stripTiny, faintInk
	s.count.Face, s.count.Size, s.count.Color = widget.MonoFont, stripCountSz, accentText
	s.Add(s.focus)
	return s
}

// show takes the cull view's state: the strip glides to the photo showing.
func (s *filmstrip) show(st Cull, prev Cull, u *gunim.UI) {
	s.st = st
	if st.ID != prev.ID || s.focus.Value() == 0 && st.Index != 0 {
		s.scroll = 0
		if prev.ID == 0 {
			s.focus.Jump(float32(st.Index))
		} else {
			s.focus.Animate(float32(st.Index), anim.Spring{Response: 0.25, Damping: 1})
		}
	}
	s.count.Text = fmt.Sprint(st.GroupCount)
	// The photo showing grows, the one before shrinks back.
	for _, t := range st.Strip {
		g, ok := s.grow[t.Index]
		if !ok {
			g = anim.NewFloat(1)
			s.grow[t.Index] = g
			s.Add(g)
		}
		to := float32(1)
		if t.Index == st.Index {
			to = stripCurrent
		}
		if !ok && to != 1 {
			g.Jump(to)
		}
		g.Animate(to, anim.Tween{Duration: 150 * time.Millisecond})
	}
	u.Invalidate()
}

// Children implements [gunim.Composite]: the groups' headings are built as
// they come.
func (s *filmstrip) Children() []gunim.Node { return []gunim.Node{s.groups, s.count} }

// thumbW is photo t's width in the strip.
func stripW(t Thumb) float32 {
	a := t.Aspect
	if a <= 0 {
		a = 1.5
	}
	return float32(math.Round(float64(stripThumbH * min(max(a, 0.4), 3))))
}

// Layout implements [gunim.Node]: the photos along a line, each group's
// first starting a heading, and the strip as wide as they need, up to the
// room given.
func (s *filmstrip) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	st := s.st
	grouped := st.GroupCount > 0
	starts := map[int]StripGroup{}
	for _, g := range st.Groups {
		starts[g.Start] = g
	}
	// Where each photo lies, from the first in the strip.
	clear(s.laid)
	x := float32(0)
	y := float32(stripPadY)
	if grouped {
		y += stripHeadH + 5
	}
	type headAt struct {
		x   float32
		g   StripGroup
		gap float32
	}
	var heads []headAt
	for i, t := range st.Strip {
		g, starts := starts[t.Index]
		if grouped && (starts || i == 0) {
			if i > 0 {
				x += stripGroupSp - stripGap
			}
			gapX := float32(-1)
			if starts && g.Gap != "" && i > 0 {
				gapX = x
				x += stripPillW + 6
			}
			if !starts {
				// The strip begins inside a group: its heading is the
				// group's own all the same.
				for _, gg := range st.Groups {
					if t.Index >= gg.Start && t.Index < gg.Start+gg.Count {
						g = gg
					}
				}
			}
			heads = append(heads, headAt{x: x, g: g, gap: gapX})
		}
		w := stripW(t)
		s.laid[t.Index] = geom.Rc(x, y, w, stripThumbH)
		x += w + stripGap
	}
	content := max(0, x-stripGap)
	// The groups' count at the left, where the folder is grouped.
	lead := float32(0)
	gs := kids.At(0).Layout(gunim.Loose(geom.Sz(100, 20)))
	cs := kids.At(1).Layout(gunim.Loose(geom.Sz(100, 20)))
	if grouped {
		lead = max(gs.W, cs.W) + 12 + 8
	}
	s.left = stripPadX + lead
	maxView := max(0, c.Max.W-2*stripPadX-lead)
	s.view = min(content, maxView)
	s.box = geom.Sz(s.view+2*stripPadX+lead, stripBoxH)
	mid := (stripBoxH - gs.H - cs.H - 3) / 2
	if grouped {
		kids.At(0).Place(geom.Pt(stripPadX, mid))
		kids.At(1).Place(geom.Pt(stripPadX, mid+gs.H+3))
	} else {
		kids.At(0).Place(geom.Pt(-10000, 0))
		kids.At(1).Place(geom.Pt(-10000, 0))
	}
	off := s.offset(content)
	// The headings: built where new, dropped where gone, each at its group.
	children := map[*widget.Label]gunim.Child{}
	for kid := range kids.All {
		if l, ok := kid.Node().(*widget.Label); ok {
			children[l] = kid
		}
	}
	want := map[int]bool{}
	for _, h := range heads {
		want[h.g.Start] = true
	}
	for k, h := range s.heads {
		if !want[k] {
			kids.Drop(h.when)
			kids.Drop(h.n)
			kids.Drop(h.gap)
			delete(s.heads, k)
		}
	}
	for _, h := range heads {
		sh, ok := s.heads[h.g.Start]
		if !ok {
			sh = &stripHead{when: widget.NewLabel(""), n: widget.NewLabel(""), gap: widget.NewLabel("")}
			sh.when.Face, sh.when.Size = widget.MonoFont, stripMono
			sh.n.Size, sh.n.Color = stripTiny, faintInk
			sh.gap.Face, sh.gap.Size, sh.gap.Color = widget.MonoFont, stripTiny, accentText
			s.heads[h.g.Start] = sh
			children[sh.when] = kids.Build(sh.when)
			children[sh.n] = kids.Build(sh.n)
			children[sh.gap] = kids.Build(sh.gap)
		}
		sh.when.Text, sh.n.Text = h.g.Label, fmt.Sprint(h.g.Count)
		sh.gap.Text = strings.TrimSuffix(strings.TrimSuffix(h.g.Gap, " before"), " after")
		ws := children[sh.when].Layout(gunim.Loose(geom.Sz(300, stripHeadH)))
		ns := children[sh.n].Layout(gunim.Loose(geom.Sz(100, stripHeadH)))
		// A heading scrolled past the strip's left edge stays at the edge
		// while its group shows, and the group's end pushes it out.
		end := h.x
		for i := h.g.Start; i < h.g.Start+h.g.Count; i++ {
			if r, ok := s.laid[i]; ok {
				end = max(end, r.Max.X)
			}
		}
		hx := s.left + h.x - off
		hx = max(hx, min(s.left, s.left+end-off-(1+ws.W+6+ns.W)))
		children[sh.when].Place(geom.Pt(hx+1, stripPadY))
		children[sh.n].Place(geom.Pt(hx+1+ws.W+6, stripPadY+ws.H-ns.H))
		gs := children[sh.gap].Layout(gunim.Loose(geom.Sz(200, 20)))
		if h.gap >= 0 && sh.gap.Text != "" {
			// Written on its side, in the pill between the groups: placed
			// about the pill's middle, and turned about it as it is drawn.
			c := geom.Pt(s.left+h.gap-off+3+(stripPillW)/2, stripPadY+stripHeadH+5+stripThumbH/2)
			children[sh.gap].Place(geom.Pt(c.X-gs.W/2, c.Y-gs.H/2))
			s.gapAt[sh.gap] = c
		} else {
			children[sh.gap].Place(geom.Pt(-10000, 0))
			delete(s.gapAt, sh.gap)
		}
	}
	s.gapPills = s.gapPills[:0]
	for _, h := range heads {
		if h.gap >= 0 {
			s.gapPills = append(s.gapPills, s.left+h.gap-off)
		}
	}
	return s.box
}

// offset is how far the strip's photos are scrolled: the one the strip is
// centred on in the middle, as far as the folder's ends allow, and the
// wheel's on top.
func (s *filmstrip) offset(content float32) float32 {
	st := s.st
	if content <= s.view || len(st.Strip) == 0 {
		return 0
	}
	f := s.focus.Value()
	// The middle of the photo at f, between the two it lies between.
	lo := int(math.Floor(float64(f)))
	a, aok := s.laid[lo]
	b, bok := s.laid[lo+1]
	var cx float32
	switch {
	case aok && bok:
		k := f - float32(lo)
		cx = a.Center().X + (b.Center().X-a.Center().X)*k
	case aok:
		cx = a.Center().X
	case bok:
		cx = b.Center().X
	default:
		cx = s.laid[st.Index].Center().X
	}
	off := cx - s.view/2 + s.scroll
	first, last := st.Strip[0].Index, st.Strip[len(st.Strip)-1].Index
	if first == 0 {
		off = max(off, 0)
	}
	if last == st.Total-1 {
		off = min(off, content-s.view)
	}
	return off
}

// Handle implements [gunim.Handler]: a click on a photo goes to it, and
// the wheel scrolls the strip.
func (s *filmstrip) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if i := s.at(e.Pos); i >= 0 {
			u.Send(s, Jump{To: i})
		}
		return true
	case input.PointerMove:
		s.hot = s.at(e.Pos)
		u.Invalidate()
		return false
	case input.PointerLeave:
		s.hot = -1
		u.Invalidate()
	case input.Scroll:
		d := e.Delta.X + e.Delta.Y
		if d == 0 {
			d = -(e.Notches.X + e.Notches.Y) * 60
		}
		s.scroll += d
		u.Invalidate()
		return true
	}
	return false
}

// at is the photo at p, or -1.
func (s *filmstrip) at(p geom.Point) int {
	off := s.offsetNow
	for i, r := range s.laid {
		r = r.Add(geom.Pt(s.left-off, 0))
		if r.Contains(p) {
			return i
		}
	}
	return -1
}

// Paint implements [gunim.Node].
func (s *filmstrip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	// Glass, darker and more blurred than the rest's, with no shadow.
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Backdrop: 40, Clip: true, Radius: 11})()
		p.RRect(r, 11, paint.Solid(stripFill))
	}()
	p.RRectStroke(r, 11, paint.Fill{}, paint.Stroke{Width: 1, Color: stripEdge})
	if s.st.GroupCount > 0 {
		x := s.left - 8 - 1
		p.RRect(geom.Rc(x, stripPadY, 1, box.H-2*stripPadY), 0, paint.Solid(stripEdge))
	}
	kids.At(0).Paint(p)
	kids.At(1).Paint(p)
	content := float32(0)
	for _, rr := range s.laid {
		content = max(content, rr.Max.X)
	}
	off := s.offset(content)
	s.offsetNow = off
	// The photos, clipped to the strip's room for them, with a little more
	// above and below for the one grown.
	view := geom.Rc(s.left-4, 2, s.view+8, box.H-4)
	defer p.Layer(paint.LayerOpts{Bounds: view, Opacity: 1, Clip: true})()
	for kid := range kids.All {
		if l, ok := kid.Node().(*widget.Label); ok && l != s.groups && l != s.count && !s.isGap(l) {
			kid.Paint(p)
		}
	}
	for _, x := range s.gapPills {
		pill := geom.Rc(x+3, stripPadY+stripHeadH+5-2, stripPillW-6+6, stripThumbH+4)
		p.RRect(pill, 4, paint.Solid(color.NRGBA{R: 0x7c, G: 0x83, B: 0xff, A: 0x26}))
		p.RRectStroke(pill, 4, paint.Fill{}, paint.Stroke{Width: 1, Color: color.NRGBA{R: 0x7c, G: 0x83, B: 0xff, A: 0x4c}})
	}
	for kid := range kids.All {
		if l, ok := kid.Node().(*widget.Label); ok && s.isGap(l) && l.Text != "" {
			if c, ok := s.gapAt[l]; ok {
				func() {
					// On its side, reading up.
					defer p.Push(paint.Rotate(-math.Pi/2, c))()
					kid.Paint(p)
				}()
			}
		}
	}
	// The photo showing last, over its neighbours as it grows.
	var cur *Thumb
	for i := range s.st.Strip {
		t := &s.st.Strip[i]
		if t.Index == s.st.Index {
			cur = t
			continue
		}
		s.paintThumb(p, th, *t, off)
	}
	if cur != nil {
		s.paintThumb(p, th, *cur, off)
	}
}

// isGap reports whether l is a gap's pill's text.
func (s *filmstrip) isGap(l *widget.Label) bool {
	for _, h := range s.heads {
		if h.gap == l {
			return true
		}
	}
	return false
}

// paintThumb draws photo t, scrolled by off: its picture filling its place,
// a rejected one dimmed, grown and ringed where it shows, and its badges.
func (s *filmstrip) paintThumb(p *paint.Painter, th *theme.Live, t Thumb, off float32) {
	r, ok := s.laid[t.Index]
	if !ok {
		return
	}
	r = r.Add(geom.Pt(s.left-off, 0))
	k := float32(1)
	if g := s.grow[t.Index]; g != nil {
		k = g.Value()
	}
	if r.Max.X < -40 || r.Min.X > s.box.W+40 {
		return
	}
	defer p.Push(paint.Scale(k, r.Center()))()
	if k > 1.01 {
		p.ShadowRRect(r, 3, paint.Solid(color.NRGBA{A: 0xff}), paint.Shadow{Offset: geom.Pt(0, 5), Blur: 16, Color: color.NRGBA{A: 0x80}})
	}
	p.RRect(r, 3, paint.Solid(color.NRGBA{R: 8, G: 9, B: 11, A: 0xff}))
	if t.Img != nil {
		op := float32(1)
		if t.Flag == "exclude" {
			op = 0.4
		}
		p.Image(t.Img, r, paint.ImageOpts{Src: coverSrc(t.Img, r), Radius: 3, Opacity: op})
	}
	if t.Index == s.hot && t.Index != s.st.Index {
		p.RRect(r, 3, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x18}))
	}
	if t.Index == s.st.Index {
		p.RRectStroke(r.Inset(geom.Uniform(1)), 3, paint.Fill{}, paint.Stroke{Width: 2, Color: stripRing})
	}
	// The badges: the flag's square at the top right, the rating's stars at
	// the bottom left, the burst at the top left, soft and eyes at the
	// bottom right.
	if flagged(t.Flag) {
		p.RRect(geom.Rc(r.Max.X-8, r.Min.Y+2, 6, 6), 2, paint.Solid(flagInk(t.Flag)))
	}
	if t.Rating > 0 && r.Size().W > 22 {
		w := float32(4 + t.Rating*6)
		p.RRect(geom.Rc(r.Min.X+1, r.Max.Y-9, w, 8), 2, paint.Solid(color.NRGBA{A: 0x8c}))
		for i := range t.Rating {
			p.Mask(icon.Stroke{Icon: starLit, Width: 2, Progress: 1}, geom.Rc(r.Min.X+3+float32(i)*6, r.Max.Y-8.5, 6, 6), starInk)
		}
	}
	aids := t.Aids
	if s.v.st.Panel {
		// Developing, softness is no news, as in marraw.
		aids.Soft = false
	}
	paintStripAids(p, r, aids)
}
