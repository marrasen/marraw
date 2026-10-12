package main

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// railWidth is the library sidebar's width beside the grid, until the
// user drags its edge, and railMin and railMax how narrow and wide it
// goes.
//
// The two bounds are the ones the server will keep, in
// internal/api/uisettings.go. A rail dragged outside them is refused
// when it is saved, so they have to say the same thing.
const (
	railWidth        = 270
	railMin, railMax = 180, 440
)

var (
	railFill       = color.NRGBA{R: 0x13, G: 0x15, B: 0x1a, A: 0xff}
	railHot        = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0c}
	railPick       = color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30}
	railMark       = color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff}
	railOffline    = color.NRGBA{R: 0xe0, G: 0xa0, B: 0x50, A: 0xff}
	railOfflineInk = theme.Color("marraw.rail.offline", railOffline)
	railShared     = color.NRGBA{R: 0x6d, G: 0xd8, B: 0x8f, A: 0xff}
	railSize       = theme.Length("marraw.rail.size", 13)
	railCount      = theme.Length("marraw.rail.count", 11.5)
	railSub        = theme.Length("marraw.rail.sub", 10.5)
	railLine       = theme.Length("marraw.rail.line", 11.5)
	coverBlank     = color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xff}
)

// shootH is a shoot's row's height, with its cover, its name and the line
// under it.
const shootH = 48

// railView is the library sidebar: the library's folders in blocks, a
// library folder with its shoots under it and the shoots added by hand
// under the folder they share; a filter, a menu for ordering, a menu on
// each row, and Add folder at the foot. Rows come and go as the library
// changes, the mark fades from the shoot left to the one chosen, a row
// lights under the pointer, and folders dropped on it join the library.
type railView struct {
	title, count *widget.Label
	empty        *widget.Label
	filter       *widget.TextField
	order        *widget.MenuButton
	list         *widget.List
	menu         *widget.ContextMenu
	body         gunim.Node
	add          gunim.Node
	st           RailState
	// hotKey is the row under the pointer, for its menu; acts are the
	// menu's items' actions.
	hotKey string
	acts   []string
	// top is the title bar's height: the sidebar's fill runs under it,
	// its rows below.
	top float32
	// width is the width the user dragged the edge to, dragging says the
	// edge is held, and dropIn lights the sidebar as files are dragged
	// over it.
	width    float32
	dragging bool
	dropIn   *anim.Float
	// covers are the shoots' covers' pictures, by their keys.
	covers map[string]*paint.Image
	// footY is where the foot's hairline runs.
	footY float32
}

// railFootH is the foot's height, and railFootInk its buttons' ink, a
// step under the rows' names.
const railFootH = 34

var railFootInk = theme.Color("marraw.rail.foot", color.NRGBA{R: 0xa8, G: 0xae, B: 0xb8, A: 0xff})

// railFoot is the sidebar's foot: Add folder at the left, the settings'
// gear at the right.
type railFoot struct{ add, gear gunim.Node }

// Children implements [gunim.Composite].
func (f *railFoot) Children() []gunim.Node { return []gunim.Node{f.add, f.gear} }

// Layout implements [gunim.Node].
func (f *railFoot) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	gs := kids.At(1).Layout(gunim.Loose(box))
	kids.At(1).Place(geom.Pt(box.W-gs.W, (box.H-gs.H)/2))
	as := kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, box.W-gs.W-8), box.H)))
	kids.At(0).Place(geom.Pt(0, (box.H-as.H)/2))
	return box
}

// Paint implements [gunim.Node].
func (f *railFoot) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	kids.At(1).Paint(p)
}

// railHeadH is the room above the rows: the title, the filter, and the
// caption.
const railHeadH = gridHeadHeight + 70

func newRailView() *railView {
	v := &railView{title: widget.NewLabel("Library"), count: newSmallLabel(""), empty: widget.NewLabel(""), list: widget.NewList(),
		filter: widget.NewTextField(), width: railWidth, dropIn: anim.NewFloat(0), covers: map[string]*paint.Image{}}
	v.title.Color, v.title.Size = headingInk, headingSize
	v.count.Color = noteInk
	v.empty.Color, v.empty.Size, v.empty.MaxLines = noteInk, noteSize, 4
	v.filter.Placeholder = "Filter folders"
	v.filter.OnChange = func(s string, _ *gunim.UI) gunim.Intent { return RailFilter{Text: s} }
	v.order = widget.NewMenuButton("", nil)
	v.order.Icon, v.order.Tooltip = icon.ArrowUpDown, "Sort and group the folders"
	v.order.OnPick = func(i int, _ *gunim.UI) gunim.Intent { return railOrderPick(i) }
	v.list.SkipFocus, v.list.ClickOnce = true, true
	v.list.OnActivate = func(k widget.Key, _ *gunim.UI) gunim.Intent {
		it, ok := v.item(string(k))
		switch {
		case !ok || it.Kind == "note":
			return nil
		case it.Kind == "time":
			return RailToggle{Key: it.Key}
		case it.Group:
			// A folder: its shoots, side by side, where the photos show.
			// Its chevron folds it.
			return OpenOverview{Key: it.Key}
		}
		return OpenShoot{Path: it.Path}
	}
	v.menu = widget.NewContextMenu(widget.NewScroll(v.list), nil)
	v.menu.Prepare = func(_ geom.Point, _ *gunim.UI) bool {
		it, ok := v.item(v.hotKey)
		if !ok || it.Kind == "note" {
			return false
		}
		items, acts := railMenu(it)
		v.acts = acts
		v.menu.SetItems(items)
		return true
	}
	v.menu.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		if i < 0 || i >= len(v.acts) {
			return nil
		}
		it, ok := v.item(v.hotKey)
		if !ok {
			return nil
		}
		if v.acts[i] == "copyPath" {
			u.SetClipboard(it.Path)
			return Notify{Text: "Path copied"}
		}
		return RailAct{Key: it.Key, Act: v.acts[i]}
	}
	v.body = v.menu
	// The foot's two, quiet as the sidebar's own rows: no fill till the
	// pointer is over them.
	add := widget.NewButton("Add folder")
	add.Icon, add.KeepFocus, add.OnClick = icon.FolderPlus, true, widget.Sends(AskAddFolder{})
	gear := widget.NewButton("")
	gear.Icon, gear.KeepFocus, gear.Tooltip, gear.OnClick = icon.Settings, true, "Settings", widget.Sends(AskSettings{})
	for _, b := range []*widget.Button{add, gear} {
		b.Ghost, b.Ink = true, railFootInk
	}
	v.add = &railFoot{add: add, gear: gear}
	return v
}

// item is the row key.
func (v *railView) item(key string) (RailItem, bool) {
	for _, it := range v.st.Items {
		if it.Key == key {
			return it, true
		}
	}
	return RailItem{}, false
}

// railOrderItems are the order menu's items, and railOrderPick what a pick
// in it asks for.
var (
	railSorts   = []string{"nameAsc", "nameDesc", "dateAsc", "dateDesc"}
	railSortsAs = []string{"Name, A to Z", "Name, Z to A", "Date, oldest first", "Date, newest first"}
	railGroups  = []string{"none", "year", "month", "day"}
	railGroupAs = []string{"None", "Year", "Month", "Day"}
)

func railOrderItems(sort, by string) []widget.MenuItem {
	items := []widget.MenuItem{{Label: "Sort folders", Caption: true}}
	for i, s := range railSorts {
		items = append(items, widget.MenuItem{Label: railSortsAs[i], Checked: s == sort})
	}
	items = append(items, widget.MenuItem{Label: "Group by date", Caption: true, Break: true})
	for i, g := range railGroups {
		items = append(items, widget.MenuItem{Label: railGroupAs[i], Checked: g == by})
	}
	items = append(items, widget.MenuItem{Label: "Collapse previous years", Break: true, Disabled: by == "none" || by == ""})
	return items
}

func railOrderPick(i int) gunim.Intent {
	switch {
	case i >= 1 && i <= 4:
		return RailOrder{Sort: railSorts[i-1]}
	case i >= 6 && i <= 9:
		return RailOrder{GroupBy: railGroups[i-6]}
	case i == 10:
		return RailOrder{CollapseYears: true}
	}
	return nil
}

// railMenu is the menu of row it, and the action of each item.
func railMenu(it RailItem) ([]widget.MenuItem, []string) {
	var items []widget.MenuItem
	var acts []string
	add := func(m widget.MenuItem, act string) {
		items = append(items, m)
		acts = append(acts, act)
	}
	caption := it.Path
	switch it.Kind {
	case "parent":
		caption += fmt.Sprintf(" · %d folders", it.Count)
		if it.Offline {
			caption = it.Path + " · offline"
		}
	case "group":
		caption += fmt.Sprintf(" · %d shoots", it.Count)
	case "shoot", "root":
		if it.Count > 0 {
			caption = fmt.Sprintf("%s · %d RAW", it.Name, it.Count)
		}
	}
	add(widget.MenuItem{Label: caption, Caption: true}, "")
	switch it.Kind {
	case "parent", "group":
		add(widget.MenuItem{Label: "Rename group…", Icon: icon.Pencil}, "rename")
		add(widget.MenuItem{Label: "Locate on disk", Icon: icon.ExternalLink, Disabled: it.Offline}, "reveal")
		if it.Kind == "parent" {
			add(widget.MenuItem{Label: "Refresh", Icon: icon.RefreshCw}, "refresh")
		}
		add(widget.MenuItem{Label: "Rescan all shoots", Icon: icon.RefreshCw, Disabled: it.Offline}, "rescanAll")
		add(widget.MenuItem{Label: "Move up", Icon: icon.ArrowUp, Disabled: it.First}, "up")
		add(widget.MenuItem{Label: "Move down", Icon: icon.ArrowDown, Disabled: it.Last}, "down")
		label := "Remove group"
		if it.Kind == "parent" {
			label = "Remove library folder"
		}
		add(widget.MenuItem{Label: label + " (the files stay on disk)", Icon: icon.Trash2, Break: true}, "remove")
	case "time":
		return nil, nil
	case "dir":
		add(widget.MenuItem{Label: "Show as one shoot", Icon: icon.Folder}, "folderOne")
		add(widget.MenuItem{Label: "Locate on disk", Icon: icon.ExternalLink}, "reveal")
		add(widget.MenuItem{Label: "Copy path", Icon: icon.Copy}, "copyPath")
		add(widget.MenuItem{Label: "Hide from library (the files stay on disk)", Icon: icon.EyeOff, Break: true}, "hide")
		return items, acts
	case "shoot", "root":
		add(widget.MenuItem{Label: "Open in Cull", Icon: icon.Play, Hint: "Enter", Disabled: it.Offline}, "cull")
		add(widget.MenuItem{Label: "Open in new window", Icon: icon.AppWindow, Disabled: it.Offline}, "newWindow")
		if it.Outside {
			add(widget.MenuItem{Label: "Add to the library", Icon: icon.FolderPlus}, "addLib")
		}
		if it.Kind == "root" && !it.Outside {
			add(widget.MenuItem{Label: "Rename…", Icon: icon.Pencil, Hint: "F2"}, "rename")
		}
		if !it.Self {
			add(widget.MenuItem{Label: "Rename on disk…", Icon: icon.FolderPen, Disabled: it.Offline}, "renameDisk")
		}
		switch {
		case it.Split:
			add(widget.MenuItem{Label: "Show as one shoot", Icon: icon.Folder}, "folderDefault")
		case it.CanSplit:
			add(widget.MenuItem{Label: "Show its folders as separate shoots", Icon: icon.FolderTree}, "folderSplit")
		}
		add(widget.MenuItem{Label: "Locate on disk", Icon: icon.ExternalLink, Disabled: it.Offline}, "reveal")
		add(widget.MenuItem{Label: "Copy path", Icon: icon.Copy}, "copyPath")
		add(widget.MenuItem{Label: "Share album…", Icon: icon.Share2, Disabled: it.Offline}, "share")
		if it.Kind == "root" && !it.Outside {
			add(widget.MenuItem{Label: "Include subfolders", Checked: it.Subfolders, Disabled: it.Offline}, "subfolders")
		}
		add(widget.MenuItem{Label: "Rescan for new photos", Icon: icon.RefreshCw, Disabled: it.Offline}, "rescan")
		add(widget.MenuItem{Label: "Render 1:1", Icon: icon.Maximize2, Disabled: it.Offline}, "fullres")
		if it.Shared > 0 {
			label := "Revoke shared access (the link stops working)"
			if it.Shared > 1 {
				label = fmt.Sprintf("Revoke shared access (%d links stop working)", it.Shared)
			}
			add(widget.MenuItem{Label: label, Icon: icon.Share2, Break: true}, "revoke")
		}
		switch {
		case it.Kind == "shoot" && !it.Self:
			add(widget.MenuItem{Label: "Hide from library (the files stay on disk)", Icon: icon.EyeOff, Break: true}, "hide")
		case it.Kind == "root" && !it.Outside:
			add(widget.MenuItem{Label: "Remove from library (the files stay on disk)", Icon: icon.Trash2, Break: true}, "remove")
		}
	}
	return items, acts
}

// railEntry is a row's item, and whether it is the shoot showing.
type railEntry struct {
	RailItem
	current bool
}

func (v *railView) show(s RailState, u *gunim.UI) {
	v.st = s
	entries := make([]railEntry, len(s.Items))
	for i, it := range s.Items {
		// The row of what shows: the folder whose overview shows, or else
		// the shoot open.
		current := !it.Group && s.Overview == "" && strings.EqualFold(it.Path, s.Current)
		if it.Group {
			current = s.Overview != "" && it.Key == s.Overview
		}
		entries[i] = railEntry{RailItem: it, current: current}
	}
	widget.Sync(v.list, u, entries,
		func(e railEntry) widget.Key { return widget.Key(e.Key) },
		func(e railEntry) *railRow { return newRailRow(e, v) }, (*railRow).set)
	switch {
	case s.Loaded && s.Folders == 0 && len(s.Items) == 0:
		v.empty.Text = "No folders yet. Add one below, or drop a folder here."
	case s.Loaded && len(s.Items) == 0 && s.Filter != "":
		v.empty.Text = "No folders match the filter."
	default:
		v.empty.Text = ""
	}
	v.count.Text = ""
	if s.Folders > 0 {
		v.count.Text = fmt.Sprintf("%d folder%s", s.Folders, map[bool]string{true: "", false: "s"}[s.Folders == 1])
	}
	v.order.SetItems(railOrderItems(s.Sort, s.GroupBy))
	v.order.Active = s.Sort != "nameAsc" || s.GroupBy != "none"
	if !v.dragging && s.Width > 0 {
		v.width = float32(max(railMin, min(s.Width, railMax)))
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (v *railView) Children() []gunim.Node {
	return []gunim.Node{v.title, v.count, v.order, v.filter, v.empty, v.body, v.add}
}

// Layout implements [gunim.Node]: the title and its buttons, the filter,
// then the rows, scrolling, and Add folder at the foot.
func (v *railView) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	title, count, order, filter, empty, body, add := kids.At(0), kids.At(1), kids.At(2), kids.At(3), kids.At(4), kids.At(5), kids.At(6)
	ts := title.Layout(gunim.Loose(geom.Sz(box.W-80, 40)))
	title.Place(geom.Pt(16, v.top+(gridHeadHeight-ts.H)/2))
	fs := filter.Layout(gunim.Tight(geom.Sz(box.W-24, 30)))
	filter.Place(geom.Pt(12, v.top+gridHeadHeight+6))
	os := order.Layout(gunim.Loose(geom.Sz(28, 26)))
	order.Place(geom.Pt(box.W-10-os.W, v.top+gridHeadHeight+6+fs.H+4))
	cs := count.Layout(gunim.Loose(geom.Sz(box.W-60, 20)))
	count.Place(geom.Pt(16, v.top+gridHeadHeight+6+fs.H+4+(os.H-cs.H)/2))
	empty.Layout(gunim.Loose(geom.Sz(box.W-32, 200)))
	empty.Place(geom.Pt(16, v.top+railHeadH+8))
	add.Layout(gunim.Tight(geom.Sz(box.W-16, railFootH)))
	add.Place(geom.Pt(8, box.H-railFootH-6))
	v.footY = box.H - railFootH - 12
	body.Layout(gunim.Tight(geom.Sz(box.W, max(0, v.footY-railHeadH-v.top))))
	body.Place(geom.Pt(0, v.top+railHeadH))
	return box
}

// Paint implements [gunim.Node].
func (v *railView) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(railFill))
	edge := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}
	if v.dragging {
		edge = withAlpha(railMark, 0.8)
	}
	p.RRect(geom.Rc(box.W-1, 0, 1, box.H), 0, paint.Solid(edge))
	p.RRect(geom.Rc(0, v.top+gridHeadHeight-1, box.W, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}))
	// A hairline over the foot, as under the title.
	p.RRect(geom.Rc(0, v.footY, box.W-1, 1), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0e}))
	for k := range kids.All {
		k.Paint(p)
	}
	if k := v.dropIn.Value(); k > 0.01 {
		r := geom.Rect{Max: box.Point()}.Inset(geom.Uniform(4))
		p.RRect(r, 10, paint.Solid(withAlpha(railPick, k)))
		p.RRectStroke(r, 10, paint.Fill{}, paint.Stroke{Width: 2, Color: withAlpha(railMark, k)})
	}
}

// Step implements [gunim.Animator].
func (v *railView) Step(dt time.Duration) bool { return v.dropIn.Step(dt) }

// ClaimsPointer implements [gunim.PointerClaimer]: the sidebar's right
// edge is a handle for its width.
func (v *railView) ClaimsPointer(p geom.Point) bool { return v.dragging || p.X >= v.width-5 }

// Cursor implements [gunim.CursorShaper].
func (v *railView) Cursor(p geom.Point) input.Cursor {
	if v.dragging || p.X >= v.width-5 {
		return input.CursorResizeH
	}
	return input.CursorArrow
}

// Handle implements [gunim.Handler]: the edge drags the width, and
// folders dropped from a file manager join the library.
func (v *railView) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button == input.ButtonPrimary && e.Pos.X >= v.width-5 {
			v.dragging = true
			u.Invalidate()
			return true
		}
	case input.PointerMove:
		if v.dragging {
			v.width = max(railMin, min(e.Pos.X, railMax))
			u.Invalidate()
			return true
		}
	case input.PointerUp:
		if v.dragging {
			v.dragging = false
			u.Send(v, RailWidth{Px: int(v.width + 0.5)})
			u.Invalidate()
			return true
		}
	case input.DragOver:
		if _, ok := e.Data.(input.Files); ok {
			v.dropIn.Animate(1, widget.Quick.Get(u.Theme()))
			u.Invalidate()
			return true
		}
	case input.DragLeave:
		v.dropIn.Animate(0, widget.Settle.Get(u.Theme()))
		u.Invalidate()
	case input.Drop:
		v.dropIn.Animate(0, widget.Settle.Get(u.Theme()))
		u.Invalidate()
		if len(e.Paths) > 0 {
			u.Send(v, AddFolders{Paths: e.Paths})
			return true
		}
	}
	return false
}

// railRow is one row of the sidebar: a header over rows, with a chevron
// that turns as it opens; a shoot, its name and how many photos it
// holds; or a note.
type railRow struct {
	anim.Group
	v              *railView
	item           railEntry
	name, n, sub   *widget.Label
	badge          *widget.Label
	badgeAt        geom.Rect
	pick, hot, rot *anim.Float
	// turn is the spinner's turn, while the folder is busy, and painted
	// says it was drawn since the last step.
	turn    float32
	painted bool
}

// Step implements [gunim.Animator]: the spinner turns while it shows.
func (r *railRow) Step(dt time.Duration) bool {
	moving := r.item.Busy && r.painted
	if dt > 0 {
		r.painted = false
		r.turn = float32(math.Mod(float64(r.turn)+dt.Seconds()/0.9, 1))
	}
	return r.Group.Step(dt) || moving
}

func newRailRow(e railEntry, v *railView) *railRow {
	r := &railRow{v: v, name: widget.NewLabel(""), n: widget.NewLabel(""), sub: widget.NewLabel(""), badge: widget.NewLabel("Offline"),
		pick: anim.NewFloat(0), hot: anim.NewFloat(0), rot: anim.NewFloat(0)}
	// One line, cut with an ellipsis where a name is long.
	r.name.Size, r.name.MaxLines = railSize, 1
	r.n.Size, r.n.Color, r.n.NoWrap, r.n.Align = railCount, noteInk, true, text.AlignEnd
	r.sub.Size, r.sub.Color, r.sub.MaxLines = railSub, noteInk, 1
	r.badge.Size, r.badge.Color, r.badge.NoWrap = railSub, railOfflineInk, true
	r.Add(r.pick, r.hot, r.rot)
	r.set(e, nil)
	return r
}

// set shows e: the mark fades in on the shoot showing and out of the one
// left, and a header's chevron turns as it opens or closes.
func (r *railRow) set(e railEntry, u *gunim.UI) {
	r.item = e
	r.name.Text = e.Name
	r.name.Face = theme.Token[*text.Face]{}
	r.sub.Text, r.sub.Align = "", text.AlignStart
	r.n.Text = ""
	switch e.Kind {
	case "parent", "group":
		// A library's heading: its name in small capitals, its folder
		// faint at the right.
		r.name.Text = strings.ToUpper(e.Name)
		r.name.Color, r.name.Size, r.name.MaxLines, r.name.Face = sectionLabelInk, sectionLabelSize, 1, widget.BoldFont
		r.sub.Text, r.sub.Size, r.sub.Align = e.Sub, railSub, text.AlignEnd
	case "time":
		r.name.Color, r.name.Size = noteInk, railCount
		if e.Count > 0 {
			r.n.Text = fmt.Sprint(e.Count)
		}
	case "dir":
		// A year's or a client's folder over its shoots.
		r.name.Color, r.name.Size = widget.Ink, railSize
		if e.Count > 0 {
			r.n.Text = fmt.Sprint(e.Count)
		}
	case "note":
		r.name.Color, r.name.Size, r.name.MaxLines = noteInk, railCount, 3
	default:
		// A shoot: its name without the date it starts with, and under
		// it the date and how many photos; the count is in that line.
		r.name.Color, r.name.Size = widget.Ink, railSize
		if e.Offline {
			r.name.Color = noteInk
		}
		if e.Title != "" {
			r.name.Text = e.Title
		}
		r.sub.Text, r.sub.Size = e.Line, railLine
	}
	to := map[bool]float32{false: 0, true: 1}[e.current]
	turn := map[bool]float32{false: 0, true: 1}[e.Open]
	if u == nil {
		r.pick.Jump(to)
		r.rot.Jump(turn)
		return
	}
	if e.current && r.pick.Value() < 0.01 {
		// Chosen: it fades in from a touch narrower, as a press lands.
		r.pick.Jump(0)
	}
	r.pick.Animate(to, widget.Quick.Get(u.Theme()))
	r.rot.Animate(turn, widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (r *railRow) Children() []gunim.Node { return []gunim.Node{r.name, r.n, r.sub, r.badge} }

// Handle implements [gunim.Handler]: the row lights under the pointer,
// and is the one its menu is for.
func (r *railRow) Handle(e input.Event, u *gunim.UI) bool {
	// A header's chevron folds it; a click on the rest of a folder's row
	// shows its overview.
	if d, ok := e.(input.PointerDown); ok && d.Button == input.ButtonPrimary && r.item.Group && r.item.Kind != "time" &&
		d.Pos.X < r.indent()-2 {
		u.Send(r, RailToggle{Key: r.item.Key})
		return true
	}
	switch e.(type) {
	case input.PointerEnter, input.PointerMove:
		r.v.hotKey = r.item.Key
		if r.item.Kind == "note" {
			return false
		}
		r.hot.Animate(1, widget.Quick.Get(u.Theme()))
		u.Invalidate()
	case input.PointerLeave:
		r.hot.Animate(0, widget.Settle.Get(u.Theme()))
		u.Invalidate()
	}
	return false
}

// indent is where the row's text starts: past the chevron of a header,
// past the cover of a shoot.
func (r *railRow) indent() float32 {
	if r.isShoot() {
		return r.coverAt().Max.X + 10
	}
	if r.item.Kind == "dir" {
		// The chevron where a cover of its depth starts, so its shoots'
		// covers sit a step in, under its name.
		return treeX(r.item.Depth) + 20
	}
	x := float32(16 + 14*r.item.Depth)
	if r.item.Kind != "note" {
		x += 20
	}
	return x
}

// isShoot says the row is a shoot, with a cover.
func (r *railRow) isShoot() bool { return r.item.Kind == "shoot" || r.item.Kind == "root" }

// coverAt is where a shoot's cover is.
func (r *railRow) coverAt() geom.Rect {
	return geom.Rc(treeX(r.item.Depth), (shootH-coverH)/2, coverW, coverH)
}

// treeX is where a row of the library's tree starts at depth: a step in for
// each folder above it.
func treeX(depth int) float32 { return float32(14 + 18*max(0, depth-1)) }

// Layout implements [gunim.Node].
func (r *railRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	if w <= 0 {
		w = railWidth
	}
	name, n, sub, badge := kids.At(0), kids.At(1), kids.At(2), kids.At(3)
	indent := r.indent()
	bs := badge.Layout(gunim.Loose(geom.Sz(80, 20)))
	ns := n.Layout(gunim.Loose(geom.Sz(60, 30)))
	room := max(0, w-indent-ns.W-26)
	if r.item.Offline || r.item.Shared > 0 {
		room -= 56
	}
	switch r.item.Kind {
	case "parent", "group":
		const h = 34
		ms := name.Layout(gunim.Loose(geom.Sz(max(0, w-indent-14-60), h)))
		name.Place(geom.Pt(indent, (h-ms.H)/2))
		// The folder in what the name leaves, ending in "…" where it is
		// long; the Offline badge in its place while the drive is gone.
		pathRoom := max(0, w-indent-ms.W-14-14)
		if r.item.Offline {
			pathRoom = 0
		}
		ss := sub.Layout(gunim.Loose(geom.Sz(pathRoom, 16)))
		sub.Place(geom.Pt(w-14-ss.W, (h-ss.H)/2))
		n.Place(geom.Pt(w-14-ns.W, (h-ns.H)/2))
		r.badgeAt = geom.Rc(w-14-bs.W-10, (h-bs.H-2)/2, bs.W+10, bs.H+2)
		badge.Place(r.badgeAt.Min.Add(geom.Pt(5, 1)))
		return geom.Sz(w, h)
	case "note":
		ms := name.Layout(gunim.Loose(geom.Sz(max(0, w-indent-14), 60)))
		name.Place(geom.Pt(indent, 4))
		sub.Layout(gunim.Loose(geom.Sz(0, 0)))
		return geom.Sz(w, ms.H+8)
	}
	if r.isShoot() {
		// The name over the line under it, beside the cover; a spinner, a
		// badge or the shared mark at the right of the name.
		if r.item.Busy {
			room -= 18
		}
		ms := name.Layout(gunim.Loose(geom.Sz(room+ns.W, 20)))
		ss := sub.Layout(gunim.Loose(geom.Sz(max(0, w-indent-14), 16)))
		if r.sub.Text == "" {
			// Nothing to say under it yet: the name in the middle.
			ss.H = -2
		}
		top := (shootH - ms.H - ss.H - 2) / 2
		if r.item.Picked+r.item.Rejected > 0 {
			top -= 3
		}
		name.Place(geom.Pt(indent, top))
		sub.Place(geom.Pt(indent, top+ms.H+2))
		n.Layout(gunim.Loose(geom.Sz(0, 0)))
		r.badgeAt = geom.Rc(w-14-bs.W-10, top, bs.W+10, bs.H+2)
		badge.Place(r.badgeAt.Min.Add(geom.Pt(5, 1)))
		return geom.Sz(w, shootH)
	}
	h := float32(30)
	if r.item.Kind == "time" {
		h = 28
	}
	ms := name.Layout(gunim.Loose(geom.Sz(room, h)))
	name.Place(geom.Pt(indent, (h-ms.H)/2))
	n.Place(geom.Pt(w-14-ns.W, (h-ns.H)/2))
	sub.Layout(gunim.Loose(geom.Sz(0, 0)))
	r.badgeAt = geom.Rc(w-14-ns.W-bs.W-16, (h-bs.H-2)/2, bs.W+10, bs.H+2)
	badge.Place(r.badgeAt.Min.Add(geom.Pt(5, 1)))
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (r *railRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	back := geom.Rc(6, 1, box.W-12, box.H-2)
	if h := r.hot.Value(); h > 0.01 {
		p.RRect(back, 6, paint.Solid(withAlpha(railHot, h)))
	}
	if k := r.pick.Value(); k > 0.01 {
		p.RRect(scaleAbout(back, 0.96+0.04*k), 6, paint.Solid(withAlpha(railPick, k)))
		bar := geom.Rc(back.Min.X+3, back.Min.Y+back.Size().H*(0.5-0.3*k), 3, back.Size().H*0.6*k)
		p.RRect(bar, 1.5, paint.Solid(withAlpha(railMark, k)))
	}
	ink := noteInk.Get(f.Theme)
	x := r.indent() - 20
	switch r.item.Kind {
	case "parent", "group", "time", "dir":
		// The chevron, turning down as the header opens.
		cy := box.H / 2
		func() {
			defer p.Push(paint.Rotate(r.rot.Value()*1.5708, geom.Pt(x+7, cy)))()
			p.Mask(icon.Stroke{Icon: icon.ChevronRight, Width: 2, Progress: 1}, geom.Rc(x+1, cy-6, 12, 12), ink)
		}()
	case "shoot", "root":
		r.paintCover(p, ink)
		r.paintBar(p, box)
	}
	if r.item.Offline {
		p.RRect(r.badgeAt, 4, paint.Solid(withAlpha(railOffline, 0.16)))
	}
	for i := range kids.Len() {
		if i == 3 && !r.item.Offline || i == 1 && r.item.Busy {
			continue
		}
		kids.At(i).Paint(p)
	}
	if r.item.Busy {
		// A spinner in the count's place while the folder opens or is
		// worked on.
		r.painted = true
		at := geom.Rc(box.W-14-13, box.H/2-6.5, 13, 13)
		p.RRectStroke(at.Inset(geom.Uniform(1.5)), 5, paint.Fill{}, paint.Stroke{Width: 1.5, Color: withAlpha(spinnerInk, 0.3)})
		func() {
			defer p.Push(paint.Rotate(r.turn*2*math.Pi, at.Center()))()
			p.Mask(icon.Stroke{Icon: spinnerArc, Width: 1.5, Progress: 1}, at, spinnerInk)
		}()
	}
	if r.item.Shared > 0 && !r.item.Offline {
		p.Mask(icon.Stroke{Icon: icon.Share2, Width: 1.6, Progress: 1}, geom.Rc(r.badgeAt.Max.X-16, r.badgeAt.Min.Y+1, 14, 14), railShared)
	}
}

// paintCover draws a shoot's cover, or a blank with a picture's mark
// until it comes, and a ring round it while the shoot is the one open.
func (r *railRow) paintCover(p *paint.Painter, ink color.NRGBA) {
	at := r.coverAt()
	img := r.v.covers[r.item.Cover]
	op := float32(1)
	if r.item.Offline {
		op = 0.45
	}
	if img != nil {
		p.Image(img, at, paint.ImageOpts{Opacity: op, Radius: 5})
	} else {
		p.RRect(at, 5, paint.Solid(coverBlank))
		mark := geom.Rc(at.Center().X-7, at.Center().Y-7, 14, 14)
		p.Mask(icon.Stroke{Icon: icon.Image, Width: 1.5, Progress: 1}, mark, withAlpha(ink, 0.45*op))
	}
	if k := r.pick.Value(); k > 0.01 {
		p.RRectStroke(at.Inset(geom.Uniform(-1.5)), 6.5, paint.Fill{}, paint.Stroke{Width: 1.5, Color: withAlpha(railMark, k)})
	}
}

// paintBar draws how far the shoot is culled: its picks in green and its
// rejects in red, along a thin track under its line, in proportion to its
// photos.
func (r *railRow) paintBar(p *paint.Painter, box geom.Size) {
	it := r.item
	if it.Picked+it.Rejected == 0 {
		return
	}
	total := max(it.Count, it.Picked+it.Rejected)
	x0 := r.indent()
	track := geom.Rc(x0, shootH-11, max(0, box.W-14-x0), 3)
	p.RRect(track, 1.5, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x14}))
	pw := track.Size().W * float32(it.Picked) / float32(total)
	rw := track.Size().W * float32(it.Rejected) / float32(total)
	if pw > 0 {
		p.RRect(geom.Rc(track.Min.X, track.Min.Y, max(pw, 3), 3), 1.5, paint.Solid(pickInk))
	}
	if rw > 0 {
		p.RRect(geom.Rc(track.Min.X+pw, track.Min.Y, max(rw, 3), 3), 1.5, paint.Solid(rejectInk))
	}
}
