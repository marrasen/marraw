package main

import (
	"fmt"
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// devSpec is one adjustment in the panel: its slider's range and step in
// the edit's own units, as marraw's control table has them, how its value
// reads, and where it lives in the edit.
type devSpec struct {
	key, label     string
	min, max, snap float32
	format         func(v float32) string
	gradient       []color.NRGBA
	get            func(p *marrawclient.Params) float64
	set            func(p *marrawclient.Params, v float64)
	// rest is the value the slider rests at, nought unless set.
	rest func(s DevelopState) float32
}

// devSection is a group of adjustments under a heading.
type devSection struct {
	title string
	keys  []string
}

// The panel's adjustments, in order, as marraw's develop panel has them.
var devSections = []devSection{
	{"Tone", []string{"expEV", "contrast", "toneHighlights", "toneShadows", "whites", "blacks"}},
	{"Presence", []string{"texture", "clarity", "dehaze"}},
	{"White balance", []string{"wbTemp", "wbTint"}},
	{"Color", []string{"vibrance", "saturation"}},
	{"Effects", []string{"vignette"}},
}

// hundred reads a value from -1 to 1 as -100 to 100, signed.
func hundred(v float32) string {
	n := int(v*100 + 0.5*sign(v))
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", n)
}

func sign(v float32) float32 {
	if v < 0 {
		return -1
	}
	return 1
}

// unit is a slider from -1 to 1, read as -100 to 100.
func unit(key, label string, get func(p *marrawclient.Params) *float64) devSpec {
	return devSpec{key: key, label: label, min: -1, max: 1, snap: 0.02, format: hundred,
		get: func(p *marrawclient.Params) float64 { return *get(p) },
		set: func(p *marrawclient.Params, v float64) { *get(p) = v }}
}

var (
	temperatureTrack = []color.NRGBA{{R: 0x6f, G: 0xa8, B: 0xff, A: 0xff}, {R: 0xe9, G: 0xe3, B: 0xd0, A: 0xff}, {R: 0xff, G: 0xb0, B: 0x66, A: 0xff}}
	tintTrack        = []color.NRGBA{{R: 0x5c, G: 0xd0, B: 0x6e, A: 0xff}, {R: 0xd9, G: 0xd9, B: 0xd9, A: 0xff}, {R: 0xc8, G: 0x6f, B: 0xd0, A: 0xff}}
)

var devSpecs = func() map[string]devSpec {
	specs := []devSpec{
		{key: "expEV", label: "Exposure", min: -5, max: 5, snap: 0.05,
			format: func(v float32) string { return fmt.Sprintf("%+.2f EV", v) },
			get:    func(p *marrawclient.Params) float64 { return p.ExpEV },
			set:    func(p *marrawclient.Params, v float64) { p.ExpEV = v },
			rest:   func(s DevelopState) float32 { return float32(s.BaseExpEV) }},
		unit("contrast", "Contrast", func(p *marrawclient.Params) *float64 { return &p.Contrast }),
		unit("toneHighlights", "Highlights", func(p *marrawclient.Params) *float64 { return &p.ToneHighlights }),
		unit("toneShadows", "Shadows", func(p *marrawclient.Params) *float64 { return &p.ToneShadows }),
		unit("whites", "Whites", func(p *marrawclient.Params) *float64 { return &p.Whites }),
		unit("blacks", "Blacks", func(p *marrawclient.Params) *float64 { return &p.Blacks }),
		unit("texture", "Texture", func(p *marrawclient.Params) *float64 { return &p.Texture }),
		unit("clarity", "Clarity", func(p *marrawclient.Params) *float64 { return &p.Clarity }),
		unit("dehaze", "Dehaze", func(p *marrawclient.Params) *float64 { return &p.Dehaze }),
		unit("wbTemp", "Temperature", func(p *marrawclient.Params) *float64 { return &p.WBTemp }),
		unit("wbTint", "Tint", func(p *marrawclient.Params) *float64 { return &p.WBTint }),
		unit("vibrance", "Vibrance", func(p *marrawclient.Params) *float64 { return &p.Vibrance }),
		unit("saturation", "Saturation", func(p *marrawclient.Params) *float64 { return &p.Saturation }),
		unit("vignette", "Vignette", func(p *marrawclient.Params) *float64 { return &p.Vignette }),
	}
	out := map[string]devSpec{}
	for _, s := range specs {
		switch s.key {
		case "wbTemp":
			s.gradient = temperatureTrack
		case "wbTint":
			s.gradient = tintTrack
		}
		out[s.key] = s
	}
	return out
}()

// The curve's channels, and their colours.
var (
	curveChannels = []string{"RGB", "Red", "Green", "Blue"}
	curveInk      = []color.NRGBA{{}, {R: 0xef, G: 0x44, B: 0x44, A: 0xff}, {R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}, {R: 0x3b, G: 0x82, B: 0xf6, A: 0xff}}
)

// panelWidth is the develop panel's width beside the photo.
const panelWidth = 330

// panelSlide carries the panel in and out, and the photo beside it.
var panelSlide = anim.Spring{Response: 0.34, Damping: 0.92}

var (
	panelFill    = color.NRGBA{R: 0x15, G: 0x17, B: 0x1c, A: 0xff}
	headingInk   = theme.Color("marraw.heading", color.NRGBA{R: 0x8a, G: 0x90, B: 0x9c, A: 0xff})
	headingSize  = theme.Length("marraw.heading.size", 11.5)
	developInset = theme.Insets("marraw.develop.inset", geom.Insets{Top: 14, Right: 14, Bottom: 18, Left: 16})
)

// developView is the develop panel, beside the photo in the cull view:
// the histogram, the adjustments under their headings, and the tone
// curve. It slides in from the right. The sliders glide to each photo's
// edit as the cull view steps; the histogram morphs to each new preview.
type developView struct {
	anim.Group
	st      DevelopState
	shown   bool
	in      *anim.Float
	hist    *widget.Histogram
	rows    map[string]*widget.SliderRow
	curve   *widget.ToneCurve
	channel *widget.Segmented
	body    gunim.Node
}

func newDevelopView(s DevelopState) *developView {
	v := &developView{in: anim.NewFloat(0), hist: widget.NewHistogram(), rows: map[string]*widget.SliderRow{},
		curve: widget.NewToneCurve(), channel: widget.NewSegmented(curveChannels...)}
	v.Add(v.in)
	kids := []gunim.Node{v.hist}
	for _, sec := range devSections {
		kids = append(kids, heading(sec.title))
		for _, key := range sec.keys {
			kids = append(kids, v.row(devSpecs[key], s))
		}
	}
	v.channel.KeepFocus = true
	v.channel.OnChange = func(i int) gunim.Intent { return DevChannel{Channel: i} }
	v.curve.OnChange = func(pts []geom.Point) gunim.Intent {
		return DevCurve{Channel: v.st.Channel, Points: pts}
	}
	v.curve.OnCommit = func(pts []geom.Point) gunim.Intent {
		return DevCurve{Channel: v.st.Channel, Points: pts, Commit: true}
	}
	kids = append(kids, heading("Tone curve"), v.channel, v.curve)
	col := widget.Column(kids...)
	pad := widget.NewPad(col)
	pad.Padding = developInset
	v.body = widget.NewScroll(pad)
	return v
}

// heading is a section's title.
func heading(s string) gunim.Node {
	l := widget.NewLabel(s)
	l.Color, l.Size = headingInk, headingSize
	p := widget.NewPad(l)
	p.Padding = theme.Insets("marraw.heading.pad", geom.Insets{Top: 12, Bottom: 2})
	return p
}

// row is the slider row of spec sp, wired to the culler.
func (v *developView) row(sp devSpec, s DevelopState) *widget.SliderRow {
	sl := widget.NewSlider(sp.min, sp.max)
	sl.Snap, sl.KeepFocus, sl.Gradient = sp.snap, true, sp.gradient
	sl.HasRest = true
	key := sp.key
	sl.OnChange = func(x float32) gunim.Intent { return DevSet{Key: key, Value: float64(x)} }
	sl.OnCommit = func(x float32) gunim.Intent { return DevSet{Key: key, Value: float64(x), Commit: true} }
	r := widget.NewSliderRow(sp.label, sl)
	r.Format = sp.format
	v.rows[key] = r
	return r
}

func (v *developView) show(s DevelopState, u *gunim.UI) {
	first := !v.shown
	v.shown = true
	v.st = s
	for key, r := range v.rows {
		sp := devSpecs[key]
		sl := r.Slider
		if sp.rest != nil {
			sl.Rest = sp.rest(s)
		}
		if sl.Held() {
			continue
		}
		x := float32(sp.get(&s.Params))
		if first {
			sl.Set(x)
		} else {
			// Another photo's edit: each slider glides to it.
			sl.SetValue(x, u)
		}
	}
	v.channel.SetSelected(s.Channel, u)
	v.curve.Color = curveInk[s.Channel]
	var guides []widget.CurveGuide
	for ch := range 4 {
		if ch != s.Channel && ch != 0 {
			guides = append(guides, widget.CurveGuide{Points: points(*curveOf(&s.Params, ch)), Color: curveInk[ch]})
		}
	}
	v.curve.Guides = guides
	v.curve.SetPoints(points(*curveOf(&s.Params, s.Channel)), u)
	u.Invalidate()
}

// points are a curve's points for the curve widget.
func points(c []marrawclient.CurvePoint) []geom.Point {
	var out []geom.Point
	for _, p := range c {
		out = append(out, geom.Pt(float32(p.X), float32(p.Y)))
	}
	return out
}

// histIn shows a new histogram.
func (v *developView) histIn(h DevHist, u *gunim.UI) { v.hist.SetCounts(h.Counts, u) }

// Transition implements [gunim.Transitioner]: it slides in from the
// right, and out again.
func (v *developView) Transition(p gunim.Presence, _ gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		v.in.Animate(1, panelSlide)
	case gunim.Exiting:
		v.in.Animate(0, panelSlide)
	case gunim.Present:
	}
	return !v.in.Active()
}

// Children implements [gunim.Composite].
func (v *developView) Children() []gunim.Node { return []gunim.Node{v.body} }

// Layout implements [gunim.Node].
func (v *developView) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	k.Layout(gunim.Tight(c.Max))
	k.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (v *developView) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(v.in.Value(), 0), 1)
	if in < 0.001 {
		return
	}
	defer p.Push(paint.Translate(geom.Pt((1-in)*box.W, 0)))()
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(panelFill))
	p.RRect(geom.Rc(0, 0, 1, box.H), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
	kids.At(0).Paint(p)
}
