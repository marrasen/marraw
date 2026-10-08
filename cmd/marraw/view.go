package main

import (
	"context"
	"image/color"
	"image/png"
	"os"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// registerViews is the window half: the library grid, and the cull view
// that opens over it.
func registerViews(w *gunim.Window) {
	w.RegisterTheme(marrawTheme())
	gunim.RegisterView(w, "grid", newGridView, (*gridView).show)
	gunim.RegisterPatch(w, "grid", (*gridView).thumbIn)
	gunim.RegisterPatch(w, "grid", (*gridView).photoMarked)
	gunim.RegisterPatch(w, "grid", (*gridView).gridAt)
	gunim.RegisterPatch(w, "grid", (*gridView).gridSel)
	gunim.RegisterPatch(w, "grid", (*gridView).photoAspect)
	gunim.RegisterPatch(w, "grid", (*gridView).railIn)
	gunim.RegisterPatch(w, "grid", (*gridView).gridNotice)
	gunim.RegisterView(w, "cull", newCullView, (*cullView).show)
	gunim.RegisterView(w, "develop", newDevelopView, (*developView).show)
	gunim.RegisterView(w, "confirm", newConfirmDialog, nil)
	gunim.RegisterView(w, "export", newExportDialog, nil)
	gunim.RegisterView(w, "shortcuts", newShortcutsDialog, nil)
	gunim.RegisterView(w, "preset", newPresetDialog, nil)
	gunim.RegisterPatch(w, "develop", (*developView).histIn)
	gunim.RegisterPatch(w, "develop", (*developView).presetThumbIn)
}

// marrawTheme is gunim's dark theme with room for the develop panel's
// longer labels, as "Preserve highlights". The window takes it as it
// opens.
func marrawTheme() theme.Theme {
	th := widget.Dark().With(theme.Set(widget.SliderRowLabel, 136), theme.Set(widget.SliderRowValue, 58))
	th.Name = "marraw"
	return th
}

// The colours and sizes the views share.
var (
	noteInk  = theme.Color("marraw.note", color.NRGBA{R: 0xa4, G: 0xab, B: 0xbb, A: 0xff})
	noteSize = theme.Length("marraw.note.size", 12.5)

	pickInk   = color.NRGBA{R: 0x34, G: 0xc7, B: 0x6f, A: 0xff}
	rejectInk = color.NRGBA{R: 0xe5, G: 0x5a, B: 0x52, A: 0xff}
	starInk   = color.NRGBA{R: 0xf5, G: 0xc4, B: 0x42, A: 0xff}
	starOff   = color.NRGBA{R: 0x55, G: 0x58, B: 0x62, A: 0xc0}
	backdrop  = color.NRGBA{R: 0x0b, G: 0x0c, B: 0x0f, A: 0xff}
	frameInk  = color.NRGBA{R: 0x1a, G: 0x1c, B: 0x22, A: 0xff}
)

// heroTag names a photo's picture, so it flies between its tile and the
// cull view.
func heroTag(id int64) string { return "photo:" + itoa(id) }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for neg := n < 0; n != 0; n /= 10 {
		d := n % 10
		if neg {
			d = -d
		}
		i--
		b[i] = byte('0' + d)
		if n/10 == 0 && neg {
			i--
			b[i] = '-'
		}
	}
	return string(b[i:])
}

// fitIn is the largest rectangle of aspect, width over height, inside box,
// centred.
func fitIn(box geom.Rect, aspect float32) geom.Rect {
	s := box.Size()
	if aspect <= 0 || s.W <= 0 || s.H <= 0 {
		return box
	}
	w, h := s.W, s.W/aspect
	if h > s.H {
		w, h = s.H*aspect, s.H
	}
	return geom.Rc(box.Min.X+(s.W-w)/2, box.Min.Y+(s.H-h)/2, w, h)
}

// scaleAbout is r scaled by k about its middle.
func scaleAbout(r geom.Rect, k float32) geom.Rect {
	c, s := r.Center(), r.Size()
	return geom.Rc(c.X-s.W*k/2, c.Y-s.H*k/2, s.W*k, s.H*k)
}

// keyRight is the Right key pressed, for the script's steps, and keyZ the
// zoom key.
func keyRight() input.Event { return input.KeyPress{Key: input.KeyRight} }
func keyZ() input.Event     { return input.KeyPress{Key: input.KeyZ} }

// namedKeys are the keys -keys can press.
var namedKeys = map[string]input.Key{
	"0": input.Key0, "1": input.Key1, "2": input.Key2, "3": input.Key3, "4": input.Key4, "5": input.Key5,
	"r": input.KeyR, "w": input.KeyW, "p": input.KeyP, "x": input.KeyX, "u": input.KeyU, "z": input.KeyZ, "space": input.KeySpace, "d": input.KeyD,
	"right": input.KeyRight, "left": input.KeyLeft, "escape": input.KeyEscape, "enter": input.KeyEnter,
}

// markKey is the rating or flag a key gives, as marraw's keys do.
// burstKey is Shift and P, which picks the photo and rejects the rest of
// its burst, or Shift and X, which only rejects the rest.
func burstKey(e input.KeyPress) (gunim.Intent, bool) {
	if !e.Mods.Has(input.ModShift) || e.Mods.Has(input.ModControl) {
		return nil, false
	}
	switch e.Key {
	case input.KeyP:
		return BurstKeep{Pick: true}, true
	case input.KeyX:
		return BurstKeep{}, true
	}
	return nil, false
}

func markKey(k input.Key) (gunim.Intent, bool) {
	switch k {
	case input.Key0, input.Key1, input.Key2, input.Key3, input.Key4, input.Key5:
		return Rate{Stars: int(k - input.Key0)}, true
	case input.KeyP:
		return Mark{Flag: "pick"}, true
	case input.KeyX:
		return Mark{Flag: "exclude"}, true
	case input.KeyU:
		return Mark{Flag: "none"}, true
	}
	return nil, false
}

// writeShot writes what the window shows to a PNG file.
func writeShot(ctx context.Context, c gunim.Client, path string) error {
	img, err := c.Shot(ctx)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// fullSurface is gunim's surface with its views laid out to the window's
// edges, the title bar's band included: the views keep their own controls
// below the bar, as [gunim.Frame.Safe] says, and let the photo run under
// it. gunim's own keeps the views clear of the band, as for a phone's
// status bar.
type fullSurface struct{ widget.Surface }

func newFullSurface() *fullSurface { return &fullSurface{} }

// Layout implements [gunim.Node].
func (s *fullSurface) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	for kid := range kids.All {
		kid.Layout(gunim.Tight(c.Max))
		kid.Place(geom.Point{})
	}
	return c.Max
}
