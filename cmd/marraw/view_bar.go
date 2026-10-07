package main

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// barHeight is the filter bar's band under the grid's heading, and
// gridTop where the tiles begin.
const (
	barHeight = 44
	gridTop   = gridHeadHeight + barHeight
)

// The filter bar's choices, as marraw's filter bar has them.
var (
	sortNames  = []string{"Capture time, oldest first", "Capture time, newest first", "File name, A to Z", "File name, Z to A"}
	sortKeys   = []string{"captureAsc", "captureDesc", "nameAsc", "nameDesc"}
	flagNames  = []string{"All", "Picks", "Not rejected", "Rejected"}
	flagKeys   = []string{"all", "pick", "not-excluded", "exclude"}
	ratingKeys = []string{"Any rating", "★ and up", "★★ and up", "★★★ and up", "★★★★ and up", "★★★★★"}
)

// gridBar is the filter bar over the tiles: the order, the flags and the
// least rating shown, and the tiles' size. A change of the view sends the
// tiles out and back in their new order.
type gridBar struct {
	view   LibView
	sorts  *widget.Dropdown
	flags  *widget.Segmented
	rating *widget.Dropdown
	size   *widget.Slider
	row    gunim.Node
}

func newGridBar(g *gridView) *gridBar {
	b := &gridBar{sorts: widget.NewDropdown(sortNames...), flags: widget.NewSegmented(flagNames...),
		rating: widget.NewDropdown(ratingKeys...), size: widget.NewSlider(tileMin, tileMax)}
	b.sorts.Label, b.rating.Label = "Sort", "Least rating"
	b.sorts.OnChange = func(i int) gunim.Intent {
		v := b.view
		v.Sort = sortKeys[i]
		return SetLibView{View: v}
	}
	b.flags.KeepFocus = true
	b.flags.OnChange = func(i int) gunim.Intent {
		v := b.view
		v.Flag = flagKeys[i]
		return SetLibView{View: v}
	}
	b.rating.OnChange = func(i int) gunim.Intent {
		v := b.view
		v.MinRating = i
		return SetLibView{View: v}
	}
	// The tiles' size is the window's own: they spring to it.
	b.size.KeepFocus = true
	b.size.Set(200)
	b.size.OnMove(func(x float32, u *gunim.UI) {
		g.grid.Size = cellSize(x)
		u.Invalidate()
	})
	label := widget.NewLabel("Size")
	label.Color, label.Size = noteInk, noteSize
	sp := widget.NewSpacer()
	sized := &fixedWidth{w: 160, child: b.size}
	row := widget.Row(b.sorts, b.flags, b.rating, sp, label, sized).Grow(sp, 1)
	row.Cross = widget.CrossCenter
	b.row = row
	return b
}

// set shows view v.
func (b *gridBar) set(v LibView, u *gunim.UI) {
	b.view = v
	for i, k := range sortKeys {
		if k == v.Sort {
			b.sorts.Selected = i
		}
	}
	for i, k := range flagKeys {
		if k == v.Flag {
			b.flags.SetSelected(i, u)
		}
	}
	b.rating.Selected = max(0, min(v.MinRating, 5))
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (b *gridBar) Children() []gunim.Node { return []gunim.Node{b.row} }

// Layout implements [gunim.Node].
func (b *gridBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Constraints{Max: geom.Sz(c.Max.W-36, barHeight)})
	k.Place(geom.Pt(20, (barHeight-s.H)/2))
	return geom.Sz(c.Max.W, barHeight)
}

// Paint implements [gunim.Node].
func (b *gridBar) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
	kids.At(0).Paint(p)
}

// fixedWidth lays its child out w wide.
type fixedWidth struct {
	w     float32
	child gunim.Node
}

// Children implements [gunim.Composite].
func (f *fixedWidth) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node].
func (f *fixedWidth) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Constraints{Min: geom.Sz(f.w, 0), Max: geom.Sz(f.w, c.Max.H)})
	k.Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (f *fixedWidth) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}
