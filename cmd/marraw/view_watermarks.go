package main

import (
	"fmt"
	"image/color"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
	"github.com/marrasen/marraw/internal/watermark"
)

// wmFonts are the faces a text element can take, as the exporter has
// them, and wmFontKeys their ids.
var (
	wmFonts    = []string{"Sans", "Serif", "Mono", "Script"}
	wmFontKeys = []marrawclient.WatermarkFontID{marrawclient.WatermarkFontIDSans, marrawclient.WatermarkFontIDSerif,
		marrawclient.WatermarkFontIDMono, marrawclient.WatermarkFontIDScript}
	wmDirs    = []string{"↓", "↑", "→", "←"}
	wmDirKeys = []marrawclient.WatermarkGradientDir{marrawclient.WatermarkGradientDirDown, marrawclient.WatermarkGradientDirUp,
		marrawclient.WatermarkGradientDirRight, marrawclient.WatermarkGradientDirLeft}
	wmAnchors = []marrawclient.WatermarkAnchor{
		marrawclient.WatermarkAnchorTopLeft, marrawclient.WatermarkAnchorTop, marrawclient.WatermarkAnchorTopRight,
		marrawclient.WatermarkAnchorLeft, marrawclient.WatermarkAnchorCenter, marrawclient.WatermarkAnchorRight,
		marrawclient.WatermarkAnchorBottomLeft, marrawclient.WatermarkAnchorBottom, marrawclient.WatermarkAnchorBottomRight,
	}
)

// wmView is the watermark editor: the watermarks down the left, the
// chosen one drawn over the photo in the middle with its frame below,
// and its elements and the chosen element's settings at the right.
type wmView struct {
	*widget.Dialog
	seq   int
	list  []marrawclient.Watermark
	sel   string
	elSel string

	marks    *widget.List
	none     *widget.Label
	noneFold *widget.Fold
	name     *widget.TextField
	preview  *widget.Image

	frameOn            *widget.Switch
	frameW, frameB     *widget.SliderRow
	frameColor         *widget.ColorButton
	frameFold          *widget.Fold
	els                *widget.List
	noEls              *widget.Label
	noElsFold          *widget.Fold
	editor             *widget.Fold
	text               *widget.TextField
	font               *widget.Segmented
	textFold           *widget.Fold
	color              *widget.ColorButton
	colorFold          *widget.Fold
	fill               *widget.Segmented
	color2             *widget.ColorButton
	dir                *widget.Segmented
	opacity2           *widget.SliderRow
	rectFold, gradFold *widget.Fold
	width, height      *widget.SliderRow
	size               *widget.SliderRow
	sizeFold           *widget.Fold
	margin, opacity    *widget.SliderRow
	anchor             *anchorGrid
	middle, right      gunim.Node
	// has, hasFrame and hasEls hold what shows only with a watermark chosen.
	has, hasFrame, hasEls *widget.Fold
}

func newWatermarkView(s WatermarkState) *wmView {
	d := widget.NewDialog("Watermarks")
	d.Width = 1120
	d.SetButtons("Done", "")
	d.OnAccept = widget.Sends(WMDone{})
	d.OnDismiss = widget.Sends(WMDone{})
	v := &wmView{Dialog: d}

	// The watermarks.
	v.marks = widget.NewList()
	v.marks.SkipFocus = true
	v.marks.OnActivate = func(k widget.Key, u *gunim.UI) gunim.Intent {
		v.choose(string(k), u)
		return nil
	}
	v.none = newSmallLabel("No watermarks yet. Make one to sign your exports with your name or logo.")
	v.none.Color, v.none.MaxLines = noteInk, 4
	add := widget.NewButton("New watermark")
	add.Icon, add.KeepFocus = icon.Plus, true
	add.OnClick = func(u *gunim.UI) gunim.Intent {
		m := marrawclient.Watermark{ID: newPresetID(), Name: wmName(v.list, "Watermark"),
			Elements: []marrawclient.WatermarkElement{wmText("marraw")}, Frame: wmFrame()}
		v.list = append(v.list, m)
		v.sel = m.ID
		return v.changed(true, u)
	}
	v.noneFold = widget.NewFold(v.none, len(s.List) == 0)
	left := widget.Column(v.marks, v.noneFold, spacer(8), glassButton(add))

	// The chosen watermark: its name, the picture, the frame.
	v.name = widget.NewTextField()
	v.name.Placeholder = "Name"
	v.name.OnChange = func(s string, u *gunim.UI) gunim.Intent {
		return v.editMark(func(m *marrawclient.Watermark) { m.Name = s }, false, u)
	}
	dup := widget.NewButton("Duplicate")
	dup.Icon, dup.KeepFocus = icon.Copy, true
	dup.OnClick = func(u *gunim.UI) gunim.Intent {
		m := v.mark()
		if m == nil {
			return nil
		}
		c := *m
		c.ID, c.Name = newPresetID(), wmName(v.list, m.Name+" copy")
		c.Elements = slices.Clone(m.Elements)
		for i := range c.Elements {
			c.Elements[i].ID = newPresetID()
		}
		v.list = append(v.list, c)
		v.sel = c.ID
		return v.changed(true, u)
	}
	del := widget.NewButton("Delete")
	del.Icon, del.KeepFocus = icon.Trash2, true
	del.OnClick = func(u *gunim.UI) gunim.Intent {
		i := slices.IndexFunc(v.list, func(m marrawclient.Watermark) bool { return m.ID == v.sel })
		if i < 0 {
			return nil
		}
		v.list = slices.Delete(v.list, i, i+1)
		v.sel = ""
		if len(v.list) > 0 {
			v.sel = v.list[min(i, len(v.list)-1)].ID
		}
		return v.changed(true, u)
	}
	v.preview = widget.NewImage(nil)
	v.preview.Size, v.preview.Fit, v.preview.Radius = geom.Sz(float32(wmPreviewBox.X), float32(wmPreviewBox.Y)), widget.FitContain, 4
	v.frameOn = widget.NewSwitch("Frame round the photo")
	v.frameOn.KeepFocus = true
	v.frameOn.OnChange = func(on bool, u *gunim.UI) gunim.Intent {
		v.frameFold.SetOpen(on, u)
		return v.editMark(func(m *marrawclient.Watermark) { m.Frame.Enabled = on }, true, u)
	}
	v.frameW = wmSlider("Border", 0.5, 15, 0.5, "%", func(x float64, m *marrawclient.Watermark, _ *marrawclient.WatermarkElement) { m.Frame.WidthPct = x }, v)
	v.frameB = wmSlider("Below", 0, 30, 0.5, "%", func(x float64, m *marrawclient.Watermark, _ *marrawclient.WatermarkElement) { m.Frame.BottomPct = x }, v)
	v.frameColor = widget.NewColorButton(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	v.frameColor.Opaque, v.frameColor.Hex, v.frameColor.Label = true, true, "Frame colour"
	v.frameColor.OnChange = func(c color.NRGBA, u *gunim.UI) gunim.Intent {
		return v.editMark(func(m *marrawclient.Watermark) { m.Frame.Color = hexOf(c) }, false, u)
	}
	v.frameFold = widget.NewFold(widget.Column(v.frameW, v.frameB, wmLine("Colour", v.frameColor)), false)
	nameRow := widget.Row(&fixedWidth{w: 200, child: v.name}, glassButton(dup), glassButton(del))
	nameRow.Cross = widget.CrossCenter
	v.has = widget.NewFold(nameRow, false)
	v.hasFrame = widget.NewFold(widget.Column(spacer(8), v.frameOn, v.frameFold), false)
	v.middle = widget.Column(v.has, spacer(8), &framedPreview{child: v.preview}, v.hasFrame)

	// The elements, and the chosen one.
	v.els = widget.NewList()
	v.els.SkipFocus = true
	v.els.OnActivate = func(k widget.Key, u *gunim.UI) gunim.Intent {
		v.elSel = string(k)
		v.showElement(u)
		v.syncElements(u)
		return nil
	}
	v.els.OnReorder = func(keys []widget.Key, u *gunim.UI) gunim.Intent {
		return v.editMark(func(m *marrawclient.Watermark) {
			out := make([]marrawclient.WatermarkElement, 0, len(m.Elements))
			for _, k := range keys {
				if i := slices.IndexFunc(m.Elements, func(e marrawclient.WatermarkElement) bool { return e.ID == string(k) }); i >= 0 {
					out = append(out, m.Elements[i])
				}
			}
			m.Elements = out
		}, true, u)
	}
	v.noEls = newSmallLabel("Nothing on it yet: add text, a logo or a bar.")
	v.noEls.Color, v.noEls.MaxLines = noteInk, 2
	addText := widget.NewButton("Text")
	addText.Icon = icon.Type
	addText.OnClick = func(u *gunim.UI) gunim.Intent { return v.addElement(wmText(""), u) }
	addImage := widget.NewButton("Logo…")
	addImage.Icon, addImage.OnClick = icon.ImagePlus, widget.Sends(WMAddImage{})
	addBar := widget.NewButton("Bar")
	addBar.Icon = icon.RectangleHorizontal
	addBar.OnClick = func(u *gunim.UI) gunim.Intent { return v.addElement(wmBar(), u) }

	v.text = widget.NewTextField()
	v.text.Placeholder = "Text, as © 2026 Jane Doe"
	v.text.OnChange = func(s string, u *gunim.UI) gunim.Intent {
		return v.editElement(func(e *marrawclient.WatermarkElement) { e.Text = s }, false, u)
	}
	v.font = widget.NewSegmented(wmFonts...)
	v.font.KeepFocus = true
	v.font.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		return v.editElement(func(e *marrawclient.WatermarkElement) { e.Font = wmFontKeys[max(0, i)] }, true, u)
	}
	v.textFold = widget.NewFold(widget.Column(v.text, spacer(6), glassSegmented(v.font)), false)
	v.color = widget.NewColorButton(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	v.color.Opaque, v.color.Hex, v.color.Label = true, true, "Colour"
	v.color.OnChange = func(c color.NRGBA, u *gunim.UI) gunim.Intent {
		return v.editElement(func(e *marrawclient.WatermarkElement) { e.Color = hexOf(c) }, false, u)
	}
	v.colorFold = widget.NewFold(wmLine("Colour", v.color), false)
	v.fill = widget.NewSegmented("Solid", "Gradient")
	v.fill.KeepFocus = true
	v.fill.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		v.gradFold.SetOpen(i == 1, u)
		return v.editElement(func(e *marrawclient.WatermarkElement) {
			e.Fill = map[bool]marrawclient.WatermarkFill{false: marrawclient.WatermarkFillSolid, true: marrawclient.WatermarkFillGradient}[i == 1]
		}, true, u)
	}
	v.color2 = widget.NewColorButton(color.NRGBA{A: 0xff})
	v.color2.Opaque, v.color2.Hex, v.color2.Label = true, true, "Fades to"
	v.color2.OnChange = func(c color.NRGBA, u *gunim.UI) gunim.Intent {
		return v.editElement(func(e *marrawclient.WatermarkElement) { e.Color2 = hexOf(c) }, false, u)
	}
	v.dir = widget.NewSegmented(wmDirs...)
	v.dir.KeepFocus = true
	v.dir.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		return v.editElement(func(e *marrawclient.WatermarkElement) { e.GradientDir = wmDirKeys[max(0, i)] }, true, u)
	}
	v.opacity2 = wmSlider("End opacity", 0, 100, 5, "%", func(x float64, _ *marrawclient.Watermark, e *marrawclient.WatermarkElement) { e.Opacity2 = x / 100 }, v)
	v.gradFold = widget.NewFold(widget.Column(wmLine("To", v.color2), wmLine("Running", glassSegmented(v.dir)), v.opacity2), false)
	v.width = wmSlider("Width", 1, 100, 1, "%", func(x float64, _ *marrawclient.Watermark, e *marrawclient.WatermarkElement) { e.WidthPct = x }, v)
	v.height = wmSlider("Height", 1, 100, 1, "%", func(x float64, _ *marrawclient.Watermark, e *marrawclient.WatermarkElement) { e.HeightPct = x }, v)
	v.rectFold = widget.NewFold(widget.Column(wmLine("Fill", glassSegmented(v.fill)), v.gradFold, v.width, v.height), false)
	v.size = wmSlider("Size", 0.5, 50, 0.5, "%", func(x float64, _ *marrawclient.Watermark, e *marrawclient.WatermarkElement) { e.SizePct = x }, v)
	v.sizeFold = widget.NewFold(v.size, false)
	v.margin = wmSlider("Margin", 0, 25, 0.5, "%", func(x float64, _ *marrawclient.Watermark, e *marrawclient.WatermarkElement) { e.MarginPct = x }, v)
	v.opacity = wmSlider("Opacity", 5, 100, 5, "%", func(x float64, _ *marrawclient.Watermark, e *marrawclient.WatermarkElement) { e.Opacity = x / 100 }, v)
	v.anchor = &anchorGrid{hot: -1}
	v.anchor.onPick = func(a marrawclient.WatermarkAnchor, u *gunim.UI) gunim.Intent {
		return v.editElement(func(e *marrawclient.WatermarkElement) { e.Anchor = a }, true, u)
	}
	v.editor = widget.NewFold(widget.Column(spacer(6), v.textFold, v.colorFold, v.rectFold, v.sizeFold, v.margin, v.opacity,
		wmLine("Place", v.anchor)), false)
	v.noElsFold = widget.NewFold(v.noEls, false)
	v.hasEls = widget.NewFold(widget.Column(sectionLabel("On the photo"), v.els, v.noElsFold, spacer(6), smallButtons(addText, addImage, addBar), v.editor), false)
	v.right = v.hasEls

	d.Body = &wmBody{left: left, middle: widget.NewScroll(v.middle), right: widget.NewScroll(v.right)}
	v.take(s, nil)
	return v
}

// show takes s: a list new to the editor replaces its own.
func (v *wmView) show(s WatermarkState, u *gunim.UI) {
	if s.Seq != v.seq {
		v.take(s, u)
	}
	v.syncMarks(u)
	v.showMark(u)
}

// take starts from s.
func (v *wmView) take(s WatermarkState, u *gunim.UI) {
	v.seq, v.list, v.sel, v.elSel = s.Seq, slices.Clone(s.List), s.Selected, ""
	if v.mark() == nil && len(v.list) > 0 {
		v.sel = v.list[0].ID
	}
}

// showPreview takes the drawing of the chosen watermark.
func (v *wmView) showPreview(p WMPreview, u *gunim.UI) { v.preview.SetSource(p.Img, u) }

// assetAdded adds the logo the user chose to the chosen watermark.
func (v *wmView) assetAdded(a WMAssetAdded, u *gunim.UI) {
	e := wmElement(marrawclient.WatermarkElement{Type: marrawclient.WatermarkElementTypeImage, Asset: a.Info.FileName,
		AssetWidth: a.Info.Width, AssetHeight: a.Info.Height, SizePct: 8})
	if v.mark() == nil {
		m := marrawclient.Watermark{ID: newPresetID(), Name: wmName(v.list, "Watermark"), Frame: wmFrame()}
		v.list = append(v.list, m)
		v.sel = m.ID
	}
	u.Send(v, v.addElement(e, u))
}

// mark is the chosen watermark, or nil.
func (v *wmView) mark() *marrawclient.Watermark {
	for i := range v.list {
		if v.list[i].ID == v.sel {
			return &v.list[i]
		}
	}
	return nil
}

// element is the chosen element, or nil.
func (v *wmView) element() *marrawclient.WatermarkElement {
	m := v.mark()
	if m == nil {
		return nil
	}
	for i := range m.Elements {
		if m.Elements[i].ID == v.elSel {
			return &m.Elements[i]
		}
	}
	return nil
}

// choose makes the watermark id the one edited.
func (v *wmView) choose(id string, u *gunim.UI) {
	if id == v.sel {
		return
	}
	v.sel, v.elSel = id, ""
	v.syncMarks(u)
	v.showMark(u)
	u.Send(v, WMChanged{List: v.list, Selected: v.sel, Now: false})
}

// changed shows the list as it now is and tells the controller, Now
// for a change made at once rather than one in a drag or typing.
func (v *wmView) changed(now bool, u *gunim.UI) gunim.Intent {
	v.syncMarks(u)
	v.showMark(u)
	return WMChanged{List: slices.Clone(v.list), Selected: v.sel, Now: now}
}

// editMark changes the chosen watermark with f.
func (v *wmView) editMark(f func(*marrawclient.Watermark), now bool, u *gunim.UI) gunim.Intent {
	m := v.mark()
	if m == nil {
		return nil
	}
	f(m)
	v.syncMarks(u)
	return WMChanged{List: slices.Clone(v.list), Selected: v.sel, Now: now}
}

// editElement changes the chosen element with f.
func (v *wmView) editElement(f func(*marrawclient.WatermarkElement), now bool, u *gunim.UI) gunim.Intent {
	m := v.mark()
	e := v.element()
	if m == nil || e == nil {
		return nil
	}
	f(e)
	v.syncElements(u)
	return WMChanged{List: slices.Clone(v.list), Selected: v.sel, Now: now}
}

// addElement puts e on the chosen watermark, and chooses it.
func (v *wmView) addElement(e marrawclient.WatermarkElement, u *gunim.UI) gunim.Intent {
	m := v.mark()
	if m == nil {
		return nil
	}
	m.Elements = append(slices.Clone(m.Elements), e)
	v.elSel = e.ID
	if e.Type == marrawclient.WatermarkElementTypeText {
		defer u.Focus(v.text)
	}
	return v.changed(true, u)
}

// syncMarks shows the list of watermarks.
func (v *wmView) syncMarks(u *gunim.UI) {
	show := func(r *wmRow, m marrawclient.Watermark, u *gunim.UI) {
		n := len(m.Elements)
		sub := fmt.Sprintf("%d element%s", n, map[bool]string{true: "", false: "s"}[n == 1])
		if m.Frame.Enabled {
			sub += " · framed"
		}
		r.set(m.Name, sub, m.ID == v.sel, u)
	}
	widget.Sync(v.marks, u, v.list, func(m marrawclient.Watermark) widget.Key { return widget.Key(m.ID) },
		func(m marrawclient.Watermark) *wmRow {
			r := newWMRow(nil)
			show(r, m, u)
			return r
		}, show)
	v.noneFold.SetOpen(len(v.list) == 0, u)
}

// syncElements shows the chosen watermark's elements.
func (v *wmView) syncElements(u *gunim.UI) {
	var els []marrawclient.WatermarkElement
	if m := v.mark(); m != nil {
		els = m.Elements
	}
	show := func(r *wmRow, e marrawclient.WatermarkElement, u *gunim.UI) {
		title, sub := wmDescribe(e)
		r.set(title, sub, e.ID == v.elSel, u)
	}
	widget.Sync(v.els, u, els, func(e marrawclient.WatermarkElement) widget.Key { return widget.Key(e.ID) },
		func(e marrawclient.WatermarkElement) *wmRow {
			id := e.ID
			r := newWMRow(func(u *gunim.UI) gunim.Intent {
				m := v.mark()
				if m == nil {
					return nil
				}
				m.Elements = slices.DeleteFunc(slices.Clone(m.Elements), func(e marrawclient.WatermarkElement) bool { return e.ID == id })
				if v.elSel == id {
					v.elSel = ""
					v.showElement(u)
				}
				return v.changed(true, u)
			})
			show(r, e, u)
			return r
		}, show)
	v.noElsFold.SetOpen(len(els) == 0, u)
}

// showMark shows the chosen watermark in the middle and at the right.
func (v *wmView) showMark(u *gunim.UI) {
	m := v.mark()
	v.has.SetOpen(m != nil, u)
	v.hasFrame.SetOpen(m != nil, u)
	v.hasEls.SetOpen(m != nil, u)
	if m == nil {
		v.name.SetText("", u)
		v.frameOn.SetChecked(false, u)
		v.frameFold.SetOpen(false, u)
		v.syncElements(u)
		v.showElement(u)
		return
	}
	if v.name.Text() != m.Name {
		v.name.SetText(m.Name, u)
	}
	f := m.Frame
	v.frameOn.SetChecked(f.Enabled, u)
	v.frameFold.SetOpen(f.Enabled, u)
	v.frameW.Slider.SetValue(float32(f.WidthPct), u)
	v.frameB.Slider.SetValue(float32(f.BottomPct), u)
	v.frameColor.SetValue(watermark.ParseHexColor(f.Color), u)
	if v.element() == nil && len(m.Elements) > 0 {
		v.elSel = m.Elements[0].ID
	}
	v.syncElements(u)
	v.showElement(u)
}

// showElement shows the chosen element's settings, those its kind has.
func (v *wmView) showElement(u *gunim.UI) {
	e := v.element()
	v.editor.SetOpen(e != nil, u)
	if e == nil {
		return
	}
	text := e.Type == marrawclient.WatermarkElementTypeText
	rect := e.Type == marrawclient.WatermarkElementTypeRect
	v.textFold.SetOpen(text, u)
	v.colorFold.SetOpen(text || rect, u)
	v.rectFold.SetOpen(rect, u)
	v.gradFold.SetOpen(rect && e.Fill == marrawclient.WatermarkFillGradient, u)
	v.sizeFold.SetOpen(!rect, u)
	if v.text.Text() != e.Text {
		v.text.SetText(e.Text, u)
	}
	v.font.SetSelected(max(0, slices.Index(wmFontKeys, e.Font)), u)
	v.color.SetValue(watermark.ParseHexColor(e.Color), u)
	v.fill.SetSelected(map[bool]int{false: 0, true: 1}[e.Fill == marrawclient.WatermarkFillGradient], u)
	v.color2.SetValue(watermark.ParseHexColor(e.Color2), u)
	v.dir.SetSelected(max(0, slices.Index(wmDirKeys, e.GradientDir)), u)
	v.opacity2.Slider.SetValue(float32(e.Opacity2*100), u)
	v.width.Slider.SetValue(float32(e.WidthPct), u)
	v.height.Slider.SetValue(float32(e.HeightPct), u)
	v.size.Slider.SetValue(float32(e.SizePct), u)
	v.margin.Slider.SetValue(float32(e.MarginPct), u)
	v.opacity.Slider.SetValue(float32(e.Opacity*100), u)
	v.anchor.set(e.Anchor, u)
}

// wmSlider is a slider row from lo to hi in steps of step, its value
// shown with unit, that sets what it sets with set: on the watermark,
// or on its element when it has one chosen.
func wmSlider(label string, lo, hi, step float32, unit string,
	set func(x float64, m *marrawclient.Watermark, e *marrawclient.WatermarkElement), v *wmView,
) *widget.SliderRow {
	s := widget.NewSlider(lo, hi)
	s.Snap, s.KeepFocus = step, true
	r := widget.NewSliderRow(label, s)
	r.Format = func(x float32) string {
		if step < 1 {
			return fmt.Sprintf("%.1f%s", x, unit)
		}
		return fmt.Sprintf("%.0f%s", x, unit)
	}
	s.OnChange = func(x float32, u *gunim.UI) gunim.Intent {
		m := v.mark()
		if m == nil {
			return nil
		}
		set(float64(x), m, v.element())
		v.syncMarks(u)
		v.syncElements(u)
		return WMChanged{List: slices.Clone(v.list), Selected: v.sel}
	}
	return r
}

// wmLine is a small label beside a control, as the slider rows have
// theirs.
func wmLine(label string, n gunim.Node) gunim.Node {
	l := &labeled{label: newSmallLabel(label), child: n, active: anim.NewFloat(0)}
	l.Add(l.active)
	return l
}

// wmDescribe names element e in its list, and says what it holds.
func wmDescribe(e marrawclient.WatermarkElement) (title, sub string) {
	place := wmAnchorName(e.Anchor)
	switch e.Type {
	case marrawclient.WatermarkElementTypeText:
		t := strings.TrimSpace(e.Text)
		if t == "" {
			t = "Empty text"
		}
		return t, fmt.Sprintf("Text · %s", place)
	case marrawclient.WatermarkElementTypeImage:
		return "Logo", fmt.Sprintf("%d × %d · %s", e.AssetWidth, e.AssetHeight, place)
	default:
		kind := "Solid bar"
		if e.Fill == marrawclient.WatermarkFillGradient {
			kind = "Fading bar"
		}
		return kind, place
	}
}

// wmAnchorName is where anchor a puts an element, in words.
func wmAnchorName(a marrawclient.WatermarkAnchor) string {
	return map[marrawclient.WatermarkAnchor]string{
		marrawclient.WatermarkAnchorTopLeft: "top left", marrawclient.WatermarkAnchorTop: "top", marrawclient.WatermarkAnchorTopRight: "top right",
		marrawclient.WatermarkAnchorLeft: "left", marrawclient.WatermarkAnchorCenter: "centre", marrawclient.WatermarkAnchorRight: "right",
		marrawclient.WatermarkAnchorBottomLeft: "bottom left", marrawclient.WatermarkAnchorBottom: "bottom", marrawclient.WatermarkAnchorBottomRight: "bottom right",
	}[a]
}

// wmElement is e with what the backend would fill in filled in, so the
// preview and the controls start where the export will.
func wmElement(e marrawclient.WatermarkElement) marrawclient.WatermarkElement {
	if e.ID == "" {
		e.ID = newPresetID()
	}
	if e.Font == "" {
		e.Font = marrawclient.WatermarkFontIDSans
	}
	if e.Anchor == "" {
		e.Anchor = marrawclient.WatermarkAnchorBottomRight
	}
	if e.Color == "" {
		e.Color = "#ffffff"
	}
	if e.Color2 == "" {
		e.Color2 = "#ffffff"
	}
	if e.Fill == "" {
		e.Fill = marrawclient.WatermarkFillSolid
	}
	if e.GradientDir == "" {
		e.GradientDir = marrawclient.WatermarkGradientDirDown
	}
	if e.WidthPct <= 0 {
		e.WidthPct = 100
	}
	if e.HeightPct <= 0 {
		e.HeightPct = 14
	}
	if e.SizePct <= 0 {
		e.SizePct = 4
	}
	if e.Opacity <= 0 {
		e.Opacity = 1
	}
	return e
}

// wmText is a text element saying s.
func wmText(s string) marrawclient.WatermarkElement {
	return wmElement(marrawclient.WatermarkElement{Type: marrawclient.WatermarkElementTypeText, Text: s, MarginPct: 3})
}

// wmBar is a bar as a caption sits on: as wide as the photo, black
// fading up to nothing, along the bottom.
func wmBar() marrawclient.WatermarkElement {
	return wmElement(marrawclient.WatermarkElement{Type: marrawclient.WatermarkElementTypeRect, Fill: marrawclient.WatermarkFillGradient,
		Color: "#000000", Opacity: 0.55, Color2: "#000000", Opacity2: 0, GradientDir: marrawclient.WatermarkGradientDirUp,
		Anchor: marrawclient.WatermarkAnchorBottom, WidthPct: 100, HeightPct: 14})
}

// wmFrame is a frame as the backend starts one: white, 3% wide.
func wmFrame() marrawclient.WatermarkFrame {
	return marrawclient.WatermarkFrame{WidthPct: 3, Color: "#ffffff"}
}

// hexOf is c as #rrggbb.
func hexOf(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// wmBody lays the editor's three columns side by side, a fixed height.
type wmBody struct {
	left, middle, right gunim.Node
}

// wmBody's columns' widths and its height.
const (
	wmLeftW, wmMiddleW = 190, 450
	wmBodyH            = 520
)

// Children implements [gunim.Composite].
func (b *wmBody) Children() []gunim.Node { return []gunim.Node{b.left, b.middle, b.right} }

// Layout implements [gunim.Node].
func (b *wmBody) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const gap = 20
	h := min(float32(wmBodyH), max(c.Max.H, 200))
	if c.Max.H == 0 {
		h = wmBodyH
	}
	rightW := max(0, c.Max.W-wmLeftW-wmMiddleW-2*gap)
	kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(wmLeftW, h)})
	kids.At(0).Place(geom.Point{})
	kids.At(1).Layout(gunim.Tight(geom.Sz(wmMiddleW, h)))
	kids.At(1).Place(geom.Pt(wmLeftW+gap, 0))
	kids.At(2).Layout(gunim.Tight(geom.Sz(rightW, h)))
	kids.At(2).Place(geom.Pt(wmLeftW+wmMiddleW+2*gap, 0))
	return geom.Sz(c.Max.W, h)
}

// Paint implements [gunim.Node]: hairlines between the columns.
func (b *wmBody) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	for _, x := range []float32{wmLeftW + 10, wmLeftW + wmMiddleW + 30} {
		p.RRect(geom.Rc(x, 0, 1, box.H), 0, paint.Solid(frost(0x14)))
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// framedPreview is the preview on a dark ground the size of its box,
// so a narrow photo sits centred.
type framedPreview struct{ child gunim.Node }

// Children implements [gunim.Composite].
func (f *framedPreview) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node].
func (f *framedPreview) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := geom.Sz(min(c.Max.W, float32(wmPreviewBox.X)+20), float32(wmPreviewBox.Y)+20)
	kids.At(0).Layout(gunim.Tight(geom.Sz(box.W-20, box.H-20)))
	kids.At(0).Place(geom.Pt(10, 10))
	return box
}

// Paint implements [gunim.Node].
func (f *framedPreview) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 8, paint.Solid(color.NRGBA{R: 0x0c, G: 0x0e, B: 0x12, A: 0xff}))
	kids.At(0).Paint(p)
}

// wmRow is a row of the editor's lists: a title, a line under it, and,
// for an element, a cross that takes it off. The row chosen is lit.
type wmRow struct {
	anim.Group
	title, sub *widget.Label
	del        *widget.IconButton
	on         bool
	lit        *anim.Float
}

func newWMRow(remove func(u *gunim.UI) gunim.Intent) *wmRow {
	r := &wmRow{title: widget.NewLabel(""), sub: newSmallLabel(""), lit: anim.NewFloat(0)}
	r.title.MaxLines, r.title.NoWrap = 1, true
	r.sub.Color, r.sub.NoWrap = noteInk, true
	r.Add(r.lit)
	if remove != nil {
		r.del = widget.NewIconButton(icon.X, "Take it off")
		r.del.KeepFocus, r.del.OnClick = true, remove
	}
	return r
}

// set shows title and sub, lit when on.
func (r *wmRow) set(title, sub string, on bool, u *gunim.UI) {
	r.title.Text, r.sub.Text = title, sub
	if on != r.on {
		r.on = on
		r.lit.Animate(map[bool]float32{false: 0, true: 1}[on], widget.Quick.Get(u.Theme()))
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (r *wmRow) Children() []gunim.Node {
	if r.del != nil {
		return []gunim.Node{r.title, r.sub, r.del}
	}
	return []gunim.Node{r.title, r.sub}
}

// Layout implements [gunim.Node].
func (r *wmRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const h, pad = 44, 10
	room := c.Max.W - 2*pad
	if r.del != nil {
		ds := kids.At(2).Layout(gunim.Loose(geom.Sz(28, 28)))
		kids.At(2).Place(geom.Pt(c.Max.W-pad/2-ds.W, (h-ds.H)/2))
		room -= ds.W
	}
	ts := kids.At(0).Layout(gunim.Loose(geom.Sz(room, 20)))
	kids.At(0).Place(geom.Pt(pad, 5))
	kids.At(1).Layout(gunim.Loose(geom.Sz(room, 16)))
	kids.At(1).Place(geom.Pt(pad, 5+ts.H+1))
	return geom.Sz(c.Max.W, h)
}

// Paint implements [gunim.Node].
func (r *wmRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	t := min(max(r.lit.Value(), 0), 1)
	full := geom.Rect{Max: box.Point()}
	if t > 0 {
		acc := widget.Accent.Get(f.Theme)
		p.RRect(full, 9, paint.Solid(withAlpha(acc, 0.16*t)))
		p.RRectStroke(full.Inset(geom.Uniform(0.5)), 8.5, paint.Fill{}, paint.Stroke{Width: 1, Color: withAlpha(acc, 0.45*t)})
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// anchorGrid is the nine places an element can sit, as a grid of
// squares: the one it sits at filled.
type anchorGrid struct {
	anim.Group
	at     int
	hot    int
	onPick func(a marrawclient.WatermarkAnchor, u *gunim.UI) gunim.Intent
}

// anchorCell is a square's size, and anchorGap the room between.
const anchorCell, anchorGap = 22, 3

// set shows a.
func (g *anchorGrid) set(a marrawclient.WatermarkAnchor, u *gunim.UI) {
	if i := slices.Index(wmAnchors, a); i >= 0 && i != g.at {
		g.at = i
		u.Invalidate()
	}
}

// cellAt is the square under pt, or -1.
func (g *anchorGrid) cellAt(pt geom.Point) int {
	col, row := int(pt.X/(anchorCell+anchorGap)), int(pt.Y/(anchorCell+anchorGap))
	if pt.X < 0 || pt.Y < 0 || col > 2 || row > 2 {
		return -1
	}
	return row*3 + col
}

// Layout implements [gunim.Node].
func (g *anchorGrid) Layout(gunim.Constraints, gunim.Frame, gunim.Children) geom.Size {
	return geom.Sz(3*anchorCell+2*anchorGap, 3*anchorCell+2*anchorGap)
}

// Handle implements [gunim.Handler].
func (g *anchorGrid) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		g.hot = g.cellAt(e.Pos)
	case input.PointerLeave:
		g.hot = -1
	case input.PointerDown:
		i := g.cellAt(e.Pos)
		if e.Button != input.ButtonPrimary || i < 0 {
			return false
		}
		g.at = i
		if g.onPick != nil {
			u.Send(g, g.onPick(wmAnchors[i], u))
		}
		u.Invalidate()
		return true
	default:
		return false
	}
	u.Invalidate()
	return false
}

// Paint implements [gunim.Node].
func (g *anchorGrid) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, _ gunim.Children) {
	acc := widget.Accent.Get(f.Theme)
	for i := range 9 {
		r := geom.Rc(float32(i%3)*(anchorCell+anchorGap), float32(i/3)*(anchorCell+anchorGap), anchorCell, anchorCell)
		switch {
		case i == g.at:
			p.RRect(r, 5, paint.Solid(acc))
		case i == g.hot:
			p.RRect(r, 5, paint.Solid(frost(0x30)))
		default:
			p.RRect(r, 5, paint.Solid(frost(0x12)))
		}
	}
}
