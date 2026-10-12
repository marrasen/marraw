package main

import (
	"fmt"
	"image/color"
	"reflect"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// overviewView is the overview of a folder, where the photos show: a filter
// over a grid of the folder's shoots, a card each, under a header for each
// folder between. A click on a card selects it, and a double click opens
// its shoot, as Enter does on the card the keyboard is on.
type overviewView struct {
	v      *gridView
	st     OverviewIn
	filter *widget.TextField
	grid   *widget.TileGrid
	// cards are the cards shown, the filter's, in order; starts and titles
	// each section's first card and its folders.
	cards  []OverviewCard
	starts []int
	titles []string
	// big are the large covers come, by their keys, and wanted the keys
	// last asked for, so the same are not asked for again.
	big    map[string]*paint.Image
	wanted string
	// none says why there are no cards, where there are none.
	none *widget.Label
	// gridW is how wide the cards' room is, beside the info panel; the
	// bar above them runs the overview's whole width.
	gridW float32
}

// ovTextH is the band under a card's cover, for its name and line, and
// ovTop the room above the cover, clear of the header over its row.
const (
	ovTextH = 54
	ovTop   = 12
)

func newOverviewView(v *gridView) *overviewView {
	o := &overviewView{v: v, filter: widget.NewTextField(), big: map[string]*paint.Image{}}
	o.filter.Placeholder = "Filter shoots"
	// The text typed narrows the cards as it changes.
	o.filter.OnChange = func(_ string, u *gunim.UI) gunim.Intent {
		o.refilter(u)
		return nil
	}
	o.none = widget.NewLabel("")
	o.none.Color, o.none.Size = noteInk, noteSize
	o.grid = widget.NewTileGrid(geom.Sz(ovCardW, ovTop+ovCoverH+ovTextH))
	o.grid.Pace = 2
	o.grid.Tile = o.newCard
	o.grid.Header = o.newHeader
	o.grid.HeaderHeight = gapHeaderHeight
	o.grid.OnActivate = func(i int, _ *gunim.UI) gunim.Intent { return o.open(i) }
	o.grid.OnView = func(first, count int, _ *gunim.UI) gunim.Intent {
		var keys []string
		for i := first; i < first+count && i < len(o.cards); i++ {
			if k := o.cards[i].Cover; k != "" && o.big[k] == nil {
				keys = append(keys, k)
			}
		}
		if s := strings.Join(keys, "\\x00"); s != o.wanted && len(keys) > 0 {
			o.wanted = s
			return WantBigCovers{Keys: keys}
		}
		return nil
	}
	return o
}

// open opens card i's shoot.
func (o *overviewView) open(i int) gunim.Intent {
	if i < 0 || i >= len(o.cards) || o.cards[i].Offline {
		return nil
	}
	return OpenFromOverview{Path: o.cards[i].Path}
}

// show takes the overview to show; one closed leaves the cards as they
// are, to fade out.
func (o *overviewView) show(in OverviewIn, u *gunim.UI) {
	if !in.On || reflect.DeepEqual(in, o.st) {
		return
	}
	if in.Key != o.st.Key {
		// Another folder: the filter starts empty, and the grid at the top.
		o.filter.SetText("", u)
		o.wanted = ""
	}
	o.st = in
	o.refilter(u)
}

// refilter shows the cards the filter lets through.
func (o *overviewView) refilter(u *gunim.UI) {
	q := strings.ToLower(strings.TrimSpace(o.filter.Text()))
	o.cards, o.starts, o.titles = o.cards[:0], o.starts[:0], o.titles[:0]
	for _, s := range o.st.Sections {
		first := true
		for _, c := range s.Cards {
			if q != "" && !strings.Contains(strings.ToLower(c.Title), q) && !strings.Contains(strings.ToLower(c.Line), q) {
				continue
			}
			if first {
				o.starts, o.titles = append(o.starts, len(o.cards)), append(o.titles, s.Title)
				first = false
			}
			o.cards = append(o.cards, c)
		}
	}
	o.none.Text = ""
	if len(o.cards) == 0 {
		o.none.Text = "No shoots here yet."
		if q != "" {
			o.none.Text = "No shoots match the filter."
		}
	}
	o.grid.SetLen(len(o.cards), u)
	o.grid.Rebuild(u)
	if len(o.titles) == 1 && o.titles[0] == "" {
		// The shoots right in the folder, and none under: no header.
		o.grid.SetGroups(nil, u)
	} else {
		o.grid.SetGroups(o.starts, u)
	}
	u.Invalidate()
}

// cover is the picture of card c's cover: the large one where it has come,
// else the sidebar's, or nil.
func (o *overviewView) cover(c OverviewCard) *paint.Image {
	if m := o.big[c.Cover]; m != nil {
		return m
	}
	return o.v.rail.covers[c.Cover]
}

// Children implements [gunim.Composite].
func (o *overviewView) Children() []gunim.Node { return []gunim.Node{o.filter, o.grid, o.none} }

// Layout implements [gunim.Node]: the filter where the filter bar is, the
// cards under it.
func (o *overviewView) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	// The filter after the lead the grid view floats over the bar.
	x := 20 + o.v.bar.lead.w + widget.Gap.Get(f.Theme)
	fs := kids.At(0).Layout(gunim.Tight(geom.Sz(min(280, max(0, box.W-x-20)), 32)))
	kids.At(0).Place(geom.Pt(x, (barHeight-fs.H)/2))
	gw := box.W
	if o.gridW > 0 {
		gw = min(o.gridW, box.W)
	}
	kids.At(1).Layout(gunim.Tight(geom.Sz(gw, max(0, box.H-barHeight))))
	kids.At(1).Place(geom.Pt(0, barHeight))
	ns := kids.At(2).Layout(gunim.Loose(geom.Sz(gw, 40)))
	kids.At(2).Place(geom.Pt((gw-ns.W)/2, barHeight+60))
	return box
}

// Paint implements [gunim.Node]: over the photos, the window's ground.
func (o *overviewView) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	ground := paint.Solid(gunim.WindowBackground.Get(f.Theme))
	gw := box.W
	if o.gridW > 0 {
		gw = min(o.gridW, box.W)
	}
	// The bar's row across, the cards' room under it, the info panel
	// left as it is.
	p.RRect(geom.Rc(0, 0, box.W, barHeight), 0, ground)
	p.RRect(geom.Rc(0, barHeight, gw, max(0, box.H-barHeight)), 0, ground)
	p.RRect(geom.Rc(0, barHeight-1, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}))
	for k := range kids.All {
		k.Paint(p)
	}
}

// ovCard is one shoot's card: its cover, its name and the line under it,
// and how far it is culled.
type ovCard struct {
	anim.Group
	o           *overviewView
	i           int
	title, line *widget.Label
	hot         *anim.Float
}

func (o *overviewView) newCard(i int) gunim.Node {
	c := &ovCard{o: o, i: i, title: widget.NewLabel(""), line: newSmallLabel(""), hot: anim.NewFloat(0)}
	c.title.Size, c.title.MaxLines = railSize, 1
	c.line.Size, c.line.Color, c.line.MaxLines = railLine, noteInk, 1
	c.Add(c.hot)
	if i < len(o.cards) {
		c.title.Text, c.line.Text = o.cards[i].Title, o.cards[i].Line
	}
	return c
}

// Children implements [gunim.Composite].
func (c *ovCard) Children() []gunim.Node { return []gunim.Node{c.title, c.line} }

// Layout implements [gunim.Node].
func (c *ovCard) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := cs.Max
	ts := kids.At(0).Layout(gunim.Loose(geom.Sz(box.W-4, 20)))
	kids.At(0).Place(geom.Pt(2, ovTop+ovCoverH+9))
	kids.At(1).Layout(gunim.Loose(geom.Sz(box.W-4, 18)))
	kids.At(1).Place(geom.Pt(2, ovTop+ovCoverH+9+ts.H+2))
	return box
}

// Paint implements [gunim.Node].
func (c *ovCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if c.i >= len(c.o.cards) {
		return
	}
	card := c.o.cards[c.i]
	at := geom.Rc(0, ovTop, box.W, ovCoverH)
	op := float32(1)
	if card.Offline {
		op = 0.45
	}
	if img := c.o.cover(card); img != nil {
		p.Image(img, at, paint.ImageOpts{Opacity: op, Radius: 8})
	} else {
		p.RRect(at, 8, paint.Solid(coverBlank))
		mark := geom.Rc(at.Center().X-12, at.Center().Y-12, 24, 24)
		p.Mask(icon.Stroke{Icon: icon.Image, Width: 1.5, Progress: 1}, mark, withAlpha(noteInk.Get(f.Theme), 0.45*op))
	}
	if h := c.hot.Value(); h > 0.01 {
		// Under the pointer, a pale pane over the cover.
		p.RRect(at, 8, paint.Solid(withAlpha(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.08*h)))
		p.RRectStroke(at.Inset(geom.Uniform(0.5)), 7.5, paint.Fill{}, paint.Stroke{Width: 1, Color: withAlpha(railMark, 0.6*h)})
	}
	// How far it is culled, along the cover's foot.
	if done := card.Picked + card.Rejected; done > 0 {
		total := float32(max(card.Count, done))
		track := geom.Rc(8, ovTop+ovCoverH-11, box.W-16, 4)
		p.RRect(track, 2, paint.Solid(color.NRGBA{A: 0x90}))
		pw := track.Size().W * float32(card.Picked) / total
		rw := track.Size().W * float32(card.Rejected) / total
		if pw > 0 {
			p.RRect(geom.Rc(track.Min.X, track.Min.Y, max(pw, 4), 4), 2, paint.Solid(pickInk))
		}
		if rw > 0 {
			p.RRect(geom.Rc(track.Min.X+pw, track.Min.Y, max(rw, 4), 4), 2, paint.Solid(rejectInk))
		}
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// Handle implements [gunim.Handler]: it lights under the pointer. The
// grid takes the clicks: one selects the card, as a photo's, and a double
// click or Enter opens its shoot.
func (c *ovCard) Handle(e input.Event, u *gunim.UI) bool {
	switch e.(type) {
	case input.PointerEnter, input.PointerMove:
		c.hot.Animate(1, widget.Quick.Get(u.Theme()))
		u.Invalidate()
	case input.PointerLeave:
		c.hot.Animate(0, widget.Settle.Get(u.Theme()))
		u.Invalidate()
	}
	return false
}

// Step implements [gunim.Animator].
func (c *ovCard) Step(dt time.Duration) bool { return c.Group.Step(dt) }

// ovHeader is a section's header: the folders between the folder shown and
// its shoots, and how many shoots.
type ovHeader struct {
	o           *overviewView
	k           int
	name, count *widget.Label
}

func (o *overviewView) newHeader(k int) gunim.Node {
	h := &ovHeader{o: o, k: k, name: widget.NewLabel(""), count: widget.NewLabel("")}
	h.name.Size = noteSize
	h.count.Size, h.count.Color = noteSize, noteInk
	return h
}

// Children implements [gunim.Composite].
func (h *ovHeader) Children() []gunim.Node { return []gunim.Node{h.name, h.count} }

// Layout implements [gunim.Node].
func (h *ovHeader) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	h.name.Text, h.count.Text = "", ""
	if h.k < len(h.o.titles) {
		h.name.Text = h.o.titles[h.k]
		if h.name.Text == "" {
			h.name.Text = h.o.st.Title
		}
		end := len(h.o.cards)
		if h.k+1 < len(h.o.starts) {
			end = h.o.starts[h.k+1]
		}
		n := end - h.o.starts[h.k]
		h.count.Text = fmt.Sprintf("%d %s", n, map[bool]string{false: "shoots", true: "shoot"}[n == 1])
	}
	mid := box.H/2 + 3
	x := float32(22)
	ns := kids.At(0).Layout(gunim.Loose(geom.Sz(box.W/2, box.H)))
	kids.At(0).Place(geom.Pt(x, mid-ns.H/2))
	cs := kids.At(1).Layout(gunim.Loose(geom.Sz(box.W/3, box.H)))
	kids.At(1).Place(geom.Pt(x+ns.W+10, mid-cs.H/2))
	return box
}

// Paint implements [gunim.Node].
func (h *ovHeader) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rc(22, box.H-1, max(0, box.W-44), 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}))
	for k := range kids.All {
		k.Paint(p)
	}
}
