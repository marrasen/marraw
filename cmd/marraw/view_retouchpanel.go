package main

import (
	"fmt"
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// retouchPanel is the Local tab's Retouch, as marraw's: the heal tool's
// button, and while it is on, how new spots are made; then the spots, a
// row each, the chosen one's settings under its row.
type retouchPanel struct {
	st       DevelopState
	toggle   *widget.Button
	tools    gunim.Node
	tool     *widget.Segmented
	mode     *widget.Segmented
	toolFold *widget.Fold
	size     *widget.SliderRow
	feather  *widget.SliderRow
	brushOn  *widget.Fold
	hint     *widget.Label
	editor   *spotEditor
	rows     []*spotRow
	places   map[*spotRow]*anim.Float
	kids     []gunim.Node
}

// spotModes are the spots' modes, as the panel names them and the edit
// spells them.
var spotModes = []struct{ label, mode string }{{"Heal", ""}, {"Clone", "clone"}, {"Fill", "fill"}}

func spotModeIndex(m string) int {
	for i, s := range spotModes {
		if s.mode == m {
			return i
		}
	}
	return 0
}

func newRetouchPanel() *retouchPanel {
	p := &retouchPanel{places: map[*spotRow]*anim.Float{}}
	p.toggle = widget.NewButton("Heal spots")
	p.toggle.Icon, p.toggle.KeepFocus, p.toggle.Tooltip = icon.Bandage, true, "Heal, clone or fill spots on the photo (Q)"
	p.toggle.OnClick = widget.Sends(HealToggle{})
	p.tool = widget.NewSegmented("Spot", "Brush")
	p.tool.KeepFocus = true
	p.tool.OnChange = func(i int, _ *gunim.UI) gunim.Intent {
		h := p.heal()
		return HealSet{Tool: map[int]string{0: "spot", 1: "brush"}[i], Mode: h.Mode, Radius: h.Radius, Feather: h.Feather}
	}
	var labels []string
	for _, m := range spotModes {
		labels = append(labels, m.label)
	}
	p.mode = widget.NewSegmented(labels...)
	p.mode.KeepFocus = true
	p.mode.OnChange = func(i int, _ *gunim.UI) gunim.Intent {
		h := p.heal()
		return HealSet{Tool: h.Tool, Mode: spotModes[i].mode, Radius: h.Radius, Feather: h.Feather}
	}
	size := widget.NewSlider(0.3, 15)
	size.Snap, size.KeepFocus = 0.1, true
	size.OnChange = func(x float32, _ *gunim.UI) gunim.Intent {
		h := p.heal()
		return HealSet{Tool: h.Tool, Mode: h.Mode, Radius: float64(x) / 100, Feather: h.Feather}
	}
	p.size = widget.NewSliderRow("Size", size)
	p.size.Format = func(x float32) string { return fmt.Sprintf("%.1f", x*2) }
	feather := widget.NewSlider(0, 100)
	feather.Snap, feather.KeepFocus = 2, true
	feather.OnChange = func(x float32, _ *gunim.UI) gunim.Intent {
		h := p.heal()
		return HealSet{Tool: h.Tool, Mode: h.Mode, Radius: h.Radius, Feather: float64(x) / 100}
	}
	p.feather = widget.NewSliderRow("Feather", feather)
	p.feather.Format = func(x float32) string { return fmt.Sprintf("%.0f", x) }
	p.brushOn = widget.NewFold(widget.Column(p.size, p.feather), false)
	p.hint = newSmallLabel("Click a spot on the photo to heal it, or drag to make it larger. The brush paints one along a stroke.")
	p.hint.Color, p.hint.MaxLines = noteInk, 3
	p.toolFold = widget.NewFold(widget.Column(
		labelledRow("Tool", glassSegmented(p.tool)), labelledRow("New spots", glassSegmented(p.mode)), p.brushOn), false)
	p.editor = newSpotEditor()
	p.kids = []gunim.Node{glassButton(p.toggle), p.toolFold, p.hint, p.editor}
	return p
}

// heal is the heal tool showing, never nil.
func (p *retouchPanel) heal() HealView {
	if p.st.Heal == nil {
		return HealView{Tool: "spot", Radius: 0.02, Feather: 0.5, Sel: -1}
	}
	return *p.st.Heal
}

// show takes the spots and the heal tool of s.
func (p *retouchPanel) show(s DevelopState, u *gunim.UI) {
	p.st = s
	h := p.heal()
	n := len(s.Params.Spots)
	p.toggle.Active = h.On
	p.toggle.Label = map[bool]string{false: "Heal spots", true: "Done healing"}[h.On]
	if n > 0 {
		p.toggle.Label += fmt.Sprintf("  ·  %d", n)
	}
	p.toolFold.SetOpen(h.On, u)
	p.brushOn.SetOpen(h.Tool == "brush", u)
	p.tool.SetSelected(map[string]int{"spot": 0, "brush": 1}[h.Tool], u)
	p.mode.SetSelected(spotModeIndex(h.Mode), u)
	if !p.size.Slider.Held() {
		p.size.Slider.SetValue(float32(h.Radius*100), u)
	}
	if !p.feather.Slider.Held() {
		p.feather.Slider.SetValue(float32(h.Feather*100), u)
	}
	if h.Sel >= 0 && h.Sel < n {
		p.editor.show(h.Sel, s.Params.Spots[h.Sel], u)
	}
	u.Invalidate()
}

// labelledRowPad is the room above a labelled row.
var labelledRowPad = theme.Insets("marraw.labelled.pad", geom.Insets{Top: 6})

// labelledRow is a small label over a control taking the row.
func labelledRow(label string, n gunim.Node) gunim.Node {
	l := newSmallLabel(label)
	l.Color = noteInk
	pad := widget.NewPad(widget.Column(l, n))
	pad.Padding = labelledRowPad
	return pad
}

// Children implements [gunim.Composite]: the rows are built as the spots
// come.
func (p *retouchPanel) Children() []gunim.Node { return p.kids }

// Layout implements [gunim.Node].
func (p *retouchPanel) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	spots := p.st.Params.Spots
	h := p.heal()
	byNode := map[gunim.Node]gunim.Child{}
	for k := range kids.All {
		byNode[k.Node()] = k
	}
	for len(p.rows) > len(spots) {
		r := p.rows[len(p.rows)-1]
		kids.Drop(r)
		delete(p.places, r)
		p.rows = p.rows[:len(p.rows)-1]
	}
	for len(p.rows) < len(spots) {
		r := newSpotRow(len(p.rows))
		p.rows = append(p.rows, r)
		byNode[r] = kids.Build(r)
	}
	y := float32(0)
	place := func(k gunim.Child, h float32) {
		s := k.Layout(gunim.Loose(geom.Sz(w, h)))
		k.Place(geom.Pt(0, y))
		y += s.H + 6
	}
	place(kids.At(0), 40)
	place(kids.At(1), 400)
	if h.On && len(spots) == 0 {
		place(kids.At(2), 80)
	} else {
		kids.At(2).Layout(gunim.Tight(geom.Size{}))
		kids.At(2).Place(geom.Pt(-10000, 0))
	}
	ed := kids.At(3)
	edPlaced := false
	for i, r := range p.rows {
		r.set(spots[i], i, i == h.Sel)
		k := byNode[r]
		s := k.Layout(gunim.Tight(geom.Sz(w, maskRowHeight)))
		a, ok := p.places[r]
		if !ok {
			a = anim.NewFloat(y)
			p.places[r] = a
			r.Add(a)
		}
		a.Animate(y, widget.Quick.Get(f.Theme))
		k.Place(geom.Pt(0, a.Value()))
		y += s.H + 2
		if i == h.Sel {
			es := ed.Layout(gunim.Loose(geom.Sz(w, 400)))
			ed.Place(geom.Pt(0, y+4))
			y += es.H + 10
			edPlaced = true
		}
	}
	if !edPlaced {
		ed.Layout(gunim.Tight(geom.Size{}))
		ed.Place(geom.Pt(-10000, 0))
	}
	return geom.Sz(w, y)
}

// Paint implements [gunim.Node].
func (p *retouchPanel) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// spotRow is a spot's row: its name, and to show or hide it and delete
// it. A click chooses it, or lets it go.
type spotRow struct {
	anim.Group
	i          int
	spot       marrawclient.Spot
	name       *widget.Label
	eye, trash *widget.IconButton
	sel, hot   *anim.Float
	kids       []gunim.Node
}

func newSpotRow(i int) *spotRow {
	r := &spotRow{i: i, name: newSmallLabel(""), sel: anim.NewFloat(0), hot: anim.NewFloat(0)}
	r.Add(r.sel, r.hot)
	r.eye = widget.NewIconButton(icon.Eye, "Hide the spot")
	r.eye.KeepFocus = true
	r.eye.OnClick = func(*gunim.UI) gunim.Intent {
		s := r.spot
		s.Disabled = !s.Disabled
		return SpotSet{Index: r.i, Spot: s, Commit: true, Label: map[bool]string{false: "Show spot", true: "Hide spot"}[s.Disabled]}
	}
	r.trash = widget.NewIconButton(icon.Trash2, "Delete the spot")
	r.trash.KeepFocus = true
	r.trash.OnClick = func(*gunim.UI) gunim.Intent { return SpotDelete{Index: r.i} }
	icons := marrawTheme().With(theme.Set(widget.ButtonHeight, 24), theme.Set(widget.IconSize, 14))
	r.kids = []gunim.Node{r.name, widget.NewThemed(r.eye, icons), widget.NewThemed(r.trash, icons)}
	return r
}

// set shows spot s, at i, chosen or not.
func (r *spotRow) set(s marrawclient.Spot, i int, chosen bool) {
	r.i, r.spot = i, s
	r.name.Text = spotLabel(s, i)
	r.eye.Icon, r.eye.Tooltip = icon.Eye, "Hide the spot"
	if s.Disabled {
		r.eye.Icon, r.eye.Tooltip = icon.EyeOff, "Show the spot"
	}
	r.sel.Animate(on(chosen), anim.Spring{Response: 0.2, Damping: 1})
}

// Children implements [gunim.Composite].
func (r *spotRow) Children() []gunim.Node { return r.kids }

// Layout implements [gunim.Node].
func (r *spotRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	right := w - 2
	for i := 2; i >= 1; i-- {
		s := kids.At(i).Layout(gunim.Loose(geom.Sz(40, maskRowHeight)))
		right -= s.W
		kids.At(i).Place(geom.Pt(right, (maskRowHeight-s.H)/2))
		right -= 2
	}
	ns := kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, right-16), maskRowHeight)))
	kids.At(0).Place(geom.Pt(10, (maskRowHeight-ns.H)/2))
	return geom.Sz(w, maskRowHeight)
}

// Handle implements [gunim.Handler].
func (r *spotRow) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerEnter:
		r.hot.Animate(1, widget.Quick.Get(th))
	case input.PointerLeave:
		r.hot.Animate(0, widget.Quick.Get(th))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		u.Send(r, SpotSelect{Index: map[bool]int{false: r.i, true: -1}[r.sel.Target() > 0.5]})
		return true
	}
	return false
}

// Paint implements [gunim.Node].
func (r *spotRow) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	rr := geom.Rect{Max: box.Point()}
	if s := r.sel.Value(); s > 0.01 {
		p.RRect(rr, 6, paint.Solid(withAlpha(color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30}, s)))
	}
	if h := r.hot.Value() * (1 - r.sel.Value()); h > 0.01 {
		p.RRect(rr, 6, paint.Solid(withAlpha(frost(0x0e), h)))
	}
	func() {
		if r.spot.Disabled {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, box.W/2, box.H), Opacity: 0.45})()
		}
		kids.At(0).Paint(p)
	}()
	kids.At(1).Paint(p)
	kids.At(2).Paint(p)
}

// spotEditor is the chosen spot's settings: its mode, its feather and its
// opacity.
type spotEditor struct {
	i       int
	spot    marrawclient.Spot
	mode    *widget.Segmented
	feather *widget.SliderRow
	opacity *widget.SliderRow
	col     gunim.Node
}

func newSpotEditor() *spotEditor {
	e := &spotEditor{}
	var labels []string
	for _, m := range spotModes {
		labels = append(labels, m.label)
	}
	e.mode = widget.NewSegmented(labels...)
	e.mode.KeepFocus = true
	e.mode.OnChange = func(i int, _ *gunim.UI) gunim.Intent {
		s := e.spot
		s.Mode = marrawclient.SpotMode(spotModes[i].mode)
		return SpotSet{Index: e.i, Spot: s, Commit: true, Label: spotModes[i].label + " spot"}
	}
	slider := func(label string, lo, hi float32, get func(s *marrawclient.Spot) *float64, scale float64, step string) *widget.SliderRow {
		sl := widget.NewSlider(lo, hi)
		sl.Snap, sl.KeepFocus = 1, true
		set := func(x float32, commit bool) gunim.Intent {
			s := e.spot
			v := float64(x) / scale
			if label == "Opacity" && x >= 100 {
				v = 0
			}
			*get(&s) = v
			return SpotSet{Index: e.i, Spot: s, Commit: commit, Label: step}
		}
		sl.OnChange = func(x float32, _ *gunim.UI) gunim.Intent { return set(x, false) }
		sl.OnCommit = func(x float32, _ *gunim.UI) gunim.Intent { return set(x, true) }
		r := widget.NewSliderRow(label, sl)
		r.Format = func(x float32) string { return fmt.Sprintf("%.0f%%", x) }
		return r
	}
	e.feather = slider("Feather", 0, 100, func(s *marrawclient.Spot) *float64 { return &s.Feather }, 100, "Spot feather")
	e.opacity = slider("Opacity", 10, 100, func(s *marrawclient.Spot) *float64 { return &s.Opacity }, 100, "Spot opacity")
	e.col = widget.Column(glassSegmented(e.mode), e.feather, e.opacity)
	return e
}

// show takes spot s, at i.
func (e *spotEditor) show(i int, s marrawclient.Spot, u *gunim.UI) {
	e.i, e.spot = i, s
	e.mode.SetSelected(spotModeIndex(string(s.Mode)), u)
	if !e.feather.Slider.Held() {
		e.feather.Slider.SetValue(float32(s.Feather*100), u)
	}
	op := s.Opacity
	if op == 0 {
		op = 1
	}
	if !e.opacity.Slider.Held() {
		e.opacity.Slider.SetValue(float32(op*100), u)
	}
}

// Children implements [gunim.Composite].
func (e *spotEditor) Children() []gunim.Node { return []gunim.Node{e.col} }

// Layout implements [gunim.Node].
func (e *spotEditor) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s := kids.At(0).Layout(gunim.Loose(geom.Sz(c.Max.W-12, c.Max.H)))
	kids.At(0).Place(geom.Pt(12, 0))
	return geom.Sz(c.Max.W, s.H)
}

// Paint implements [gunim.Node]: a line down its left, under the spot's
// row it belongs to.
func (e *spotEditor) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rc(3, 0, 2, box.H), 1, paint.Solid(color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x60}))
	kids.At(0).Paint(p)
}
