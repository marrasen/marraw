package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"slices"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// Quick dials are develop controls the user pins to the bar at the foot of
// the photo, as marraw's Settings → Toolbars offers them: in Cull beside
// Pick and Reject (UISettings.CullDials), in Develop before Original and
// the zoom (QuickDials). None is the default, which keeps the bar compact.
//
// In Develop a dial is the panel's control. In Cull the panel is away, so
// the first touch of a dial opens the edit without it (developer.hidden),
// and stepping to another photo closes it again: culling loads no edit
// and holds no decode until a dial is used.

// quickDial is a control a toolbar can hold: a slider of devSpecs, or a
// choice of devChoices a click steps through. Its label is short, for the
// dial's 82 px; group is the panel's section it is chosen under.
type quickDial struct {
	key, label, group string
	// show writes a slider's value compactly; nil is a choice.
	show func(v float32) string
}

// Compact value formats, as marraw's dials write them.
var (
	dialPct    = func(v float32) string { return signedInt(v * 100) }
	dialEV     = func(v float32) string { return fmt.Sprintf("%+.2f", v) }
	dialPct0   = func(v float32) string { return strconv.Itoa(int(math.Round(float64(v * 100)))) }
	dialMult   = func(v float32) string { return fmt.Sprintf("%.2f×", v) }
	dialNum2   = func(v float32) string { return fmt.Sprintf("%.2f", v) }
	dialNum1   = func(v float32) string { return fmt.Sprintf("%.1f", v) }
	dialKelvin = func(v float32) string { return fmt.Sprintf("%dK", int(math.Round(float64(v)))) }
	dialHue    = func(v float32) string { return fmt.Sprintf("%d°", int(math.Round(float64(v)))) }
	dialInt    = func(v float32) string { return strconv.Itoa(int(math.Round(float64(v)))) }
	dialLens   = func(v float32) string { return fmt.Sprintf("%d%%", int(math.Round(float64((v+1)*100)))) }
	dialAngle  = func(v float32) string {
		if v == 0 {
			return "0°"
		}
		return fmt.Sprintf("%+.1f°", v)
	}
)

// signedInt writes v rounded, with its sign, and 0 bare.
func signedInt(v float32) string {
	n := int(math.Round(float64(v)))
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", n)
}

// quickDials are the dials, in the panel's order, as marraw's DIALS.
var quickDials = []quickDial{
	{"cropAngle", "Straighten", "Crop", dialAngle},
	{"expEV", "Exposure", "Tone", dialEV},
	{"expPreserve", "Preserve", "Tone", dialPct0},
	{"bright", "Brightness", "Tone", dialMult},
	{"gamma", "Gamma", "Tone", dialNum2},
	{"shadow", "Slope", "Tone", dialNum1},
	{"contrast", "Contrast", "Tone", dialPct},
	{"whites", "Whites", "Tone", dialPct},
	{"blacks", "Blacks", "Tone", dialPct},
	{"toneShadows", "Shadows", "Tone", dialPct},
	{"toneHighlights", "Highlights", "Tone", dialPct},
	{"clarity", "Clarity", "Presence", dialPct},
	{"texture", "Texture", "Presence", dialPct},
	{"dehaze", "Dehaze", "Presence", dialPct},
	{"wbMode", "WB mode", "White balance", nil},
	{"wbTemp", "Temp", "White balance", dialPct},
	{"wbKelvin", "Kelvin", "White balance", dialKelvin},
	{"wbTint", "Tint", "White balance", dialPct},
	{"bw", "B&W", "Color", nil},
	{"saturation", "Saturation", "Color", dialPct},
	{"vibrance", "Vibrance", "Color", dialPct},
	{"splitShadowHue", "Shadow hue", "Color", dialHue},
	{"splitShadowAmt", "Shadow amt", "Color", dialPct0},
	{"splitHighlightHue", "Highl hue", "Color", dialHue},
	{"splitHighlightAmt", "Highl amt", "Color", dialPct0},
	{"vignette", "Vignette", "Effects", dialPct},
	{"sharpen", "Sharpen", "Detail", dialPct0},
	{"highlight", "Recovery", "Detail", nil},
	{"nrThreshold", "Noise", "Detail", dialInt},
	{"fbddNoiseRd", "FBDD", "Detail", nil},
	{"medPasses", "Median", "Detail", dialInt},
	{"demosaic", "Demosaic", "Detail", nil},
	{"caRed", "CA red", "Detail", dialPct},
	{"caBlue", "CA blue", "Detail", dialPct},
	{"lensDistortion", "Lens dist", "Detail", dialLens},
	{"lensVignetting", "Lens vign", "Detail", dialLens},
	{"lensCA", "Lens CA", "Detail", dialLens},
}

// dialsOf narrows a stored choice of dials to those there are, in the
// catalog's order, as marraw's sanitizeDialKeys does.
func dialsOf(keys []string) []quickDial {
	var out []quickDial
	for _, d := range quickDials {
		if slices.Contains(keys, d.key) {
			out = append(out, d)
		}
	}
	return out
}

// The dials' vocabulary.
type (
	// DialsSet makes Keys the dials of Develop's bar, or of Cull's.
	DialsSet struct {
		Develop bool
		Keys    []string
	}
	// DialSet moves the slider dial Key to Value, letting go if Commit,
	// as DevSet does in the panel.
	DialSet struct {
		Key    string
		Value  float64
		Commit bool
	}
	// DialStep steps the choice dial Key on to its next option.
	DialStep struct{ Key string }
)

// dialsSet keeps a toolbar's dials, here and in the backend.
func (cu *culler) dialsSet(in DialsSet) {
	if cu.ui == nil {
		cu.ui = &marrawclient.UISettings{}
	}
	keys := make([]string, 0, len(in.Keys))
	for _, d := range dialsOf(in.Keys) {
		keys = append(keys, d.key)
	}
	if in.Develop {
		cu.ui.QuickDials = keys
		cu.call("The Develop dials could not be saved", func(ctx context.Context) error {
			return cu.api.Settings.SetQuickDials(ctx, keys)
		}, nil)
	} else {
		cu.ui.CullDials = keys
		cu.call("The Cull dials could not be saved", func(ctx context.Context) error {
			return cu.api.Settings.SetCullDials(ctx, keys)
		}, nil)
		cu.wantDialParams()
	}
	cu.settingsChanged()
	cu.showCull()
}

// panelShown reports whether the develop panel is out: open, and not
// only for a dial of Cull's bar.
func (cu *culler) panelShown() bool { return cu.dev.open && !cu.dev.hidden }

// dial runs fn, a change a dial makes, on the edit of the photo showing:
// at once where the edit is loaded, after it loads where it is loading,
// and in Cull, where it is not, after loading it without the panel.
func (cu *culler) dial(fn func()) {
	d := &cu.dev
	if !cu.culling || len(cu.photos) == 0 {
		return
	}
	id := cu.photos[cu.at].ID
	switch {
	case d.open && d.id == id && d.mounted && len(d.pending) == 0:
		fn()
		return
	case !d.open:
		// Cull: the edit opens without the panel. d.id goes, so no
		// change lands on the edit of the photo it last held before
		// this one's loads.
		d.open, d.hidden, d.id = true, true, 0
		d.edits, d.committed, d.confirmed = 0, 0, 0
		cu.loadEdit(cu.at)
	}
	d.pending = append(d.pending, fn)
}

// dialSet takes a slider dial's move.
func (cu *culler) dialSet(in DialSet) {
	cu.dial(func() {
		if cu.dev.hidden {
			// No panel: the keys stay Cull's, so the dial chooses no
			// control for them.
			cu.devSetQuiet(DevSet{Key: in.Key, Value: in.Value, Commit: in.Commit})
			return
		}
		cu.devSet(DevSet{Key: in.Key, Value: in.Value, Commit: in.Commit})
	})
}

// dialStep takes a choice dial's click: the next option, saved.
func (cu *culler) dialStep(in DialStep) {
	cu.dial(func() {
		d := &cu.dev
		ch, ok := devChoices[in.Key]
		if !ok {
			return
		}
		n := len(ch.options)
		if in.Key == "wbMode" {
			// Picked comes of the eyedropper alone.
			n = 3
		}
		next := (ch.get(&d.params) + 1) % n
		cu.devChoose(DevChoice{Key: in.Key, Index: next})
		if d.hidden {
			d.active = ""
		}
		cu.showCull()
	})
}

// devSetQuiet is devSet without choosing the control for the keys.
func (cu *culler) devSetQuiet(in DevSet) {
	d := &cu.dev
	active := d.active
	d.active = in.Key
	cu.devSet(in)
	d.active = active
	cu.showCull()
}

// dialParams are the edit the dials of Cull's bar show, of photo id: the
// loaded edit while it is open on the photo, else one fetched as the view
// steps, while Cull's bar has dials.
type dialParams struct {
	id     int64
	params *marrawclient.Params
	gen    int
}

// wantDialParams fetches the edit of the photo showing for Cull's dials,
// unless the bar has none or the loaded edit serves.
func (cu *culler) wantDialParams() {
	if !cu.culling || len(cu.photos) == 0 || cu.ui == nil || len(cu.ui.CullDials) == 0 {
		return
	}
	id := cu.photos[cu.at].ID
	dp := &cu.dialP
	if dp.id == id && dp.params != nil {
		return
	}
	dp.gen++
	gen := dp.gen
	dp.id, dp.params = id, nil
	go func() {
		p, err := cu.api.Edits.GetEditParams(cu.ctx, id)
		select {
		case cu.do <- func() {
			if gen != dp.gen {
				return
			}
			if err != nil {
				log.Printf("dials: %v", err)
				return
			}
			if p == nil {
				p = &marrawclient.Params{}
			}
			dp.params = p
			cu.showCull()
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// dialView is what the bar's dials show: the edit, and whether it is the
// photo's own yet (the dials take no input until it is).
func (cu *culler) dialView(p marrawclient.Photo) (*marrawclient.Params, bool) {
	if d := &cu.dev; d.open && d.id == p.ID {
		params := d.params
		if d.hover != nil {
			params = *d.hover
		}
		return &params, true
	}
	if dp := &cu.dialP; dp.id == p.ID && dp.params != nil {
		return dp.params, true
	}
	return nil, false
}

// dialText is the size of a dial's words.
var dialText = theme.Length("marraw.dial.text", 10)

// miniDial is one dial, 82 px wide, as marraw's MiniSlider and MiniCycle:
// its label and value over a thin slider, or its label over a chip that
// steps through the choice's options.
type miniDial struct {
	dial   quickDial
	label  *widget.Label
	value  *widget.Label
	slider *widget.Slider
	chip   *widget.Button
	kids   []gunim.Node
}

const miniDialW = 82

func newMiniDial(q quickDial) *miniDial {
	m := &miniDial{dial: q, label: newSmallLabel(q.label)}
	m.label.Size, m.label.Color, m.label.NoWrap = dialText, noteInk, true
	key := q.key
	if q.show == nil {
		m.chip = widget.NewButton("")
		m.chip.KeepFocus = true
		m.chip.OnClick = widget.Sends(DialStep{Key: key})
		th := marrawTheme().With(theme.Set(widget.ButtonHeight, 18), theme.Set(widget.ButtonPadding, 4),
			theme.Set(widget.ButtonRadius, 5), theme.Set(widget.TextSize, 10),
			theme.Set(widget.ButtonFill, frost(0x0d)), theme.Set(widget.ButtonHover, frost(0x18)))
		m.kids = []gunim.Node{m.label, widget.NewThemed(m.chip, th)}
		return m
	}
	sp := devSpecs[key]
	m.value = newSmallLabel("")
	m.value.Size, m.value.Face, m.value.NoWrap = dialText, widget.MonoFont, true
	m.slider = widget.NewSlider(sp.min, sp.max)
	m.slider.Snap, m.slider.KeepFocus, m.slider.HasRest = sp.snap, true, !sp.noRest
	m.slider.OnChange = func(x float32, _ *gunim.UI) gunim.Intent { return DialSet{Key: key, Value: float64(x)} }
	m.slider.OnCommit = func(x float32, _ *gunim.UI) gunim.Intent {
		return DialSet{Key: key, Value: float64(x), Commit: true}
	}
	th := marrawTheme().With(theme.Set(widget.KnobSize, 12), theme.Set(widget.SliderTrack, 3))
	m.kids = []gunim.Node{m.label, m.value, widget.NewThemed(m.slider, th)}
	return m
}

// set shows the dial's value in p, the photo's edit, taking input only
// when live; s is the panel's state, for a slider's rest.
func (m *miniDial) set(p *marrawclient.Params, live bool, s DevelopState, u *gunim.UI) {
	var params marrawclient.Params
	if p != nil {
		params = *p
	}
	if m.chip != nil {
		ch := devChoices[m.dial.key]
		m.chip.Label = ch.options[ch.get(&params)]
		m.chip.Disabled = !live
		return
	}
	sp := devSpecs[m.dial.key]
	v := float32(sp.get(&params))
	if p == nil {
		v = sp.restOf(s)
	}
	m.value.Text = m.dial.show(v)
	m.slider.Rest = sp.restOf(s)
	m.slider.Disabled = !live
	if !m.slider.Held() {
		m.slider.SetValue(v, u)
	}
}

// Children implements [gunim.Composite].
func (m *miniDial) Children() []gunim.Node { return m.kids }

// Layout implements [gunim.Node]: the label over the control, 82 px wide.
func (m *miniDial) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	ls := kids.At(0).Layout(gunim.Loose(geom.Sz(miniDialW, 14)))
	kids.At(0).Place(geom.Point{})
	if m.chip != nil {
		kids.At(1).Layout(gunim.Tight(geom.Sz(miniDialW, 18)))
		kids.At(1).Place(geom.Pt(0, ls.H+5))
		return geom.Sz(miniDialW, ls.H+5+18)
	}
	vs := kids.At(1).Layout(gunim.Loose(geom.Sz(miniDialW, 14)))
	kids.At(1).Place(geom.Pt(miniDialW-vs.W, 0))
	kids.At(2).Layout(gunim.Tight(geom.Sz(miniDialW, 14)))
	kids.At(2).Place(geom.Pt(0, ls.H+3))
	return geom.Sz(miniDialW, ls.H+3+14)
}

// Paint implements [gunim.Node].
func (m *miniDial) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// dialRow is the bar's dials: every dial there is, so none comes or goes
// from the tree, with the chosen ones laid out in the catalog's order and
// the rest away.
type dialRow struct {
	all   []*miniDial
	kids  []gunim.Node
	shown map[string]bool
}

func newDialRow() *dialRow {
	r := &dialRow{shown: map[string]bool{}}
	for _, q := range quickDials {
		m := newMiniDial(q)
		r.all = append(r.all, m)
		r.kids = append(r.kids, m)
	}
	return r
}

// set shows the dials keys, at their values in p, taking input when live.
func (r *dialRow) set(keys []string, p *marrawclient.Params, live bool, s DevelopState, u *gunim.UI) {
	clear(r.shown)
	for _, k := range keys {
		r.shown[k] = true
	}
	for _, m := range r.all {
		if r.shown[m.dial.key] {
			m.set(p, live, s, u)
		}
	}
}

// Children implements [gunim.Composite].
func (r *dialRow) Children() []gunim.Node { return r.kids }

// Layout implements [gunim.Node]: the chosen dials in a row, nothing wide
// where none is chosen.
func (r *dialRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const gap = 14
	var x, h float32
	for i, m := range r.all {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(miniDialW, 60)))
		if !r.shown[m.dial.key] {
			k.Place(geom.Pt(-10000, -10000))
			continue
		}
		if x > 0 {
			x += gap
		}
		k.Place(geom.Pt(x, 0))
		x += s.W
		h = max(h, s.H)
	}
	return geom.Sz(x, h)
}

// Paint implements [gunim.Node].
func (r *dialRow) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for i, m := range r.all {
		if r.shown[m.dial.key] {
			kids.At(i).Paint(p)
		}
	}
}

// toolbarsPage is the settings' Toolbars: which dials the bar at the foot
// of the photo holds in Cull and in Develop, as marraw's.
type toolbarsPage struct {
	n     gunim.Node
	cull  *dialPicker
	quick *dialPicker
}

func newToolbarsPage() *toolbarsPage {
	p := &toolbarsPage{
		cull: newDialPicker(false, "Cull toolbar dials",
			"Develop dials shown in Cull's bar, next to Pick and Reject. None keeps the bar compact."),
		quick: newDialPicker(true, "Develop quick dials",
			"Dials in Develop's bar over the photo, before Original and the zoom. None leaves just those."),
	}
	p.n = settingsScroll(p.cull.node, spacer(18), p.quick.node)
	return p
}

func (p *toolbarsPage) node() gunim.Node { return p.n }
func (p *toolbarsPage) show(s SettingsState, u *gunim.UI) {
	p.cull.show(s.CullDials)
	p.quick.show(s.QuickDials)
	u.Invalidate()
}

// dialPicker chooses one bar's dials: None, and a button for each dial,
// under the panel's section names, lit while chosen.
type dialPicker struct {
	develop bool
	node    gunim.Node
	none    *widget.Button
	buttons map[string]*widget.Button
	chosen  []string
}

func newDialPicker(develop bool, title, help string) *dialPicker {
	d := &dialPicker{develop: develop, buttons: map[string]*widget.Button{}}
	th := marrawTheme().With(theme.Set(widget.ButtonHeight, 28), theme.Set(widget.ButtonPadding, 10),
		theme.Set(widget.ButtonRadius, 8), theme.Set(widget.TextSize, 12))
	button := func(label string, onClick func() gunim.Intent) *widget.Button {
		b := widget.NewButton(label)
		b.KeepFocus = true
		b.OnClick = func(*gunim.UI) gunim.Intent { return onClick() }
		return b
	}
	d.none = button("None", func() gunim.Intent { return DialsSet{Develop: develop} })
	heading := newSmallLabel(title)
	note := newSmallLabel(help)
	note.Color, note.MaxLines = noteInk, 3
	rows := []gunim.Node{heading, spacer(2), note, spacer(10), widget.NewThemed(d.none, th)}
	var group string
	var kids []gunim.Node
	flush := func() {
		if len(kids) > 0 {
			rows = append(rows, widget.NewWrap(kids...))
			kids = nil
		}
	}
	for _, q := range quickDials {
		if q.group != group {
			flush()
			group = q.group
			rows = append(rows, spacer(10), sectionLabel(group))
		}
		key := q.key
		b := button(q.label, func() gunim.Intent {
			// The catalog's order whatever order they are clicked in, so
			// the bar's layout stays put.
			var keys []string
			for _, o := range quickDials {
				if on := slices.Contains(d.chosen, o.key); on != (o.key == key) {
					keys = append(keys, o.key)
				}
			}
			return DialsSet{Develop: develop, Keys: keys}
		})
		d.buttons[key] = b
		kids = append(kids, widget.NewThemed(b, th))
	}
	flush()
	d.node = widget.Column(rows...)
	return d
}

// show lights the dials chosen.
func (d *dialPicker) show(keys []string) {
	d.chosen = keys
	d.none.Active = len(keys) == 0
	for k, b := range d.buttons {
		b.Active = slices.Contains(keys, k)
	}
}
