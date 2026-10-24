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
	// sel takes the bar's place while selN, more than one, are selected,
	// coming in as selIn does; batch is the selection's card, in the info
	// panel aside, which is out as asideIn says while asideOn.
	sel                         *selBar
	barGate, selGate, asideGate *gate
	batch                       *batchCard
	aside                       *libAside
	selN                        int
	selKey                      string
	selIn, asideIn              *anim.Float
	asideOn                     bool
	// status is the line at the foot, and band the top band's handle that
	// moves the window, off while the cull view covers the grid.
	status *statusBar
	band   *dragBand
	// rw carries the library's edge as it is put away and brought back,
	// to rwTo.
	rw   *anim.Float
	rwTo float32
	// dim fades the tiles while another shoot opens.
	dim *anim.Float
	// ov is the overview of a folder, over the photos while ovOn, coming
	// in as ovIn does.
	ov   *overviewView
	ovIn *anim.Float
	ovOn bool
	// lobby is the page with no shoot open, coming in as lobbyIn does.
	lobby   *lobby
	lobbyIn *anim.Float
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
	v.bar.lead.views = v.head.viewsNode
	v.selIn, v.asideIn, v.rw, v.rwTo = anim.NewFloat(0), anim.NewFloat(1), anim.NewFloat(railWidth), railWidth
	v.asideOn = true
	v.dim = anim.NewFloat(1)
	v.ov, v.ovIn = newOverviewView(v), anim.NewFloat(0)
	v.lobby, v.lobbyIn = newLobby(), anim.NewFloat(0)
	v.sel = newSelBar(func(u *gunim.UI) { u.Send(v, AsideToggle{}) })
	v.batch = newBatchCard(func(u *gunim.UI) { u.Send(v, AsideToggle{}) })
	v.aside = newLibAside(v.batch)
	v.status = newStatusBar()
	v.band = &dragBand{}
	v.head.pills = v.status.pills
	v.head.jump.act = func(u *gunim.UI) {
		_, cursor := v.grid.Selected()
		openPalette(v, geom.Rc(v.railX(), v.top, v.box.W-v.railX(), v.box.H-v.top), u, paletteFor{cursor: cursor})
	}
	v.barGate, v.selGate, v.asideGate = &gate{child: v.bar, open: true}, &gate{child: v.sel}, &gate{child: v.aside, open: true}
	v.notice.Size = noteSize
	v.grid = widget.NewTileGrid(cellSize(200))
	// A folder's tiles come and go at twice gunim's pace.
	v.grid.Pace = 2
	v.grid.Tile = v.newTile
	v.grid.Header = v.newGapHeader
	v.grid.HeaderHeight = gapHeaderHeight
	v.grid.OnView = func(first, count int, _ *gunim.UI) gunim.Intent { return NeedThumbs{First: first, Count: count} }
	v.grid.OnSelect = func(sel [][2]int, cursor int, u *gunim.UI) gunim.Intent {
		n := 0
		for _, r := range sel {
			n += r[1] - r[0]
		}
		v.head.selected(n)
		v.selCount(n, fmt.Sprint(sel), u)
		return Selected{Runs: sel, Cursor: cursor}
	}
	v.grid.OnActivate = func(i int, _ *gunim.UI) gunim.Intent { return OpenCull{Index: i} }
	v.grid.OnZoom = func(notches float32, u *gunim.UI) gunim.Intent {
		w := v.grid.Size.W * float32(math.Pow(1.15, float64(notches)))
		v.grid.Size = cellSize(max(tileMin, min(w, tileMax)))
		v.bar.size.SetValue(v.grid.Size.W, u)
		v.bar.showSize(v.grid.Size.W)
		u.Invalidate()
		return nil
	}
	return v
}

func (v *gridView) show(s GridState, u *gunim.UI) {
	lastOf(u).grid = s
	v.bar.gapBox.hidden = s.Set
	v.head.showSet(s.Set, s.Shoots, u)
	v.bar.set(s.View, u)
	v.bar.setOff(s.Off, u)
	v.setCrop(s.Crop || s.Natural, u)
	v.setNatural(s.Natural, u)
	v.batch.set(max(v.selN, 0), s.Presets, u)
	v.setAside(s.Aside, u)
	v.status.set(s.Path, len(s.Photos), s.Total, v.selN)
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
	v.head.sheet = s.Sheet
	v.head.set(s, v.shown, u.Theme())
	if s.Sheet {
		v.head.modes.show(modeCull, u)
	} else if !v.band.off {
		v.head.modes.show(modeLibrary, u)
	}
	v.showNone(s, u)
	v.showLobby(u)
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
	// A folder's photos as one show only the shoots read already.
	unread := s.Set && s.Total == 0
	// The words change only as the note comes: going, it keeps those it
	// came with.
	switch {
	case !none && !unread:
	case !unread:
		v.empty.Text = "No photos match the filter"
	case s.Reading:
		v.empty.Text = "Reading these shoots… their photos show here as each is read."
	case s.Unread > 0:
		v.empty.Text = "marraw has not read these shoots yet. Open one to see its photos here."
	default:
		v.empty.Text = "There are no photos in these shoots."
	}
	none = none || unread
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
	v.selCount(n, fmt.Sprint(g.Runs), u)
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
	lastOf(u).rail = s
	v.rail.show(s, u)
	// A shoot on its way: its name heads the grid at once, and the photos
	// of the one leaving dim until the new ones come.
	if s.Opening != v.head.opening {
		v.head.opening = s.Opening
		v.head.subText()
		v.head.title.Text = v.st.Folder
		if s.Opening != "" {
			v.head.title.Text = baseName(s.Opening)
		}
		v.dim.Animate(map[bool]float32{false: 1, true: 0.35}[s.Opening != ""], widget.Quick.Get(u.Theme()))
	}
	v.bar.setRail(s.Hidden, u)
	v.showLobby(u)
	u.Invalidate()
}

// showLobby brings the page in with no shoot open, nor one opening, nor
// a folder's overview up, once the sidebar knows the library: how to
// start where the library is empty, where to go next where it is not.
func (v *gridView) showLobby(u *gunim.UI) {
	show := v.st.FolderID == 0 && v.head.opening == "" && !v.ovOn && v.rail.st.Loaded
	if show {
		v.lobby.set(v.rail.st.Folders == 0)
	}
	v.lobbyIn.Animate(on(show), widget.Settle.Get(u.Theme()))
	u.Invalidate()
}

// coverIn takes a shoot's cover's picture for the sidebar.
func (v *gridView) coverIn(c CoverIn, u *gunim.UI) {
	v.rail.covers[c.Key] = c.Img
	u.Invalidate()
}

// railX is where the grid starts: the library's edge, as it slides.
func (v *gridView) railX() float32 { return v.rw.Value() }

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
	if v.grid.Aspect != nil {
		// Its shape is its tile's: the rows find their places anew.
		v.grid.Reshape(u)
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
	// The cards come last, over the info panel and the rest, so a click
	// on one reaches it wherever it lies.
	return []gunim.Node{v.head, v.grid, v.rail, v.notice, v.barGate, v.empty, v.selGate, v.asideGate, v.status, v.band, v.ov, v.errors, v.bar.lead, v.lobby}
}

// asideIn shows the info panel, or puts it away.
func (v *gridView) setAside(on bool, u *gunim.UI) {
	if on == v.asideOn {
		return
	}
	v.asideOn = on
	v.asideGate.open = on
	v.asideIn.Animate(map[bool]float32{false: 0, true: 1}[on], widget.Quick.Get(u.Theme()))
	v.bar.setAside(on, u)
	v.sel.set(v.selN, on, u)
	u.Invalidate()
}

// colourIn takes how the window's colours reach the display.
func (v *gridView) colourIn(c ColourIn, u *gunim.UI) {
	v.status.colour.Text = c.Short
	u.Invalidate()
}

// asidePatch takes the info panel shown or put away.
func (v *gridView) asidePatch(a AsideIn, u *gunim.UI) { v.setAside(a.On, u) }

// libInfoIn takes the photo the info panel shows.
func (v *gridView) libInfoIn(l LibInfo, u *gunim.UI) {
	if l.Folder != v.folder {
		return
	}
	v.aside.has = l.ID != 0
	if l.ID != 0 {
		v.aside.info.show(l, u)
	}
	u.Invalidate()
}

// overviewIn shows a folder's overview over the photos, or puts it away,
// leaving the shoot's head a way back to it where one is named.
func (v *gridView) overviewIn(in OverviewIn, u *gunim.UI) {
	v.ov.show(in, u)
	if in.On != v.ovOn {
		v.ovOn = in.On
		v.showLobby(u)
		v.ovIn.Animate(map[bool]float32{false: 0, true: 1}[in.On], widget.Quick.Get(u.Theme()))
		focus := gunim.Node(v.grid)
		if in.On {
			focus = v.ov.grid
		}
		u.After(0, func(u *gunim.UI) { u.Focus(focus) })
	}
	v.head.overview(in, v.st)
	u.Invalidate()
}

// bigCoverIn takes a large cover for the overview's cards, or, nil, lets
// one go.
func (v *gridView) bigCoverIn(c BigCoverIn, u *gunim.UI) {
	if c.Img == nil {
		delete(v.ov.big, c.Key)
	} else {
		v.ov.big[c.Key] = c.Img
	}
	u.Invalidate()
}

// viewerOpenIn lights the pop-out button while the photo's window is open.
func (v *gridView) viewerOpenIn(o ViewerOpen, u *gunim.UI) {
	showPop(v.head.pop, o.On)
	u.Invalidate()
}

// libMarksIn takes a photo's rating and flag, changed: the info panel
// shows them at once where it shows that photo.
func (v *gridView) libMarksIn(m LibMarks, u *gunim.UI) {
	if v.aside.has && v.aside.info.st.ID == m.ID {
		v.aside.info.showMarks(m.Rating, m.Flag, false, u)
	}
}

// coveredIn says whether the cull view covers the grid: the band then
// moves no window.
func (v *gridView) coveredIn(c Covered, u *gunim.UI) {
	v.band.off = c.On
	if !c.On {
		// Back in the library: its switch stands on Library again, or
		// on Cull for its contact sheet.
		v.head.modes.show(map[bool]int{false: modeLibrary, true: modeCull}[v.st.Sheet], u)
	}
	u.Invalidate()
}

// selCount shows n photos selected: with more than one, the selection's
// bar in the bar's place, and its card unless the user hid it.
func (v *gridView) selCount(n int, sel string, u *gunim.UI) {
	many := n > 1
	if sel != v.selKey && many {
		// Another selection: the card's changes start from nought.
		v.batch.fresh(u)
	}
	v.selKey = sel
	v.selN = n
	v.barGate.open, v.selGate.open = !many, many
	v.aside.many = many
	v.sel.set(n, v.asideOn, u)
	v.batch.set(n, v.st.Presets, u)
	v.status.set(v.st.Path, len(v.st.Photos), v.st.Total, n)
	th := u.Theme()
	v.selIn.Animate(map[bool]float32{false: 0, true: 1}[many], widget.Quick.Get(th))
	u.Invalidate()
}

// Step implements [gunim.Animator]: the note's coming and going.
func (v *gridView) Step(dt time.Duration) bool {
	a := v.noticeIn.Step(dt)
	b := v.noneIn.Step(dt)
	c := v.selIn.Step(dt)
	d := v.asideIn.Step(dt)
	e := v.rw.Step(dt)
	g := v.dim.Step(dt)
	h := v.ovIn.Step(dt)
	i := v.lobbyIn.Step(dt)
	return a || b || c || d || e || g || h || i
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
	case input.PointerDown:
		// A press on nothing that takes the keyboard, as the sidebar's
		// empty space, leaves the keyboard with the grid, so its keys,
		// as Ctrl+K, go on working.
		u.After(0, func(u *gunim.UI) {
			if f := u.Focused(); f == nil || f == gunim.Node(v) {
				u.Focus(v.grid)
			}
		})
		return false
	case input.DragOver:
		// A folder dropped anywhere in the window joins the library, as
		// on the sidebar.
		_, ok := e.Data.(input.Files)
		return ok
	case input.Drop:
		if len(e.Paths) > 0 {
			u.Send(v, AddFolders{Paths: e.Paths})
			return true
		}
	case input.HistoryStep:
		// The mouse's back button goes back to the overview the shoot
		// was opened from, as the Back button does.
		return !e.Forward && v.head.goBack(u)
	case input.KeyPress:
		// Backspace goes back too, where nothing typed takes it.
		if e.Key == input.KeyBackspace && e.Mods == 0 && v.head.goBack(u) {
			return true
		}
		// Ctrl and C copies the edit of the photo the keyboard is on, and
		// Ctrl and V pastes it on the photos selected.
		if e.Mods.Has(input.ModControl) && !e.Mods.Has(input.ModAlt) {
			switch e.Key {
			case input.KeyC:
				if e.Mods.Has(input.ModShift) {
					u.Send(v, CopyImage{})
				} else {
					u.Send(v, EditCopy{})
				}
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
			case input.KeyN:
				if e.Mods.Has(input.ModShift) {
					// Ctrl+Shift+N: another window, on the library.
					u.Send(v, NewWindow{})
				} else {
					u.Send(v, ToggleViewer{})
				}
				return true
			case input.KeyK:
				_, cursor := v.grid.Selected()
				openPalette(v, geom.Rc(v.railX(), v.top, v.box.W-v.railX(), v.box.H-v.top), u, paletteFor{cursor: cursor})
				return true
			case input.KeyU:
				// Auto tone on the photos selected; with Shift white
				// balance and colour; with Alt everything.
				secs := []string{"tone"}
				switch {
				case e.Mods.Has(input.ModAlt):
					secs = []string{"all"}
				case e.Mods.Has(input.ModShift):
					secs = []string{"wb", "color"}
				}
				u.Send(v, BatchAuto{Sections: secs})
				return true
			}
			// Ctrl and 1 to 9 lay an auto preset over the photos
			// selected, with Shift one of my presets.
			if e.Key >= input.Key1 && e.Key <= input.Key9 && !e.Mods.Has(input.ModAlt) {
				u.Send(v, BatchPreset{Auto: !e.Mods.Has(input.ModShift), Index: int(e.Key - input.Key1)})
				return true
			}
			return false
		}
		if e.Key == input.KeyF11 {
			u.SetFullScreen(!u.FullScreen())
			return true
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
		// R crops and Q heals the photo in hand, in Develop.
		if (e.Key == input.KeyR || e.Key == input.KeyQ) && e.Mods == 0 {
			tool := map[bool]string{true: "crop", false: "heal"}[e.Key == input.KeyR]
			u.Send(v, DevelopTool{Tool: tool})
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
	to := v.rail.width
	if v.rail.st.Hidden {
		to = 0
	}
	switch {
	case v.rail.dragging:
		v.rw.Jump(to)
		v.rwTo = to
	case to != v.rwTo:
		v.rwTo = to
		v.rw.Animate(to, widget.Quick.Get(f.Theme))
	}
	railWidth := v.railX()
	rail.Layout(gunim.Tight(geom.Sz(v.rail.width, box.H)))
	rail.Place(geom.Pt(railWidth-v.rail.width, 0))
	w := max(0, box.W-railWidth)
	v.head.mid = box.W/2 - railWidth
	head.Layout(gunim.Tight(geom.Sz(w, gridHeadHeight)))
	head.Place(geom.Pt(railWidth, top))
	// The info panel takes its width from the tiles as it comes.
	aw := asideW * min(max(v.asideIn.Value(), 0), 1)
	gw := max(0, w-aw)
	grid.Layout(gunim.Tight(geom.Sz(gw, max(0, box.H-gridTop-top-statusH))))
	grid.Place(geom.Pt(railWidth, gridTop+top))
	// The bar's lead first, as the bar and the overview leave it room; it
	// sits where the bar's row starts.
	v.bar.lead.on = v.head.inFolder
	lead := kids.At(12)
	ls := lead.Layout(gunim.Loose(geom.Sz(w, barHeight)))
	lead.Place(geom.Pt(railWidth+20, gridHeadHeight+top+(barHeight-ls.H)/2))
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
	kids.At(11).Layout(gunim.Tight(box))
	kids.At(11).Place(geom.Point{})
	lob := kids.At(13)
	gh := max(0, box.H-gridTop-top-statusH)
	lbs := lob.Layout(gunim.Loose(geom.Sz(gw, gh)))
	if v.lobbyIn.Value() > 0.01 {
		lob.Place(geom.Pt(railWidth+gw/2-lbs.W/2, gridTop+top+max(0, (gh-lbs.H)/2-40)))
	} else {
		// Out of sight, out of reach of the pointer too.
		lob.Place(geom.Pt(-10000, 0))
	}
	sel := kids.At(6)
	sel.Layout(gunim.Tight(geom.Sz(w, barHeight)))
	sel.Place(geom.Pt(railWidth, gridHeadHeight+top))
	aside := kids.At(7)
	aside.Layout(gunim.Tight(geom.Sz(asideW, max(0, box.H-gridTop-top-statusH))))
	aside.Place(geom.Pt(box.W-aw, gridTop+top))
	// The overview over the filter bar and the photos, while it shows.
	ovk := kids.At(10)
	if v.ovOn || v.ovIn.Value() > 0.001 {
		// Its bar across the window, as the filter bar it covers; its
		// cards where the photos are, beside the info panel.
		v.ov.gridW = gw
		ovk.Layout(gunim.Tight(geom.Sz(w, max(0, box.H-gridHeadHeight-top-statusH))))
		ovk.Place(geom.Pt(railWidth, gridHeadHeight+top))
	} else {
		ovk.Layout(gunim.Tight(geom.Size{}))
		ovk.Place(geom.Pt(-10000, 0))
	}
	st := kids.At(8)
	st.Layout(gunim.Tight(geom.Sz(w, statusH)))
	st.Place(geom.Pt(railWidth, box.H-statusH))
	// The cards in the window's lower right corner, as marraw's toasts,
	// over the info panel where it shows.
	v.errors.corner = geom.Pt(box.W-16, box.H-statusH-16)
	// The band along the top moves the window, but where its controls
	// are: the switcher, the jump, the window's buttons, and the library's
	// own column.
	v.band.holes = v.band.holes[:0]
	v.band.holes = append(v.band.holes, geom.Rc(0, 0, railWidth, gridHeadHeight))
	for _, h := range v.head.holes {
		v.band.holes = append(v.band.holes, h.Add(geom.Pt(railWidth, 0)))
	}
	band := kids.At(9)
	band.Layout(gunim.Tight(geom.Sz(box.W, gridHeadHeight)))
	band.Place(geom.Pt(0, top))
	return box
}

// errorsIn shows the errors not cleared yet.
func (v *gridView) errorsIn(e ErrorsIn, u *gunim.UI) { v.errors.set(e.List, u) }

// tasksIn shows the background tasks under way.
func (v *gridView) tasksIn(t TasksIn, u *gunim.UI) { v.errors.setTasks(t.List, u) }

// Paint implements [gunim.Node].
func (v *gridView) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	if k := v.dim.Value(); k < 0.999 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: max(k, 0)})()
			kids.At(1).Paint(p)
		}()
	} else {
		kids.At(1).Paint(p)
	}
	kids.At(0).Paint(p)
	p.RRect(geom.Rc(v.railX(), v.top+gridHeadHeight-1, box.W-v.railX(), 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}))
	if k := v.selIn.Value(); k < 0.99 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(max(1-k, 0), 1)})()
			defer p.Push(paint.Translate(geom.Pt(0, -6*k)))()
			kids.At(4).Paint(p)
		}()
	}
	if k := v.selIn.Value(); k > 0.01 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(max(k, 0), 1)})()
			defer p.Push(paint.Translate(geom.Pt(0, 6*(1-k))))()
			kids.At(6).Paint(p)
		}()
	}
	if k := v.ovIn.Value(); k > 0.01 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(k, 1)})()
			kids.At(10).Paint(p)
		}()
	}
	// The bar's lead over the bar and the overview, going as the bar does
	// for the selection's.
	if k := v.selIn.Value(); k < 0.99 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(max(1-k, 0), 1)})()
			defer p.Push(paint.Translate(geom.Pt(0, -6*k)))()
			kids.At(12).Paint(p)
		}()
	}
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
	if k := v.lobbyIn.Value(); k > 0.01 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(k, 1)})()
			defer p.Push(paint.Translate(geom.Pt(0, (1-k)*8)))()
			kids.At(13).Paint(p)
		}()
	}
	if v.asideIn.Value() > 0.01 {
		kids.At(7).Paint(p)
	}
	kids.At(8).Paint(p)
	kids.At(9).Paint(p)
	kids.At(11).Paint(p)
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
	// A narrow tile, as a portrait in Natural rows, gets smaller stars,
	// so they keep clear of the flags at its right.
	star, gap := float32(12), float32(3)
	if room := box.W - 10 - 48; room < 5*star+4*gap {
		gap = 2
		star = max(7, (room-4*gap)/5)
	}
	return markPlace{stars: geom.Pt(10, y), star: star, gap: gap, pick: geom.Pt(box.W-38, y), reject: geom.Pt(box.W-16, y), flag: 13}
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

// gridHead is the band along the top, as marraw's top bar: the folder's
// name and how many photos it holds or are selected at the left, the mode
// switcher in the window's middle, and Jump to anything and the window's
// buttons at the right. The counts of picks, rejects and rated photos
// are the status bar's, each popping as it changes.
type gridHead struct {
	title, sub *widget.Label
	// sheet says the grid shows as Cull's contact sheet.
	sheet bool
	// subFull is the count line in full, subShort its photos alone, for
	// a heading too narrow for the rest; both empty for other lines.
	subFull, subShort string
	// noSub leaves the count out where the name needs the room.
	noSub      bool
	pills      [3]*countPill
	total, sel int
	// none says no folder is chosen yet, and folderTotal is how many
	// photos the folder holds, filtered or not; opening is the folder on
	// its way, or "".
	none        bool
	folderTotal int
	opening     string
	// modes is the switcher, jump the palette's button, and win the
	// window's buttons. mid is the window's middle in the band's space,
	// and holes where the band takes presses itself.
	modes *modeSwitch
	jump  *jumpButton
	win   gunim.Node
	pop   *widget.IconButton
	mid   float32
	holes []geom.Rect
	// over and overSub are the overview's name and count while it
	// shows; back is the way back to the overview the shoot was opened
	// from, backKey that overview's row, or "" for none.
	over, overSub string
	back          *widget.Button
	backNode      gunim.Node
	backKey       string
	// views switches a folder between its photos and its shoots; it shows
	// while a folder's photos or its overview do, in the bar's lead (the
	// grid view places it, as viewsNode). shoots is how many
	// shoots a folder's photos are of, nought for one shoot.
	views     *widget.Segmented
	viewsNode gunim.Node
	inFolder  bool
	shoots    int
}

var (
	// The shoot's name in the title bar: a step over the sidebar's names,
	// and a soft white, so it labels the view rather than shouts.
	headTitleSize = theme.Length("marraw.head.size", 14)
	headTitleInk  = theme.Color("marraw.head.ink", color.NRGBA{R: 0xd2, G: 0xd6, B: 0xde, A: 0xff})
	ratedInk      = starInk
)

func newGridHead() *gridHead {
	h := &gridHead{title: widget.NewLabel(""), sub: widget.NewLabel(""), modes: newModeSwitch(modeLibrary)}
	h.win, h.pop = newWindowPill()
	h.jump = newJumpButton("Jump to anything")
	h.back = widget.NewButton("")
	h.back.Icon, h.back.KeepFocus = icon.ChevronLeft, true
	h.back.Tooltip = "Back to the folder's shoots"
	h.back.OnClick = func(*gunim.UI) gunim.Intent { return OpenOverview{Key: h.backKey} }
	h.backNode = glassButton(h.back)
	h.views = widget.NewSegmented("Photos", "Shoots")
	h.views.KeepFocus = true
	h.views.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return FolderView{Photos: i == 0} }
	h.viewsNode = frosted(h.views, marrawTheme().With(theme.Set(widget.SegmentedHeight, 28)))
	h.title.Size, h.title.Color = headTitleSize, headTitleInk
	// A long shoot name ends in an ellipsis on its one line, and the
	// count beside it stays whole.
	h.title.MaxLines = 1
	h.sub.Color, h.sub.Size = noteInk, noteSize
	h.sub.NoWrap, h.sub.MaxLines = true, 1
	return h
}

// set shows s's counts, popping those that changed when animate is set.
func (h *gridHead) set(s GridState, animate bool, th *theme.Live) {
	h.title.Text = s.Folder
	if h.opening != "" {
		h.title.Text = baseName(h.opening)
	}
	h.none, h.folderTotal = s.FolderID == 0, s.Total
	if h.none {
		h.title.Text = "No shoot open"
	}
	if h.over != "" {
		h.title.Text = h.over
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
		if h.pills[i] != nil {
			h.pills[i].set(n, animate, th)
		}
	}
}

// selected shows how many photos are selected.
func (h *gridHead) selected(n int) {
	h.sel = n
	h.subText()
}

func (h *gridHead) subText() {
	h.subFull, h.subShort = "", ""
	if h.sheet && !h.none && h.opening == "" {
		h.sub.Text = fmt.Sprintf("Contact sheet · %d %s · Esc for the photo", h.total, map[bool]string{false: "photos", true: "photo"}[h.total == 1])
		return
	}
	if h.over != "" {
		h.sub.Text = h.overSub
		return
	}
	if h.opening != "" {
		h.sub.Text = "Opening…"
		return
	}
	if h.none {
		h.sub.Text = ""
		return
	}
	s := fmt.Sprintf("%d %s", h.total, map[bool]string{false: "photos", true: "photo"}[h.total == 1])
	if h.folderTotal > h.total {
		s = fmt.Sprintf("%d of %d photos", h.total, h.folderTotal)
	}
	h.subFull, h.subShort = "", s
	if h.shoots > 0 {
		s += fmt.Sprintf(" · %d %s", h.shoots, map[bool]string{false: "shoots", true: "shoot"}[h.shoots == 1])
	}
	if h.sel > 1 {
		s += fmt.Sprintf(" · %d selected", h.sel)
	}
	h.sub.Text, h.subFull = s, s
}

// Children implements [gunim.Composite].
func (h *gridHead) Children() []gunim.Node {
	return []gunim.Node{h.title, h.sub, h.modes.node, h.jump, h.win, h.backNode}
}

// showSet says whether a folder's photos show, of shoots shoots, for the
// switch and the count.
func (h *gridHead) showSet(on bool, shoots int, u *gunim.UI) {
	h.shoots = 0
	if on {
		h.shoots = shoots
		h.views.SetSelected(0, u)
	}
	h.inFolder = on || h.over != ""
	h.subText()
}

// backOn says the way back to an overview shows.
func (h *gridHead) backOn() bool { return h.backKey != "" && h.over == "" }

// goBack goes back to the overview, where the way back shows, and says
// whether it did.
func (h *gridHead) goBack(u *gunim.UI) bool {
	if !h.backOn() {
		return false
	}
	u.Send(h, OpenOverview{Key: h.backKey})
	return true
}

// overview shows the overview's name and count while it shows, and the
// shoot's own, st's, with a way back where in names one, once it is put
// away.
func (h *gridHead) overview(in OverviewIn, st GridState) {
	if in.On {
		h.over, h.overSub, h.backKey = in.Title, in.Sub, ""
		h.title.Text, h.sub.Text = in.Title, in.Sub
		h.subFull, h.subShort = "", ""
		h.inFolder = true
		h.views.SetSelected(1, nil)
		return
	}
	h.over, h.overSub = "", ""
	h.inFolder = st.Set
	h.backKey, h.back.Label = in.BackKey, in.BackTitle
	h.title.Text = st.Folder
	if h.opening != "" {
		h.title.Text = baseName(h.opening)
	}
	if h.none {
		h.title.Text = "No shoot open"
	}
	h.subText()
}

// Layout implements [gunim.Node]: the name and the count on the left,
// the switcher in the window's middle, the jump and the window's buttons
// on the right, all on the band's middle line.
func (h *gridHead) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	mid := box.H / 2
	h.holes = h.holes[:0]
	place := func(k gunim.Child, s geom.Size, x float32, hole bool) {
		k.Place(geom.Pt(x, mid-s.H/2))
		if hole {
			h.holes = append(h.holes, geom.Rc(x, mid-s.H/2, s.W, s.H))
		}
	}
	ws := kids.At(4).Layout(gunim.Loose(box))
	right := box.W - 12 - ws.W
	place(kids.At(4), ws, right, true)
	js := kids.At(3).Layout(gunim.Loose(box))
	right -= 10 + js.W
	place(kids.At(3), js, right, true)
	ms := kids.At(2).Layout(gunim.Loose(box))
	mx := min(max(h.mid-ms.W/2, 0), right-16-ms.W)
	place(kids.At(2), ms, mx, true)
	// The way back to the overview first, where there is one; the count
	// keeps its own width; the name takes what is left.
	x := float32(20)
	if h.backOn() {
		bs := kids.At(5).Layout(gunim.Loose(geom.Sz(max(0, mx-x-16)/2, 32)))
		place(kids.At(5), bs, x, true)
		x += bs.W + 12
	} else {
		kids.At(5).Layout(gunim.Tight(geom.Size{}))
		kids.At(5).Place(geom.Pt(-10000, 0))
	}
	room := max(0, mx-x-16)
	// The name keeps its own width, up to 120; the count goes short
	// (the photos only) before it goes.
	name := min(kids.At(0).Layout(gunim.Loose(geom.Sz(room, box.H))).W, 120)
	if h.subFull != "" {
		h.sub.Text = h.subFull
	}
	ss := kids.At(1).Layout(gunim.Loose(geom.Sz(room, box.H)))
	if room-ss.W-14 < name && h.subShort != "" && h.subShort != h.sub.Text {
		h.sub.Text = h.subShort
		ss = kids.At(1).Layout(gunim.Loose(geom.Sz(room, box.H)))
	}
	h.noSub = room-ss.W-14 < name
	if h.noSub {
		// Too narrow for both: the name, as much of it as fits.
		ss = geom.Size{}
	}
	ts := kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, room-ss.W-14), box.H)))
	kids.At(0).Place(geom.Pt(x, mid-ts.H/2))
	x += ts.W + 14
	kids.At(1).Place(geom.Pt(x, mid-ss.H/2))
	return box
}

// Paint implements [gunim.Node].
func (h *gridHead) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for i := range kids.Len() {
		if (i != 1 || !h.noSub) && (i != 5 || h.backOn()) {
			kids.At(i).Paint(p)
		}
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

// setNatural lays the tiles out in rows of their own shapes, as
// marraw's Natural thumbnails, or back in a grid of cells.
func (v *gridView) setNatural(on bool, u *gunim.UI) {
	if on == (v.grid.Aspect != nil) {
		return
	}
	if on {
		v.grid.Caption = captionHeight + 14
		v.grid.Aspect = func(i int) float32 {
			if i < 0 || i >= len(v.st.Photos) {
				return 0
			}
			return v.st.Photos[i].Aspect
		}
	} else {
		v.grid.Aspect = nil
	}
	v.grid.Reshape(u)
	u.Invalidate()
}

// lerpRect is the rect t of the way from a to b.
func lerpRect(a, b geom.Rect, t float32) geom.Rect {
	l := func(x, y float32) float32 { return x + (y-x)*t }
	return geom.Rect{Min: geom.Pt(l(a.Min.X, b.Min.X), l(a.Min.Y, b.Min.Y)), Max: geom.Pt(l(a.Max.X, b.Max.X), l(a.Max.Y, b.Max.Y))}
}

// Overhear implements [gunim.Overhearer]: on the contact sheet, Esc and G
// go back to the photo the keyboard is on, before the tiles take Esc to
// clear the selection.
func (v *gridView) Overhear(e input.Event, u *gunim.UI) {
	k, ok := e.(input.KeyPress)
	if !ok || !v.st.Sheet || v.band.off || k.Mods != 0 || k.Repeat || u.Focused() != gunim.Node(v.grid) {
		return
	}
	if k.Key != input.KeyEscape && k.Key != input.KeyG {
		return
	}
	if _, cur := v.grid.Selected(); cur >= 0 {
		u.Send(v, OpenCull{Index: cur})
	}
}
