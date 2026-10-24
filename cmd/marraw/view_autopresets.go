package main

import (
	"fmt"
	"math"
	"slices"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// --- Auto presets

// autoPresetsPage is the creative auto presets, as marraw's settings have
// them: a card each, its name, the autos it runs, and its offsets, then
// Add preset and Restore defaults.
type autoPresetsPage struct {
	n     gunim.Node
	cards []*autoPresetCard
}

func newAutoPresetsPage(s SettingsState) *autoPresetsPage {
	p := &autoPresetsPage{}
	rows := []gunim.Node{newSettingRow("Creative auto presets", "A preset runs the selected autos, then layers your "+
		"style on top. Sliders whose auto is active are added to the computed value; the rest are set to their exact "+
		"value (0 included). Apply the first nine with Ctrl+1…9.", nil), spacer(12)}
	// The offsets sit two to a row, their labels narrower than the panel's.
	th := marrawTheme().With(theme.Set(widget.SliderRowLabel, 90), theme.Set(widget.SliderRowValue, 58),
		theme.Set(widget.SliderRowSize, 12))
	for i, ap := range s.AutoPresets {
		c := newAutoPresetCard(i, ap)
		p.cards = append(p.cards, c)
		rows = append(rows, widget.NewThemed(c, th), spacer(12))
	}
	add, restore := widget.NewButton("Add preset"), widget.NewButton("Restore defaults")
	add.KeepFocus, add.OnClick = true, widget.Sends(AutoPresetOp{Op: "add"})
	restore.Ghost, restore.KeepFocus, restore.OnClick = true, true, widget.Sends(AutoPresetOp{Op: "restore"})
	restore.Tooltip = "The six presets marraw ships with, as they were and in their places; presets you added stay"
	p.n = settingsScroll(append(rows, widget.Row(add, restore))...)
	return p
}

func (p *autoPresetsPage) node() gunim.Node { return p.n }
func (p *autoPresetsPage) show(s SettingsState, u *gunim.UI) {
	for i, c := range p.cards {
		if i < len(s.AutoPresets) {
			c.show(s.AutoPresets[i], u)
		}
	}
}

// autoPresetCard is a creative auto preset: its place for Ctrl and a
// number, its name and Delete; the autos it runs; and its offsets, those
// added to an auto's result under one heading and those set as they are
// under another.
type autoPresetCard struct {
	num      *widget.Label
	name     *widget.TextField
	del      *confirmButton
	autoNote *widget.Label
	autos    []*widget.Checkbox
	// adds and sets head the offsets added to an auto and those set as
	// they are; rows are the offsets, in autoOffsets' order.
	adds, sets *widget.Label
	rows       []*widget.SliderRow
	sections   []string
	kids       []gunim.Node
}

// autoOffsetSnap is the step an offset's slider moves by, as marraw's
// settings have it: a twentieth of a stop, the hues' own step, and a
// hundredth for the rest.
func autoOffsetSnap(key string) float32 {
	switch key {
	case "expEV":
		return 0.05
	case "splitShadowHue", "splitHighlightHue":
		return devSpecs[key].snap
	}
	return 0.01
}

func newAutoPresetCard(i int, ap marrawclient.AutoPreset) *autoPresetCard {
	c := &autoPresetCard{num: newSmallLabel("·"), name: widget.NewTextField(), sections: slices.Clone(ap.Sections)}
	if i < 9 {
		c.num.Text = fmt.Sprint(i + 1)
	}
	c.num.Color = noteInk
	c.name.SetText(ap.Name, nil)
	c.name.Placeholder = "Preset name"
	c.name.OnChange = func(text string, _ *gunim.UI) gunim.Intent {
		return AutoPresetOp{Op: "name", Index: i, Name: text}
	}
	c.del = newConfirmButton("Delete", AutoPresetOp{Op: "delete", Index: i})
	c.autoNote = newSmallLabel("Auto")
	c.autoNote.Color = noteInk
	for _, s := range autoSections {
		b := widget.NewCheckbox(s.label)
		b.KeepFocus = true
		id := s.id
		b.OnChange = func(on bool, _ *gunim.UI) gunim.Intent {
			return AutoPresetOp{Op: "section", Index: i, Section: id, On: on}
		}
		c.autos = append(c.autos, b)
	}
	c.adds, c.sets = newSmallLabel("On top of auto  ·  added to the auto result"), newSmallLabel("Creative  ·  set to the exact value")
	c.adds.Color, c.sets.Color = headingInk, headingInk
	c.kids = []gunim.Node{c.num, c.name, c.del, c.autoNote}
	for _, b := range c.autos {
		c.kids = append(c.kids, b)
	}
	c.kids = append(c.kids, c.adds, c.sets)
	for _, o := range autoOffsets {
		sp := devSpecs[o.key]
		sl := widget.NewSlider(sp.min, sp.max)
		sl.Snap, sl.KeepFocus, sl.HasRest, sl.Gradient = autoOffsetSnap(o.key), true, true, sp.gradient
		key := o.key
		sl.OnCommit = func(x float32, _ *gunim.UI) gunim.Intent {
			// The slider's float32 rounded back to the step it moves by.
			return AutoPresetOp{Op: "offset", Index: i, Key: key, Value: math.Round(float64(x)*1e4) / 1e4}
		}
		r := widget.NewSliderRow(sp.label, sl)
		r.Format = sp.format
		sl.SetValue(float32(ap.Offsets[key]), nil)
		c.rows = append(c.rows, r)
		c.kids = append(c.kids, r)
	}
	c.show(ap, nil)
	return c
}

// show takes the preset as it is now. Its name is the field's own, set
// as the card is made, so a name being typed is never written over.
func (c *autoPresetCard) show(ap marrawclient.AutoPreset, u *gunim.UI) {
	c.sections = slices.Clone(ap.Sections)
	for i, s := range autoSections {
		c.autos[i].SetChecked(slices.Contains(ap.Sections, s.id), u)
	}
	for i, o := range autoOffsets {
		if s := c.rows[i].Slider; !s.Held() {
			s.SetValue(float32(ap.Offsets[o.key]), u)
		}
	}
	if u != nil {
		u.Invalidate()
	}
}

// Children implements [gunim.Composite].
func (c *autoPresetCard) Children() []gunim.Node { return c.kids }

// autoCardPad is the room inside a card's edge.
const autoCardPad = 12

// Layout implements [gunim.Node].
func (c *autoPresetCard) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := cs.Max.W
	x0, x1 := float32(autoCardPad), w-autoCardPad
	y := float32(autoCardPad)
	// The number, the name, and Delete.
	ds := kids.At(2).Layout(gunim.Loose(geom.Sz(120, 40)))
	ns := kids.At(1).Layout(gunim.Tight(geom.Sz(max(0, x1-ds.W-8-(x0+24)), 30)))
	nums := kids.At(0).Layout(gunim.Loose(geom.Sz(20, 30)))
	h := max(ns.H, ds.H)
	kids.At(0).Place(geom.Pt(x0+(16-nums.W)/2, y+(h-nums.H)/2))
	kids.At(1).Place(geom.Pt(x0+24, y+(h-ns.H)/2))
	kids.At(2).Place(geom.Pt(x1-ds.W, y+(h-ds.H)/2))
	y += h + 10
	// The autos it runs.
	as := kids.At(3).Layout(gunim.Loose(geom.Sz(60, 30)))
	x, rowH := x0+as.W+10, float32(0)
	for i := range c.autos {
		k := kids.At(4 + i)
		s := k.Layout(gunim.Loose(geom.Sz(x1-x, 30)))
		k.Place(geom.Pt(x, y))
		x += s.W + 6
		rowH = max(rowH, s.H)
	}
	kids.At(3).Place(geom.Pt(x0, y+(rowH-as.H)/2))
	y += rowH + 12
	// The offsets, those added to an auto first, in two columns.
	first := 4 + len(c.autos)
	colW := (x1 - x0 - 16) / 2
	block := func(head gunim.Child, adds bool) {
		var keys []int
		for i, o := range autoOffsets {
			if offsetAdds(o.key, c.sections) == adds {
				keys = append(keys, i)
			}
		}
		if len(keys) == 0 {
			head.Layout(gunim.Tight(geom.Size{}))
			head.Place(geom.Pt(-10000, 0))
			return
		}
		hs := head.Layout(gunim.Loose(geom.Sz(x1-x0, 30)))
		head.Place(geom.Pt(x0, y))
		y += hs.H + 4
		for n, i := range keys {
			k := kids.At(first + 2 + i)
			s := k.Layout(gunim.Loose(geom.Sz(colW, 40)))
			k.Place(geom.Pt(x0+float32(n%2)*(colW+16), y))
			if n%2 == 1 || n == len(keys)-1 {
				y += s.H + 2
			}
		}
		y += 8
	}
	block(kids.At(first), true)
	block(kids.At(first+1), false)
	return geom.Sz(w, y+autoCardPad-8)
}

// Paint implements [gunim.Node].
func (c *autoPresetCard) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRectStroke(geom.Rect{Max: box.Point()}.Inset(geom.Uniform(0.5)), 10, paint.Fill{},
		paint.Stroke{Width: 1, Color: autoCardEdge})
	for k := range kids.All {
		k.Paint(p)
	}
}

// autoCardEdge is a card's edge, a hairline of frost.
var autoCardEdge = frost(0x1c)
