package main

import (
	"fmt"
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

type (
	// BatchDelta adds By to Field of every photo selected: "expEV",
	// "contrast" or "saturation". BatchPreset lays a preset over each
	// of them.
	BatchDelta struct {
		Field string
		By    float64
	}
	BatchPreset struct {
		Auto  bool
		Index int
	}
)

// Inks of the selection bar's buttons.
var (
	selStarInk   = theme.Color("marraw.sel.star", starInk)
	selPickInk   = theme.Color("marraw.sel.pick", color.NRGBA{R: 0x6d, G: 0xd8, B: 0x8f, A: 0xff})
	selRejectInk = theme.Color("marraw.sel.reject", color.NRGBA{R: 0xf0, G: 0x80, B: 0x80, A: 0xff})
)

// selBar takes the place of the grid's bar while more than one photo is
// selected: how many, stars to rate them, Pick, Reject, Delete, and the
// batch card's button.
type selBar struct {
	count  *widget.Label
	row    gunim.Node
	adjust *widget.Button
}

func newSelBar(toggle func(u *gunim.UI)) *selBar {
	b := &selBar{count: widget.NewLabel("")}
	b.count.Face = widget.MonoFont
	selected := widget.NewLabel("selected")
	rate := newSmallLabel("Rate")
	rate.Color = noteInk
	var stars []gunim.Node
	for n := 1; n <= 5; n++ {
		s := widget.NewIconButton(starLit, fmt.Sprintf("Rate %d", n))
		s.Ink, s.KeepFocus = selStarInk, true
		s.OnClick = widget.Sends(Rate{Stars: n})
		stars = append(stars, s)
	}
	pick := widget.NewButton("Pick")
	pick.Icon, pick.Ink, pick.Tooltip = flagSet, selPickInk, "Pick them (P)"
	pick.OnClick = widget.Sends(Mark{Flag: "pick"})
	reject := widget.NewButton("Reject")
	reject.Icon, reject.Ink, reject.Tooltip = icon.X, selRejectInk, "Reject them (X)"
	reject.OnClick = widget.Sends(Mark{Flag: "exclude"})
	del := widget.NewIconButton(icon.Trash2, "Move them to the Recycle Bin")
	del.Ink, del.KeepFocus, del.OnClick = selRejectInk, true, widget.Sends(AskDelete{})
	b.adjust = widget.NewButton("Adjust")
	b.adjust.Icon, b.adjust.Tooltip = icon.SlidersHorizontal, "Adjust them all at once"
	b.adjust.OnClick = func(u *gunim.UI) gunim.Intent {
		toggle(u)
		return nil
	}
	esc := newSmallLabel("Esc to clear")
	esc.Face, esc.Color = widget.MonoFont, noteInk
	pick.KeepFocus, reject.KeepFocus, b.adjust.KeepFocus = true, true, true
	row := &hflow{items: []gunim.Node{&selCount{label: b.count}, selected, rate, &hflow{items: stars, gap: 0},
		&edged{child: pick}, &edged{child: reject}, del, &edged{child: b.adjust}}, gap: 12, tail: esc, pad: 18}
	b.row = widget.NewThemed(row, glassTheme(marrawTheme().With(theme.Set(widget.ButtonHeight, 28), theme.Set(widget.ButtonPadding, 10),
		theme.Set(widget.ButtonRadius, 7), theme.Set(widget.TextSize, 12))))
	return b
}

// set shows n selected, and whether the batch card is out.
func (b *selBar) set(n int, out bool, u *gunim.UI) {
	b.count.Text = fmt.Sprint(n)
	b.adjust.Active = out
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (b *selBar) Children() []gunim.Node { return []gunim.Node{b.row} }

// hflow lays its items in a row from the left, gap apart, centred on the
// row's middle line, with tail against the right end; pad is the room at
// either end.
type hflow struct {
	items    []gunim.Node
	tail     gunim.Node
	gap, pad float32
}

// Children implements [gunim.Composite].
func (h *hflow) Children() []gunim.Node {
	if h.tail != nil {
		return append(append([]gunim.Node{}, h.items...), h.tail)
	}
	return h.items
}

// Layout implements [gunim.Node].
func (h *hflow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	n := len(h.items)
	sizes := make([]geom.Size, kids.Len())
	tall := float32(0)
	for i := range kids.Len() {
		sizes[i] = kids.At(i).Layout(gunim.Loose(geom.Sz(max(0, c.Max.W), max(c.Max.H, 40))))
		tall = max(tall, sizes[i].H)
	}
	hh := max(c.Min.H, tall)
	if c.Max.H > 0 && c.Min.H == c.Max.H {
		hh = c.Max.H
	}
	x := h.pad
	for i := range n {
		kids.At(i).Place(geom.Pt(x, (hh-sizes[i].H)/2))
		x += sizes[i].W + h.gap
	}
	w := x - h.gap + h.pad
	if h.tail != nil {
		ts := sizes[n]
		w = max(c.Max.W, w)
		kids.At(n).Place(geom.Pt(w-h.pad-ts.W, (hh-ts.H)/2))
	}
	return geom.Sz(max(0, w), hh)
}

// Paint implements [gunim.Node].
func (h *hflow) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// Layout implements [gunim.Node].
func (b *selBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(c.Max))
	kids.At(0).Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node]: a wash of the accent, edged below.
func (b *selBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	acc := widget.Accent.Get(f.Theme)
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(withAlpha(acc, 0.10)))
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(withAlpha(acc, 0.28)))
	kids.At(0).Paint(p)
}

// selCount is the count in a small badge of the accent.
type selCount struct{ label *widget.Label }

// Children implements [gunim.Composite].
func (s *selCount) Children() []gunim.Node { return []gunim.Node{s.label} }

// Layout implements [gunim.Node].
func (s *selCount) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	ls := kids.At(0).Layout(gunim.Loose(c.Max))
	kids.At(0).Place(geom.Pt(7, 2))
	return geom.Sz(ls.W+14, ls.H+4)
}

// Paint implements [gunim.Node].
func (s *selCount) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 5, paint.Solid(widget.Accent.Get(f.Theme)))
	kids.At(0).Paint(p)
}

// gate passes the pointer to its child only while open, so of two
// things drawn in one place, the one going takes no clicks.
type gate struct {
	child gunim.Node
	open  bool
}

// Children implements [gunim.Composite].
func (g *gate) Children() []gunim.Node { return []gunim.Node{g.child} }

// Covers implements [gunim.Shaped].
func (g *gate) Covers(geom.Point) bool { return g.open }

// Layout implements [gunim.Node].
func (g *gate) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s := kids.At(0).Layout(c)
	kids.At(0).Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (g *gate) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// batchCard is the selection's card of glass at the right of the grid:
// relative changes to them all, paste and restore, and the presets.
type batchCard struct {
	anim.Group
	title   *widget.Label
	status  *widget.Label
	sliders []*widget.SliderRow
	applied []float64
	presets *widget.MenuButton
	body    gunim.Node
	close   *widget.IconButton
	busy    int
}

// batchFields are the fields the card's sliders change, as their deltas
// name them, their ranges, and how they show.
var batchFields = []struct {
	key, label string
	lo, hi     float32
	step       float32
	scale      float64
	format     func(float32) string
}{
	{"expEV", "Exposure", -2, 2, 0.05, 1, func(x float32) string { return fmt.Sprintf("%+.2f EV", x) }},
	{"contrast", "Contrast", -100, 100, 2, 100, func(x float32) string { return fmt.Sprintf("%+.0f", x) }},
	{"saturation", "Saturation", -100, 100, 2, 100, func(x float32) string { return fmt.Sprintf("%+.0f", x) }},
}

func newBatchCard(hide func(u *gunim.UI)) *batchCard {
	c := &batchCard{title: widget.NewLabel(""), status: newSmallLabel("")}
	c.title.Size = batchTitleSize
	c.status.Color, c.status.MaxLines = noteInk, 2
	note := newSmallLabel("The changes add to each photo's own edit, so different edits stay different.")
	note.Color, note.MaxLines = noteInk, 3
	c.applied = make([]float64, len(batchFields))
	var rows []gunim.Node
	for i, f := range batchFields {
		s := widget.NewSlider(f.lo, f.hi)
		s.Snap, s.HasRest, s.KeepFocus = f.step, true, true
		r := widget.NewSliderRow(f.label, s)
		r.Format = f.format
		s.OnCommit = func(x float32, u *gunim.UI) gunim.Intent {
			inc := float64(x)/batchFields[i].scale - c.applied[i]
			if inc > -1e-9 && inc < 1e-9 {
				return nil
			}
			c.applied[i] += inc
			return BatchDelta{Field: batchFields[i].key, By: inc}
		}
		c.sliders = append(c.sliders, r)
		rows = append(rows, r)
	}
	paste := widget.NewButton("Paste settings")
	paste.Icon, paste.Tooltip, paste.OnClick = icon.ClipboardPaste, "Paste the edit copied with Ctrl+C on them all", widget.Sends(EditPaste{})
	restore := widget.NewButton("Restore original")
	restore.Icon, restore.Tooltip, restore.OnClick = icon.RotateCcw, "Take every edit off them", widget.Sends(DevReset{})
	c.presets = widget.NewMenuButton("Apply a preset", nil)
	c.close = widget.NewIconButton(icon.X, "Hide")
	c.close.KeepFocus = true
	c.close.OnClick = func(u *gunim.UI) gunim.Intent {
		hide(u)
		return nil
	}
	c.body = widget.Column(c.title, spacer(2), note, spacer(10), sectionLabel("Relative adjustment"),
		widget.NewThemed(widget.Column(rows...), marrawTheme().With(theme.Set(widget.SliderRowLabel, 84))), c.status, spacer(8), sectionLabel("Selection"), smallButtons(paste, restore),
		spacer(8), sectionLabel("Presets"), widget.NewThemed(c.presets, glassTheme(marrawTheme())))
	return c
}

// batchCardW is the card's width.
const batchCardW = 320

// batchTitleSize is the size of the card's title.
var batchTitleSize = theme.Length("marraw.batch.title", 15)

// set shows n selected, and the presets to choose from.
func (c *batchCard) set(n int, presets []PresetCard, u *gunim.UI) {
	c.title.Text = fmt.Sprintf("%d photos selected", n)
	items := make([]widget.MenuItem, 0, len(presets))
	for _, p := range presets {
		items = append(items, widget.MenuItem{Label: p.Name})
	}
	c.presets.SetItems(items)
	c.presets.OnPick = func(i int, _ *gunim.UI) gunim.Intent {
		if i < 0 || i >= len(presets) {
			return nil
		}
		return BatchPreset{Auto: presets[i].Auto, Index: presets[i].Index}
	}
	c.showStatus(n)
	u.Invalidate()
}

// fresh starts the sliders at nought, as for a new selection.
func (c *batchCard) fresh(u *gunim.UI) {
	for i, r := range c.sliders {
		r.Slider.SetValue(0, u)
		c.applied[i] = 0
	}
}

// showStatus says whether changes are on their way.
func (c *batchCard) showStatus(n int) {
	c.status.Text = "Thumbnails follow as each change lands."
	if c.busy > 0 {
		c.status.Text = fmt.Sprintf("Applying to %d photos…", n)
	}
}

// Children implements [gunim.Composite].
func (c *batchCard) Children() []gunim.Node { return []gunim.Node{c.body, c.close} }

// Layout implements [gunim.Node].
func (c *batchCard) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const pad = 16
	bs := kids.At(0).Layout(gunim.Loose(geom.Sz(batchCardW-2*pad, cs.Max.H)))
	kids.At(0).Place(geom.Pt(pad, pad))
	xs := kids.At(1).Layout(gunim.Loose(geom.Sz(28, 28)))
	kids.At(1).Place(geom.Pt(batchCardW-8-xs.W, 8))
	return geom.Sz(batchCardW, bs.H+2*pad)
}

// Paint implements [gunim.Node].
func (c *batchCard) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	paintGlass(p, geom.Rect{Max: box.Point()}, 14)
	for k := range kids.All {
		k.Paint(p)
	}
}
