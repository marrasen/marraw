package main

import (
	"fmt"
	"math"
	"slices"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// A mask's geometry lives in fractions of the oriented frame: after the
// quarter turns and the mirror, before the straighten and the crop, as the
// crop's rectangle does. These map it to the photo as it shows and back,
// ported from marraw's crop.ts.

// frameFromShown is the point bx, by of the photo as it shows, 0 to 1
// across, as a point of the oriented frame of p, fw by fh large.
func frameFromShown(bx, by float64, p marrawclient.Params, fw, fh float64) (float64, float64) {
	px, py := bx, by
	if p.CropW > 0 && p.CropH > 0 {
		px, py = p.CropX+bx*p.CropW, p.CropY+by*p.CropH
	}
	px, py = px*fw, py*fh
	rad := -p.CropAngle * math.Pi / 180
	c, s := math.Cos(rad), math.Sin(rad)
	cx, cy := fw/2, fh/2
	dx, dy := px-cx, py-cy
	return (cx + dx*c - dy*s) / fw, (cy + dx*s + dy*c) / fh
}

// shownFromFrame is frameFromShown's inverse.
func shownFromFrame(fx, fy float64, p marrawclient.Params, fw, fh float64) (float64, float64) {
	rad := p.CropAngle * math.Pi / 180
	c, s := math.Cos(rad), math.Sin(rad)
	cx, cy := fw/2, fh/2
	dx, dy := fx*fw-cx, fy*fh-cy
	px, py := (cx+dx*c-dy*s)/fw, (cy+dx*s+dy*c)/fh
	if p.CropW > 0 && p.CropH > 0 {
		return (px - p.CropX) / p.CropW, (py - p.CropY) / p.CropH
	}
	return px, py
}

// newMask is a freshly added mask of kind, as marraw's defaults have it:
// centred and plain to see, its handles showing what they do.
func newMask(kind string) marrawclient.Mask {
	switch kind {
	case "linear":
		return marrawclient.Mask{Type: "linear", X0: 0.5, Y0: 0.3, X1: 0.5, Y1: 0.6}
	case "radial":
		return marrawclient.Mask{Type: "radial", CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.25, Feather: 0.5}
	case "range":
		// Both windows open, as a range mask starts selecting everything.
		return marrawclient.Mask{Type: "range", RangeLumaLo: 0, RangeLumaHi: 1, RangeHueLo: 0, RangeHueHi: 1, Feather: 0.25}
	}
	return marrawclient.Mask{Type: "brush"}
}

// aiMaskOf is a freshly generated AI mask of kind, on the map mapVer, as
// marraw's recipes have it.
func aiMaskOf(kind, mapVer string, focus float64) marrawclient.Mask {
	m := marrawclient.Mask{Type: "ai", AIKind: marrawclient.AIKind(kind), MapVer: mapVer}
	switch kind {
	case "depth":
		m.DepthLo, m.DepthHi, m.Feather = 0.6, 1, 0.3
	case "background":
		m.Adjust = marrawclient.MaskAdjust{Glow: 0.1, Streaks: 0.2, Prism: 0.6, FXAngle: 25}
	case "tilt":
		// Tilt shift: everything outside a band in the middle distance,
		// defocused more the further from it, as a lens's depth of field.
		lo, hi := 0.35, 0.45
		if focus >= 0 {
			half := (hi - lo) / 2
			c := min(max(focus, half), 1-half)
			lo, hi = c-half, c+half
		}
		m.AIKind, m.Invert, m.DepthLo, m.DepthHi, m.Feather = "depth", true, lo, hi, 1
		m.Adjust = marrawclient.MaskAdjust{Bokeh: 0.2, Glow: 0.2, Prism: 0.4}
	}
	return m
}

// aiCategories are the scene categories' names, by their ids, as marraw's.
var aiCategories = []string{"Other", "Sky", "People", "Foliage", "Water", "Ground", "Architecture", "Mountains & rocks", "Vehicles", "Animals"}

// maskLabel is how mask m, at i in the list, is named, as marraw names it.
func maskLabel(m marrawclient.Mask, i int) string {
	if m.Type == "ai" {
		switch m.AIKind {
		case "person":
			return fmt.Sprintf("Person %d", m.ClassID)
		case "subject":
			return fmt.Sprintf("Subject %d", i+1)
		case "background":
			return fmt.Sprintf("Background %d", i+1)
		case "depth":
			return fmt.Sprintf("Depth %d", i+1)
		case "class":
			if m.ClassID >= 0 && m.ClassID < len(aiCategories) {
				return fmt.Sprintf("%s %d", aiCategories[m.ClassID], i+1)
			}
		}
		return fmt.Sprintf("AI %d", i+1)
	}
	name := map[string]string{"linear": "Linear gradient", "radial": "Radial", "brush": "Brush", "range": "Range"}[string(m.Type)]
	if name == "" {
		name = "Mask"
	}
	return fmt.Sprintf("%s %d", name, i+1)
}

// canRemove reports whether mask m may fill what it covers from around it.
func canRemove(m marrawclient.Mask) bool {
	if m.Type == "brush" {
		return !m.Invert && len(m.Strokes) > 0
	}
	if m.Type != "ai" || m.Invert != (m.AIKind == "background") {
		return false
	}
	kind := m.AIKind
	if kind == "background" {
		kind = "subject"
	}
	return kind == "subject" || kind == "person" || kind == "class"
}

// maskSpec is a mask adjustment's slider: its key, label and range, how to
// read it and write it, and how it reads out.
type maskSpec struct {
	key, label string
	min, max   float32
	snap       float32
	format     func(float32) string
	get        func(a *marrawclient.MaskAdjust) *float64
}

// maskTone and maskFX are a mask's adjustments, in marraw's order.
var (
	maskTone = []maskSpec{
		{"expEV", "Exposure", -4, 4, 0.05, func(v float32) string { return fmt.Sprintf("%+.2f EV", v) }, func(a *marrawclient.MaskAdjust) *float64 { return &a.ExpEV }},
		{"contrast", "Contrast", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.Contrast }},
		{"toneHighlights", "Highlights", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.ToneHighlights }},
		{"toneShadows", "Shadows", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.ToneShadows }},
		{"whites", "Whites", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.Whites }},
		{"blacks", "Blacks", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.Blacks }},
		{"temp", "Temperature", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.Temp }},
		{"tint", "Tint", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.Tint }},
		{"saturation", "Saturation", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.Saturation }},
	}
	maskFX = []maskSpec{
		{"blur", "Blur", 0, 1, 0.02, percentOff, func(a *marrawclient.MaskAdjust) *float64 { return &a.Blur }},
		{"bokeh", "Bokeh", 0, 1, 0.02, percentOff, func(a *marrawclient.MaskAdjust) *float64 { return &a.Bokeh }},
		{"motionBlur", "Motion blur", 0, 1, 0.02, percentOff, func(a *marrawclient.MaskAdjust) *float64 { return &a.MotionBlur }},
		{"zoomBlur", "Zoom blur", 0, 1, 0.02, percentOff, func(a *marrawclient.MaskAdjust) *float64 { return &a.ZoomBlur }},
		{"glow", "Glow", 0, 1, 0.02, percentOff, func(a *marrawclient.MaskAdjust) *float64 { return &a.Glow }},
		{"streaks", "Light streaks", 0, 1, 0.02, percentOff, func(a *marrawclient.MaskAdjust) *float64 { return &a.Streaks }},
		{"prism", "Prism", -1, 1, 0.02, hundred, func(a *marrawclient.MaskAdjust) *float64 { return &a.Prism }},
		{"mosaic", "Mosaic", 0, 1, 0.02, percentOff, func(a *marrawclient.MaskAdjust) *float64 { return &a.Mosaic }},
		{"fxAngle", "Direction", 0, 180, 1, func(v float32) string { return fmt.Sprintf("%.0f°", v) }, func(a *marrawclient.MaskAdjust) *float64 { return &a.FXAngle }},
	}
)

// maskSpecOf is the adjustment named key.
func maskSpecOf(key string) (maskSpec, bool) {
	for _, s := range slices.Concat(maskTone, maskFX) {
		if s.key == key {
			return s, true
		}
	}
	return maskSpec{}, false
}

// hasAdjust reports whether a moves anything.
func hasAdjust(a marrawclient.MaskAdjust) bool { return a != marrawclient.MaskAdjust{} }

// hasFX reports whether a carries effects.
func hasFX(a marrawclient.MaskAdjust) bool {
	for _, s := range maskFX {
		if s.key != "fxAngle" && *s.get(&a) != 0 {
			return true
		}
	}
	return false
}

// remapMasks moves the masks' geometry with a quarter turn or a mirror of
// the frame, so each keeps to what it covers: the point map is the one the
// crop's rectangle goes through. AI masks follow by themselves.
func remapMasks(ms []marrawclient.Mask, pt func(x, y float64) (float64, float64), quarter bool) []marrawclient.Mask {
	if len(ms) == 0 {
		return ms
	}
	out := make([]marrawclient.Mask, len(ms))
	for i, m := range ms {
		switch m.Type {
		case "linear":
			m.X0, m.Y0 = pt(m.X0, m.Y0)
			m.X1, m.Y1 = pt(m.X1, m.Y1)
		case "radial":
			m.CX, m.CY = pt(m.CX, m.CY)
			if quarter {
				m.RX, m.RY = m.RY, m.RX
			} else {
				m.Angle = math.Mod(math.Mod(-m.Angle, 180)+180, 180)
			}
		case "brush":
			ss := make([]marrawclient.Stroke, len(m.Strokes))
			for j, s := range m.Strokes {
				s.Pts = slices.Clone(s.Pts)
				for k := 0; k+1 < len(s.Pts); k += 2 {
					s.Pts[k], s.Pts[k+1] = pt(s.Pts[k], s.Pts[k+1])
				}
				ss[j] = s
			}
			m.Strokes = ss
		}
		out[i] = m
	}
	return out
}
