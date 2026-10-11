package main

import (
	"fmt"
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/install"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// statusH is the status bar's height.
const statusH = 26

// statusBar is the line along the foot of the library, as marraw's: the
// folder's path, how many photos show of how many, the counts of picks,
// rejects and rated photos, how many are selected, and at the right the
// build and the backend's state.
type statusBar struct {
	path, shown, sel *widget.Label
	pills            [3]*countPill
	build, state     *widget.Label
	colour           *widget.Label
}

func newStatusBar() *statusBar {
	s := &statusBar{path: newSmallLabel(""), shown: newSmallLabel(""), sel: newSmallLabel(""),
		build: newSmallLabel(buildName()), state: newSmallLabel("backend connected"), colour: newSmallLabel("")}
	for _, l := range []*widget.Label{s.path, s.shown, s.sel, s.build, s.state, s.colour} {
		l.Face, l.Color, l.NoWrap, l.MaxLines = widget.MonoFont, noteInk, true, 1
	}
	s.pills = [3]*countPill{newCountPill(pickInk, flagSet, "picked"), newCountPill(rejectInk, rejectMark, "rejected"),
		newCountPill(ratedInk, starLit, "rated")}
	return s
}

// set shows the folder at path, shown of total photos, and sel selected.
func (s *statusBar) set(path string, shown, total, sel int) {
	s.path.Text = path
	s.shown.Text = fmt.Sprintf("%d / %d", shown, total)
	s.sel.Text = ""
	if sel > 1 {
		s.sel.Text = fmt.Sprintf("%d selected", sel)
	}
}

// Children implements [gunim.Composite].
func (s *statusBar) Children() []gunim.Node {
	return []gunim.Node{s.path, s.shown, s.pills[0], s.pills[1], s.pills[2], s.sel, s.build, s.state, s.colour}
}

// Layout implements [gunim.Node]: from the left, the path, the counts
// and the selection; at the right, the build and the backend's state.
func (s *statusBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := geom.Sz(c.Max.W, statusH)
	mid := box.H / 2
	right := box.W - 16
	for _, i := range []int{7, 6, 8} {
		k := kids.At(i)
		ks := k.Layout(gunim.Loose(geom.Sz(box.W/4, box.H)))
		if i == 7 {
			right -= ks.W
			k.Place(geom.Pt(right, mid-ks.H/2))
			right -= 12 + 14
		} else if i == 8 {
			ks = k.Layout(gunim.Loose(geom.Sz(box.W/5, box.H)))
			right -= ks.W
			k.Place(geom.Pt(right, mid-ks.H/2))
			right -= 16
		} else {
			right -= ks.W
			k.Place(geom.Pt(right, mid-ks.H/2))
			right -= 16
		}
	}
	x := float32(16)
	ps := kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, min(box.W*0.4, right-x-300)), box.H)))
	kids.At(0).Place(geom.Pt(x, mid-ps.H/2))
	x += ps.W + 16
	for i := 1; i <= 5; i++ {
		k := kids.At(i)
		ks := k.Layout(gunim.Loose(geom.Sz(max(0, right-x), box.H)))
		k.Place(geom.Pt(x, mid-ks.H/2))
		if ks.W > 0 {
			x += ks.W + 14
		}
	}
	return box
}

// Paint implements [gunim.Node].
func (s *statusBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(railFill))
	p.RRect(geom.Rc(0, 0, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}))
	for k := range kids.All {
		k.Paint(p)
	}
	// The backend's state, a dot before its words.
	st := kids.At(7)
	p.RRect(geom.Rc(st.Size().W*0+box.W-16-st.Size().W-12, box.H/2-3, 6, 6), 3, paint.Solid(doneInk))
}

// buildName is what the status bar calls this build: its version, for a
// release, or its commit.
func buildName() string {
	if install.IsRelease(version) {
		return "marraw " + version
	}
	return "build " + build()
}
