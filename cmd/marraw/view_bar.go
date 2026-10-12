package main

import (
	"fmt"
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// barHeight is the filter bar's band under the grid's heading, and
// gridTop where the tiles begin.
const (
	barHeight = 44
	gridTop   = gridHeadHeight + barHeight
)

// The filter bar's flags, as marraw's filter bar has them.
var (
	flagNames = []string{"All", "Picks", "Not rejected", "Rejected"}
	flagKeys  = []string{"all", "pick", "not-excluded", "exclude"}
)

// gridBar is the filter bar over the tiles: the order, by capture time or
// by name, each way; the flags shown; the least rating shown, as stars;
// the culling aids; then the time gap that groups them, the tiles' size,
// and the info panel's button. A change of the view carries the tiles to
// their new places.
type gridBar struct {
	view  LibView
	sorts *sortButtons
	flags *widget.Segmented
	stars *starFilter
	// starBox holds the stars, put away where the bar has no room, and
	// flagBox the flags, measured to know how far they can squeeze.
	starBox *hideable
	flagBox *measured
	size    *widget.Slider
	row     gunim.Node
	// soft, blinks and bursts show only the soft photos, those with eyes
	// closed, and each burst's sharpest frame; judge judges the bursts,
	// and eyes and subjects start the backend looking.
	soft, blinks, bursts  *widget.IconButton
	judge, eyes, subjects *widget.IconButton
	// hide are the buttons each culling aid has, which go while it is
	// turned off.
	hide map[string][]*hideable
	// railBtn puts the library away or brings it back, as railHidden
	// says it is.
	railBtn    *widget.IconButton
	railHidden bool
	// gapCtl groups the photos by the time between them, and panelBtn
	// puts the info panel away or brings it back. sizeBox holds the
	// tiles' size slider and sizeN its width in pixels.
	gapCtl *gapControl
	// gapBox holds the gap control, put away while a folder's photos
	// show, grouped a shoot each.
	gapBox   *hideable
	panelBtn *widget.IconButton
	sizeBox  *hideable
	sizeN    *widget.Label
	right    gunim.Node
}

// barRow is the bar's controls: the filters at the left, the gap, the
// tiles' size and the info panel's button at the right; the size goes
// when the two would meet.
type barRow struct {
	left, right gunim.Node
	bar         *gridBar
}

// Children implements [gunim.Composite].
func (r *barRow) Children() []gunim.Node { return []gunim.Node{r.left, r.right} }

// Layout implements [gunim.Node].
func (r *barRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	// The left at its natural width, the flags' among it.
	r.bar.starBox.hidden = false
	ls := kids.At(0).Layout(gunim.Loose(geom.Sz(1e5, c.Max.H)))
	flags := r.bar.flagBox.last.W
	// Short of room, the size slider goes first, then the gap's words,
	// leaving its chip.
	r.bar.sizeBox.hidden, r.bar.gapCtl.compact = false, false
	rs := kids.At(1).Layout(gunim.Loose(geom.Sz(w, c.Max.H)))
	if ls.W+16+rs.W > w {
		r.bar.sizeBox.hidden = true
		rs = kids.At(1).Layout(gunim.Loose(geom.Sz(w, c.Max.H)))
	}
	if ls.W+16+rs.W > w {
		r.bar.gapCtl.compact = true
		rs = kids.At(1).Layout(gunim.Loose(geom.Sz(w, c.Max.H)))
	}
	if room := max(0, w-16-rs.W); ls.W > room {
		// Still short: the flags squeeze into what the right leaves, to
		// a short word each, and past that the stars go.
		const leastFlags = 200
		if ls.W-flags+leastFlags > room {
			r.bar.starBox.hidden = true
		}
		ls = kids.At(0).Layout(gunim.Loose(geom.Sz(room, c.Max.H)))
	}
	h := max(ls.H, rs.H)
	kids.At(0).Place(geom.Pt(0, (h-ls.H)/2))
	kids.At(1).Place(geom.Pt(w-rs.W, (h-rs.H)/2))
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (r *barRow) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	kids.At(1).Paint(p)
}

// showSize says the tiles' width in pixels.
func (b *gridBar) showSize(w float32) { b.sizeN.Text = fmt.Sprintf("%.0fpx", w) }

// setAside shows whether the info panel is out.
func (b *gridBar) setAside(on bool, u *gunim.UI) {
	b.panelBtn.Active = on
	b.panelBtn.Tooltip = map[bool]string{false: "Show the info panel", true: "Hide the info panel"}[on]
	u.Invalidate()
}

// setRail shows whether the library is put away.
func (b *gridBar) setRail(hidden bool, u *gunim.UI) {
	b.railHidden = hidden
	b.railBtn.Active = !hidden
	b.railBtn.Tooltip = map[bool]string{false: "Hide the library", true: "Show the library"}[hidden]
	u.Invalidate()
}

// setOff hides the buttons of the culling aids off.
func (b *gridBar) setOff(off map[string]bool, u *gunim.UI) {
	for id, hs := range b.hide {
		for _, h := range hs {
			h.hidden = off[id]
		}
	}
	u.Invalidate()
}

// hideable is a node that takes no room while hidden.
type hideable struct {
	child  gunim.Node
	hidden bool
}

// Children implements [gunim.Composite].
func (h *hideable) Children() []gunim.Node { return []gunim.Node{h.child} }

// Layout implements [gunim.Node].
func (h *hideable) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	if h.hidden {
		kids.At(0).Layout(gunim.Tight(geom.Size{}))
		kids.At(0).Place(geom.Pt(-10000, 0))
		return geom.Size{}
	}
	s := kids.At(0).Layout(c)
	kids.At(0).Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (h *hideable) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if !h.hidden {
		kids.At(0).Paint(p)
	}
}

// rigid is a node that keeps its natural width in a row short of room,
// for one that cannot draw narrower.
type rigid struct {
	child gunim.Node
	last  geom.Size
}

// Children implements [gunim.Composite].
func (r *rigid) Children() []gunim.Node { return []gunim.Node{r.child} }

// MinWidth implements [widget.Shrinker]: its width as last laid out,
// which a row asks after laying it out at its natural size.
func (r *rigid) MinWidth(gunim.Frame) float32 { return r.last.W }

// Layout implements [gunim.Node].
func (r *rigid) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	r.last = kids.At(0).Layout(gunim.Loose(geom.Sz(1e5, c.Max.H)))
	kids.At(0).Place(geom.Point{})
	return r.last
}

// Paint implements [gunim.Node].
func (r *rigid) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// measured is a node that keeps the size it was last laid out at.
type measured struct {
	child gunim.Node
	last  geom.Size
}

// Children implements [gunim.Composite].
func (m *measured) Children() []gunim.Node { return []gunim.Node{m.child} }

// Layout implements [gunim.Node].
func (m *measured) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	m.last = kids.At(0).Layout(c)
	kids.At(0).Place(geom.Point{})
	return m.last
}

// Paint implements [gunim.Node].
func (m *measured) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

func newGridBar(g *gridView) *gridBar {
	b := &gridBar{sorts: newSortButtons(), flags: widget.NewSegmented(flagNames...), stars: newStarFilter(),
		size: widget.NewSlider(tileMin, tileMax)}
	b.sorts.onSort = func(key string) gunim.Intent {
		v := b.view
		v.Sort = key
		return SetLibView{View: v}
	}
	b.flags.KeepFocus = true
	b.flags.OnChange = func(i int, _ *gunim.UI) gunim.Intent {
		v := b.view
		v.Flag = flagKeys[i]
		return SetLibView{View: v}
	}
	b.stars.onPick = func(n int) gunim.Intent {
		v := b.view
		v.MinRating = n
		return SetLibView{View: v}
	}
	// The tiles' size is the window's own: they spring to it.
	b.size.KeepFocus = true
	b.size.SetValue(200, nil)
	b.sizeN = newSmallLabel("")
	b.showSize(200)
	b.size.Snap = 10
	b.size.OnChange = func(x float32, u *gunim.UI) gunim.Intent {
		g.grid.Size = cellSize(x)
		b.showSize(x)
		u.Invalidate()
		return nil
	}
	toggle := func(ic *icon.Icon, tip string, flip func(*LibView)) *widget.IconButton {
		bt := widget.NewIconButton(ic, tip)
		bt.KeepFocus = true
		bt.OnClick = func(*gunim.UI) gunim.Intent {
			v := b.view
			flip(&v)
			return SetLibView{View: v}
		}
		return bt
	}
	act := func(ic *icon.Icon, tip string, in gunim.Intent) *widget.IconButton {
		bt := widget.NewIconButton(ic, tip)
		bt.KeepFocus, bt.OnClick = true, widget.Sends(in)
		return bt
	}
	b.soft = toggle(icon.Focus, "Soft photos only", func(v *LibView) { v.Soft = !v.Soft })
	b.blinks = toggle(icon.EyeClosed, "Photos with closed eyes only", func(v *LibView) { v.Blinks = !v.Blinks })
	b.bursts = toggle(icon.Layers, "The sharpest frame of each burst only", func(v *LibView) { v.Collapse = !v.Collapse })
	b.judge = act(icon.WandSparkles, "Judge the bursts: pick each one's sharpest frame, reject the rest", JudgeBursts{})
	b.eyes = act(icon.ScanEye, "Look for closed eyes in the photos not checked yet", CheckEyes{})
	b.subjects = act(icon.ScanFace, "Find each photo's subject, to judge its sharpness there", CheckSubjects{})
	b.hide = map[string][]*hideable{}
	wrap := func(n gunim.Node, feature string) gunim.Node {
		h := &hideable{child: n}
		b.hide[feature] = append(b.hide[feature], h)
		return h
	}
	aids := widget.Row(wrap(b.soft, "softFilter"), wrap(b.blinks, "eyes"), wrap(b.bursts, "bursts"), &divider{},
		wrap(b.judge, "bursts"), wrap(b.eyes, "eyes"), wrap(b.subjects, "subjects"))
	aids.Cross = widget.CrossCenter
	b.railBtn = widget.NewIconButton(icon.PanelLeft, "Hide the library")
	b.railBtn.KeepFocus = true
	b.railBtn.OnClick = func(*gunim.UI) gunim.Intent { return RailHide{Hidden: !b.railHidden} }
	b.gapCtl = newGapControl()
	b.panelBtn = widget.NewIconButton(icon.PanelRight, "Hide the info panel")
	b.panelBtn.KeepFocus, b.panelBtn.Active = true, true
	b.panelBtn.OnClick = widget.Sends(AsideToggle{})
	// The tiles' size, as marraw's: a slider and its width in pixels,
	// put away when the bar has no room for it.
	b.sizeN.Face, b.sizeN.Color, b.sizeN.NoWrap = widget.MonoFont, noteInk, true
	sizeRow := widget.Row(&fixedWidth{w: 96, child: b.size}, &fixedWidth{w: 44, child: b.sizeN})
	sizeRow.Cross = widget.CrossCenter
	b.sizeBox = &hideable{child: sizeRow}
	// Short of room only the flags squeeze; the stars go once they can
	// squeeze no more, and the rest keep their width.
	b.starBox = &hideable{child: b.stars}
	b.flagBox = &measured{child: frosted(b.flags, marrawTheme())}
	left := widget.Row(&rigid{child: b.railBtn}, &rigid{child: b.sorts}, b.flagBox, &rigid{child: b.starBox}, &rigid{child: aids})
	left.Cross = widget.CrossCenter
	b.gapBox = &hideable{child: b.gapCtl}
	right := widget.Row(b.gapBox, spacerW(4), b.sizeBox, b.panelBtn)
	right.Cross = widget.CrossCenter
	b.right = right
	row := &barRow{left: left, right: right, bar: b}
	b.row = row
	return b
}

// set shows view v.
func (b *gridBar) set(v LibView, u *gunim.UI) {
	b.view = v
	b.sorts.set(v.Sort, u)
	for i, k := range flagKeys {
		if k == v.Flag {
			b.flags.SetSelected(i, u)
		}
	}
	b.stars.set(v.MinRating, u)
	b.soft.Active, b.blinks.Active, b.bursts.Active = v.Soft, v.Blinks, v.Collapse
	b.gapCtl.set(v.Gap)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (b *gridBar) Children() []gunim.Node { return []gunim.Node{b.row} }

// Layout implements [gunim.Node].
func (b *gridBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Constraints{Min: geom.Sz(c.Max.W-36, 0), Max: geom.Sz(c.Max.W-36, barHeight)})
	k.Place(geom.Pt(20, (barHeight-s.H)/2))
	return geom.Sz(c.Max.W, barHeight)
}

// Paint implements [gunim.Node].
func (b *gridBar) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
	kids.At(0).Paint(p)
}

// sortButtons are the two orders, by capture time and by file name, side
// by side. The one in use is lit, with an arrow for its way, oldest or A
// first up; a click on it turns the order about, the arrow turning with
// it, and a click on the other sorts by that, its own way as last used.
type sortButtons struct {
	anim.Group
	key string
	// at slides the light between the buttons, and turn turns the arrow:
	// nought up, one down.
	at, turn *anim.Float
	hot      int
	// glow is how far each button has lit under the pointer, fading in
	// and out as the switchers' labels do.
	glow [2]*anim.Float
	// way is each order's way as last used: true for newest or Z first.
	way    [2]bool
	widths [2]float32
	labels [2]*widget.Label
	onSort func(key string) gunim.Intent
}

func newSortButtons() *sortButtons {
	s := &sortButtons{at: anim.NewFloat(0), turn: anim.NewFloat(0), hot: -1,
		labels: [2]*widget.Label{widget.NewLabel("Time"), widget.NewLabel("Name")},
		glow:   [2]*anim.Float{anim.NewFloat(0), anim.NewFloat(0)}}
	s.Add(s.at, s.turn, s.glow[0], s.glow[1])
	return s
}

// Children implements [gunim.Composite].
func (s *sortButtons) Children() []gunim.Node { return []gunim.Node{s.labels[0], s.labels[1]} }

// sortKey is order i's key, its way down or not.
func sortKey(i int, down bool) string {
	return [2][2]string{{"captureAsc", "captureDesc"}, {"nameAsc", "nameDesc"}}[i][map[bool]int{false: 0, true: 1}[down]]
}

// sortOf is key's order, and whether it runs down.
func sortOf(key string) (int, bool) {
	switch key {
	case "captureDesc":
		return 0, true
	case "nameAsc":
		return 1, false
	case "nameDesc":
		return 1, true
	}
	return 0, false
}

func (s *sortButtons) set(key string, u *gunim.UI) {
	if key == s.key {
		return
	}
	first := s.key == ""
	s.key = key
	i, down := sortOf(key)
	s.way[i] = down
	if first {
		s.at.Jump(float32(i))
		s.turn.Jump(on(down))
		return
	}
	s.at.Animate(float32(i), widget.Quick.Get(u.Theme()))
	s.turn.Animate(on(down), widget.Bounce.Get(u.Theme()))
}

const (
	sortPad   = 12
	sortArrow = 14
	sortH     = 28
)

// button is the button at x, or -1.
func (s *sortButtons) button(x float32) int {
	if x < 0 {
		return -1
	}
	if x < s.widths[0] {
		return 0
	}
	if x < s.widths[0]+s.widths[1] {
		return 1
	}
	return -1
}

// Layout implements [gunim.Node].
func (s *sortButtons) Layout(_ gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	x := float32(0)
	for i := range 2 {
		k := kids.At(i)
		ls := k.Layout(gunim.Loose(geom.Sz(200, sortH)))
		k.Place(geom.Pt(x+sortPad, (sortH-ls.H)/2))
		s.widths[i] = ls.W + 2*sortPad + sortArrow + 4
		x += s.widths[i]
	}
	return geom.Sz(s.widths[0]+s.widths[1], sortH)
}

// setHot lights button i under the pointer, or none for -1: quickly in,
// more slowly out.
func (s *sortButtons) setHot(i int, u *gunim.UI) {
	if i == s.hot {
		return
	}
	s.hot = i
	for j, g := range s.glow {
		if j == i {
			g.Animate(1, widget.Quick.Get(u.Theme()))
		} else {
			g.Animate(0, widget.Settle.Get(u.Theme()))
		}
	}
	u.Invalidate()
}

// Handle implements [gunim.Handler].
func (s *sortButtons) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		s.setHot(s.button(e.Pos.X), u)
		return true
	case input.PointerLeave:
		s.setHot(-1, u)
	case input.PointerDown:
		i := s.button(e.Pos.X)
		if e.Button != input.ButtonPrimary || i < 0 || s.onSort == nil {
			return false
		}
		cur, down := sortOf(s.key)
		if i == cur {
			down = !down
		} else {
			down = s.way[i]
		}
		u.Send(s, s.onSort(sortKey(i, down)))
		return true
	}
	return false
}

// Paint implements [gunim.Node].
func (s *sortButtons) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	paintFrostedTrack(p, geom.Rect{Max: box.Point()})
	at := s.at.Value()
	x := at * s.widths[0]
	w := s.widths[0] + (s.widths[1]-s.widths[0])*at
	p.RRect(geom.Rc(x+2, 2, w-4, box.H-4), (box.H-4)/2, paint.Solid(color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x40}))
	left := float32(0)
	for i := range 2 {
		lit := 1 - min(float32(math.Abs(float64(at-float32(i)))), 1)
		k := kids.At(i)
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 0.6 + 0.4*max(lit, s.glow[i].Value()*0.5)})()
			k.Paint(p)
		}()
		ink := widget.Ink.Get(th)
		// The arrow, lit with the button, turns as the order does.
		ar := geom.Rc(left+sortPad+k.Size().W+4, (box.H-sortArrow)/2, sortArrow, sortArrow)
		turn := s.turn.Value()
		if i != int(math.Round(float64(at))) {
			turn = on(s.way[i])
		}
		func() {
			defer p.Push(paint.Rotate(math.Pi*turn, ar.Center()))()
			p.Mask(icon.Stroke{Icon: icon.ArrowUp, Width: 2.2, Progress: 1}, ar, withAlpha(ink, 0.25+0.75*lit))
		}()
		left += s.widths[i]
	}
}

// starFilter is the least rating shown, as five stars: a click on one
// shows the photos rated so at least, and on the one lit last shows all
// again. The pointer over them lights the ones a click would.
type starFilter struct {
	anim.Group
	marks  *marks
	n      int
	onPick func(n int) gunim.Intent
}

func newStarFilter() *starFilter {
	s := &starFilter{}
	s.marks = newMarks(&s.Group, 0, "")
	return s
}

// starPlace is where the filter's stars are.
var starPlace = markPlace{stars: geom.Pt(10, sortH/2), star: 15, gap: 4}

func (s *starFilter) set(n int, u *gunim.UI) {
	if n == s.n {
		return
	}
	s.n = n
	s.marks.set(n, "", u.Theme())
}

// Layout implements [gunim.Node].
func (s *starFilter) Layout(gunim.Constraints, gunim.Frame, gunim.Children) geom.Size {
	return geom.Sz(20+5*starPlace.star+4*starPlace.gap, sortH)
}

// Handle implements [gunim.Handler].
func (s *starFilter) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		s.marks.hover(e.Pos, true, starPlace, u.Theme())
	case input.PointerMove:
		s.marks.hover(e.Pos, true, starPlace, u.Theme())
	case input.PointerLeave:
		s.marks.hover(geom.Point{}, false, starPlace, u.Theme())
	case input.PointerDown:
		k := starPlace.starAt(e.Pos)
		if e.Button != input.ButtonPrimary || k == 0 || s.onPick == nil {
			return false
		}
		if k == s.n {
			k = 0
		}
		u.Send(s, s.onPick(k))
		return true
	default:
		return false
	}
	u.Invalidate()
	return false
}

// Paint implements [gunim.Node].
func (s *starFilter) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, box.H/2, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0c}))
	s.marks.paintStars(p, f.Theme, starPlace, 0x90)
}

// fixedWidth lays its child out w wide.
type fixedWidth struct {
	w     float32
	child gunim.Node
}

// Children implements [gunim.Composite].
func (f *fixedWidth) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node].
func (f *fixedWidth) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Constraints{Min: geom.Sz(f.w, 0), Max: geom.Sz(f.w, c.Max.H)})
	k.Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (f *fixedWidth) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}
