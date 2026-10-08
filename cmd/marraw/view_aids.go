package main

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The badges' looks, as marraw's: small marks on a shade at the
// picture's corners, the burst's at the top left, green on its sharpest
// frame, and soft focus, amber, and closed eyes, rose, at the bottom
// right.
var (
	badgeSize  = theme.Length("marraw.badge.size", 10)
	badgeInk   = theme.Color("marraw.badge", color.NRGBA{R: 0xd4, G: 0xd4, B: 0xd8, A: 0xff})
	bestInk    = color.NRGBA{R: 0x4a, G: 0xde, B: 0x80, A: 0xff}
	softInk    = color.NRGBA{R: 0xfb, G: 0xbf, B: 0x24, A: 0xff}
	eyesInk    = color.NRGBA{R: 0xfb, G: 0x71, B: 0x85, A: 0xff}
	badgeShade = color.NRGBA{A: 0x8c}
)

const (
	badgeH    = 16
	badgeIcon = 11
)

// newBadgeLabel is the label for a burst's place, as "2/4".
func newBadgeLabel() *widget.Label {
	l := widget.NewLabel("")
	l.Face, l.Size, l.Color = widget.MonoFont, badgeSize, badgeInk
	return l
}

// paintBadges draws aids a's badges on the picture in pic, k of the way
// in, the burst's place written by label.
func paintBadges(p *paint.Painter, pic geom.Rect, a Aids, k float32, label gunim.Child) {
	if k < 0.01 || pic.Size().W < 40 {
		return
	}
	pop := 0.7 + 0.3*min(k, 1.2)
	if a.BurstOf > 0 {
		ls := label.Size()
		r := geom.Rc(pic.Min.X+3, pic.Min.Y+3, 2+badgeIcon+3+ls.W+5, badgeH)
		func() {
			defer p.Push(paint.Scale(pop, r.Min))()
			defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-2)), Opacity: min(k, 1)})()
			p.RRect(r, 4, paint.Solid(badgeShade))
			ink := color.NRGBA{R: 0xd4, G: 0xd4, B: 0xd8, A: 0xff}
			if a.BurstBest {
				ink = bestInk
			}
			p.Mask(icon.Stroke{Icon: icon.Layers, Width: 2.2, Progress: 1}, geom.Rc(r.Min.X+3, r.Min.Y+(badgeH-badgeIcon)/2, badgeIcon, badgeIcon), ink)
			label.Paint(p)
		}()
	}
	var marks []struct {
		ic  *icon.Icon
		ink color.NRGBA
	}
	if a.Eyes {
		marks = append(marks, struct {
			ic  *icon.Icon
			ink color.NRGBA
		}{icon.EyeClosed, eyesInk})
	}
	if a.Soft {
		marks = append(marks, struct {
			ic  *icon.Icon
			ink color.NRGBA
		}{icon.Focus, softInk})
	}
	if len(marks) == 0 {
		return
	}
	w := float32(4 + len(marks)*(badgeIcon+4))
	r := geom.Rc(pic.Max.X-3-w, pic.Max.Y-3-badgeH, w, badgeH)
	defer p.Push(paint.Scale(pop, r.Max))()
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-2)), Opacity: min(k, 1)})()
	p.RRect(r, 4, paint.Solid(badgeShade))
	for i, m := range marks {
		x := r.Min.X + 4 + float32(i)*(badgeIcon+4)
		p.Mask(icon.Stroke{Icon: m.ic, Width: 2.2, Progress: 1}, geom.Rc(x, r.Min.Y+(badgeH-badgeIcon)/2, badgeIcon, badgeIcon), m.ink)
	}
}

// paintStripAids draws aids a's badges on a filmstrip picture in pic, as
// icons alone.
func paintStripAids(p *paint.Painter, pic geom.Rect, a Aids) {
	if a.BurstOf > 0 {
		ink := color.NRGBA{R: 0xd4, G: 0xd4, B: 0xd8, A: 0xff}
		if a.BurstBest {
			ink = bestInk
		}
		r := geom.Rc(pic.Max.X-16, pic.Min.Y+3, 13, 13)
		p.RRect(r, 3, paint.Solid(badgeShade))
		p.Mask(icon.Stroke{Icon: icon.Layers, Width: 2.2, Progress: 1}, r.Inset(geom.Uniform(2)), ink)
	}
	x := pic.Max.X - 16
	for _, m := range []struct {
		on  bool
		ic  *icon.Icon
		ink color.NRGBA
	}{{a.Soft, icon.Focus, softInk}, {a.Eyes, icon.EyeClosed, eyesInk}} {
		if !m.on {
			continue
		}
		r := geom.Rc(x, pic.Max.Y-16, 13, 13)
		p.RRect(r, 3, paint.Solid(badgeShade))
		p.Mask(icon.Stroke{Icon: m.ic, Width: 2.2, Progress: 1}, r.Inset(geom.Uniform(2)), m.ink)
		x -= 16
	}
}
