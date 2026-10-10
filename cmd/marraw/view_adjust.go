package main

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// DevAdjust is the control + and - just stepped, for the heads-up
// readout: its label, its range and value, or for a choice the option
// it is on.
type DevAdjust struct {
	Label           string
	Min, Max, Value float32
	Rest            float32
	HasRest         bool
	Text            string
	Choice          bool
}

// adjustHUD is the heads-up readout of the control + and - step: while
// they step it, the panel and the rest of the chrome step aside, and
// this small glass pill at the foot of the photo shows just that one
// control. The pointer moving brings the chrome back.
type adjustHUD struct {
	label, value *widget.Label
	slider       *widget.Slider
	choice       bool
}

func newAdjustHUD() *adjustHUD {
	h := &adjustHUD{label: newSmallLabel(""), value: newSmallLabel(""), slider: widget.NewSlider(0, 1)}
	h.label.Color = noteInk
	h.value.Face, h.value.NoWrap, h.value.Align = widget.MonoFont, true, text.AlignEnd
	return h
}

// set shows a.
func (h *adjustHUD) set(a DevAdjust, u *gunim.UI) {
	h.label.Text, h.value.Text, h.choice = a.Label, a.Text, a.Choice
	if !a.Choice {
		h.slider.Min, h.slider.Max = a.Min, a.Max
		h.slider.Rest, h.slider.HasRest = a.Rest, a.HasRest
		h.slider.SetValue(a.Value, u)
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (h *adjustHUD) Children() []gunim.Node { return []gunim.Node{h.label, h.slider, h.value} }

// Layout implements [gunim.Node]: the label, the slider, the value, in
// a row; a choice shows no slider.
func (h *adjustHUD) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const padX, gap, sliderW, valueW, hh = 16, 14, 160, 64, 40
	ls := kids.At(0).Layout(gunim.Loose(geom.Sz(160, hh)))
	kids.At(0).Place(geom.Pt(padX, (hh-ls.H)/2))
	x := padX + ls.W + gap
	if h.choice {
		kids.At(1).Layout(gunim.Tight(geom.Sz(0, 0)))
		vs := kids.At(2).Layout(gunim.Loose(geom.Sz(200, hh)))
		kids.At(2).Place(geom.Pt(x, (hh-vs.H)/2))
		return geom.Sz(x+vs.W+padX, hh)
	}
	ss := kids.At(1).Layout(gunim.Tight(geom.Sz(sliderW, 24)))
	kids.At(1).Place(geom.Pt(x, (hh-ss.H)/2))
	x += sliderW + gap
	vs := kids.At(2).Layout(gunim.Tight(geom.Sz(valueW, 18)))
	kids.At(2).Place(geom.Pt(x, (hh-vs.H)/2))
	return geom.Sz(x+valueW+padX, hh)
}

// Paint implements [gunim.Node].
func (h *adjustHUD) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	paintGlass(p, geom.Rect{Max: box.Point()}, 13)
	kids.At(0).Paint(p)
	if !h.choice {
		kids.At(1).Paint(p)
	}
	kids.At(2).Paint(p)
}

// devAdjust shows the control + and - stepped in the heads-up readout,
// and puts the chrome aside while they step it.
func (v *cullView) devAdjust(a DevAdjust, u *gunim.UI) {
	v.adjust.set(a, u)
	if !v.adjusting {
		v.adjusting = true
		v.adjustIn.Animate(1, widget.Quick.Get(u.Theme()))
	}
	u.Invalidate()
}

// endAdjust brings the chrome back as the pointer moves.
func (v *cullView) endAdjust(u *gunim.UI) {
	if !v.adjusting {
		return
	}
	v.adjusting = false
	v.adjustIn.Animate(0, widget.Settle.Get(u.Theme()))
	u.Invalidate()
}
