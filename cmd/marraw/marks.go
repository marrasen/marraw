package main

import (
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The marks' icons: a star, lit filled, and the pick's flag, set filled,
// and the reject's cross.
var (
	starLit = &icon.Icon{Name: "star-lit", Path: icon.Star.Path, Fill: icon.Star.Path}
	flagSet = &icon.Icon{Name: "flag-set", Path: icon.Flag.Path,
		Fill: "M4 15V4a1 1 0 0 1 .4-.8A6 6 0 0 1 8 2c3 0 5 2 7.333 2q2 0 3.067-.8A1 1 0 0 1 20 4v10a1 1 0 0 1-.4.8A6 6 0 0 1 16 16c-3 0-5-2-8-2a6 6 0 0 0-4 1.528Z"}
	rejectMark = icon.X
)

// markPlace is where a photo's marks sit: the five stars, size star and
// gap apart, from the left middle at stars, and the pick's flag and the
// reject's cross, size flag, about their middles.
type markPlace struct {
	stars        geom.Point
	star, gap    float32
	pick, reject geom.Point
	flag         float32
}

// starRect is star k's box, from nought.
func (m markPlace) starRect(k int) geom.Rect {
	x := m.stars.X + float32(k)*(m.star+m.gap)
	return geom.Rc(x, m.stars.Y-m.star/2, m.star, m.star)
}

// starAt is the star at p, from one, or nought for none: a star takes the
// gaps beside it and a little above and below.
func (m markPlace) starAt(p geom.Point) int {
	pad := max(4, m.gap/2)
	if p.Y < m.stars.Y-m.star/2-pad || p.Y > m.stars.Y+m.star/2+pad {
		return 0
	}
	for k := range 5 {
		r := m.starRect(k)
		if p.X >= r.Min.X-m.gap/2-1 && p.X < r.Max.X+m.gap/2+1 {
			return k + 1
		}
	}
	return 0
}

// flagAt is the flag at p: "pick", "exclude", or "" for neither.
func (m markPlace) flagAt(p geom.Point) string {
	reach := m.flag/2 + 4
	for _, f := range []string{"pick", "exclude"} {
		c := m.pick
		if f == "exclude" {
			c = m.reject
		}
		if math.Abs(float64(p.X-c.X)) <= float64(reach) && math.Abs(float64(p.Y-c.Y)) <= float64(reach) {
			return f
		}
	}
	return ""
}

// marks are a photo's stars and flags as they show, animated: the stars
// sweep from the old rating to the new, each swelling as the sweep passes,
// a flag pops in, drawing itself on, with a ring going out from it, and a
// rejected photo dims. With the pointer over them the stars it would give
// light faintly, and the flags not set show, to be clicked.
type marks struct {
	fill, pick, reject, ring, dim *anim.Float
	ringInk                       color.NRGBA
	// hot is how far the flags not set show, preview the stars the
	// pointer would give, and over the flag the pointer is over.
	hot, preview *anim.Float
	over         string
	rating       int
	flag         string
}

// sweep carries the stars to a new rating, straight on, so no star past it
// lights for a moment.
var sweep = anim.Spring{Response: 0.32, Damping: 1}

func flagged(f string) bool { return f == "pick" || f == "exclude" }

func flagInk(f string) color.NRGBA {
	if f == "exclude" {
		return rejectInk
	}
	return pickInk
}

func on(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

func newMarks(g *anim.Group, rating int, flag string) *marks {
	m := &marks{fill: anim.NewFloat(0), pick: anim.NewFloat(0), reject: anim.NewFloat(0), ring: anim.NewFloat(1),
		dim: anim.NewFloat(0), hot: anim.NewFloat(0), preview: anim.NewFloat(0)}
	m.jump(rating, flag)
	g.Add(m.fill, m.pick, m.reject, m.ring, m.dim, m.hot, m.preview)
	return m
}

// jump shows rating and flag at once, for another photo.
func (m *marks) jump(rating int, flag string) {
	m.rating, m.flag = rating, flag
	m.fill.Jump(float32(rating))
	m.pick.Jump(on(flag == "pick"))
	m.reject.Jump(on(flag == "exclude"))
	m.dim.Jump(on(flag == "exclude"))
	m.ring.Jump(1)
}

// set animates to rating and flag.
func (m *marks) set(rating int, flag string, th *theme.Live) {
	if rating != m.rating {
		m.rating = rating
		m.fill.Animate(float32(rating), sweep)
	}
	if flag == m.flag {
		return
	}
	m.flag = flag
	for f, a := range map[string]*anim.Float{"pick": m.pick, "exclude": m.reject} {
		if flag == f {
			a.Jump(0)
			a.Animate(1, widget.Bounce.Get(th))
		} else {
			a.Animate(0, widget.Quick.Get(th))
		}
	}
	if flagged(flag) {
		m.ringInk = flagInk(flag)
		m.ring.Jump(0)
		m.ring.Animate(1, anim.Tween{Duration: 450 * time.Millisecond})
	}
	m.dim.Animate(on(flag == "exclude"), widget.Settle.Get(th))
}

// hover shows the pointer over the marks at place: the stars it would give
// preview, and the flags not set show; outside, over nothing.
func (m *marks) hover(at geom.Point, inside bool, place markPlace, th *theme.Live) {
	m.hot.Animate(on(inside), widget.Quick.Get(th))
	star := 0
	m.over = ""
	if inside {
		star = place.starAt(at)
		m.over = place.flagAt(at)
	}
	m.preview.Animate(float32(star), widget.Quick.Get(th))
}

// paint draws the marks at place; offAlpha is how strongly the stars not
// lit show, and alwaysFlags shows the flags not set without the pointer.
func (m *marks) paint(p *paint.Painter, th *theme.Live, place markPlace, offAlpha uint8, alwaysFlags bool) {
	m.paintStars(p, th, place, offAlpha)
	m.paintFlags(p, th, place, alwaysFlags)
}

func (m *marks) paintStars(p *paint.Painter, th *theme.Live, place markPlace, offAlpha uint8) {
	fill, preview, hot := m.fill.Value(), m.preview.Value(), m.hot.Value()
	off := starOff
	off.A = uint8(min(255, float32(offAlpha)*(1+0.8*hot)))
	for k := range 5 {
		t := min(max(fill-float32(k), 0), 1)
		swell := 1 + 0.45*float32(math.Sin(math.Pi*float64(t)))
		r := scaleAbout(place.starRect(k), swell)
		// The star the pointer would give lights faintly.
		pv := min(max(preview-float32(k), 0), 1) * (1 - t)
		if t < 0.999 {
			c := anim.Mix(anim.ColorCodec, off, withAlpha(starInk, 0.75), pv)
			p.Mask(icon.Stroke{Icon: icon.Star, Width: 2.2, Progress: 1}, r, withAlpha(c, 1-t))
		}
		if t > 0.001 {
			p.Mask(icon.Stroke{Icon: starLit, Width: 2, Progress: 1}, r, withAlpha(starInk, t))
		}
	}
}

func (m *marks) paintFlags(p *paint.Painter, th *theme.Live, place markPlace, always bool) {
	hot := m.hot.Value()
	if always {
		hot = max(hot, 0.6)
	}
	for _, f := range []struct {
		key      string
		at       geom.Point
		set      *anim.Float
		plain    *icon.Icon
		lit      *icon.Icon
		ink      color.NRGBA
		strokeOn float32
	}{
		{"pick", place.pick, m.pick, icon.Flag, flagSet, pickInk, 2},
		{"exclude", place.reject, m.reject, rejectMark, rejectMark, rejectInk, 3},
	} {
		r := geom.Rc(f.at.X-place.flag/2, f.at.Y-place.flag/2, place.flag, place.flag)
		s := f.set.Value()
		// Not set, it shows faintly under the pointer, and more so over it.
		if faint := hot * (1 - min(s, 1)); faint > 0.01 {
			a := float32(0.35)
			if m.over == f.key {
				a = 0.85
			}
			p.Mask(icon.Stroke{Icon: f.plain, Width: 2, Progress: 1}, r, withAlpha(color.NRGBA{R: 0xd8, G: 0xdc, B: 0xe4, A: 0xff}, faint*a))
		}
		if s > 0.01 {
			// Set, it pops in and draws itself on.
			rr := scaleAbout(r, 0.6+0.4*s)
			p.Mask(icon.Stroke{Icon: f.lit, Width: f.strokeOn, Progress: min(s*1.4, 1)}, rr, withAlpha(f.ink, min(s, 1)))
		}
		if m.flag == f.key {
			if g := m.ring.Value(); g < 0.999 {
				size := place.flag * (1 + 1.4*g)
				c := withAlpha(m.ringInk, (1-g)*0.8)
				p.RRectStroke(geom.Rc(f.at.X-size/2, f.at.Y-size/2, size, size), size/2, paint.Fill{}, paint.Stroke{Width: 1.5, Color: c})
			}
		}
	}
}
