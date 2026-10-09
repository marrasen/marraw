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

// maskPanel is the panel's masks, as marraw's Local tab has them: buttons
// to add each kind, a row for each mask, and under the chosen one its
// shape's settings, its adjustments and its effects.
type maskPanel struct {
	v      *developView
	adds   gunim.Node
	ais    gunim.Node
	aiBtn  map[string]*widget.Button
	none   *widget.Label
	editor *maskEditor
	rows   []*maskRow
	st     DevelopState
	// places glides each row to its place as masks come, go and open.
	places map[*maskRow]*anim.Float
}

// The AI masks' buttons, as marraw's: what each adds, and its label.
var aiKinds = []struct{ kind, label string }{{"subject", "Subject"}, {"background", "Background"}, {"depth", "Depth"}, {"tilt", "Tilt shift"}}

func newMaskPanel(v *developView) *maskPanel {
	p := &maskPanel{v: v, aiBtn: map[string]*widget.Button{}, places: map[*maskRow]*anim.Float{}}
	add := func(label string, ic *icon.Icon, tip string, in gunim.Intent) *widget.Button {
		b := widget.NewButton(label)
		b.KeepFocus, b.Tooltip, b.OnClick = true, tip, widget.Sends(in)
		return b
	}
	small := func(n gunim.Node) gunim.Node {
		th := marrawTheme().With(theme.Set(widget.ButtonHeight, 26), theme.Set(widget.ButtonPadding, 9),
			theme.Set(widget.ButtonRadius, 6), theme.Set(widget.TextSize, 12))
		return widget.NewThemed(n, th)
	}
	row := widget.Row(add("Linear", icon.Spline, "Add a linear gradient", MaskAdd{Kind: "linear"}),
		add("Radial", icon.Circle, "Add a radial mask", MaskAdd{Kind: "radial"}),
		add("Brush", icon.Brush, "Add a brush mask", MaskAdd{Kind: "brush"}),
		add("Range", icon.Palette, "Add a luminance and colour range mask: select pixels by tone and hue", MaskAdd{Kind: "range"}))
	p.adds = small(row)
	var ai []gunim.Node
	for _, k := range aiKinds {
		b := add(k.label, icon.Sparkles, "Add an AI "+k.label+" mask", MaskAI{Kind: k.kind})
		p.aiBtn[k.kind] = b
		ai = append(ai, b)
	}
	p.ais = small(widget.Row(ai...))
	p.none = newSmallLabel("No masks yet: add one to adjust a part of the photo.")
	p.none.Color, p.none.MaxLines = noteInk, 2
	p.editor = newMaskEditor(p)
	return p
}

// show takes the masks of s.
func (p *maskPanel) show(s DevelopState, u *gunim.UI) {
	p.st = s
	for k, b := range p.aiBtn {
		b.Disabled = s.AIBusy != ""
		label := ""
		for _, a := range aiKinds {
			if a.kind == k {
				label = a.label
			}
		}
		if s.AIBusy == k {
			label += "…"
		}
		b.Label = label
	}
	p.editor.show(s, u)
	u.Invalidate()
}

// Children implements [gunim.Composite]: the rows are built as the masks
// come.
func (p *maskPanel) Children() []gunim.Node { return []gunim.Node{p.adds, p.ais, p.none, p.editor} }

// Layout implements [gunim.Node].
func (p *maskPanel) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	masks := p.st.Params.Masks
	// One row a mask: built where there are more, dropped where fewer.
	children := map[*maskRow]gunim.Child{}
	for kid := range kids.All {
		if r, ok := kid.Node().(*maskRow); ok {
			children[r] = kid
		}
	}
	for len(p.rows) > len(masks) {
		r := p.rows[len(p.rows)-1]
		kids.Drop(r)
		delete(children, r)
		delete(p.places, r)
		p.rows = p.rows[:len(p.rows)-1]
	}
	for len(p.rows) < len(masks) {
		r := newMaskRow(len(p.rows))
		p.rows = append(p.rows, r)
		children[r] = kids.Build(r)
	}
	y := float32(0)
	place := func(k gunim.Child, h float32) float32 {
		s := k.Layout(gunim.Loose(geom.Sz(w, h)))
		k.Place(geom.Pt(0, y))
		y += s.H + 6
		return s.H
	}
	place(kids.At(0), 40)
	place(kids.At(1), 40)
	y += 4
	if len(masks) == 0 {
		place(kids.At(2), 60)
	} else {
		kids.At(2).Layout(gunim.Tight(geom.Size{}))
		kids.At(2).Place(geom.Pt(-10000, 0))
	}
	th := f.Theme
	ed := kids.At(3)
	edPlaced := false
	for i, r := range p.rows {
		r.set(masks[i], i, i == p.st.MaskSel)
		k := children[r]
		s := k.Layout(gunim.Tight(geom.Sz(w, maskRowHeight)))
		a, ok := p.places[r]
		if !ok {
			a = anim.NewFloat(y)
			p.places[r] = a
			r.Add(a)
		}
		a.Animate(y, widget.Quick.Get(th))
		k.Place(geom.Pt(0, a.Value()))
		y += s.H + 2
		if i == p.st.MaskSel {
			es := ed.Layout(gunim.Loose(geom.Sz(w, 4000)))
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
func (p *maskPanel) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// maskRowHeight is a mask's row's height.
const maskRowHeight = 30

// maskRow is a mask's row: its name, a dot where it adjusts something,
// and its pills and buttons: remove what it covers, invert, show or hide,
// delete. A click on it chooses it, or lets it go; the pointer over it
// tints it on the photo.
type maskRow struct {
	anim.Group
	i                 int
	name              *widget.Label
	remove, invert    *widget.Button
	eye, trash        *widget.IconButton
	sel, hot, dim     *anim.Float
	adjusted, canDrop bool
	box               geom.Size
}

func newMaskRow(i int) *maskRow {
	r := &maskRow{i: i, name: newSmallLabel(""), sel: anim.NewFloat(0), hot: anim.NewFloat(0), dim: anim.NewFloat(0)}
	r.Add(r.sel, r.hot, r.dim)
	r.name.MaxLines = 1
	pill := func(label, tip string) *widget.Button {
		b := widget.NewButton(label)
		b.Ghost, b.KeepFocus, b.Tooltip = true, true, tip
		return b
	}
	r.remove = pill("Remove", "Remove what this mask covers, filling it from around")
	r.invert = pill("Invert", "Invert the mask")
	r.remove.OnClick = func(*gunim.UI) gunim.Intent { return MaskFlag{Index: r.i, What: "remove"} }
	r.invert.OnClick = func(*gunim.UI) gunim.Intent { return MaskFlag{Index: r.i, What: "invert"} }
	r.eye = widget.NewIconButton(icon.Eye, "Hide the mask")
	r.eye.KeepFocus = true
	r.eye.OnClick = func(*gunim.UI) gunim.Intent { return MaskFlag{Index: r.i, What: "hide"} }
	r.trash = widget.NewIconButton(icon.Trash2, "Delete the mask")
	r.trash.KeepFocus = true
	r.trash.OnClick = func(*gunim.UI) gunim.Intent { return MaskDelete{Index: r.i} }
	return r
}

// set shows mask m, at i, chosen or not.
func (r *maskRow) set(m marrawclient.Mask, i int, chosen bool) {
	r.i = i
	r.name.Text = maskLabel(m, i)
	r.adjusted = hasAdjust(m.Adjust)
	r.canDrop = canRemove(m) || m.Remove
	r.remove.Active, r.invert.Active = m.Remove, m.Invert
	r.eye.Icon = icon.Eye
	r.eye.Tooltip = "Hide the mask"
	if m.Disabled {
		r.eye.Icon, r.eye.Tooltip = icon.EyeOff, "Show the mask"
	}
	r.sel.Animate(on(chosen), anim.Spring{Response: 0.2, Damping: 1})
	r.dim.Animate(on(m.Disabled), anim.Spring{Response: 0.2, Damping: 1})
}

// Children implements [gunim.Composite].
func (r *maskRow) Children() []gunim.Node {
	return []gunim.Node{r.name, r.remove, r.invert, r.eye, r.trash}
}

// Layout implements [gunim.Node].
func (r *maskRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	r.box = geom.Sz(c.Max.W, maskRowHeight)
	right := r.box.W - 2
	for i := 4; i >= 1; i-- {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(120, maskRowHeight-4)))
		if i == 1 && !r.canDrop {
			k.Layout(gunim.Tight(geom.Size{}))
			k.Place(geom.Pt(-10000, 0))
			continue
		}
		right -= s.W
		k.Place(geom.Pt(right, (maskRowHeight-s.H)/2))
		right -= 2
	}
	n := kids.At(0)
	ns := n.Layout(gunim.Loose(geom.Sz(max(0, right-30), maskRowHeight)))
	n.Place(geom.Pt(22, (maskRowHeight-ns.H)/2))
	return r.box
}

// Handle implements [gunim.Handler]: the pointer over the row tints the
// mask, and a click on its name chooses it.
func (r *maskRow) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerEnter:
		r.hot.Animate(1, widget.Quick.Get(th))
		u.Send(r, MaskHover{Index: r.i})
	case input.PointerLeave:
		r.hot.Animate(0, widget.Quick.Get(th))
		u.Send(r, MaskHover{Index: -1})
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		chosen := r.sel.Target() > 0.5
		u.Send(r, MaskSelect{Index: map[bool]int{false: r.i, true: -1}[chosen]})
		return true
	default:
		return false
	}
	u.Invalidate()
	return false
}

// Paint implements [gunim.Node].
func (r *maskRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	rr := geom.Rect{Max: box.Point()}
	if s := r.sel.Value(); s > 0.01 {
		p.RRect(rr, 6, paint.Solid(withAlpha(color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30}, s)))
	}
	if h := r.hot.Value() * (1 - r.sel.Value()); h > 0.01 {
		p.RRect(rr, 6, paint.Solid(withAlpha(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0e}, h)))
	}
	if r.adjusted {
		p.RRect(geom.Rc(9, box.H/2-3, 6, 6), 3, paint.Solid(widget.Accent.Get(th)))
	}
	op := 1 - 0.55*r.dim.Value()
	func() {
		if op < 0.999 {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, box.W/2, box.H), Opacity: op})()
		}
		kids.At(0).Paint(p)
	}()
	for i := 1; i <= 4; i++ {
		kids.At(i).Paint(p)
	}
}

// maskEditor is the chosen mask's settings: its shape's, as its kind has
// them, folding open and shut as the kind changes; the brush's or the
// range picker's tool; its tone adjustments; and its effects, folded
// away until it has some.
type maskEditor struct {
	p       *maskPanel
	col     gunim.Node
	rows    map[string]*widget.SliderRow
	folds   map[string]*widget.Fold
	paint   *widget.Button
	erase   *widget.IconButton
	clear   *widget.Button
	pick    *widget.Button
	fxFold  *widget.Fold
	fxHead  *widget.Button
	fxOpen  bool
	lastSel int
}

// The shape's settings' sliders: their keys, labels and ranges.
var maskShapeSpecs = []maskSpec{
	{"threshold", "Threshold", 0.02, 1, 0.01, percent, nil},
	{"depthCentre", "Focus distance", 0, 1, 0.01, percent, nil},
	{"depthWidth", "Focus depth", 0.02, 1, 0.01, percent, nil},
	{"lumaLo", "Luminance from", 0, 1, 0.01, percent, nil},
	{"lumaHi", "Luminance to", 0, 1, 0.01, percent, nil},
	{"hueCentre", "Hue", 0, 1, 1.0 / 360, func(v float32) string { return fmt.Sprintf("%.0f°", v*360) }, nil},
	{"hueRange", "Hue range", 0, 0.5, 1.0 / 360, func(v float32) string {
		if v >= 0.499 {
			return "All"
		}
		return fmt.Sprintf("±%.0f°", v*360)
	}, nil},
	{"satMin", "Least saturation", 0, 1, 0.01, percent, nil},
	{"feather", "Feather", 0, 1, 0.01, percent, nil},
}

func percent(v float32) string { return fmt.Sprintf("%.0f%%", v*100) }

// The brush's tool sliders.
var brushSpecs = []maskSpec{
	{"size", "Size", 0.5, 25, 0.5, func(v float32) string { return fmt.Sprintf("%.1f", v) }, nil},
	{"bfeather", "Feather", 0, 100, 2, func(v float32) string { return fmt.Sprintf("%.0f", v) }, nil},
	{"flow", "Flow", 5, 100, 5, func(v float32) string { return fmt.Sprintf("%.0f", v) }, nil},
}

func newMaskEditor(p *maskPanel) *maskEditor {
	e := &maskEditor{p: p, rows: map[string]*widget.SliderRow{}, folds: map[string]*widget.Fold{}, lastSel: -2}
	slider := func(sp maskSpec, brush bool) *widget.SliderRow {
		sl := widget.NewSlider(sp.min, sp.max)
		sl.Snap, sl.KeepFocus, sl.HasRest = sp.snap, true, !brush
		key := sp.key
		if brush {
			sl.OnChange = func(x float32, _ *gunim.UI) gunim.Intent { return BrushSet{Tool: e.brushWith(key, float64(x))} }
		} else {
			sl.OnChange = func(x float32, _ *gunim.UI) gunim.Intent {
				return MaskSet{Index: p.st.MaskSel, Key: key, Value: float64(x)}
			}
			sl.OnCommit = func(x float32, _ *gunim.UI) gunim.Intent {
				return MaskSet{Index: p.st.MaskSel, Key: key, Value: float64(x), Commit: true}
			}
		}
		if key == "hueCentre" {
			sl.Gradient = hueTrack
		}
		r := widget.NewSliderRow(sp.label, sl)
		r.Format = sp.format
		e.rows[key] = r
		return r
	}
	fold := func(name string, nodes ...gunim.Node) *widget.Fold {
		f := widget.NewFold(widget.Column(nodes...), false)
		e.folds[name] = f
		return f
	}
	// The brush's tool.
	e.paint = widget.NewButton("Paint")
	e.paint.Icon, e.paint.KeepFocus = icon.Paintbrush, true
	e.paint.OnClick = func(*gunim.UI) gunim.Intent {
		t := p.st.Brush
		t.Painting = !t.Painting
		return BrushSet{Tool: t}
	}
	e.erase = widget.NewIconButton(icon.Eraser, "Erase strokes")
	e.erase.KeepFocus = true
	e.erase.OnClick = func(*gunim.UI) gunim.Intent {
		t := p.st.Brush
		t.Erase, t.Painting = !t.Erase, true
		return BrushSet{Tool: t}
	}
	e.clear = widget.NewButton("Clear")
	e.clear.Ghost, e.clear.KeepFocus, e.clear.Tooltip = true, true, "Clear all the strokes"
	e.clear.OnClick = func(*gunim.UI) gunim.Intent { return BrushClear{Index: p.st.MaskSel} }
	brushBar := widget.Row(e.paint, e.erase, e.clear)
	brushBar.Cross = widget.CrossCenter
	var brushRows []gunim.Node
	brushRows = append(brushRows, brushBar)
	for _, sp := range brushSpecs {
		brushRows = append(brushRows, slider(sp, true))
	}
	// The range mask's picker.
	e.pick = widget.NewButton("Pick colour")
	e.pick.Icon, e.pick.KeepFocus = icon.Pipette, true
	e.pick.OnClick = func(*gunim.UI) gunim.Intent { return RangePick{On: !p.st.RangePick} }
	shape := map[string]maskSpec{}
	for _, sp := range maskShapeSpecs {
		shape[sp.key] = sp
	}
	nodes := []gunim.Node{
		fold("brush", brushRows...),
		fold("range", e.pick, slider(shape["lumaLo"], false), slider(shape["lumaHi"], false), slider(shape["hueCentre"], false),
			slider(shape["hueRange"], false), slider(shape["satMin"], false)),
		fold("threshold", slider(shape["threshold"], false)),
		fold("depth", slider(shape["depthCentre"], false), slider(shape["depthWidth"], false)),
		fold("feather", slider(shape["feather"], false)),
	}
	for _, sp := range maskTone {
		nodes = append(nodes, slider(sp, false))
	}
	var fx []gunim.Node
	for _, sp := range maskFX {
		fx = append(fx, slider(sp, false))
	}
	e.fxHead = widget.NewButton("Effects")
	e.fxHead.Ghost, e.fxHead.KeepFocus = true, true
	e.fxHead.Icon = icon.Sparkles
	e.fxFold = widget.NewFold(widget.Column(fx...), false)
	e.fxHead.OnClick = func(u *gunim.UI) gunim.Intent {
		e.fxOpen = !e.fxOpen
		e.fxFold.SetOpen(e.fxOpen, u)
		return nil
	}
	nodes = append(nodes, e.fxHead, e.fxFold)
	e.col = widget.Column(nodes...)
	return e
}

// brushWith is the brush with key set to x, as its slider shows it.
func (e *maskEditor) brushWith(key string, x float64) BrushTool {
	t := e.p.st.Brush
	switch key {
	case "size":
		t.Radius = x / 100
	case "bfeather":
		t.Feather = x / 100
	case "flow":
		t.Flow = x / 100
	}
	return t
}

// show takes the chosen mask of s: the folds of its kind open, the others
// shut, and the sliders glide to its values.
func (e *maskEditor) show(s DevelopState, u *gunim.UI) {
	i := s.MaskSel
	if i < 0 || i >= len(s.Params.Masks) {
		e.lastSel = -1
		return
	}
	m := s.Params.Masks[i]
	another := i != e.lastSel
	e.lastSel = i
	isAI := m.Type == "ai"
	open := map[string]bool{
		"brush":     m.Type == "brush",
		"range":     m.Type == "range",
		"threshold": isAI && (m.AIKind == "subject" || m.AIKind == "background"),
		"depth":     isAI && m.AIKind == "depth",
		"feather":   isAI || m.Type == "range" || m.Type == "radial",
	}
	for k, f := range e.folds {
		f.SetOpen(open[k], u)
	}
	if another && hasFX(m.Adjust) != e.fxOpen {
		e.fxOpen = hasFX(m.Adjust)
		e.fxFold.SetOpen(e.fxOpen, u)
	}
	set := func(key string, v float64) {
		if r := e.rows[key]; r != nil && !r.Slider.Held() {
			r.Slider.SetValue(float32(v), u)
		}
	}
	for _, sp := range maskTone {
		set(sp.key, *sp.get(&m.Adjust))
	}
	for _, sp := range maskFX {
		set(sp.key, *sp.get(&m.Adjust))
	}
	th := m.Threshold
	if th == 0 {
		th = 0.5
	}
	set("threshold", th)
	set("depthCentre", (m.DepthLo+m.DepthHi)/2)
	set("depthWidth", m.DepthHi-m.DepthLo)
	set("lumaLo", m.RangeLumaLo)
	set("lumaHi", m.RangeLumaHi)
	c, w := hueWindow(m)
	set("hueCentre", c)
	set("hueRange", w)
	set("satMin", m.RangeSatMin)
	set("feather", m.Feather)
	set("size", s.Brush.Radius*100)
	set("bfeather", s.Brush.Feather*100)
	set("flow", s.Brush.Flow*100)
	e.paint.Active, e.erase.Active = s.Brush.Painting, s.Brush.Erase
	e.paint.Label = map[bool]string{false: "Paint", true: "Done painting"}[s.Brush.Painting]
	e.clear.Disabled = len(m.Strokes) == 0
	e.pick.Active = s.RangePick
	e.pick.Label = map[bool]string{false: "Pick colour", true: "Picking colour…"}[s.RangePick]
	if r := e.rows["fxAngle"]; r != nil {
		r.Slider.Disabled = m.Adjust.MotionBlur == 0 && m.Adjust.Streaks == 0
	}
}

// Children implements [gunim.Composite].
func (e *maskEditor) Children() []gunim.Node { return []gunim.Node{e.col} }

// Layout implements [gunim.Node].
func (e *maskEditor) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Loose(geom.Sz(c.Max.W-12, c.Max.H)))
	k.Place(geom.Pt(12, 0))
	return geom.Sz(c.Max.W, s.H)
}

// Paint implements [gunim.Node]: a line down its left, under the mask's
// row it belongs to.
func (e *maskEditor) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rc(3, 0, 2, box.H), 1, paint.Solid(color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x60}))
	kids.At(0).Paint(p)
}
