package main

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// settingsView is the settings dialog: a list of its sections down the
// left, and the section chosen at the right, as marraw's.
type settingsView struct {
	*widget.Dialog
	body *settingsBody
}

func newSettingsView(s SettingsState) *settingsView {
	d := widget.NewDialog("Settings")
	b := newSettingsBody(s)
	d.Body = b
	d.Width = 760
	d.SetButtons("Done", "")
	d.OnAccept = widget.Sends(SettingsDone{})
	d.OnDismiss = widget.Sends(SettingsDone{})
	return &settingsView{Dialog: d, body: b}
}

// show takes s.
func (v *settingsView) show(s SettingsState, u *gunim.UI) { v.body.show(s, u) }

// settingsBody is the dialog's body: the sections' list and the section
// chosen, which slides in as another is chosen.
type settingsBody struct {
	anim.Group
	st      SettingsState
	tabs    []*widget.Button
	pages   map[string]settingsPage
	shown   string
	prev    string
	slide   *anim.Float
	kids    []gunim.Node
	nodes   map[string]gunim.Node
	builtAt map[string]string
}

// settingsPage is a section of the settings: its node, which takes the
// settings as they change, and a key saying when its rows must be made
// anew, as a list of models changes.
type settingsPage interface {
	node() gunim.Node
	show(s SettingsState, u *gunim.UI)
}

// settingsBodyH is the body's height.
const settingsBodyH = 440

func newSettingsBody(s SettingsState) *settingsBody {
	b := &settingsBody{st: s, pages: map[string]settingsPage{}, nodes: map[string]gunim.Node{}, slide: anim.NewFloat(1),
		builtAt: map[string]string{}}
	b.Add(b.slide)
	for _, name := range settingsSections {
		t := widget.NewButton(name)
		t.Ghost, t.KeepFocus = true, true
		section := name
		t.OnClick = func(u *gunim.UI) gunim.Intent {
			b.choose(section, u)
			return nil
		}
		b.tabs = append(b.tabs, t)
	}
	th := marrawTheme().With(theme.Set(widget.ButtonHeight, 32), theme.Set(widget.ButtonPadding, 12),
		theme.Set(widget.ButtonRadius, 8), theme.Set(widget.TextSize, 12.5))
	var tabs []gunim.Node
	for _, t := range b.tabs {
		tabs = append(tabs, t)
	}
	b.kids = []gunim.Node{widget.NewThemed(widget.Column(tabs...), th)}
	b.shown = s.Section
	return b
}

// choose shows section name, sliding it in.
func (b *settingsBody) choose(name string, u *gunim.UI) {
	if name == b.shown {
		return
	}
	b.prev, b.shown = b.shown, name
	b.slide.Jump(0)
	b.slide.Animate(1, anim.Tween{Duration: 180 * time.Millisecond})
	u.Invalidate()
}

// show takes s.
func (b *settingsBody) show(s SettingsState, u *gunim.UI) {
	b.st = s
	for _, p := range b.pages {
		p.show(s, u)
	}
	u.Invalidate()
}

// page is section name's page, made the first time it shows, or anew
// as its rows change.
func (b *settingsBody) page(name string) settingsPage {
	if p, ok := b.pages[name]; ok && b.builtAt[name] == pageKey(name, b.st) {
		return p
	}
	var p settingsPage
	switch name {
	case "General":
		p = newGeneralPage()
	case "Features":
		p = newFeaturesPage()
	case "Default presets":
		p = newDefaultsPage(b.st)
	case "Cache":
		p = newCachePage()
	case "Models":
		p = newModelsPage(b.st)
	case "Sidecars":
		p = newSidecarsPage()
	default:
		p = newLinksPage(b.st)
	}
	b.pages[name] = p
	b.builtAt[name] = pageKey(name, b.st)
	return p
}

// pageKey says when section name's rows must be made anew: as what they
// list changes.
func pageKey(name string, s SettingsState) string {
	var parts []string
	switch name {
	case "Default presets":
		for _, c := range s.Cameras {
			parts = append(parts, c.Key)
		}
		for _, p := range s.Presets {
			parts = append(parts, p.ID, p.Name)
		}
	case "Models":
		if s.Models != nil {
			for _, m := range s.Models.Models {
				parts = append(parts, m.FileName)
			}
		}
	case "Shared albums":
		parts = append(parts, fmt.Sprint(s.LinksKnown))
		for _, l := range s.Links {
			parts = append(parts, l.ID, fmt.Sprint(l.Online, l.Expired, l.LastSeen))
		}
	}
	return strings.Join(parts, "|")
}

// Children implements [gunim.Composite]: the pages are mounted as they
// are first shown.
func (b *settingsBody) Children() []gunim.Node { return b.kids }

// sidebarW is the sections' list's width.
const sidebarW = 168

// Layout implements [gunim.Node].
func (b *settingsBody) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	tabs := kids.At(0)
	tabs.Layout(gunim.Loose(geom.Sz(sidebarW-12, settingsBodyH)))
	tabs.Place(geom.Point{})
	for i, t := range b.tabs {
		t.Active = settingsSections[i] == b.shown
	}
	byNode := map[gunim.Node]gunim.Child{}
	for k := range kids.All {
		byNode[k.Node()] = k
	}
	want := map[gunim.Node]bool{}
	for _, name := range []string{b.shown, b.prev} {
		if name == "" || name == b.prev && b.slide.Value() >= 1 {
			continue
		}
		p := b.page(name)
		n := p.node()
		if old, ok := b.nodes[name]; ok && old != n {
			kids.Drop(old)
			delete(byNode, old)
		}
		b.nodes[name] = n
		k, ok := byNode[n]
		if !ok {
			k = kids.Build(n)
			p.show(b.st, nil)
		}
		want[n] = true
		k.Layout(gunim.Tight(geom.Sz(w-sidebarW, settingsBodyH)))
		k.Place(geom.Pt(sidebarW, 0))
	}
	for name, n := range b.nodes {
		if !want[n] {
			if k, ok := byNode[n]; ok {
				k.Layout(gunim.Tight(geom.Size{}))
				k.Place(geom.Pt(-10000, 0))
			}
			_ = name
		}
	}
	return geom.Sz(w, settingsBodyH)
}

// Paint implements [gunim.Node].
func (b *settingsBody) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	p.RRect(geom.Rc(sidebarW-8, 0, 1, box.H), 0, paint.Solid(panelLine))
	body := geom.Rc(sidebarW, 0, box.W-sidebarW, box.H)
	defer p.Layer(paint.LayerOpts{Bounds: body, Opacity: 1, Clip: true})()
	s := min(max(b.slide.Value(), 0), 1)
	byNode := map[gunim.Node]gunim.Child{}
	for k := range kids.All {
		byNode[k.Node()] = k
	}
	if n, ok := b.nodes[b.prev]; ok && s < 1 && b.prev != b.shown {
		if k, ok := byNode[n]; ok {
			func() {
				defer p.Layer(paint.LayerOpts{Bounds: body, Opacity: 1 - s})()
				k.Paint(p)
			}()
		}
	}
	if n, ok := b.nodes[b.shown]; ok {
		if k, ok := byNode[n]; ok {
			defer p.Layer(paint.LayerOpts{Bounds: body, Opacity: s})()
			defer p.Push(paint.Translate(geom.Pt(0, (1-s)*10)))()
			k.Paint(p)
		}
	}
}

// settingRow is a setting: its title, what it does under it, and its
// control at the right, as marraw's rows.
type settingRow struct {
	title, help *widget.Label
	control     gunim.Node
	kids        []gunim.Node
}

// newSettingRow is a row titled title, help under it, with control.
func newSettingRow(title, help string, control gunim.Node) *settingRow {
	r := &settingRow{title: widget.NewLabel(title), help: newSmallLabel(help), control: control}
	r.title.Size = settingTitleSize
	r.help.Color, r.help.MaxLines = noteInk, 6
	r.kids = []gunim.Node{r.title, r.help}
	if control != nil {
		r.kids = append(r.kids, control)
	}
	return r
}

var settingTitleSize = theme.Length("marraw.setting.title", 13.5)

// Children implements [gunim.Composite].
func (r *settingRow) Children() []gunim.Node { return r.kids }

// Layout implements [gunim.Node].
func (r *settingRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const pad = 14
	w := c.Max.W
	cw := float32(0)
	var cs geom.Size
	if len(r.kids) > 2 {
		cs = kids.At(2).Layout(gunim.Loose(geom.Sz(w*0.5, 200)))
		cw = cs.W + 16
	}
	ts := kids.At(0).Layout(gunim.Loose(geom.Sz(w-cw, 40)))
	kids.At(0).Place(geom.Pt(0, pad))
	hs := kids.At(1).Layout(gunim.Loose(geom.Sz(w-cw, 200)))
	kids.At(1).Place(geom.Pt(0, pad+ts.H+3))
	h := max(ts.H+3+hs.H, cs.H) + 2*pad
	if len(r.kids) > 2 {
		kids.At(2).Place(geom.Pt(w-cs.W, (h-cs.H)/2))
	}
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (r *settingRow) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(panelLine))
}

// settingsScroll is a page's rows in a column that scrolls.
func settingsScroll(nodes ...gunim.Node) gunim.Node {
	pad := widget.NewPad(widget.Column(nodes...))
	pad.Padding = settingsPagePad
	return widget.NewScroll(pad)
}

var settingsPagePad = theme.Insets("marraw.settings.pad", geom.Insets{Left: 16, Right: 18, Bottom: 12})

// --- General

type generalPage struct {
	n   gunim.Node
	fit *widget.Segmented
}

func newGeneralPage() *generalPage {
	p := &generalPage{fit: widget.NewSegmented("Crop", "Fit")}
	p.fit.KeepFocus = true
	p.fit.OnChange = func(i int, _ *gunim.UI) gunim.Intent {
		return SettingSet{Key: "thumbFit", Value: map[int]string{0: "crop", 1: "fit"}[i]}
	}
	p.n = settingsScroll(newSettingRow("Thumbnails",
		"Crop fills each cell, the pictures cropped to it. Fit shows the whole frame in its cell.", glassSegmented(p.fit)))
	return p
}

func (p *generalPage) node() gunim.Node { return p.n }
func (p *generalPage) show(s SettingsState, u *gunim.UI) {
	i := 1
	if s.ThumbFit == "crop" {
		i = 0
	}
	p.fit.SetSelected(i, u)
}

// --- Features

type featuresPage struct {
	n        gunim.Node
	switches map[string]*widget.Switch
	hamming  *widget.SliderRow
	gap      *widget.SliderRow
	burstOn  *widget.Fold
}

func newFeaturesPage() *featuresPage {
	p := &featuresPage{switches: map[string]*widget.Switch{}}
	var rows []gunim.Node
	rows = append(rows, sectionLabel("Culling aids"))
	for _, f := range features {
		sw := widget.NewSwitch("")
		sw.KeepFocus = true
		id := f.id
		sw.OnChange = func(on bool, _ *gunim.UI) gunim.Intent { return SettingSet{Key: id, On: on} }
		p.switches[id] = sw
		rows = append(rows, newSettingRow(f.title, f.help, sw))
		if id == "bursts" {
			h := widget.NewSlider(4, 64)
			h.Snap, h.KeepFocus = 1, true
			h.OnCommit = func(x float32, _ *gunim.UI) gunim.Intent { return SettingSet{Key: "burstHamming", N: int(x)} }
			p.hamming = widget.NewSliderRow("Burst grouping", h)
			p.hamming.Format = func(x float32) string { return fmt.Sprintf("%.0f", x) }
			g := widget.NewSlider(1, 30)
			g.Snap, g.KeepFocus = 1, true
			g.OnCommit = func(x float32, _ *gunim.UI) gunim.Intent { return SettingSet{Key: "burstGap", N: int(x)} }
			p.gap = widget.NewSliderRow("Burst time window", g)
			p.gap.Format = func(x float32) string { return fmt.Sprintf("%.0f s", x) }
			help := newSmallLabel("Grouping is how different two frames may be and still be one burst: higher groups shots " +
				"where the subject moves between frames, lower only near-identical ones. The time window is how far " +
				"apart in time they may be.")
			help.Color, help.MaxLines = noteInk, 6
			p.burstOn = widget.NewFold(widget.Column(p.hamming, p.gap, help), true)
			rows = append(rows, p.burstOn)
		}
	}
	p.n = settingsScroll(rows...)
	return p
}

func (p *featuresPage) node() gunim.Node { return p.n }
func (p *featuresPage) show(s SettingsState, u *gunim.UI) {
	for id, sw := range p.switches {
		sw.SetChecked(s.Features[id], u)
	}
	if u != nil {
		p.burstOn.SetOpen(s.Features["bursts"], u)
	}
	if !p.hamming.Slider.Held() {
		p.hamming.Slider.SetValue(float32(s.BurstHamming), u)
	}
	if !p.gap.Slider.Held() {
		p.gap.Slider.SetValue(float32(s.BurstGap), u)
	}
}

// --- Default presets

type defaultsPage struct {
	n     gunim.Node
	drops map[string]*widget.Dropdown
	ids   []string
}

func newDefaultsPage(s SettingsState) *defaultsPage {
	p := &defaultsPage{drops: map[string]*widget.Dropdown{}}
	intro := newSmallLabel("New photos get the chosen look as they are first read, per camera, or one for every camera. " +
		"Only photos you have never edited are touched; Reset takes a photo back to the camera's own look.")
	intro.Color, intro.MaxLines = noteInk, 6
	rows := []gunim.Node{spacer(10), intro}
	if len(s.Presets) == 0 {
		none := newSmallLabel("No presets of your own yet: save a look on the panel's Presets tab first.")
		none.Color = noteInk
		p.n = settingsScroll(append(rows, spacer(10), none)...)
		return p
	}
	items := []widget.MenuItem{{Label: "No default"}}
	p.ids = []string{""}
	for _, pr := range s.Presets {
		items = append(items, widget.MenuItem{Label: pr.Name})
		p.ids = append(p.ids, pr.ID)
	}
	row := func(key, title, help string) gunim.Node {
		d := widget.NewDropdown(items)
		d.KeepFocus = true
		d.OnChange = func(i int, _ *gunim.UI) gunim.Intent {
			if i < 0 || i >= len(p.ids) {
				return nil
			}
			return DefaultPreset{Key: key, ID: p.ids[i]}
		}
		p.drops[key] = d
		return newSettingRow(title, help, &fixedWidth{w: 210, child: d})
	}
	rows = append(rows, row("*", "Any camera", "Used where no camera's own row has one."))
	for _, c := range s.Cameras {
		rows = append(rows, row(c.Key, c.Key, ""))
	}
	p.n = settingsScroll(rows...)
	return p
}

func (p *defaultsPage) node() gunim.Node { return p.n }
func (p *defaultsPage) show(s SettingsState, u *gunim.UI) {
	for key, d := range p.drops {
		d.SetSelected(max(0, slices.Index(p.ids, s.Defaults[key])), u)
	}
}

// --- Cache

type cachePage struct {
	n        gunim.Node
	pre      *widget.Switch
	dir      *settingRow
	useDef   *widget.Button
	cap      *widget.NumberField
	usage    *settingRow
	bar      *usageBar
	clear    *widget.Button
	busyNote *widget.Label
}

func newCachePage() *cachePage {
	p := &cachePage{pre: widget.NewSwitch(""), cap: widget.NewNumberField(1, 2048), bar: &usageBar{}}
	p.pre.KeepFocus = true
	p.pre.OnChange = func(on bool, _ *gunim.UI) gunim.Intent { return SettingSet{Key: "prerender", On: on} }
	p.useDef = widget.NewButton("Use default")
	p.useDef.Ghost, p.useDef.KeepFocus, p.useDef.OnClick = true, true, widget.Sends(CacheDir{Path: ""})
	change := widget.NewButton("Change…")
	change.KeepFocus, change.OnClick = true, widget.Sends(CacheDirAsk{})
	p.cap.Suffix = " GB"
	p.cap.OnCommit = func(v float64, _ *gunim.UI) gunim.Intent { return CacheCap{GB: int(v)} }
	p.clear = widget.NewButton("Clear cache")
	p.clear.Kind, p.clear.KeepFocus, p.clear.OnClick = widget.ButtonDanger, true, widget.Sends(CacheClear{})
	p.dir = newSettingRow("Cache folder", "…", widget.Row(p.useDef, change))
	p.dir.help.Face = widget.MonoFont
	p.usage = newSettingRow("On-disk usage", "measuring…", p.clear)
	p.usage.help.Face = widget.MonoFont
	note := newSmallLabel("Rendered previews and 1:1 tiles. Clearing them is safe: they are made again as they are needed.")
	note.Color, note.MaxLines = noteInk, 4
	p.n = settingsScroll(
		newSettingRow("Pre-render 1:1 full resolution", "After a folder's previews are made, render every photo's 1:1 "+
			"tiles ahead, so zooming to 100% is instant. They are large: raise the limit below for big libraries.", p.pre),
		p.dir,
		newSettingRow("Cache limit", "Past this size, the previews seen longest ago go, in the background.",
			&fixedWidth{w: 110, child: p.cap}),
		p.usage, spacer(6), p.bar, spacer(6), note)
	return p
}

func (p *cachePage) node() gunim.Node { return p.n }
func (p *cachePage) show(s SettingsState, u *gunim.UI) {
	p.pre.SetChecked(s.Prerender, u)
	c := s.Cache
	if c == nil {
		return
	}
	p.dir.help.Text = c.Dir
	if !c.IsCustom {
		p.dir.help.Text += " (default)"
	}
	p.useDef.Disabled = !c.IsCustom || s.Busy != ""
	p.cap.SetValue(float64((c.CapBytes+1<<29)>>30), u)
	p.usage.help.Text = fmt.Sprintf("%s used · %s limit · %d files", byteSize(c.Bytes), byteSize(c.CapBytes), c.Files)
	p.clear.Disabled = s.Busy != "" || c.Files == 0
	p.bar.set(float32(c.Bytes)/float32(max(c.CapBytes, 1)), u)
}

// usageBar is how full the cache is, a bar filling to its limit.
type usageBar struct {
	v *anim.Float
}

func (b *usageBar) set(v float32, u *gunim.UI) {
	v = min(max(v, 0), 1)
	if b.v == nil {
		b.v = anim.NewFloat(v)
		return
	}
	if u != nil {
		b.v.Animate(v, widget.Settle.Get(u.Theme()))
	} else {
		b.v.Jump(v)
	}
}

// Step implements [gunim.Animator].
func (b *usageBar) Step(dt time.Duration) bool { return b.v != nil && b.v.Step(dt) }

// Layout implements [gunim.Node].
func (b *usageBar) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return geom.Sz(min(c.Max.W, 260), 6)
}

// Paint implements [gunim.Node].
func (b *usageBar) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 3, paint.Solid(frost(0x1f)))
	if b.v != nil {
		p.RRect(geom.Rc(0, 0, box.W*b.v.Value(), box.H), 3, paint.Solid(primaryInk))
	}
}

// byteSize is n bytes in words, as marraw writes them: in steps of 1024,
// one decimal under a hundred.
func byteSize(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 || v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// --- Models

type modelsPage struct {
	n gunim.Node
}

func newModelsPage(s SettingsState) *modelsPage {
	intro := newSmallLabel("AI features fetch their models the first time they are used, always after you say yes. " +
		"Deleting one frees its room on disk, and leaves your edits and masks as they are: it is fetched again the next time it is needed.")
	intro.Color, intro.MaxLines = noteInk, 6
	rows := []gunim.Node{spacer(10), intro}
	m := s.Models
	switch {
	case m == nil:
		rows = append(rows, newSettingRow("Models", "Looking…", nil))
	case len(m.Models) == 0:
		rows = append(rows, newSettingRow("No models downloaded", "Nothing on disk yet.", nil))
	default:
		var total int64
		for _, f := range m.Models {
			total += f.Bytes
			name := f.Name
			if name == "" {
				name = f.FileName
			}
			purpose := f.Purpose
			if purpose == "" {
				purpose = "Not used by this version of marraw: safe to delete."
			}
			del := newConfirmButton("Delete", ModelDelete{File: f.FileName})
			rows = append(rows, newSettingRow(name, purpose+"\n"+f.FileName+" · "+byteSize(f.Bytes), del))
		}
		rows = append(rows, newSettingRow("On-disk usage", m.Dir, widget.NewLabel(fmt.Sprintf("%s · %d models", byteSize(total), len(m.Models)))))
	}
	return &modelsPage{n: settingsScroll(rows...)}
}

func (p *modelsPage) node() gunim.Node                  { return p.n }
func (p *modelsPage) show(s SettingsState, u *gunim.UI) {}

// confirmButton is a destructive button that asks once more, in place:
// the first click arms it, the second sends what it does; armed, it lets
// go after a few seconds.
type confirmButton struct {
	*widget.Button
	label string
}

func newConfirmButton(label string, in gunim.Intent) *confirmButton {
	b := &confirmButton{Button: widget.NewButton(label), label: label}
	b.Kind, b.KeepFocus = widget.ButtonDanger, true
	b.Ghost = true
	armed := false
	b.OnClick = func(u *gunim.UI) gunim.Intent {
		if !armed {
			armed = true
			b.Label, b.Ghost = label+"?", false
			u.After(4*time.Second, func(u *gunim.UI) {
				armed = false
				b.Label, b.Ghost = label, true
				u.Invalidate()
			})
			return nil
		}
		armed = false
		b.Label, b.Ghost = label, true
		return in
	}
	return b
}

// --- Sidecars

type sidecarsPage struct {
	n  gunim.Node
	sw *widget.Switch
}

func newSidecarsPage() *sidecarsPage {
	p := &sidecarsPage{sw: widget.NewSwitch("")}
	p.sw.KeepFocus = true
	p.sw.OnChange = func(on bool, _ *gunim.UI) gunim.Intent { return SettingSet{Key: "sidecars", On: on} }
	p.n = settingsScroll(newSettingRow("Write edit sidecars", "Keep the ratings and the develop settings in a .marraw.json "+
		"file beside each RAW, so a folder copied to another computer carries its edits. Folders that have sidecars "+
		"already are always read.", p.sw))
	return p
}

func (p *sidecarsPage) node() gunim.Node { return p.n }
func (p *sidecarsPage) show(s SettingsState, u *gunim.UI) {
	if s.SidecarsKnown {
		p.sw.SetChecked(s.Sidecars, u)
	}
}

// --- Shared albums

type linksPage struct {
	n gunim.Node
}

func newLinksPage(s SettingsState) *linksPage {
	intro := newSmallLabel("Links you have handed out. Withdrawing one stops it working at once, wherever it has been sent on.")
	intro.Color, intro.MaxLines = noteInk, 4
	rows := []gunim.Node{spacer(10), intro}
	switch {
	case !s.LinksKnown:
		rows = append(rows, newSettingRow("Shared albums", "Looking…", nil))
	case len(s.Links) == 0:
		rows = append(rows, newSettingRow("No shared albums", "Share a shoot from the library's list of folders.", nil))
	}
	for _, l := range s.Links {
		rows = append(rows, newLinkRow(l))
	}
	return &linksPage{n: settingsScroll(rows...)}
}

func (p *linksPage) node() gunim.Node                  { return p.n }
func (p *linksPage) show(s SettingsState, u *gunim.UI) {}

// newLinkRow is a shared link's row: its name, its reach and expiry, when
// it was last opened, and Copy and Withdraw.
func newLinkRow(l marrawclient.ShareLink) gunim.Node {
	name := l.Name
	if l.Online {
		name += "  ●"
	}
	var line []string
	switch {
	case l.Expired:
		line = append(line, "expired")
	case l.ExpiresAt == 0:
		line = append(line, "no expiry")
	default:
		line = append(line, "expires "+time.UnixMilli(l.ExpiresAt).Format("2 Jan 15:04"))
	}
	if l.Reach == marrawclient.ShareReachTailnet {
		line = append(line, "my devices")
	} else {
		line = append(line, "anyone with the link")
	}
	line = append(line, fmt.Sprintf("%d photo%s", l.PhotoCount, map[bool]string{false: "s", true: ""}[l.PhotoCount == 1]))
	if l.Caps.Downloads {
		e := l.ExportName
		if e == "" {
			e = "full size"
		}
		line = append(line, e)
	}
	seen := "viewing now"
	if !l.Online {
		seen = "opened " + relativeTime(l.LastSeen)
	}
	var buttons []gunim.Node
	if l.URL != "" && !l.Expired {
		copyB := widget.NewButton("Copy")
		url := l.URL
		copyB.Ghost, copyB.KeepFocus = true, true
		copyB.OnClick = func(u *gunim.UI) gunim.Intent {
			u.SetClipboard(url)
			return Notify{Text: "Link copied"}
		}
		buttons = append(buttons, copyB)
	}
	buttons = append(buttons, newConfirmButton("Withdraw", LinkRevoke{ID: l.ID}))
	r := newSettingRow(name, strings.Join(line, " · ")+"\n"+seen, widget.Row(buttons...))
	if l.Online {
		r.title.Color = onlineInk
	}
	return r
}

var onlineInk = theme.Color("marraw.online", color.NRGBA{R: 0x5e, G: 0xe0, B: 0xb0, A: 0xff})

// relativeTime is when t, in Unix milliseconds as share links keep
// times, was, in words, as marraw says it.
func relativeTime(t int64) string {
	if t == 0 {
		return "never"
	}
	d := time.Since(time.UnixMilli(t))
	switch {
	case d < 90*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
