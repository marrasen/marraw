package main

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
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
	// rest is the value the slider rests at, nought unless set; noRest
	// says it has none, as a hue.
	rest   func(s DevelopState) float32
	noRest bool
}

// bigStep is how far Shift with + or - steps sp, as marraw's control
// table has it.
func (sp devSpec) bigStep() float32 {
	switch sp.key {
	case "expEV", "bright", "gamma":
		return 0.25
	case "expPreserve":
		return 0.2
	case "shadow":
		return 1.5
	case "wbKelvin":
		return 250
	case "splitShadowHue", "splitHighlightHue":
		return 30
	case "nrThreshold":
		return 100
	case "medPasses":
		return 1
	}
	return 0.1
}

// restOf is the value spec sp rests at in s.
func (sp devSpec) restOf(s DevelopState) float32 {
	if sp.rest != nil {
		return sp.rest(s)
	}
	return 0
}

// hundred reads a value from -1 to 1 as -100 to 100, signed.
func hundred(v float32) string {
	n := int(math.Round(float64(v * 100)))
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", n)
}

// percentOff reads an amount from 0 to 1 as a percentage, and Off at
// nought.
func percentOff(v float32) string {
	n := int(math.Round(float64(v * 100)))
	if n == 0 {
		return "Off"
	}
	return strconv.Itoa(n)
}

// unit is a slider from -1 to 1, read as -100 to 100.
func unit(key, label string, get func(p *marrawclient.Params) *float64) devSpec {
	return devSpec{key: key, label: label, min: -1, max: 1, snap: 0.02, format: hundred,
		get: func(p *marrawclient.Params) float64 { return *get(p) },
		set: func(p *marrawclient.Params, v float64) { *get(p) = v }}
}

// amount is a slider from 0 to 1, read as a percentage, Off at nought.
func amount(key, label string, get func(p *marrawclient.Params) *float64) devSpec {
	s := unit(key, label, get)
	s.min, s.format = 0, percentOff
	return s
}

// stored is a slider over a value the edit stores as nought when it is at
// its default def: it reads def from nought, and writes nought at def,
// the spelling the backend keeps for untouched.
func stored(key, label string, lo, hi, snap, def float32, format func(float32) string, get func(p *marrawclient.Params) *float64) devSpec {
	return devSpec{key: key, label: label, min: lo, max: hi, snap: snap, format: format,
		get: func(p *marrawclient.Params) float64 {
			if *get(p) == 0 {
				return float64(def)
			}
			return *get(p)
		},
		set: func(p *marrawclient.Params, v float64) {
			if math.Abs(v-float64(def)) < 1e-6 {
				v = 0
			}
			*get(p) = v
		},
		rest: func(DevelopState) float32 { return def }}
}

var (
	temperatureTrack = []color.NRGBA{{R: 0x6f, G: 0xa8, B: 0xff, A: 0xff}, {R: 0xe9, G: 0xe3, B: 0xd0, A: 0xff}, {R: 0xff, G: 0xb0, B: 0x66, A: 0xff}}
	tintTrack        = []color.NRGBA{{R: 0x5c, G: 0xd0, B: 0x6e, A: 0xff}, {R: 0xd9, G: 0xd9, B: 0xd9, A: 0xff}, {R: 0xc8, G: 0x6f, B: 0xd0, A: 0xff}}
	hueTrack         = []color.NRGBA{{R: 0xff, A: 0xff}, {R: 0xff, G: 0xff, A: 0xff}, {G: 0xff, A: 0xff}, {G: 0xff, B: 0xff, A: 0xff}, {B: 0xff, A: 0xff}, {R: 0xff, B: 0xff, A: 0xff}, {R: 0xff, A: 0xff}}
)

// The colour mixer's bands, as marraw's mixer names and colours them.
var (
	bandNames = []string{"Red", "Orange", "Yellow", "Green", "Aqua", "Blue", "Purple", "Magenta"}
	bandInk   = []color.NRGBA{
		{R: 0xe5, G: 0x48, B: 0x4d, A: 0xff}, {R: 0xf7, G: 0x6b, B: 0x15, A: 0xff}, {R: 0xd9, G: 0xc4, A: 0xff}, {R: 0x46, G: 0xa7, B: 0x58, A: 0xff},
		{R: 0x12, G: 0xa5, B: 0x94, A: 0xff}, {R: 0x3d, G: 0x7d, B: 0xff, A: 0xff}, {R: 0x8e, G: 0x4e, B: 0xc6, A: 0xff}, {R: 0xd6, G: 0x40, B: 0x9f, A: 0xff},
	}
	// mixerKeys are the mixer's three adjustments, each a band's own.
	mixerKeys = []string{"hslHue", "hslSat", "hslLum"}
)

var devSpecs = func() map[string]devSpec {
	specs := []devSpec{
		{key: "expEV", label: "Exposure", min: -5, max: 5, snap: 0.05,
			format: func(v float32) string { return fmt.Sprintf("%+.2f EV", v) },
			get:    func(p *marrawclient.Params) float64 { return p.ExpEV },
			set:    func(p *marrawclient.Params, v float64) { p.ExpEV = v },
			rest:   func(s DevelopState) float32 { return float32(s.BaseExpEV) }},
		amount("expPreserve", "Preserve highlights", func(p *marrawclient.Params) *float64 { return &p.ExpPreserve }),
		stored("bright", "Brightness", 0.25, 4, 0.05, 1, func(v float32) string { return fmt.Sprintf("%.2f×", v) },
			func(p *marrawclient.Params) *float64 { return &p.Bright }),
		stored("gamma", "Gamma", 1, 3.5, 0.05, 2.222, func(v float32) string { return fmt.Sprintf("%.2f", v) },
			func(p *marrawclient.Params) *float64 { return &p.Gamma }),
		stored("shadow", "Shadow slope", 1, 12, 0.5, 4.5, func(v float32) string { return fmt.Sprintf("%.1f", v) },
			func(p *marrawclient.Params) *float64 { return &p.Shadow }),
		unit("contrast", "Contrast", func(p *marrawclient.Params) *float64 { return &p.Contrast }),
		unit("toneHighlights", "Highlights", func(p *marrawclient.Params) *float64 { return &p.ToneHighlights }),
		unit("toneShadows", "Shadows", func(p *marrawclient.Params) *float64 { return &p.ToneShadows }),
		unit("whites", "Whites", func(p *marrawclient.Params) *float64 { return &p.Whites }),
		unit("blacks", "Blacks", func(p *marrawclient.Params) *float64 { return &p.Blacks }),
		unit("texture", "Texture", func(p *marrawclient.Params) *float64 { return &p.Texture }),
		unit("clarity", "Clarity", func(p *marrawclient.Params) *float64 { return &p.Clarity }),
		unit("dehaze", "Dehaze", func(p *marrawclient.Params) *float64 { return &p.Dehaze }),
		unit("wbTemp", "Temperature", func(p *marrawclient.Params) *float64 { return &p.WBTemp }),
		{key: "wbKelvin", label: "Temperature", min: 2000, max: 12000, snap: 50,
			format: func(v float32) string { return fmt.Sprintf("%d K", int(v)) },
			get: func(p *marrawclient.Params) float64 {
				if p.WBKelvin == 0 {
					return 5500
				}
				return p.WBKelvin
			},
			set:  func(p *marrawclient.Params, v float64) { p.WBMode, p.WBKelvin = "kelvin", v },
			rest: func(DevelopState) float32 { return 5500 }},
		unit("wbTint", "Tint", func(p *marrawclient.Params) *float64 { return &p.WBTint }),
		unit("vibrance", "Vibrance", func(p *marrawclient.Params) *float64 { return &p.Vibrance }),
		unit("saturation", "Saturation", func(p *marrawclient.Params) *float64 { return &p.Saturation }),
		{key: "splitShadowHue", label: "Shadow tint", min: 0, max: 359, snap: 5, noRest: true,
			format: func(v float32) string { return fmt.Sprintf("%d°", int(v)) },
			get:    func(p *marrawclient.Params) float64 { return p.SplitShadowHue },
			set:    func(p *marrawclient.Params, v float64) { p.SplitShadowHue = v }},
		amount("splitShadowAmt", "Shadow amount", func(p *marrawclient.Params) *float64 { return &p.SplitShadowAmt }),
		{key: "splitHighlightHue", label: "Highlight tint", min: 0, max: 359, snap: 5, noRest: true,
			format: func(v float32) string { return fmt.Sprintf("%d°", int(v)) },
			get:    func(p *marrawclient.Params) float64 { return p.SplitHighlightHue },
			set:    func(p *marrawclient.Params, v float64) { p.SplitHighlightHue = v }},
		amount("splitHighlightAmt", "Highlight amount", func(p *marrawclient.Params) *float64 { return &p.SplitHighlightAmt }),
		unit("vignette", "Vignette", func(p *marrawclient.Params) *float64 { return &p.Vignette }),
		amount("sharpen", "Sharpen", func(p *marrawclient.Params) *float64 { return &p.Sharpen }),
		{key: "nrThreshold", label: "Noise reduction", min: 0, max: 1000, snap: 25,
			format: func(v float32) string { return strconv.Itoa(int(v)) },
			get:    func(p *marrawclient.Params) float64 { return p.NRThreshold },
			set:    func(p *marrawclient.Params, v float64) { p.NRThreshold = v }},
		{key: "medPasses", label: "Median passes", min: 0, max: 5, snap: 1,
			format: func(v float32) string { return strconv.Itoa(int(v)) },
			get:    func(p *marrawclient.Params) float64 { return float64(p.MedPasses) },
			set:    func(p *marrawclient.Params, v float64) { p.MedPasses = int(math.Round(v)) }},
		unit("caRed", "CA red", func(p *marrawclient.Params) *float64 { return &p.CARed }),
		unit("caBlue", "CA blue", func(p *marrawclient.Params) *float64 { return &p.CABlue }),
	}
	// The mixer's adjustments, a band's each, as "hslHue:3".
	for b := range bandNames {
		for k, key := range mixerKeys {
			label := []string{"Hue", "Saturation", "Luminance"}[k]
			at := func(p *marrawclient.Params) *float64 {
				switch k {
				case 0:
					return &p.HSLHue[b]
				case 1:
					return &p.HSLSat[b]
				}
				return &p.HSLLum[b]
			}
			s := unit(key+":"+strconv.Itoa(b), label, at)
			if k == 0 {
				// marraw reads the hue as a turn of up to 30 degrees.
				s.format = func(v float32) string {
					n := int(math.Round(float64(v * 30)))
					if n == 0 {
						return "0°"
					}
					return fmt.Sprintf("%+d°", n)
				}
			}
			specs = append(specs, s)
		}
	}
	out := map[string]devSpec{}
	for _, s := range specs {
		switch s.key {
		case "wbTemp", "wbKelvin":
			s.gradient = temperatureTrack
		case "wbTint":
			s.gradient = tintTrack
		case "splitShadowHue", "splitHighlightHue":
			s.gradient = hueTrack
		}
		out[s.key] = s
	}
	return out
}()

// devSection is a group of adjustments under a heading that folds them
// away: its sliders, choices, and the parts of its own, auto naming the
// sections its Auto button sets.
type devSection struct {
	title   string
	keys    []string
	choices []string
	auto    []string
	open    bool
}

// The panel's sections, in order, as marraw's develop panel has them.
var devSections = []devSection{
	{title: "Tone", keys: []string{"expEV", "expPreserve", "bright", "gamma", "shadow", "contrast", "toneHighlights", "toneShadows", "whites", "blacks"}, auto: []string{"tone"}, open: true},
	{title: "Presence", keys: []string{"texture", "clarity", "dehaze"}, open: true},
	{title: "White balance", keys: []string{"wbTemp", "wbKelvin", "wbTint"}, choices: []string{"wbMode"}, open: true},
	{title: "Color", keys: []string{"vibrance", "saturation", "splitShadowHue", "splitShadowAmt", "splitHighlightHue", "splitHighlightAmt"}, choices: []string{"bw"}, auto: []string{"wb", "color"}, open: true},
	{title: "Color mixer", open: false},
	{title: "Effects", keys: []string{"vignette"}, open: true},
	{title: "Detail", keys: []string{"sharpen", "nrThreshold", "medPasses", "caRed", "caBlue"}, choices: []string{"highlight", "fbddNoiseRd", "demosaic"}, open: false},
	{title: "Tone curve", open: true},
}

// sectionsOpen are the sections open, by title, kept as the panel comes
// and goes.
var sectionsOpen = map[string]bool{}

// The curve's channels, and their colours.
var (
	curveChannels = []string{"RGB", "Red", "Green", "Blue"}
	curveInk      = []color.NRGBA{{}, {R: 0xef, G: 0x44, B: 0x44, A: 0xff}, {R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}, {R: 0x3b, G: 0x82, B: 0xf6, A: 0xff}}
)

// panelWidth is the develop panel's width beside the photo.
const panelWidth = 360

// panelSlide carries the panel in and out, and the photo beside it.
var panelSlide = anim.Spring{Response: 0.34, Damping: 0.92}

var (
	panelFill    = color.NRGBA{R: 0x15, G: 0x17, B: 0x1c, A: 0xff}
	headingInk   = theme.Color("marraw.heading", color.NRGBA{R: 0x8a, G: 0x90, B: 0x9c, A: 0xff})
	headingSize  = theme.Length("marraw.heading.size", 11.5)
	autoInk      = theme.Color("marraw.auto", color.NRGBA{R: 0x7f, G: 0xb0, B: 0xff, A: 0xff})
	developInset = theme.Insets("marraw.develop.inset", geom.Insets{Top: 14, Right: 14, Bottom: 18, Left: 16})
)

// developView is the develop panel, beside the photo in the cull view:
// the histogram, then the sections, each folding open and shut under its
// heading, with a dot that shows while anything in it is changed. It
// slides in from the right. The sliders glide to each photo's edit as the
// cull view steps; the histogram morphs to each new preview; a row only
// one mode has folds in and out as the mode changes.
type developView struct {
	anim.Group
	st      DevelopState
	shown   bool
	in      *anim.Float
	hist    *widget.Histogram
	rows    map[string]*widget.SliderRow
	choices map[string]*widget.Segmented
	// folds hold the rows a mode shows or not, by key.
	folds   map[string]*widget.Fold
	heads   []*devHeading
	curve   *widget.ToneCurve
	channel *widget.Segmented
	band    int
	chips   *bandChips
	mixRows []*widget.SliderRow
	body    gunim.Node
	// choiceRows are the choices' rows, and sectionOf and nodeOf where
	// each control is, to show the one the keys act on.
	choiceRows map[string]*labeled
	sectionOf  map[string]int
	nodeOf     map[string]gunim.Node
	folds2     []*widget.Fold
	active     string
}

func newDevelopView(s DevelopState) *developView {
	v := &developView{in: anim.NewFloat(0), hist: widget.NewHistogram(), rows: map[string]*widget.SliderRow{},
		choices: map[string]*widget.Segmented{}, folds: map[string]*widget.Fold{},
		choiceRows: map[string]*labeled{}, sectionOf: map[string]int{}, nodeOf: map[string]gunim.Node{},
		curve: widget.NewToneCurve(), channel: widget.NewSegmented(curveChannels...)}
	v.Add(v.in)
	kids := []gunim.Node{v.hist}
	for si, sec := range devSections {
		var body []gunim.Node
		for _, key := range sec.choices {
			n := v.choiceRow(key)
			v.sectionOf[key], v.nodeOf[key] = si, n
			body = append(body, n)
		}
		for _, key := range sec.keys {
			var n gunim.Node = v.row(devSpecs[key])
			v.sectionOf[key], v.nodeOf[key] = si, n
			if key == "wbTemp" || key == "wbKelvin" {
				// One of the two shows, as the mode is Kelvin or not.
				f := widget.NewFold(n, (key == "wbKelvin") == (s.Params.WBMode == "kelvin"))
				v.folds[key] = f
				n = f
			}
			body = append(body, n)
		}
		switch sec.title {
		case "Color mixer":
			body = append(body, v.mixer()...)
		case "Tone curve":
			v.channel.KeepFocus = true
			v.channel.OnChange = func(i int) gunim.Intent { return DevChannel{Channel: i} }
			v.curve.OnChange = func(pts []geom.Point) gunim.Intent {
				return DevCurve{Channel: v.st.Channel, Points: pts}
			}
			v.curve.OnCommit = func(pts []geom.Point) gunim.Intent {
				return DevCurve{Channel: v.st.Channel, Points: pts, Commit: true}
			}
			body = append(body, v.channel, v.curve)
		}
		open, ok := sectionsOpen[sec.title]
		if !ok {
			open = sec.open
		}
		col := widget.Column(body...)
		fold := widget.NewFold(widget.NewPad(col), open)
		fold.Children()[0].(*widget.Pad).Padding = theme.Insets("marraw.section.pad", geom.Insets{Bottom: 6})
		h := newDevHeading(sec, fold)
		v.heads = append(v.heads, h)
		v.folds2 = append(v.folds2, fold)
		kids = append(kids, h, fold)
	}
	col := widget.Column(kids...)
	pad := widget.NewPad(col)
	pad.Padding = developInset
	v.body = widget.NewScroll(pad)
	return v
}

// row is the slider row of spec sp, wired to the culler.
func (v *developView) row(sp devSpec) *widget.SliderRow {
	sl := widget.NewSlider(sp.min, sp.max)
	sl.Snap, sl.KeepFocus, sl.Gradient = sp.snap, true, sp.gradient
	sl.HasRest = !sp.noRest
	key := sp.key
	sl.OnChange = func(x float32) gunim.Intent { return DevSet{Key: key, Value: float64(x)} }
	sl.OnCommit = func(x float32) gunim.Intent { return DevSet{Key: key, Value: float64(x), Commit: true} }
	r := widget.NewSliderRow(sp.label, sl)
	r.Format = sp.format
	v.rows[key] = r
	return r
}

// choiceRow is a choice's label and its segments, wired to the culler.
func (v *developView) choiceRow(key string) gunim.Node {
	ch := devChoices[key]
	seg := widget.NewSegmented(ch.options...)
	seg.KeepFocus = true
	seg.OnChange = func(i int) gunim.Intent { return DevChoice{Key: key, Index: i} }
	v.choices[key] = seg
	l := &labeled{label: newSmallLabel(ch.label), child: seg, active: anim.NewFloat(0)}
	l.Add(l.active)
	v.choiceRows[key] = l
	return l
}

// mixer is the colour mixer: the bands as chips, and the chosen band's
// hue, saturation and luminance.
func (v *developView) mixer() []gunim.Node {
	v.chips = newBandChips(func(b int, u *gunim.UI) { v.chooseBand(b, u) })
	out := []gunim.Node{v.chips}
	for k, key := range mixerKeys {
		sp := devSpecs[key+":0"]
		sl := widget.NewSlider(sp.min, sp.max)
		sl.Snap, sl.KeepFocus, sl.HasRest = sp.snap, true, true
		// The band the slider moves is the one chosen when it moves.
		sl.OnChange = func(x float32) gunim.Intent {
			return DevSet{Key: mixerKeys[k] + ":" + strconv.Itoa(v.band), Value: float64(x)}
		}
		sl.OnCommit = func(x float32) gunim.Intent {
			return DevSet{Key: mixerKeys[k] + ":" + strconv.Itoa(v.band), Value: float64(x), Commit: true}
		}
		r := widget.NewSliderRow(sp.label, sl)
		r.Format = sp.format
		v.mixRows = append(v.mixRows, r)
		out = append(out, r)
	}
	return out
}

// chooseBand shows band b's mixer values, the sliders gliding to them.
func (v *developView) chooseBand(b int, u *gunim.UI) {
	v.band = b
	v.showMixer(false, u)
	u.Invalidate()
}

func (v *developView) showMixer(first bool, u *gunim.UI) {
	if v.chips == nil {
		return
	}
	v.chips.choose(v.band, u)
	for k, r := range v.mixRows {
		sp := devSpecs[mixerKeys[k]+":"+strconv.Itoa(v.band)]
		x := float32(sp.get(&v.st.Params))
		if first {
			r.Slider.Set(x)
		} else if !r.Slider.Held() {
			r.Slider.SetValue(x, u)
		}
		// In black and white a band's hue and saturation mean nothing.
		r.Slider.Disabled = v.st.Params.BW && k < 2
	}
}

func (v *developView) show(s DevelopState, u *gunim.UI) {
	first := !v.shown
	v.shown = true
	v.st = s
	p := &s.Params
	for key, r := range v.rows {
		sp := devSpecs[key]
		sl := r.Slider
		sl.Rest = sp.restOf(s)
		if !sl.Held() {
			x := float32(sp.get(p))
			if first {
				sl.Set(x)
			} else {
				// Another photo's edit, or a step of its history: each
				// slider glides to it.
				sl.SetValue(x, u)
			}
		}
	}
	for key, seg := range v.choices {
		seg.SetSelected(devChoices[key].get(p), u)
	}
	// White balance: Kelvin's own slider in Kelvin mode, and nothing to
	// move in Auto.
	kelvin, auto := p.WBMode == "kelvin", p.WBMode == "auto"
	v.folds["wbKelvin"].SetOpen(kelvin, u)
	v.folds["wbTemp"].SetOpen(!kelvin, u)
	v.rows["wbTemp"].Slider.Disabled = auto
	v.rows["wbTint"].Slider.Disabled = auto
	// Black and white: no colour to saturate.
	v.rows["vibrance"].Slider.Disabled = p.BW
	v.rows["saturation"].Slider.Disabled = p.BW
	v.showMixer(first, u)
	for _, h := range v.heads {
		h.setChanged(sectionChanged(h.sec, s), u)
	}
	v.channel.SetSelected(s.Channel, u)
	v.curve.Color = curveInk[s.Channel]
	var guides []widget.CurveGuide
	for ch := range 4 {
		if ch != s.Channel && ch != 0 {
			guides = append(guides, widget.CurveGuide{Points: points(*curveOf(p, ch)), Color: curveInk[ch]})
		}
	}
	v.curve.Guides = guides
	v.curve.SetPoints(points(*curveOf(p, s.Channel)), u)
	v.showActive(s.Active, u)
	u.Invalidate()
}

// showActive marks the control the keys act on, opens its section if it
// is shut, and brings it into view.
func (v *developView) showActive(key string, u *gunim.UI) {
	for k, r := range v.rows {
		r.SetActive(k == key, u)
	}
	for k, l := range v.choiceRows {
		l.setActive(k == key, u)
	}
	if key == v.active {
		return
	}
	v.active = key
	si, ok := v.sectionOf[key]
	if !ok {
		return
	}
	n := v.nodeOf[key]
	if f := v.folds2[si]; !f.Open() {
		v.heads[si].setOpen(true, u)
		// Once it has opened.
		u.After(foldTime, func(u *gunim.UI) { u.Reveal(n) })
		return
	}
	u.Reveal(n)
}

// foldTime is about how long a section takes to fold open.
const foldTime = 320 * time.Millisecond

// sectionChanged reports whether anything in sec is off its rest in s.
func sectionChanged(sec devSection, s DevelopState) bool {
	p := &s.Params
	for _, key := range sec.keys {
		sp := devSpecs[key]
		if sp.noRest || key == "wbKelvin" && p.WBMode != "kelvin" || key == "wbTemp" && p.WBMode == "kelvin" {
			continue
		}
		if math.Abs(sp.get(p)-float64(sp.restOf(s))) > 1e-4 {
			return true
		}
	}
	for _, key := range sec.choices {
		if devChoices[key].get(p) != 0 {
			return true
		}
	}
	switch sec.title {
	case "Color mixer":
		for b := range bandNames {
			if p.HSLHue[b] != 0 || p.HSLSat[b] != 0 || p.HSLLum[b] != 0 {
				return true
			}
		}
	case "Tone curve":
		return len(p.ToneCurve)+len(p.ToneCurveR)+len(p.ToneCurveG)+len(p.ToneCurveB) > 0
	}
	return false
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

// devHeading is a section's heading: a click folds the section open or
// shut, the chevron turning with it, a dot shows while anything in the
// section is changed, and Auto, where the section has it, sets it.
type devHeading struct {
	anim.Group
	sec            devSection
	fold           *widget.Fold
	title, auto    *widget.Label
	turn, dot, hot *anim.Float
	autoRect       geom.Rect
	box            geom.Size
}

func newDevHeading(sec devSection, fold *widget.Fold) *devHeading {
	h := &devHeading{sec: sec, fold: fold, title: widget.NewLabel(strings.ToUpper(sec.title)), auto: widget.NewLabel(""),
		turn: anim.NewFloat(0), dot: anim.NewFloat(0), hot: anim.NewFloat(0)}
	h.title.Color, h.title.Size = headingInk, headingSize
	h.auto.Color, h.auto.Size = autoInk, headingSize
	if len(sec.auto) > 0 {
		h.auto.SetText("Auto")
	}
	if fold.Open() {
		h.turn.Jump(1)
	}
	h.Add(h.turn, h.dot, h.hot)
	return h
}

// setOpen folds the section open or shut, the chevron turning with it.
func (h *devHeading) setOpen(open bool, u *gunim.UI) {
	h.fold.SetOpen(open, u)
	sectionsOpen[h.sec.title] = open
	h.turn.Animate(map[bool]float32{false: 0, true: 1}[open], widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// setChanged shows or hides the changed dot.
func (h *devHeading) setChanged(on bool, u *gunim.UI) {
	h.dot.Animate(map[bool]float32{false: 0, true: 1}[on], widget.Quick.Get(u.Theme()))
}

// Children implements [gunim.Composite].
func (h *devHeading) Children() []gunim.Node { return []gunim.Node{h.title, h.auto} }

// Handle implements [gunim.Handler].
func (h *devHeading) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerMove:
		hot := len(h.sec.auto) > 0 && h.autoRect.Inset(geom.Uniform(-4)).Contains(e.Pos)
		h.hot.Animate(map[bool]float32{false: 0, true: 1}[hot], widget.Quick.Get(th))
		u.Invalidate()
		return false
	case input.PointerLeave:
		h.hot.Animate(0, widget.Settle.Get(th))
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if len(h.sec.auto) > 0 && h.autoRect.Inset(geom.Uniform(-4)).Contains(e.Pos) {
			u.Send(h, DevAuto{Sections: h.sec.auto})
			return true
		}
		h.setOpen(!h.fold.Open(), u)
		return true
	}
	return false
}

// Layout implements [gunim.Node].
func (h *devHeading) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	hh := float32(34)
	title, auto := kids.At(0), kids.At(1)
	ts := title.Layout(gunim.Loose(geom.Sz(w, hh)))
	title.Place(geom.Pt(18, hh-ts.H-6))
	as := auto.Layout(gunim.Loose(geom.Sz(80, hh)))
	h.autoRect = geom.Rc(w-as.W-2, hh-as.H-6, as.W, as.H)
	auto.Place(h.autoRect.Min)
	h.box = geom.Sz(w, hh)
	return h.box
}

// Paint implements [gunim.Node].
func (h *devHeading) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	ink := headingInk.Get(th)
	// The chevron, pointing down when open and right when shut.
	c := geom.Pt(6, box.H-12)
	func() {
		defer p.Push(paint.Rotate(float32(-math.Pi/2)*(1-h.turn.Value()), c))()
		widget.PaintIcon(p, th, icon.ChevronDown, geom.Rc(c.X-6, c.Y-6, 12, 12), ink)
	}()
	if d := h.dot.Value(); d > 0.01 {
		ts := kids.At(0).Size()
		s := 6 * d
		at := geom.Pt(18+ts.W+8, box.H-6-ts.H/2)
		p.RRect(geom.Rc(at.X-s/2, at.Y-s/2, s, s), s/2, paint.Solid(withAlpha(autoInk.Get(th), d)))
	}
	if hot := h.hot.Value(); hot > 0.01 && len(h.sec.auto) > 0 {
		p.RRect(h.autoRect.Inset(geom.Insets{Top: -3, Bottom: -3, Left: -6, Right: -6}), 5,
			paint.Solid(withAlpha(color.NRGBA{R: 0x7f, G: 0xb0, B: 0xff, A: 0x22}, hot)))
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// newSmallLabel is a label in the slider rows' size.
func newSmallLabel(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Size, l.MaxLines = widget.SliderRowSize, 1
	return l
}

// labeled is a label, then a control filling the rest of the row, as a
// slider row lays them out.
type labeled struct {
	anim.Group
	label  *widget.Label
	child  gunim.Node
	active *anim.Float
	on     bool
}

// setActive marks the row as the one the keys act on, as a slider row
// marks itself.
func (l *labeled) setActive(on bool, u *gunim.UI) {
	if on == l.on {
		return
	}
	l.on = on
	l.active.Animate(map[bool]float32{false: 0, true: 1}[on], widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (l *labeled) Children() []gunim.Node { return []gunim.Node{l.label, l.child} }

// Layout implements [gunim.Node].
func (l *labeled) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	lw := widget.SliderRowLabel.Get(f.Theme)
	label, child := kids.At(0), kids.At(1)
	cs := child.Layout(gunim.Tight(geom.Sz(max(0, w-lw), widget.SegmentedHeight.Get(f.Theme))))
	h := max(cs.H, 28) + 4
	ls := label.Layout(gunim.Loose(geom.Sz(lw, h)))
	label.Place(geom.Pt(0, (h-ls.H)/2))
	child.Place(geom.Pt(lw, (h-cs.H)/2))
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (l *labeled) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	widget.PaintActive(p, f.Theme, box, l.active.Value())
	for k := range kids.All {
		k.Paint(p)
	}
}

// bandChips are the colour mixer's bands as dots of their colours, a ring
// gliding to the one chosen, and each swelling under the pointer.
type bandChips struct {
	anim.Group
	pick  func(b int, u *gunim.UI)
	at    *anim.Float
	hover int
	swell []*anim.Float
	box   geom.Size
}

func newBandChips(pick func(b int, u *gunim.UI)) *bandChips {
	c := &bandChips{pick: pick, at: anim.NewFloat(0), hover: -1}
	c.Add(c.at)
	for range bandNames {
		f := anim.NewFloat(0)
		c.swell = append(c.swell, f)
		c.Add(f)
	}
	return c
}

// choose moves the ring to band b.
func (c *bandChips) choose(b int, u *gunim.UI) {
	if u == nil {
		c.at.Jump(float32(b))
		return
	}
	c.at.Animate(float32(b), widget.Bounce.Get(u.Theme()))
}

// centre is where band b's dot sits.
func (c *bandChips) centre(b int) geom.Point {
	step := c.box.W / float32(len(bandNames))
	return geom.Pt(step*(float32(b)+0.5), c.box.H/2)
}

func (c *bandChips) bandAt(p geom.Point) int {
	if c.box.W <= 0 {
		return -1
	}
	b := int(p.X / (c.box.W / float32(len(bandNames))))
	if b < 0 || b >= len(bandNames) {
		return -1
	}
	return b
}

// Handle implements [gunim.Handler].
func (c *bandChips) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerMove:
		if b := c.bandAt(e.Pos); b != c.hover {
			if c.hover >= 0 {
				c.swell[c.hover].Animate(0, widget.Settle.Get(th))
			}
			if b >= 0 {
				c.swell[b].Animate(1, widget.Quick.Get(th))
			}
			c.hover = b
			u.Invalidate()
		}
		return false
	case input.PointerLeave:
		if c.hover >= 0 {
			c.swell[c.hover].Animate(0, widget.Settle.Get(th))
			c.hover = -1
		}
		return false
	case input.PointerDown:
		if b := c.bandAt(e.Pos); b >= 0 && e.Button == input.ButtonPrimary {
			c.pick(b, u)
			return true
		}
	}
	return false
}

// Layout implements [gunim.Node].
func (c *bandChips) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	c.box = geom.Sz(cs.Max.W, 34)
	return c.box
}

// Paint implements [gunim.Node].
func (c *bandChips) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, _ gunim.Children) {
	for b, ink := range bandInk {
		at := c.centre(b)
		d := 14 * (1 + 0.25*c.swell[b].Value())
		p.RRect(geom.Rc(at.X-d/2, at.Y-d/2, d, d), d/2, paint.Solid(ink))
	}
	// The ring, between two dots as it glides.
	pos := c.at.Value()
	b0 := int(math.Floor(float64(pos)))
	b1 := min(b0+1, len(bandNames)-1)
	t := pos - float32(b0)
	a, b := c.centre(max(0, b0)), c.centre(b1)
	at := geom.Pt(a.X+(b.X-a.X)*t, a.Y)
	ink := anim.Mix(anim.ColorCodec, bandInk[max(0, b0)], bandInk[b1], t)
	r := geom.Rc(at.X-12, at.Y-12, 24, 24)
	p.RRectStroke(r, 12, paint.Fill{}, paint.Stroke{Width: 2, Color: ink})
}
