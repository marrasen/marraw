package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// GapGroup is a run of photos taken close together, as marraw groups a
// shoot by the time between its frames: where it starts in the view, how
// many it holds, when the first and last were taken, in seconds since
// 1970, nought for none, and the minutes since the group before, below
// nought for the first.
type GapGroup struct {
	Start, Count int
	From, To     int64
	GapBefore    int
}

// gapChoices are the gaps the grid can group by, in minutes, nought for
// none, as marraw's control offers them.
var gapChoices = []int{0, 1, 2, 5, 10, 30}

// defaultGap is the gap a folder groups by where none is chosen.
const defaultGap = 6

// gapGroups splits photos, in the view's order, into groups wherever more
// than gap minutes pass between one frame taken and the next; frames with
// no time taken never start one. No gap, or a sort by name, makes none.
func gapGroups(photos []marrawclient.Photo, gap int, sortBy string) []GapGroup {
	if gap <= 0 || len(photos) == 0 || sortBy == "nameAsc" || sortBy == "nameDesc" {
		return nil
	}
	var out []GapGroup
	var last int64
	for i, p := range photos {
		t := p.TakenAt
		var since int64
		if t > 0 && last > 0 {
			since = t - last
			if since < 0 {
				since = -since
			}
		}
		if len(out) == 0 || since > int64(gap)*60 {
			g := GapGroup{Start: i, GapBefore: -1}
			if len(out) > 0 {
				g.GapBefore = int(math.Round(float64(since) / 60))
			}
			out = append(out, g)
		}
		g := &out[len(out)-1]
		g.Count++
		if t > 0 {
			if g.From == 0 {
				g.From = t
			}
			g.To = t
			last = t
		}
	}
	return out
}

// groupStarts are where groups start, for the grid.
func groupStarts(gs []GapGroup) []int {
	if gs == nil {
		return nil
	}
	out := make([]int, len(gs))
	for i, g := range gs {
		out[i] = g.Start
	}
	return out
}

// rangeLabel is when group g was taken, as "09:12 – 09:18", with its day
// before it where the groups span days.
func rangeLabel(g GapGroup, days bool) string {
	if g.From == 0 {
		return "no time"
	}
	a, b := time.Unix(g.From, 0), time.Unix(g.To, 0)
	if a.After(b) {
		a, b = b, a
	}
	s := a.Format("15:04")
	if e := b.Format("15:04"); e != s {
		s += " – " + e
	}
	if days {
		s = a.Format("Mon 2 Jan") + " · " + s
	}
	return s
}

// gapLabel is the gap before a group, as "+5 min gap", or in hours from
// an hour and a half, after the group where the newest come first.
func gapLabel(min int, newestFirst bool) string {
	way := "before"
	if newestFirst {
		way = "after"
	}
	if min >= 90 {
		return fmt.Sprintf("+%.1f h gap %s", float64(min)/60, way)
	}
	return fmt.Sprintf("+%d min gap %s", min, way)
}

// spansDays reports whether the groups were taken on more than one day.
func spansDays(gs []GapGroup) bool {
	day := ""
	for _, g := range gs {
		if g.From == 0 {
			continue
		}
		d := time.Unix(g.From, 0).Format("2006-01-02")
		if day != "" && d != day {
			return true
		}
		day = d
	}
	return false
}

// gapHeaderHeight is a group's header's height in the grid.
const gapHeaderHeight = 40

// gapHeader is a group's header in the grid, as marraw's: a clock, when
// its photos were taken, how many there are, and, at the right, the time
// since the group before. It reads its group from the grid's state as it
// lays out, so a header kept as the photos change says what is now so.
type gapHeader struct {
	v                *gridView
	k                int
	when, count, gap *widget.Label
	// gapAt is where the gap's label is.
	gapAt geom.Rect
}

func (v *gridView) newGapHeader(k int) gunim.Node {
	h := &gapHeader{v: v, k: k, when: widget.NewLabel(""), count: widget.NewLabel(""), gap: widget.NewLabel("")}
	h.when.Size = noteSize
	h.count.Size, h.count.Color = noteSize, noteInk
	h.gap.Face, h.gap.Size, h.gap.Color = widget.MonoFont, badgeSize, noteInk
	return h
}

// Children implements [gunim.Composite].
func (h *gapHeader) Children() []gunim.Node { return []gunim.Node{h.when, h.count, h.gap} }

// Layout implements [gunim.Node].
func (h *gapHeader) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	gs := h.v.st.Groups
	h.when.Text, h.count.Text, h.gap.Text = "", "", ""
	if h.k < len(gs) {
		g := gs[h.k]
		h.when.Text = rangeLabel(g, spansDays(gs))
		h.count.Text = fmt.Sprintf("%d %s", g.Count, map[bool]string{false: "frames", true: "frame"}[g.Count == 1])
		if g.GapBefore >= 0 {
			h.gap.Text = gapLabel(g.GapBefore, h.v.st.View.Sort == "captureDesc")
		}
	}
	mid := box.H/2 + 3
	x := float32(22)
	for i := range 2 {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(box.W/2, box.H)))
		k.Place(geom.Pt(x, mid-s.H/2))
		x += s.W + 10
	}
	g := kids.At(2)
	s := g.Layout(gunim.Loose(geom.Sz(box.W/2, box.H)))
	h.gapAt = geom.Rc(box.W-10-s.W, mid-s.H/2, s.W, s.H)
	g.Place(h.gapAt.Min)
	return box
}

// Paint implements [gunim.Node].
func (h *gapHeader) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	mid := box.H/2 + 3
	p.Mask(icon.Stroke{Icon: icon.Clock, Width: 2, Progress: 1}, geom.Rc(2, mid-7, 14, 14), noteInk.Get(f.Theme))
	kids.At(0).Paint(p)
	kids.At(1).Paint(p)
	if g := kids.At(2); h.gap.Text != "" {
		r := h.gapAt.Inset(geom.Insets{Left: -8, Right: -8, Top: -3, Bottom: -3})
		p.RRect(r, r.Size().H/2, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0e}))
		g.Paint(p)
	}
	// A hairline under it, across the grid.
	p.RRect(geom.Rc(0, box.H-4, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
}
