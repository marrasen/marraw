package main

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The lobby's inks: the mark's tile in the accent, faint.
var (
	lobbyTile   = color.NRGBA{R: 0x5e, G: 0x6a, B: 0xff, A: 0x1a}
	lobbyEdge   = color.NRGBA{R: 0x5e, G: 0x6a, B: 0xff, A: 0x59}
	lobbyMark   = theme.Color("marraw.lobby.mark.ink", color.NRGBA{R: 0xaa, G: 0xb0, B: 0xff, A: 0xff})
	lobbyTitle  = theme.Length("marraw.lobby.title", 23)
	lobbyMarkSz = theme.Length("marraw.lobby.mark.size", 32)
)

// lobby is what the grid shows with no shoot open: with the library
// empty, how to start, as marraw's first page; else where to go next.
type lobby struct {
	mark        *widget.Icon
	title, body *widget.Label
	add         *widget.Button
	hint        *widget.Label
	// empty says the library has no folders: the button and the hint
	// show.
	empty bool
}

func newLobby() *lobby {
	l := &lobby{mark: widget.NewIcon(icon.FolderPlus, ""), title: widget.NewLabel(""), body: widget.NewLabel(""),
		add: widget.NewButton("Add folder"), hint: widget.NewLabel("or drop a folder anywhere in this window")}
	l.mark.Size, l.mark.Color = lobbyMarkSz, lobbyMark
	l.title.Size, l.title.Align = lobbyTitle, text.AlignCenter
	l.body.Color, l.body.Align, l.body.MaxLines = noteInk, text.AlignCenter, 4
	l.hint.Color, l.hint.Size, l.hint.Align = noteInk, noteSize, text.AlignCenter
	l.add.Icon, l.add.Kind, l.add.KeepFocus = icon.Plus, widget.ButtonPrimary, true
	l.add.OnClick = widget.Sends(AskAddFolder{})
	l.set(false)
	return l
}

// set shows the page for a library that is empty, or one that has
// shoots, none of them open.
func (l *lobby) set(empty bool) {
	l.empty = empty
	if empty {
		l.mark.Icon = icon.FolderPlus
		l.title.Text = "Your library is empty"
		l.body.Text = "Add a folder of RAW photos to start culling. marraw reads them where they live: your files never move or change."
		return
	}
	l.mark.Icon = icon.Images
	l.title.Text = "No shoot open"
	l.body.Text = "Pick a shoot in the sidebar to start culling, or press Ctrl+K to jump to one."
}

// Children implements [gunim.Composite].
func (l *lobby) Children() []gunim.Node {
	return []gunim.Node{l.mark, l.title, l.body, l.add, l.hint}
}

// Layout implements [gunim.Node]: the mark on its tile, the title and
// the words beneath, and for an empty library the button and the hint,
// all centred in a column at most 420 wide. The button and hint of a
// library with shoots wait out of sight.
func (l *lobby) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := min(c.Max.W, 420)
	y := float32(0)
	put := func(i int, gap float32) {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(w, c.Max.H)))
		k.Place(geom.Pt((w-s.W)/2, y+gap))
		y += gap + s.H
	}
	mark := kids.At(0)
	ms := mark.Layout(gunim.Loose(geom.Sz(w, c.Max.H)))
	mark.Place(geom.Pt((w-ms.W)/2, 36-ms.H/2))
	y = 72
	put(1, 20)
	put(2, 8)
	if l.empty {
		put(3, 20)
		put(4, 12)
	} else {
		for _, i := range []int{3, 4} {
			kids.At(i).Layout(gunim.Loose(geom.Sz(w, c.Max.H)))
			kids.At(i).Place(geom.Pt(-10000, -10000))
		}
	}
	return geom.Sz(w, y)
}

// Paint implements [gunim.Node].
func (l *lobby) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	tile := geom.Rc(box.W/2-36, 0, 72, 72)
	p.RRect(tile, 18, paint.Solid(lobbyTile))
	p.RRectStroke(tile.Inset(geom.Uniform(0.5)), 17.5, paint.Fill{}, paint.Stroke{Width: 1, Color: lobbyEdge})
	kids.At(0).Paint(p)
	kids.At(1).Paint(p)
	kids.At(2).Paint(p)
	if l.empty {
		kids.At(3).Paint(p)
		kids.At(4).Paint(p)
	}
}
