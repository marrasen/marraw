package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// gridView is the library: the folder's photos as tiles, with how many are
// picked, rejected and rated above them. The tiles grow in one after
// another as the folder opens, and spring to new places as the window or
// Ctrl and the wheel change their size. Each tile's picture fades in as it
// arrives, its stars sweep to a new rating, its flag pops, and a rejected
// photo dims. Enter or a double click opens the cull view, the picture
// growing out of its tile.
type gridView struct {
	// errors are the errors not cleared yet, in the corner.
	errors *errorTray
	// crop says the tiles fill their cells, cropped, as the settings'
	// Crop framing has them, rather than fitting their pictures whole.
	crop   bool
	st     GridState
	grid   *widget.TileGrid
	head   *gridHead
	rail   *railView
	thumbs map[int]*paint.Image
	tiles  map[int]*photoTile
	box    geom.Size
	shown  bool
	// folder is the folder the tiles show, viewSeq the view of it, and
	// swapStop stops a swap to another under way.
	folder   int64
	viewSeq  int
	swapStop func()
	bar      *gridBar
	// notice is a note over the grid, noticeIn how far it has come in.
	notice    *widget.Label
	noticeIn  *anim.Float
	noticeSeq int
	noteRect  geom.Rect
	// empty says the filter shows none of the folder's photos, coming
	// in as noneIn does.
	empty  *widget.Label
	noneIn *anim.Float
	// top is the title bar's height, which the window's background runs
	// under and the rest keeps below.
	top float32
}

const (
	// gridHeadHeight is the band above the tiles.
	gridHeadHeight = 52
	// The tiles' widths, from Ctrl and the wheel.
	tileMin, tileMax = 110, 440
	// captionHeight is the band under a tile's picture, for its stars
	// and flags.
	captionHeight = 24
)

// cellSize is a tile's size for its width.
func cellSize(w float32) geom.Size { return geom.Sz(w, w*0.78+captionHeight) }

func newGridView(GridState) *gridView {
	v := &gridView{errors: newErrorTray(), thumbs: map[int]*paint.Image{}, tiles: map[int]*photoTile{}, head: newGridHead(), rail: newRailView(),
		notice: widget.NewLabel(""), noticeIn: anim.NewFloat(0),
		empty: widget.NewLabel("No photos match the filter"), noneIn: anim.NewFloat(0)}
	v.empty.Color = noteInk
	v.bar = newGridBar(v)
	v.notice.Size = noteSize
	v.grid = widget.NewTileGrid(cellSize(200))
	// A folder's tiles come and go at twice gunim's pace.
	v.grid.Pace = 2
	v.grid.Tile = v.newTile
	v.grid.Header = v.newGapHeader
	v.grid.HeaderHeight = gapHeaderHeight
	v.grid.OnView = func(first, count int, _ *gunim.UI) gunim.Intent { return NeedThumbs{First: first, Count: count} }
	v.grid.OnSelect = func(sel [][2]int, cursor int, _ *gunim.UI) gunim.Intent {
		n := 0
		for _, r := range sel {
			n += r[1] - r[0]
		}
		v.head.selected(n)
		return Selected{Runs: sel, Cursor: cursor}
	}
	v.grid.OnActivate = func(i int, _ *gunim.UI) gunim.Intent { return OpenCull{Index: i} }
	v.grid.OnZoom = func(notches float32, u *gunim.UI) gunim.Intent {
		w := v.grid.Size.W * float32(math.Pow(1.15, float64(notches)))
		v.grid.Size = cellSize(max(tileMin, min(w, tileMax)))
		v.bar.size.SetValue(v.grid.Size.W, u)
		u.Invalidate()
		return nil
	}
	return v
}

func (v *gridView) show(s GridState, u *gunim.UI) {
	lastGrid = s
	v.bar.set(s.View, u)
	v.bar.setOff(s.Off, u)
	v.setCrop(s.Crop, u)
	if v.shown && s.FolderID != v.folder {
		v.swapTo(s, u)
		return
	}
	if v.shown && s.ViewSeq != v.viewSeq && v.swapStop == nil {
		v.reorder(s, u)
		return
	}
	if v.shown && s.ViewSeq != v.viewSeq {
		// A swap under way takes the new view.
		v.swapTo(s, u)
		return
	}
	v.folder, v.viewSeq = s.FolderID, s.ViewSeq
	v.st = s
	v.grid.SetLen(len(s.Photos), u)
	v.grid.SetGroups(groupStarts(s.Groups), u)
	for i, t := range v.tiles {
		if i < len(s.Photos) {
			t.marks.set(s.Photos[i].Rating, s.Photos[i].Flag, u.Theme())
			t.setAids(s.Photos[i].Aids, u)
		}
	}
	v.head.set(s, v.shown, u.Theme())
	v.showNone(s, u)
	if !v.shown {
		// The folder opens: its tiles grow in, one after another.
		v.shown = true
		v.grid.Arrive(func(int) (geom.Rect, bool) { return geom.Rect{}, false }, u)
	}
}

// reorder shows the folder in a new view, as a sort or a filter changes:
// the tiles of photos that stay glide to their new places, those of photos
// gone fade out, and those of photos come fade in.
func (v *gridView) reorder(s GridState, u *gunim.UI) {
	was := make(map[int64]int, len(v.st.Photos))
	for i, p := range v.st.Photos {
		was[p.ID] = i
	}
	from := make([]int, len(s.Photos))
	tiles, thumbs := map[int]*photoTile{}, map[int]*paint.Image{}
	for j, p := range s.Photos {
		i, ok := was[p.ID]
		if !ok {
			from[j] = -1
			continue
		}
		from[j] = i
		if t := v.tiles[i]; t != nil {
			tiles[j] = t
			t.marks.set(p.Rating, p.Flag, u.Theme())
			t.setAids(p.Aids, u)
		}
		if img := v.thumbs[i]; img != nil {
			thumbs[j] = img
		}
	}
	v.tiles, v.thumbs = tiles, thumbs
	v.viewSeq, v.st = s.ViewSeq, s
	v.grid.Reorder(from, u)
	v.grid.SetGroups(groupStarts(s.Groups), u)
	v.head.set(s, true, u.Theme())
	v.head.selected(0)
	v.showNone(s, u)
}

// showNone brings the note in that the filter shows nothing, or out.
func (v *gridView) showNone(s GridState, u *gunim.UI) {
	none := s.FolderID != 0 && len(s.Photos) == 0 && s.Total > 0
	v.noneIn.Animate(on(none), widget.Settle.Get(u.Theme()))
	u.Invalidate()
}

// gridSel selects tiles, as a filter takes photos from among them.
func (v *gridView) gridSel(g GridSel, u *gunim.UI) {
	if g.Folder != v.folder {
		return
	}
	v.grid.SetSelected(g.Runs, g.Cursor, u)
	n := 0
	for _, r := range g.Runs {
		n += r[1] - r[0]
	}
	v.head.selected(n)
	u.Invalidate()
}

// swapDelay is how long the last folder's tiles have to leave before the
// next folder's come.
const swapDelay = 120 * time.Millisecond

// swapTo shows another folder: the tiles there are sink a little and fade,
// and the new folder's grow in, one after another, from the top.
func (v *gridView) swapTo(s GridState, u *gunim.UI) {
	if v.swapStop != nil {
		v.swapStop()
		v.swapStop = nil
	}
	if v.grid.Built() == 0 {
		v.enter(s, u)
		return
	}
	v.grid.Depart(func(i int) (geom.Rect, bool) {
		return scaleAbout(v.grid.TileRect(i), 0.86).Add(geom.Pt(0, 18)), true
	}, u)
	v.swapStop = u.After(swapDelay, func(u *gunim.UI) {
		v.swapStop = nil
		v.enter(s, u)
	})
}

// enter shows folder s from the top, its tiles growing in.
func (v *gridView) enter(s GridState, u *gunim.UI) {
	v.folder, v.viewSeq, v.st = s.FolderID, s.ViewSeq, s
	clear(v.thumbs)
	clear(v.tiles)
	v.grid.SetSelected(nil, -1, u)
	v.grid.SetLen(len(s.Photos), u)
	v.grid.SetGroups(groupStarts(s.Groups), u)
	v.grid.JumpTo(0)
	v.grid.Rebuild(u)
	v.head.set(s, false, u.Theme())
	v.head.selected(0)
	v.showNone(s, u)
	v.grid.Arrive(func(int) (geom.Rect, bool) { return geom.Rect{}, false }, u)
}

// railIn shows the library.
func (v *gridView) railIn(s RailState, u *gunim.UI) {
	lastRail = s
	v.rail.show(s, u)
}

// thumbIn takes a tile's small picture, or, nil, lets it go.
func (v *gridView) thumbIn(t ThumbIn, u *gunim.UI) {
	if t.Folder != v.folder {
		return
	}
	if t.Img == nil {
		// A tile built keeps what it shows; one built later asks again.
		delete(v.thumbs, t.Index)
		return
	}
	v.thumbs[t.Index] = t.Img
	if tl := v.tiles[t.Index]; tl != nil {
		tl.pic.set(t.Img)
	}
	u.Invalidate()
}

// photoAspect takes a photo's shape learned from its pixels; its tile
// glides to it.
func (v *gridView) photoAspect(a PhotoAspect, u *gunim.UI) {
	if a.Folder != v.folder || a.Index < 0 || a.Index >= len(v.st.Photos) {
		return
	}
	v.st.Photos[a.Index].Aspect = a.Aspect
	if t := v.tiles[a.Index]; t != nil {
		t.aspect.Animate(a.Aspect, widget.Quick.Get(u.Theme()))
	}
	u.Invalidate()
}

// photoMarked shows a photo's new rating and flag.
func (v *gridView) photoMarked(m PhotoMarked, u *gunim.UI) {
	if m.Folder != v.folder || m.Index < 0 || m.Index >= len(v.st.Photos) {
		return
	}
	p := &v.st.Photos[m.Index]
	p.Rating, p.Flag = m.Rating, m.Flag
	if t := v.tiles[m.Index]; t != nil {
		t.marks.set(m.Rating, m.Flag, u.Theme())
	}
	v.head.set(v.st, true, u.Theme())
	u.Invalidate()
}

// gridAt puts the keyboard on photo i and, unseen under the cull view,
// its tile in the middle of the view, for the photo to fly back to.
func (v *gridView) gridAt(a GridAt, u *gunim.UI) {
	if a.Folder != v.folder || a.Index < 0 || a.Index >= len(v.st.Photos) {
		return
	}
	v.grid.SetSelected([][2]int{{a.Index, a.Index + 1}}, a.Index, u)
	v.head.selected(1)
	if v.grid.Columns() > 0 {
		r := v.grid.TileRect(a.Index)
		room := v.box.H - gridTop - v.top
		if r.Min.Y < 0 || r.Max.Y > room {
			v.grid.JumpTo(r.Min.Y + v.grid.Offset() - (room-r.Size().H)/2)
		}
	}
	u.Invalidate()
}

func (v *gridView) newTile(i int) gunim.Node {
	p := v.st.Photos[i]
	t := &photoTile{id: p.ID, aspect: anim.NewFloat(p.Aspect), pic: newThumbPic(v.thumbs[i]), burst: newBadgeLabel(),
		badges: anim.NewFloat(0)}
	t.cropK = anim.NewFloat(on(v.crop))
	t.Add(t.aspect, t.badges, t.cropK)
	t.setAids(p.Aids, nil)
	t.hero = widget.NewHero(heroTag(p.ID), t.pic)
	// The tile is where the cull view's picture flies from and back to.
	t.hero.Anchor = true
	t.marks = newMarks(&t.Group, p.Rating, p.Flag)
	v.tiles[i] = t
	return t
}

// Children implements [gunim.Composite].
func (v *gridView) Children() []gunim.Node {
	return []gunim.Node{v.head, v.grid, v.rail, v.notice, v.bar, v.empty, v.errors}
}

// Step implements [gunim.Animator]: the note's coming and going.
func (v *gridView) Step(dt time.Duration) bool {
	a := v.noticeIn.Step(dt)
	b := v.noneIn.Step(dt)
	return a || b
}

// gridNotice pops a note in over the grid, and lets it fade after a
// moment.
func (v *gridView) gridNotice(n GridNotice, u *gunim.UI) {
	v.noticeSeq = n.Seq
	v.notice.Text = n.Text
	v.noticeIn.Jump(min(v.noticeIn.Value(), 0.6))
	v.noticeIn.Animate(1, widget.Bounce.Get(u.Theme()))
	u.After(noticeTime(n.Text), func(u *gunim.UI) {
		if v.noticeSeq == n.Seq {
			v.noticeIn.Animate(0, widget.Settle.Get(u.Theme()))
		}
	})
	u.Invalidate()
}

// Handle implements [gunim.Handler]: the keyboard goes on to the tiles,
// and the keys they leave rate and flag the photos selected.
func (v *gridView) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.FocusGained:
		u.After(0, func(u *gunim.UI) { u.Focus(v.grid) })
		return true
	case input.KeyPress:
		// Ctrl and C copies the edit of the photo the keyboard is on, and
		// Ctrl and V pastes it on the photos selected.
		if e.Mods.Has(input.ModControl) && !e.Mods.Has(input.ModAlt) {
			switch e.Key {
			case input.KeyC:
				u.Send(v, EditCopy{})
				return true
			case input.KeyV:
				u.Send(v, EditPaste{})
				return true
			case input.KeyZ:
				u.Send(v, DevUndo{Redo: e.Mods.Has(input.ModShift)})
				return true
			case input.KeyY:
				u.Send(v, DevUndo{Redo: true})
				return true
			case input.Key0:
				u.Send(v, DevReset{})
				return true
			case input.KeyE:
				u.Send(v, AskExport{})
				return true
			case input.KeyK:
				_, cursor := v.grid.Selected()
				openPalette(v, geom.Rc(railWidth, v.top, v.box.W-railWidth, v.box.H-v.top), u, paletteFor{cursor: cursor})
				return true
			}
			return false
		}
		if e.Key == input.KeyDelete {
			u.Send(v, AskDelete{})
			return true
		}
		if e.Mods.Has(input.ModAlt) {
			return false
		}
		if in, ok := burstKey(e); ok {
			u.Send(v, in)
			return true
		}
		if in, ok := markKey(e.Key); ok {
			u.Send(v, in)
			return true
		}
	case input.TextInput:
		// By what the keys type: ? is Shift and + on a Swedish keyboard.
		if e.Text == "?" {
			u.Send(v, ShowShortcuts{})
			return true
		}
	}
	return false
}

// Layout implements [gunim.Node].
func (v *gridView) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	v.box = box
	// The sidebar runs up under the title bar; the rest starts below it.
	top := f.Safe.Top
	v.top, v.rail.top = top, top
	head, grid, rail := kids.At(0), kids.At(1), kids.At(2)
	rail.Layout(gunim.Tight(geom.Sz(railWidth, box.H)))
	rail.Place(geom.Point{})
	w := max(0, box.W-railWidth)
	head.Layout(gunim.Tight(geom.Sz(w, gridHeadHeight)))
	head.Place(geom.Pt(railWidth, top))
	grid.Layout(gunim.Tight(geom.Sz(w, max(0, box.H-gridTop-top))))
	grid.Place(geom.Pt(railWidth, gridTop+top))
	bar := kids.At(4)
	bar.Layout(gunim.Tight(geom.Sz(w, barHeight)))
	bar.Place(geom.Pt(railWidth, gridHeadHeight+top))
	note := kids.At(3)
	ns := note.Layout(gunim.Loose(geom.Sz(w/2, 60)))
	v.noteRect = geom.Rc(railWidth+w/2-ns.W/2, gridTop+top+16, ns.W, ns.H)
	note.Place(v.noteRect.Min)
	empty := kids.At(5)
	es := empty.Layout(gunim.Loose(geom.Sz(w, 40)))
	empty.Place(geom.Pt(railWidth+w/2-es.W/2, gridTop+top+80))
	v.errors.corner = geom.Pt(box.W-16, box.H-16)
	kids.At(6).Layout(gunim.Tight(box))
	kids.At(6).Place(geom.Point{})
	return box
}

// errorsIn shows the errors not cleared yet.
func (v *gridView) errorsIn(e ErrorsIn, u *gunim.UI) { v.errors.set(e.List, u) }

// tasksIn shows the background tasks under way.
func (v *gridView) tasksIn(t TasksIn, u *gunim.UI) { v.errors.setTasks(t.List, u) }

// Paint implements [gunim.Node].
func (v *gridView) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(1).Paint(p)
	kids.At(0).Paint(p)
	p.RRect(geom.Rc(railWidth, v.top+gridHeadHeight-1, box.W-railWidth, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}))
	kids.At(4).Paint(p)
	kids.At(2).Paint(p)
	paintNote(p, kids.At(3), v.noteRect, v.noticeIn.Value())
	if k := v.noneIn.Value(); k > 0.01 {
		e := kids.At(5)
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: k})()
			defer p.Push(paint.Translate(geom.Pt(0, (1-k)*8)))()
			e.Paint(p)
		}()
	}
	kids.At(6).Paint(p)
}

// photoTile is one photo in the grid: its picture, fitted, as a hero, and
// its stars and flag beneath.
type photoTile struct {
	anim.Group
	// id is the photo's: a tile keeps to its photo as the view's order
	// changes.
	id  int64
	box geom.Size
	// aids are the photo's culling aids, shown as badges on its picture,
	// picRect, coming in as badges does, and burst says its place in its
	// burst.
	aids    Aids
	picRect geom.Rect
	badges  *anim.Float
	burst   *widget.Label
	// cropK is how far the picture fills its cell, cropped: the
	// settings' Crop framing.
	cropK *anim.Float
	// aspect is the picture's shape, gliding to the one its pixels have
	// once they come.
	aspect *anim.Float
	pic    *thumbPic
	hero   *widget.Hero
	marks  *marks
}

// Children implements [gunim.Composite].
func (t *photoTile) Children() []gunim.Node { return []gunim.Node{t.hero, t.burst} }

// picRoom is where a tile's picture fits, in a tile of size box.
func picRoom(box geom.Size) geom.Rect {
	return geom.Rc(7, 7, max(0, box.W-14), max(0, box.H-14-captionHeight))
}

// Layout implements [gunim.Node].
func (t *photoTile) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	t.box = box
	room := picRoom(box)
	r := fitIn(room, t.aspect.Value())
	if k := t.cropK.Value(); k > 0.001 {
		// Cropped, the picture grows to fill its cell.
		r = lerpRect(r, room, k)
	}
	t.pic.cover = t.cropK.Value() > 0.001
	t.picRect = r
	k := kids.At(0)
	k.Layout(gunim.Tight(r.Size()))
	k.Place(r.Min)
	b := kids.At(1)
	bs := b.Layout(gunim.Loose(geom.Sz(80, 20)))
	b.Place(geom.Pt(r.Min.X+5+badgeIcon+3, r.Min.Y+3+(badgeH-bs.H)/2))
	return box
}

// Paint implements [gunim.Node].
func (t *photoTile) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	t.pic.dim = t.marks.dim.Value()
	kids.At(0).Paint(p)
	t.marks.paint(p, f.Theme, tilePlace(box), 0x60, false)
	paintBadges(p, t.picRect, t.aids, t.badges.Value(), kids.At(1))
}

// setAids shows aids a as badges, popping them in as they change.
func (t *photoTile) setAids(a Aids, u *gunim.UI) {
	if a == t.aids && u != nil {
		return
	}
	t.aids = a
	t.burst.Text = ""
	if a.BurstOf > 0 {
		t.burst.Text = fmt.Sprintf("%d/%d", a.Burst, a.BurstOf)
	}
	if u == nil {
		t.badges.Jump(1)
		return
	}
	t.badges.Jump(0)
	t.badges.Animate(1, widget.Bounce.Get(u.Theme()))
}

// tilePlace is where a tile of size box has its marks: the stars at the
// left of the band under the picture, and the flags at the right.
func tilePlace(box geom.Size) markPlace {
	y := box.H - captionHeight/2 - 6
	return markPlace{stars: geom.Pt(10, y), star: 12, gap: 3, pick: geom.Pt(box.W-38, y), reject: geom.Pt(box.W-16, y), flag: 13}
}

// Handle implements [gunim.Handler]: the pointer over the marks shows what
// a click gives, and a click on a star rates the photo, again takes the
// rating off, and one on a flag sets it, again takes it off. The rest is
// the grid's.
func (t *photoTile) Handle(e input.Event, u *gunim.UI) bool {
	place := tilePlace(t.box)
	switch e := e.(type) {
	case input.PointerEnter:
		t.marks.hover(e.Pos, true, place, u.Theme())
		u.Invalidate()
	case input.PointerMove:
		t.marks.hover(e.Pos, true, place, u.Theme())
		u.Invalidate()
	case input.PointerLeave:
		t.marks.hover(geom.Point{}, false, place, u.Theme())
		u.Invalidate()
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || e.Mods != 0 {
			return false
		}
		if k := place.starAt(e.Pos); k > 0 {
			u.Send(t, Rate{Stars: k, ID: t.id, Toggle: true})
			return true
		}
		if f := place.flagAt(e.Pos); f != "" {
			u.Send(t, Mark{Flag: f, ID: t.id})
			return true
		}
	}
	return false
}

// thumbPic is a tile's picture: its frame until the small picture comes,
// which fades and settles in.
type thumbPic struct {
	anim.Group
	img, old *paint.Image
	in       *anim.Float
	// dim is how far the picture is dimmed, for a rejected photo, and
	// cover says it fills its box, cropped, rather than fits.
	dim   float32
	cover bool
}

func newThumbPic(img *paint.Image) *thumbPic {
	q := &thumbPic{img: img, in: anim.NewFloat(1)}
	q.Add(q.in)
	return q
}

// set shows img, fading in over what showed.
func (q *thumbPic) set(img *paint.Image) {
	if img == q.img {
		return
	}
	q.old, q.img = q.img, img
	q.in.Jump(0)
	q.in.Animate(1, anim.Tween{Duration: 200 * time.Millisecond})
}

// Layout implements [gunim.Node].
func (q *thumbPic) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node].
func (q *thumbPic) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	op := 1 - 0.6*q.dim
	k := q.in.Value()
	place := func(img *paint.Image) (geom.Rect, paint.ImageOpts) {
		if q.cover {
			return r, paint.ImageOpts{Src: coverSrc(img, r), Radius: 3}
		}
		return pixelFit(r, img), paint.ImageOpts{Radius: 3}
	}
	if q.old != nil && k < 1 {
		at, o := place(q.old)
		o.Opacity = op
		p.Image(q.old, at, o)
	} else if q.img == nil || k < 1 {
		p.RRect(r, 3, paint.Solid(frameInk))
	}
	if q.img != nil {
		// It settles from a touch larger as it fades in.
		at, o := place(q.img)
		o.Opacity = op * k
		p.Image(q.img, scaleAbout(at, 1+0.04*(1-k)), o)
	}
}

// pixelFit is img fitted into r by the shape of its own pixels, so a
// frame of another shape never stretches it.
func pixelFit(r geom.Rect, img *paint.Image) geom.Rect {
	w, h := img.Size()
	if w <= 0 || h <= 0 {
		return r
	}
	return fitIn(r, float32(w)/float32(h))
}

// gridHead is the band above the tiles: the folder's name, how many
// photos it holds or are selected, and the counts of picks, rejects and
// rated photos, each popping as it changes.
type gridHead struct {
	title, sub *widget.Label
	pills      [3]*countPill
	total, sel int
	// none says no folder is chosen yet, and folderTotal is how many
	// photos the folder holds, filtered or not.
	none        bool
	folderTotal int
	// size is the tiles' size slider, before the counts, and gap the
	// time between groups, before it.
	size gunim.Node
	gap  gunim.Node
}

var (
	headTitleSize = theme.Length("marraw.head.size", 17)
	ratedInk      = starInk
)

func newGridHead() *gridHead {
	h := &gridHead{title: widget.NewLabel(""), sub: widget.NewLabel("")}
	h.title.Size = headTitleSize
	h.sub.Color, h.sub.Size = noteInk, noteSize
	h.pills = [3]*countPill{newCountPill(pickInk, flagSet, "picked"), newCountPill(rejectInk, rejectMark, "rejected"),
		newCountPill(ratedInk, starLit, "rated")}
	return h
}

// set shows s's counts, popping those that changed when animate is set.
func (h *gridHead) set(s GridState, animate bool, th *theme.Live) {
	h.title.Text = s.Folder
	h.none, h.folderTotal = s.FolderID == 0, s.Total
	if h.none {
		h.title.Text = "Choose a shoot in the library"
	}
	h.total = len(s.Photos)
	h.subText()
	var picks, rejects, rated int
	for _, p := range s.Photos {
		switch p.Flag {
		case "pick":
			picks++
		case "exclude":
			rejects++
		}
		if p.Rating > 0 {
			rated++
		}
	}
	for i, n := range []int{picks, rejects, rated} {
		h.pills[i].set(n, animate, th)
	}
}

// selected shows how many photos are selected.
func (h *gridHead) selected(n int) {
	h.sel = n
	h.subText()
}

func (h *gridHead) subText() {
	if h.none {
		h.sub.Text = ""
		return
	}
	s := fmt.Sprintf("%d photos", h.total)
	if h.folderTotal > h.total {
		s = fmt.Sprintf("%d of %d photos", h.total, h.folderTotal)
	}
	if h.sel > 1 {
		s += fmt.Sprintf(" · %d selected", h.sel)
	}
	h.sub.Text = s
}

// Children implements [gunim.Composite].
func (h *gridHead) Children() []gunim.Node {
	return []gunim.Node{h.title, h.sub, h.pills[0], h.pills[1], h.pills[2], h.size, h.gap}
}

// Layout implements [gunim.Node]: the name and the count on the left, the
// pills on the right, all on the band's middle line.
func (h *gridHead) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	mid := box.H / 2
	x := float32(20)
	for i := range 2 {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(box.W/3, box.H)))
		k.Place(geom.Pt(x, mid-s.H/2))
		x += s.W + 14
	}
	right := box.W - 16
	for i := 4; i >= 2; i-- {
		k := kids.At(i)
		s := k.Layout(gunim.Loose(geom.Sz(box.W/4, box.H)))
		right -= s.W
		k.Place(geom.Pt(right, mid-s.H/2))
		right -= 8
	}
	sz := kids.At(5)
	ss := sz.Layout(gunim.Loose(geom.Sz(box.W/4, box.H)))
	sz.Place(geom.Pt(right-16-ss.W, mid-ss.H/2))
	gp := kids.At(6)
	gs := gp.Layout(gunim.Loose(geom.Sz(220, box.H)))
	gp.Place(geom.Pt(right-16-ss.W-16-gs.W, mid-gs.H/2))
	return box
}

// Paint implements [gunim.Node].
func (h *gridHead) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// countPill is a count with its mark's icon, such as the picks' flag: it
// pops as the count changes, and fades back while it is nought.
type countPill struct {
	anim.Group
	label   *widget.Label
	what    string
	n       int
	pop, on *anim.Float
	ink     color.NRGBA
	icon    *icon.Icon
}

func newCountPill(ink color.NRGBA, ic *icon.Icon, what string) *countPill {
	c := &countPill{label: widget.NewLabel("0 " + what), what: what, ink: ink, icon: ic, pop: anim.NewFloat(1), on: anim.NewFloat(0.4)}
	c.label.Size = noteSize
	c.Add(c.pop, c.on)
	return c
}

func (c *countPill) set(n int, animate bool, th *theme.Live) {
	on := map[bool]float32{false: 0.4, true: 1}[n > 0]
	if !animate {
		c.n = n
		c.label.Text = fmt.Sprintf("%d %s", n, c.what)
		c.on.Jump(on)
		return
	}
	if n == c.n {
		return
	}
	c.n = n
	c.label.Text = fmt.Sprintf("%d %s", n, c.what)
	c.pop.Jump(1.18)
	c.pop.Animate(1, widget.Bounce.Get(th))
	c.on.Animate(on, widget.Quick.Get(th))
}

// Children implements [gunim.Composite].
func (c *countPill) Children() []gunim.Node { return []gunim.Node{c.label} }

// Layout implements [gunim.Node].
func (c *countPill) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Loose(cs.Max))
	h := max(s.H+10, 26)
	k.Place(geom.Pt(26, (h-s.H)/2))
	return geom.Sz(26+s.W+12, h)
}

// Paint implements [gunim.Node].
func (c *countPill) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	defer p.Push(paint.Scale(c.pop.Value(), r.Center()))()
	if on := c.on.Value(); on < 0.999 {
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-4)), Opacity: on})()
	}
	p.RRect(r, box.H/2, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}))
	w := map[bool]float32{false: 2, true: 3}[c.icon == rejectMark]
	p.Mask(icon.Stroke{Icon: c.icon, Width: w, Progress: 1}, geom.Rc(9, box.H/2-6, 12, 12), c.ink)
	kids.At(0).Paint(p)
}

// setCrop has the tiles fill their cells, cropped, or fit their pictures
// whole, gliding between the two.
func (v *gridView) setCrop(crop bool, u *gunim.UI) {
	if crop == v.crop {
		return
	}
	v.crop = crop
	for _, t := range v.tiles {
		if t != nil {
			t.cropK.Animate(on(crop), widget.Settle.Get(u.Theme()))
		}
	}
	u.Invalidate()
}

// lerpRect is the rect t of the way from a to b.
func lerpRect(a, b geom.Rect, t float32) geom.Rect {
	l := func(x, y float32) float32 { return x + (y-x)*t }
	return geom.Rect{Min: geom.Pt(l(a.Min.X, b.Min.X), l(a.Min.Y, b.Min.Y)), Max: geom.Pt(l(a.Max.X, b.Max.X), l(a.Max.Y, b.Max.Y))}
}
