package main

import (
	"fmt"
	"image/color"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The cull view's chrome, as marraw's: along the top, the shoot's pill at
// the left, the mode switcher in the middle, and Group by gap and the
// window's buttons at the right; at the foot, over the filmstrip, the
// confirm bar with the photo's name and stars, Pick and Reject, and the
// zoom.

// shootPill is the pill at the top left: marraw's mark, the shoot's
// name, the time the photo was taken and its frame number, and a dot that
// is green while the backend answers.
type shootPill struct {
	name, status *widget.Label
}

// statusInk is the status's, as marraw's.
var statusInk = theme.Color("marraw.status.ink", color.NRGBA{R: 0xaa, G: 0xb0, B: 0xff, A: 0xff})

func newShootPill() *shootPill {
	p := &shootPill{name: widget.NewLabel(""), status: newSmallLabel("")}
	p.name.Size, p.name.MaxLines, p.name.NoWrap = noteSize, 1, true
	p.status.Face, p.status.Color, p.status.NoWrap = widget.MonoFont, statusInk, true
	return p
}

// set shows the shoot, and the photo: when it was taken and its frame.
func (p *shootPill) set(shoot, taken string, frame int, develop bool, file string) {
	p.name.Text = shoot
	switch {
	case develop:
		p.status.Text = file
	case taken != "":
		p.status.Text = fmt.Sprintf("%s · frame %d", taken, frame)
	default:
		p.status.Text = fmt.Sprintf("frame %d", frame)
	}
}

// Children implements [gunim.Composite].
func (p *shootPill) Children() []gunim.Node { return []gunim.Node{p.name, p.status} }

// Layout implements [gunim.Node].
func (p *shootPill) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const h, padX, mark, gap = 34, 12, 18, 10
	ns := kids.At(0).Layout(gunim.Loose(geom.Sz(min(c.Max.W/3, 320), h)))
	ss := kids.At(1).Layout(gunim.Loose(geom.Sz(300, h)))
	x := float32(padX + mark + gap)
	kids.At(0).Place(geom.Pt(x, (h-ns.H)/2))
	x += ns.W + gap
	kids.At(1).Place(geom.Pt(x, (h-ss.H)/2))
	x += ss.W + gap + 6 + padX
	return geom.Sz(x, h)
}

// Paint implements [gunim.Node].
func (p *shootPill) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	paintGlass(pt, r, 9)
	pt.RRectStroke(r.Inset(geom.Uniform(0.5)), 8.5, paint.Fill{}, paint.Stroke{Width: 1, Color: frost(0x18)})
	// marraw's mark: a small square of the accent with its m.
	m := geom.Rc(12, (box.H-18)/2, 18, 18)
	pt.RRect(m, 5, paint.Solid(color.NRGBA{R: 0x6c, G: 0x63, B: 0xff, A: 0xff}))
	if ic := appMark(); ic != nil {
		pt.Image(ic, m, paint.ImageOpts{Opacity: 1, Radius: 5})
	}
	for k := range kids.All {
		k.Paint(pt)
	}
	pt.RRect(geom.Rc(box.W-12-6, box.H/2-3, 6, 6), 3, paint.Solid(doneInk))
}

// confirmBar is the glass bar at the foot of the photo, as marraw's: the
// photo's name and its stars, Pick and Reject, and the zoom: Fit or 1:1,
// a slider, and the zoom in percent.
type confirmBar struct {
	name    *widget.Label
	pick    *widget.Button
	reject  *widget.Button
	fit     *widget.Segmented
	zoom    *widget.Slider
	percent *widget.Label
	stars   *starRow
	kids    []gunim.Node
	// zoomX is where the zoom starts, for the rule before it.
	zoomX float32
	// onZoom zooms to a percent; onFit fits or goes to 1:1.
	onZoom func(pct float32, u *gunim.UI)
	onFit  func(one bool, u *gunim.UI)
}

// pickGreen and rejectRed are the bar's buttons' inks, as marraw's.
var (
	pickGreen = color.NRGBA{R: 0x34, G: 0xd3, B: 0x99, A: 0xff}
	rejectRed = color.NRGBA{R: 0xf0, G: 0x70, B: 0x70, A: 0xff}
)

func newConfirmBar() *confirmBar {
	b := &confirmBar{name: newSmallLabel(""), pick: widget.NewButton("Pick  P"), reject: widget.NewButton("Reject  X"),
		fit: widget.NewSegmented("Fit", "1:1"), zoom: widget.NewSlider(5, 400), percent: newSmallLabel(""), stars: newStarRow()}
	b.name.Face, b.name.MaxLines, b.name.NoWrap = widget.MonoFont, 1, true
	b.percent.Face, b.percent.NoWrap = widget.MonoFont, true
	b.pick.KeepFocus, b.reject.KeepFocus, b.fit.KeepFocus, b.zoom.KeepFocus = true, true, true, true
	b.pick.Ink = theme.Color("marraw.pick.ink", pickGreen)
	b.reject.Ink = theme.Color("marraw.reject.ink", rejectRed)
	b.pick.OnClick = widget.Sends(Mark{Flag: "pick"})
	b.reject.OnClick = widget.Sends(Mark{Flag: "exclude"})
	b.zoom.Snap = 5
	b.zoom.OnChange = func(x float32, u *gunim.UI) gunim.Intent {
		if b.onZoom != nil {
			b.onZoom(x, u)
		}
		return nil
	}
	b.fit.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		if b.onFit != nil {
			b.onFit(i == 1, u)
		}
		return nil
	}
	pickTh := marrawTheme().With(theme.Set(widget.ButtonHeight, 34), theme.Set(widget.ButtonPadding, 16),
		theme.Set(widget.ButtonRadius, 8), theme.Set(widget.TextSize, 12.5),
		theme.Set(widget.ButtonFill, withAlpha(pickGreen, 0.15)), theme.Set(widget.ButtonHover, withAlpha(pickGreen, 0.25)))
	rejTh := pickTh.With(theme.Set(widget.ButtonFill, withAlpha(rejectRed, 0.10)), theme.Set(widget.ButtonHover, withAlpha(rejectRed, 0.2)))
	segTh := glassSegments(marrawTheme().With(theme.Set(widget.SegmentedHeight, 26), theme.Set(widget.TextSize, 11.5)))
	b.kids = []gunim.Node{b.name, b.stars,
		widget.NewThemed(&tintEdge{child: b.pick, ink: pickGreen}, pickTh),
		widget.NewThemed(&tintEdge{child: b.reject, ink: rejectRed}, rejTh),
		widget.NewThemed(b.fit, segTh), b.zoom, b.percent}
	return b
}

// set shows the photo's name, its rating and flag, and the zoom in
// percent, fitted or not.
func (b *confirmBar) set(name string, rating int, flag string, pct float32, fitted bool, u *gunim.UI) {
	b.name.Text = name
	b.stars.set(rating, u)
	b.pick.Active, b.reject.Active = flag == "pick", flag == "exclude"
	b.percent.Text = fmt.Sprintf("%.0f%%", pct)
	if !b.zoom.Held() {
		b.zoom.SetValue(min(max(pct, 5), 400), u)
	}
	b.fit.SetSelected(map[bool]int{true: 0, false: 1}[fitted], u)
	if !fitted && pct < 99.5 || pct > 100.5 {
		if !fitted {
			b.fit.SetSelected(-1, u)
		}
	}
}

// Children implements [gunim.Composite].
func (b *confirmBar) Children() []gunim.Node { return b.kids }

// Layout implements [gunim.Node]: the name over the stars, a rule, Pick
// and Reject, a rule, and the zoom.
func (b *confirmBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const padX, padY, gap, h = 18, 12, 16, 40
	mid := float32(padY + h/2)
	x := float32(padX)
	ns := kids.At(0).Layout(gunim.Tight(geom.Sz(112, 16)))
	kids.At(0).Place(geom.Pt(x, padY))
	ss := kids.At(1).Layout(gunim.Loose(geom.Sz(112, 20)))
	kids.At(1).Place(geom.Pt(x-2, padY+ns.H+3))
	x += 112 + gap + 1 + gap
	for i := 2; i <= 3; i++ {
		s := kids.At(i).Layout(gunim.Loose(geom.Sz(200, h)))
		kids.At(i).Place(geom.Pt(x, mid-s.H/2))
		x += s.W + 8
	}
	x += gap - 8 + 1 + gap
	fs := kids.At(4).Layout(gunim.Loose(geom.Sz(120, h)))
	kids.At(4).Place(geom.Pt(x, mid-fs.H/2))
	b.zoomX = x
	x += fs.W + 10
	zs := kids.At(5).Layout(gunim.Tight(geom.Sz(110, 24)))
	kids.At(5).Place(geom.Pt(x, mid-zs.H/2))
	x += 110 + 10
	ps := kids.At(6).Layout(gunim.Loose(geom.Sz(44, 20)))
	kids.At(6).Place(geom.Pt(x+40-ps.W, mid-ps.H/2))
	x += 40 + padX
	_ = ss
	return geom.Sz(x, h+2*padY)
}

// Paint implements [gunim.Node].
func (b *confirmBar) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	paintGlass(p, r, 14)
	p.RRectStroke(r.Inset(geom.Uniform(0.5)), 13.5, paint.Fill{}, paint.Stroke{Width: 1, Color: frost(0x18)})
	rule := func(x float32) { p.RRect(geom.Rc(x, (box.H-36)/2, 1, 36), 0, paint.Solid(frost(0x26))) }
	rule(18 + 112 + 16)
	rule(b.zoomX - 16 - 1)
	for k := range kids.All {
		k.Paint(p)
	}
}

// tintEdge is a button with a hairline edge of its own ink, stronger when
// it is on, as marraw's Pick and Reject.
type tintEdge struct {
	child *widget.Button
	ink   color.NRGBA
}

// Children implements [gunim.Composite].
func (t *tintEdge) Children() []gunim.Node { return []gunim.Node{t.child} }

// Layout implements [gunim.Node].
func (t *tintEdge) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s := kids.At(0).Layout(c)
	kids.At(0).Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (t *tintEdge) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	if t.child.Active {
		p.RRect(r, 8, paint.Solid(withAlpha(t.ink, 0.25)))
	}
	kids.At(0).Paint(p)
	a := float32(0.45)
	if t.child.Active {
		a = 1
	}
	p.RRectStroke(r.Inset(geom.Uniform(0.5)), 7.5, paint.Fill{}, paint.Stroke{Width: 1, Color: withAlpha(t.ink, a)})
}

// starRow is five stars to rate the photo with, a click on its rating
// clearing it.
type starRow struct {
	n   int
	hot int
}

func newStarRow() *starRow { return &starRow{hot: -1} }

// set shows rating n.
func (s *starRow) set(n int, u *gunim.UI) {
	if n != s.n {
		s.n = n
		u.Invalidate()
	}
}

// Layout implements [gunim.Node].
func (s *starRow) Layout(gunim.Constraints, gunim.Frame, gunim.Children) geom.Size {
	return geom.Sz(5*18, 18)
}

// Handle implements [gunim.Handler].
func (s *starRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		s.hot = int(e.Pos.X / 18)
		u.Invalidate()
	case input.PointerLeave:
		s.hot = -1
		u.Invalidate()
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		k := min(max(int(e.Pos.X/18)+1, 1), 5)
		u.Send(s, Rate{Stars: k, Toggle: true})
		return true
	}
	return false
}

// Paint implements [gunim.Node].
func (s *starRow) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, _ gunim.Children) {
	lit := s.n
	if s.hot >= 0 {
		lit = s.hot + 1
	}
	for i := range 5 {
		r := geom.Rc(float32(i)*18+2, 2, 14, 14)
		if i < lit {
			p.Mask(icon.Stroke{Icon: starLit, Width: 1.5, Progress: 1}, r, starInk)
		} else {
			p.Mask(icon.Stroke{Icon: icon.Star, Width: 1.5, Progress: 1}, r, withAlpha(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.3))
		}
	}
}

// takenAt is when a photo was taken, as the pill says it: hours and
// minutes, or "".
func takenAt(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).Format("15:04")
}
