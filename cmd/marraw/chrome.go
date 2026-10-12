package main

import (
	"fmt"
	"image/color"
	"slices"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The window's chrome is marraw's own, as its Electron shell draws it:
// no title bar of the system's or of gunim's, but pills of glass along the
// top, the window's buttons in one at the right, and the band between
// them a handle to move the window by.

// bandH is the height of the band along the top that moves the window,
// and pillTop how far down it the pills sit.
const (
	bandH   = 48
	pillTop = 12
)

// bareTitleBar is the title bar the window gets from gunim: nothing, as
// the views draw their own.
type bareTitleBar struct{}

// SetTitle implements [gunim.TitleBar].
func (bareTitleBar) SetTitle(string) {}

// Layout implements [gunim.Node].
func (bareTitleBar) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Size{})
}

// Paint implements [gunim.Node].
func (bareTitleBar) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children) {}

// dragBand is the band along the top of a view that moves the window: all
// of it but holes, where the pills are. Off, it is no handle, as the
// grid's while the cull view covers it.
type dragBand struct {
	holes []geom.Rect
	off   bool
}

// Layout implements [gunim.Node].
func (b *dragBand) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node]: it draws nothing, but painting is how
// gunim learns where it is.
func (b *dragBand) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children) {}

// Covers implements [gunim.Shaped]: the band takes no pointer itself, so
// what lies on it keeps its clicks. Moving the window is the system's
// work, on the CaptionRects it is told of.
func (b *dragBand) Covers(geom.Point) bool { return false }

// CaptionRects implements [gunim.Caption]: the band between the holes.
func (b *dragBand) CaptionRects(size geom.Size) []geom.Rect {
	if b.off {
		return nil
	}
	holes := slices.Clone(b.holes)
	slices.SortFunc(holes, func(a, c geom.Rect) int { return int(a.Min.X - c.Min.X) })
	var out []geom.Rect
	x := float32(0)
	for _, h := range holes {
		if h.Min.X > x {
			out = append(out, geom.Rc(x, 0, h.Min.X-x, size.H))
		}
		x = max(x, h.Max.X)
	}
	if x < size.W {
		out = append(out, geom.Rc(x, 0, size.W-x, size.H))
	}
	return out
}

// glassPill is its child on a pane of glass, pad round it.
type glassPill struct {
	child       gunim.Node
	radius, pad float32
}

// Children implements [gunim.Composite].
func (g *glassPill) Children() []gunim.Node { return []gunim.Node{g.child} }

// Layout implements [gunim.Node].
func (g *glassPill) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s := kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, c.Max.W-2*g.pad), max(0, c.Max.H-2*g.pad))))
	kids.At(0).Place(geom.Pt(g.pad, g.pad))
	return geom.Sz(s.W+2*g.pad, s.H+2*g.pad)
}

// Paint implements [gunim.Node].
func (g *glassPill) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	paintGlass(p, r, g.radius)
	p.RRectStroke(r.Inset(geom.Uniform(0.5)), g.radius-0.5, paint.Fill{}, paint.Stroke{Width: 1, Color: frost(0x18)})
	kids.At(0).Paint(p)
}

// newWindowPill is the window's minimize, maximize and close buttons in a
// pill of glass, for the top right of a view, after a button that pops
// the photo out into a window of its own, as marraw's has: for those who
// don't reach for Ctrl+N, and at hand in Cull and Develop too, where
// this pill is the only chrome. pop is that button, lit while the
// photo's window is open.
func newWindowPill() (node gunim.Node, pop *widget.IconButton) {
	pop = widget.NewIconButton(icon.PictureInPicture2, "Pop the photo out into its own window (Ctrl+N)")
	pop.KeepFocus, pop.OnClick = true, widget.Sends(ToggleViewer{})
	wc := widget.NewWindowControls()
	wc.Compact = true
	row := widget.Row(pop, wc)
	row.Cross = widget.CrossCenter
	th := marrawTheme().With(theme.Set(widget.TitleBarCompactHeight, 28), theme.Set(widget.ButtonHeight, 28), theme.Set(widget.IconSize, 14))
	return widget.NewThemed(&glassPill{child: &fixedHeight{h: 28, child: row}, radius: 9, pad: 3}, th), pop
}

// showPop lights pop while the photo's window is open, and says what a
// click on it does.
func showPop(pop *widget.IconButton, open bool) {
	pop.Active = open
	pop.Tooltip = map[bool]string{false: "Pop the photo out into its own window (Ctrl+N)", true: "Close the photo's window (Ctrl+N)"}[open]
}

// fixedHeight lays its child out h tall, as wide as it likes.
type fixedHeight struct {
	h     float32
	child gunim.Node
}

// Children implements [gunim.Composite].
func (f *fixedHeight) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node].
func (f *fixedHeight) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s := kids.At(0).Layout(gunim.Constraints{Min: geom.Sz(0, f.h), Max: geom.Sz(c.Max.W, f.h)})
	kids.At(0).Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (f *fixedHeight) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// GapPick sets the time gap that groups the photos, in minutes, nought
// for none.
type GapPick struct{ Minutes int }

// gapMenu are the gaps the gap control offers, with their hints, as
// marraw's.
var gapMenu = []struct {
	min  int
	hint string
}{{1, "bursts"}, {2, ""}, {5, "recommended"}, {10, ""}, {30, "scenes"}}

// gapControl groups the photos by the time between them, as marraw's: a
// pill saying "Group by gap" with the gap in a chip, that opens a menu of
// gaps.
type gapControl struct {
	label, value *widget.Label
	minutes      int
	hot          bool
	popup        *gunim.Popup
	menu         *widget.Menu
	size         geom.Size
	// compact leaves its words out, the chip alone, where room is short.
	compact bool
}

// Focusable implements [gunim.Focuser]: it keeps the keyboard while its
// menu is open, and hands the keys to the menu.
func (g *gapControl) Focusable() bool { return true }

// SkipsTab keeps Tab from stopping on it.
func (g *gapControl) SkipsTab() {}

// gapInk is the chip's.
var (
	gapChip    = color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x33}
	gapChipInk = theme.Color("marraw.gap.ink", color.NRGBA{R: 0xa8, G: 0xc8, B: 0xff, A: 0xff})
)

func newGapControl() *gapControl {
	g := &gapControl{label: newSmallLabel("Group by gap"), value: newSmallLabel("")}
	g.label.Color = noteInk
	g.value.Face, g.value.Color, g.value.NoWrap = widget.MonoFont, gapChipInk, true
	return g
}

// set shows minutes, nought for none.
func (g *gapControl) set(minutes int) {
	g.minutes = minutes
	if minutes <= 0 {
		g.value.Text = "Off ▾"
	} else {
		g.value.Text = fmt.Sprintf("%d min ▾", minutes)
	}
}

// Children implements [gunim.Composite].
func (g *gapControl) Children() []gunim.Node { return []gunim.Node{g.label, g.value} }

// Layout implements [gunim.Node].
func (g *gapControl) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const h, chipPad = 34, 7
	padX, gap := float32(12), float32(8)
	ls := kids.At(0).Layout(gunim.Loose(geom.Sz(160, h)))
	if g.compact {
		ls, padX, gap = geom.Size{}, 6, 0
		kids.At(0).Layout(gunim.Tight(geom.Size{}))
	}
	vs := kids.At(1).Layout(gunim.Loose(geom.Sz(100, h)))
	kids.At(0).Place(geom.Pt(padX, (h-ls.H)/2))
	kids.At(1).Place(geom.Pt(padX+ls.W+gap+chipPad, (h-vs.H)/2))
	g.size = geom.Sz(padX+ls.W+gap+vs.W+2*chipPad+padX, h)
	return g.size
}

// Paint implements [gunim.Node].
func (g *gapControl) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	paintGlass(p, r, 9)
	edge := frost(0x18)
	if g.hot {
		edge = frost(0x30)
	}
	p.RRectStroke(r.Inset(geom.Uniform(0.5)), 8.5, paint.Fill{}, paint.Stroke{Width: 1, Color: edge})
	v := kids.At(1)
	vs := v.Size()
	pad := float32(12)
	if g.compact {
		pad = 6
	}
	chip := geom.Rc(box.W-pad-vs.W-14, (box.H-vs.H)/2-2, vs.W+14, vs.H+4)
	p.RRect(chip, 5, paint.Solid(gapChip))
	if !g.compact {
		kids.At(0).Paint(p)
	}
	kids.At(1).Paint(p)
}

// Handle implements [gunim.Handler]: a press opens the menu of gaps.
func (g *gapControl) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		g.hot = true
		u.Invalidate()
	case input.PointerLeave:
		g.hot = false
		u.Invalidate()
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		g.open(u)
		return true
	case input.KeyPress:
		if g.popup == nil || !g.popup.Open() {
			return false
		}
		if e.Key == input.KeyEscape {
			g.popup.Close()
			return true
		}
		return g.menu.Key(e, u)
	}
	return false
}

// open shows the menu of gaps under the control.
func (g *gapControl) open(u *gunim.UI) {
	if g.popup != nil && g.popup.Open() {
		g.popup.Close()
		return
	}
	items := []widget.MenuItem{{Label: "New group when gap exceeds", Caption: true}}
	for _, c := range gapMenu {
		items = append(items, widget.MenuItem{Label: strconv.Itoa(c.min) + " min", Hint: c.hint, Checked: c.min == g.minutes})
	}
	custom := g.minutes > 0 && !slices.ContainsFunc(gapMenu, func(c struct {
		min  int
		hint string
	}) bool {
		return c.min == g.minutes
	})
	label := "Custom…"
	if custom {
		label = fmt.Sprintf("Custom: %d min…", g.minutes)
	}
	items = append(items, widget.MenuItem{Label: label, Checked: custom, Break: true},
		widget.MenuItem{Label: "Off — one flat grid", Checked: g.minutes <= 0, Break: true})
	m := widget.NewMenu(items)
	m.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		if g.popup != nil {
			g.popup.Close()
		}
		switch {
		case i >= 1 && i <= len(gapMenu):
			return GapPick{Minutes: gapMenu[i-1].min}
		case i == len(gapMenu)+1:
			return AskGapCustom{Minutes: g.minutes}
		case i == len(gapMenu)+2:
			return GapPick{Minutes: 0}
		}
		return nil
	}
	g.menu = m
	g.popup = u.OpenPopup(g, m, gunim.PopupOptions{
		Anchor:  geom.Rc(0, 0, g.size.W, g.size.H+4),
		Max:     geom.Sz(300, 480),
		Dismiss: func(*gunim.UI) { g.popup.Close() },
	})
	u.Focus(g)
}

// AskGapCustom asks for a gap of the user's own, in minutes.
type AskGapCustom struct{ Minutes int }

// jumpButton opens the command palette, as marraw's "Jump to anything":
// its words and the shortcut in a chip, on glass.
type jumpButton struct {
	label, key *widget.Label
	hot        bool
	act        func(u *gunim.UI)
}

func newJumpButton(label string) *jumpButton {
	j := &jumpButton{label: newSmallLabel(label), key: newSmallLabel("Ctrl+K")}
	j.label.Color = noteInk
	j.key.Face, j.key.Color = widget.MonoFont, noteInk
	return j
}

// Children implements [gunim.Composite].
func (j *jumpButton) Children() []gunim.Node { return []gunim.Node{j.label, j.key} }

// Layout implements [gunim.Node].
func (j *jumpButton) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const h, padX, gap, chipPad = 34, 12, 10, 6
	ls := kids.At(0).Layout(gunim.Loose(geom.Sz(200, h)))
	ks := kids.At(1).Layout(gunim.Loose(geom.Sz(80, h)))
	kids.At(0).Place(geom.Pt(padX, (h-ls.H)/2))
	kids.At(1).Place(geom.Pt(padX+ls.W+gap+chipPad, (h-ks.H)/2))
	return geom.Sz(padX+ls.W+gap+ks.W+2*chipPad+padX, h)
}

// Paint implements [gunim.Node].
func (j *jumpButton) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	paintGlass(p, r, 9)
	edge := frost(0x18)
	if j.hot {
		edge = frost(0x30)
	}
	p.RRectStroke(r.Inset(geom.Uniform(0.5)), 8.5, paint.Fill{}, paint.Stroke{Width: 1, Color: edge})
	k := kids.At(1)
	ks := k.Size()
	chip := geom.Rc(box.W-12-ks.W-12, (box.H-ks.H)/2-2, ks.W+12, ks.H+4)
	p.RRectStroke(chip, 5, paint.Solid(frost(0x0c)), paint.Stroke{Width: 1, Color: frost(0x20)})
	for k := range kids.All {
		k.Paint(p)
	}
}

// Handle implements [gunim.Handler].
func (j *jumpButton) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		j.hot = true
		u.Invalidate()
	case input.PointerLeave:
		j.hot = false
		u.Invalidate()
	case input.PointerDown:
		if e.Button == input.ButtonPrimary && j.act != nil {
			j.act(u)
			return true
		}
	}
	return false
}

// spacerW is room w wide in a row.
func spacerW(w float32) gunim.Node { return &fixedWidth{w: w, child: widget.NewSpacer()} }

// frostedTrack is a segmented control on a track of frosted glass of its
// own, with no panel round it: the glass blurs what lies behind, a photo
// or the window's ground, and a hairline edges it.
type frostedTrack struct{ child gunim.Node }

// frosted is seg on a frosted track, in th with the control's own track
// left clear.
func frosted(seg gunim.Node, th theme.Theme) gunim.Node {
	return widget.NewThemed(&frostedTrack{child: seg}, th.With(theme.Set(widget.SegmentedTrack, color.NRGBA{})))
}

// Children implements [gunim.Composite].
func (t *frostedTrack) Children() []gunim.Node { return []gunim.Node{t.child} }

// Layout implements [gunim.Node].
func (t *frostedTrack) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s := kids.At(0).Layout(c)
	kids.At(0).Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (t *frostedTrack) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	paintFrostedTrack(p, geom.Rect{Max: box.Point()})
	kids.At(0).Paint(p)
}

// paintFrostedTrack draws a pill-shaped track of frosted glass in r.
func paintFrostedTrack(p *paint.Painter, r geom.Rect) {
	radius := r.Size().H / 2
	paintGlass(p, r, radius)
	p.RRectStroke(r.Inset(geom.Uniform(0.5)), radius-0.5, paint.Fill{}, paint.Stroke{Width: 1, Color: frost(0x1c)})
}
