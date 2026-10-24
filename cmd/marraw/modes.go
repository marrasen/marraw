package main

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// marraw's modes, as its top switcher has them: the library grid, the
// cull view, and the cull view with the develop panel out. Export is no
// mode: its button opens the export dialog.
const (
	modeLibrary = iota
	modeCull
	modeDevelop
	modeExport
)

// modeNames are the switcher's labels.
var modeNames = []string{"Library", "Cull", "Develop", "Export"}

// SetMode goes to mode Mode, one of the mode constants, as the switcher
// does.
type SetMode struct{ Mode int }

// setMode goes to mode m: Library leaves the cull view, Cull opens it on
// the photo in hand with the panel in, Develop with the panel out, and
// Export asks for an export.
func (cu *culler) setMode(m int) {
	switch m {
	case modeLibrary:
		cu.leaveCull()
	case modeCull, modeDevelop:
		if !cu.culling {
			if len(cu.photos) == 0 {
				return
			}
			cu.openCull(max(0, min(cu.cursor, len(cu.photos)-1)))
		}
		if open := m == modeDevelop; cu.panelShown() != open {
			if !open && cu.dev.hidden {
				break
			}
			cu.toggleDevelop()
		}
	case modeExport:
		cu.askExport()
	}
}

// Inks of the mode switcher: the chosen mode on the accent's pane, in a
// lighter accent, as marraw's.
var (
	modeChosen    = color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x40}
	modeChosenInk = color.NRGBA{R: 0xb4, G: 0xd0, B: 0xff, A: 0xff}
)

// modeSwitch is the switcher at the top: Library, Cull, Develop, and
// Export, which opens the export dialog and leaves the switch where it
// was.
type modeSwitch struct {
	seg  *widget.Segmented
	mode int
	node gunim.Node
}

func newModeSwitch(mode int) *modeSwitch {
	m := &modeSwitch{seg: widget.NewSegmented(modeNames...), mode: mode}
	m.seg.KeepFocus = true
	m.seg.SetSelected(mode, nil)
	m.seg.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		if i == modeExport {
			u.After(0, func(u *gunim.UI) { m.seg.SetSelected(m.mode, u) })
		}
		return SetMode{Mode: i}
	}
	th := marrawTheme().With(
		theme.Set(widget.Accent, modeChosen),
		theme.Set(widget.ButtonStrongInk, modeChosenInk),
		theme.Set(widget.FieldFill, color.NRGBA{}),
		theme.Set(widget.SegmentedHeight, 32))
	// Alone in its place, it needs no panel round it: its own track is
	// the glass.
	m.node = frosted(m.seg, th)
	return m
}

// show stands the switch on mode md.
func (m *modeSwitch) show(md int, u *gunim.UI) {
	m.mode = md
	if m.seg.Selected() != md {
		m.seg.SetSelected(md, u)
	}
}

// DevelopTool goes to Develop on the photo in hand and takes up Tool
// there, "crop" or "heal", as R and Q do from the library.
type DevelopTool struct{ Tool string }

// developTool opens Develop and, once the edit is loaded, the tool.
func (cu *culler) developTool(tool string) {
	cu.setMode(modeDevelop)
	d := &cu.dev
	if !cu.culling || !d.open || d.hidden {
		return
	}
	run := func() {
		switch {
		case tool == "crop" && !cu.crop.on:
			cu.toggleCrop()
		case tool == "heal" && !cu.heal.on:
			cu.healToggle()
		}
	}
	if d.mounted && d.id == cu.photos[cu.at].ID && len(d.pending) == 0 {
		run()
		return
	}
	d.pending = append(d.pending, run)
}
