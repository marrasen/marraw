package main

import (
	"context"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The vocabulary of the library sidebar, between the window and the
// culler.
type (
	// RailState is the library as the sidebar lists it, and the folder
	// showing.
	RailState struct {
		Items   []RailItem
		Current string
		// Loaded says the library has been read, so an empty one says so.
		Loaded bool
		// Folders is how many folders the library holds, Filter the text
		// the rows are narrowed by, and Sort and GroupBy how the shoots
		// of a library folder are ordered and bucketed by date.
		Folders int
		Filter  string
		Sort    string
		GroupBy string
		// Width is the sidebar's width, and Hidden says it is put away.
		Width  int
		Hidden bool
		// Opening is the folder on its way to the grid, or "".
		Opening string
		// Overview is the key of the folder row whose overview shows, or "".
		Overview string
	}
	// RailItem is one row: a header over rows, a shoot, or a note.
	RailItem struct {
		// Key names the row among the rest; Path is its folder's.
		Key, Path, Name string
		// Kind is "parent" for a library folder, "group" for shoots added
		// by hand that share a folder, "time" for a library folder's
		// shoots of a year, month or day, "shoot" for a shoot of a
		// library folder, "root" for one added by hand, and "note".
		Kind  string
		Count int
		Depth int
		// Group says the row is a header, Open that its rows show.
		Group, Open bool
		// Sub is a header's path, under its name.
		Sub string
		// Offline says the folder is not there to read, and Missing
		// that its drive is: moved or deleted, not unplugged. Self says
		// the row is a library folder's own loose photos, Shared how
		// many live links share it, and Subfolders that a root's
		// photos are read from its subfolders too.
		Offline    bool
		Missing    bool
		Self       bool
		Shared     int
		Subfolders bool
		// Parent is a shoot's library folder; First and Last say a
		// header's block is at the top or the bottom.
		Parent      string
		First, Last bool
		// Outside says the folder is open but not in the library, and
		// Busy that it is being opened or worked on in the background.
		Outside bool
		Busy    bool
		// CanSplit says a shoot has folders in it that could be shoots of
		// their own, and Split that they are.
		CanSplit, Split bool
		// Title is a shoot's name as it shows, without a date it starts
		// with, and Line the date and count under it. Picked and Rejected
		// are its cull counts, for its bar, and Cover its cover's key.
		Title, Line      string
		Picked, Rejected int
		Cover            string
		// ref is the cover, for the controller to fetch.
		ref *marrawclient.ShootCover
	}
	// OpenShoot opens the folder at Path in the grid; Cull opens it in
	// the cull view.
	OpenShoot struct {
		Path string
		Cull bool
	}
	// RailToggle opens or closes the header Key, RailFilter narrows the
	// rows to those matching Text, and RailOrder sorts the shoots by Sort
	// and groups them by GroupBy, or collapses the years before this
	// one with CollapseYears.
	RailToggle struct{ Key string }
	RailFilter struct{ Text string }
	RailOrder  struct {
		Sort, GroupBy string
		CollapseYears bool
	}
	// RailAct does Act to the row Key, as its menu says.
	RailAct struct{ Key, Act string }
	// RailWidth sets the sidebar's width; RailHide puts it away or brings
	// it back.
	RailWidth struct{ Px int }
	RailHide  struct{ Hidden bool }
	// AddFolders adds the folders at Paths to the library, as a drop of
	// them on the window does.
	AddFolders struct{ Paths []string }
)

// library is what the sidebar is made from, as the backend sends it.
type library struct {
	roots  []marrawclient.LibraryRoot
	shoots map[string][]marrawclient.Shoot
	stops  map[string]func()
	online map[string]bool
	// status is each root's, by its lowercased path.
	status map[string]marrawclient.RootStatus
	links  []marrawclient.ShareLink
	filter string
	// regroup is a change of how a folder shows that takes the open
	// shoot with it, waiting for its library folder's shoots to change.
	regroup *regroup
	loaded  bool
	// covers are the covers' pictures held, and coverWant those on their
	// way, by their keys; live the counts and covers known here of shoots
	// culled since the backend listed them, by lowercased path.
	covers    map[string]*paint.Image
	coverWant map[string]bool
	live      map[string]liveSum
	// previewSlots bounds the folders' previews asked for at once, so
	// they never hold up the grid's own pictures.
	previewSlots chan struct{}
	// big are the overview's large covers held, bigWant those on their
	// way, and bigOrder the order they were asked for in, the oldest
	// first to go.
	big      map[string]*paint.Image
	bigWant  map[string]bool
	bigOrder []string
}

// followLibrary keeps the sidebar up to date: the library's folders, the
// shoots of each library folder, which drives are connected, and the
// links that share shoots, each as the backend says they change.
func (cu *culler) followLibrary() {
	cu.lib.shoots, cu.lib.stops, cu.lib.online = map[string][]marrawclient.Shoot{}, map[string]func(){}, map[string]bool{}
	cu.lib.status, cu.lib.covers, cu.lib.coverWant = map[string]marrawclient.RootStatus{}, map[string]*paint.Image{}, map[string]bool{}
	cu.lib.live = map[string]liveSum{}
	cu.lib.previewSlots = make(chan struct{}, 3)
	roots := cu.api.Library.SubscribeGetLibraryRoots(cu.ctx)
	status := cu.api.Library.SubscribeGetRootStatus(cu.ctx)
	links := cu.api.Share.SubscribeListLinks(cu.ctx)
	go func() {
		for r := range roots.C {
			cu.onDo(func() { cu.rootsIn(r) })
		}
		if err := roots.Err(); err != nil && cu.ctx.Err() == nil {
			log.Printf("library: %v", err)
		}
	}()
	go func() {
		for s := range status.C {
			cu.onDo(func() {
				clear(cu.lib.online)
				clear(cu.lib.status)
				for _, st := range s {
					cu.lib.online[strings.ToLower(st.Path)] = st.Online
					cu.lib.status[strings.ToLower(st.Path)] = st
					cu.dropLive(st.Path)
				}
				cu.railChanged()
			})
		}
	}()
	go func() {
		for l := range links.C {
			cu.onDo(func() {
				cu.lib.links = l
				cu.railChanged()
			})
		}
	}()
}

// onDo runs fn on the controller's goroutine, unless the app is closing.
func (cu *culler) onDo(fn func()) {
	select {
	case cu.do <- fn:
	case <-cu.ctx.Done():
	}
}

// rootsIn takes the library's folders: each library folder's shoots are
// followed, and those of a folder gone let go.
func (cu *culler) rootsIn(roots []marrawclient.LibraryRoot) {
	l := &cu.lib
	l.roots, l.loaded = roots, true
	want := map[string]bool{}
	for _, r := range roots {
		if !r.IsParent {
			continue
		}
		key := strings.ToLower(r.Path)
		want[key] = true
		if l.stops[key] != nil {
			continue
		}
		ctx, cancel := context.WithCancel(cu.ctx)
		l.stops[key] = cancel
		sub := cu.api.Library.SubscribeListShoots(ctx, r.Path)
		go func() {
			for s := range sub.C {
				cu.onDo(func() {
					if l.stops[key] != nil {
						l.shoots[key] = s
						cu.dropLive(r.Path)
						cu.railChanged()
						cu.followRegroup(key)
					}
				})
			}
		}()
	}
	for key, stop := range l.stops {
		if !want[key] {
			stop()
			delete(l.stops, key)
			delete(l.shoots, key)
		}
	}
	cu.railChanged()
}

// railChanged shows the sidebar as the library now stands.
func (cu *culler) railChanged() {
	cu.rail.Items, cu.rail.Loaded = cu.railItems(), cu.lib.loaded
	cu.rail.Folders, cu.rail.Filter = len(cu.lib.roots), cu.lib.filter
	cu.rail.Sort, cu.rail.GroupBy, cu.rail.Width, cu.rail.Hidden = "nameAsc", "none", 0, false
	if ui := cu.ui; ui != nil {
		if ui.ShootSort != "" {
			cu.rail.Sort = string(ui.ShootSort)
		}
		if ui.ShootGroup != "" {
			cu.rail.GroupBy = string(ui.ShootGroup)
		}
		cu.rail.Width, cu.rail.Hidden = ui.RailWidth, ui.RailHidden
	}
	// The contact sheet has the window to itself.
	cu.rail.Hidden = cu.rail.Hidden || cu.sheet
	cu.rail.Current = cu.folderPath
	if cu.opening != "" {
		cu.rail.Current = cu.opening
	}
	cu.rail.Opening = cu.opening
	cu.rail.Overview = ""
	switch {
	case cu.ov != nil:
		cu.rail.Overview = cu.ov.key
	case cu.openingSet != "":
		cu.rail.Overview = cu.openingSet
	case cu.set != nil:
		cu.rail.Overview = cu.set.key
	}
	// The open folder's counts and cover as culled here, newer than the
	// backend's listing, and kept for it until a newer listing comes.
	switch {
	case cu.set != nil:
		// Each shoot of a folder's photos, as culled here.
		for path, s := range cu.setLive() {
			cu.lib.live[path] = s
		}
	case cu.folder != 0 && cu.folderPath != "":
		cu.lib.live[strings.ToLower(cu.folderPath)] = cu.liveSummary()
	}
	for i := range cu.rail.Items {
		it := &cu.rail.Items[i]
		if it.Kind == "shoot" || it.Kind == "root" {
			it.Busy = strings.EqualFold(it.Path, cu.opening) || cu.busyFolders[strings.ToLower(it.Path)]
			if s, ok := cu.lib.live[strings.ToLower(it.Path)]; ok {
				it.Picked, it.Rejected, it.ref = s.picked, s.rejected, s.cover
			}
			if it.ref != nil {
				it.Cover = coverKey(it.ref)
				cu.wantCover(it.ref)
			} else if !it.Offline && !it.Outside {
				// Not scanned yet: the preview in its first RAW stands in.
				it.Cover = previewKey(it.Path)
				cu.wantPreview(it.Path)
			}
		}
	}
	_ = cu.c.Patch("grid", cu.rail)
	// The overview follows the library as it changes.
	cu.showOverview()
}

// railItems are the sidebar's rows: each block of the library in its
// stored order, a library folder with its shoots, sorted and bucketed by
// date as the settings say, and the shoots added by hand under the
// folder they share. A filter keeps the rows matching it, and opens the
// headers over them.
func (cu *culler) railItems() []RailItem {
	l := &cu.lib
	ui := cu.ui
	if ui == nil {
		ui = &marrawclient.UISettings{}
	}
	q := strings.ToLower(strings.TrimSpace(l.filter))
	match := func(s string) bool { return q == "" || strings.Contains(strings.ToLower(s), q) }
	open := func(key string) bool {
		return q != "" || ui.RailGroups[strings.ToLower(key)] || !hasKey(ui.RailGroups, strings.ToLower(key))
	}
	alias := func(key, fallback string) string {
		if a := ui.GroupAliases[strings.ToLower(key)]; a != "" {
			return a
		}
		return fallback
	}
	online := func(path string) bool {
		on, ok := l.online[strings.ToLower(path)]
		return on || !ok
	}
	missing := func(path string) bool { return l.status[strings.ToLower(path)].Missing }
	shared := func(path string) int {
		n := 0
		for _, k := range l.links {
			if !k.Expired && strings.EqualFold(k.Path, path) {
				n++
			}
		}
		return n
	}
	blocks := railBlocks(l.roots)
	var items []RailItem
	for bi, b := range blocks {
		first, last := bi == 0, bi == len(blocks)-1
		if b.parent != nil {
			r := *b.parent
			key := "parent:" + r.Path
			name := alias(key, baseName(r.Path))
			on := online(r.Path)
			shoots := sortShoots(l.shoots[strings.ToLower(r.Path)], string(ui.ShootSort))
			var rows []marrawclient.Shoot
			for _, s := range shoots {
				if match(s.Name) {
					rows = append(rows, s)
				}
			}
			if q != "" && len(rows) == 0 && !match(name) {
				continue
			}
			isOpen := open(key)
			items = append(items, RailItem{Key: key, Path: r.Path, Name: name, Kind: "parent", Group: true, Open: isOpen,
				Sub: r.Path, Offline: !on, Missing: !on && missing(r.Path), Count: len(rows), First: first, Last: last})
			if !isOpen {
				continue
			}
			if !on {
				note := "Drive not connected. Its folders come back when you plug it in again."
				if missing(r.Path) {
					note = "This folder was moved or deleted. Right-click it and choose Find folder… to show where it is now."
				}
				items = append(items, RailItem{Key: key + "|note", Kind: "note", Depth: 1, Name: note})
				continue
			}
			shoot := func(s marrawclient.Shoot, depth int) RailItem {
				name := s.Name
				if s.IsSelf {
					name = "Loose photos"
				}
				title, line := shootLabels(name, s.EarliestTakenAt, s.PhotoCount)
				return RailItem{Key: s.Path, Path: s.Path, Name: name, Kind: "shoot", Count: s.PhotoCount, Depth: depth,
					Self: s.IsSelf, Shared: shared(s.Path), Parent: r.Path, Title: title, Line: line,
					Picked: s.Picked, Rejected: s.Rejected, ref: s.Cover, CanSplit: s.Subfolders && !s.IsSelf, Split: s.Split}
			}
			by := string(ui.ShootGroup)
			if by == "" || by == "none" {
				// The folders as they lie on disk: a year's or a
				// client's folder a header over its shoots.
				var walk func(n *shootNode, depth int)
				walk = func(n *shootNode, depth int) {
					for _, k := range n.kids {
						if k.shoot != nil {
							items = append(items, shoot(*k.shoot, depth))
							// A split shoot's own shoots, under it.
							walk(k, depth+1)
							continue
						}
						gk := key + "|dir:" + strings.ToLower(k.rel)
						gOpen := open(gk)
						items = append(items, RailItem{Key: gk, Path: filepath.Join(r.Path, filepath.FromSlash(k.rel)), Name: k.name,
							Kind: "dir", Group: true, Open: gOpen, Count: k.count, Depth: depth, Parent: r.Path})
						if gOpen {
							walk(k, depth+1)
						}
					}
				}
				tree := shootTree(rows, string(ui.ShootSort))
				for _, s := range rows {
					if s.IsSelf {
						items = append(items, shoot(s, 1))
					}
				}
				walk(tree, 1)
				continue
			}
			for _, s := range rows {
				if s.IsSelf {
					items = append(items, shoot(s, 1))
				}
			}
			for _, g := range groupShoots(rows, by) {
				gk := key + "|tg:" + g.id
				n := 0
				for _, s := range g.shoots {
					n += s.PhotoCount
				}
				gOpen := open(gk)
				items = append(items, RailItem{Key: gk, Name: g.label, Kind: "time", Group: true, Open: gOpen, Count: n, Depth: 1, Parent: r.Path})
				if gOpen {
					for _, s := range g.shoots {
						items = append(items, shoot(s, 2))
					}
				}
			}
			continue
		}
		g := b.group
		name := alias(g.parent, baseName(g.parent))
		var rows []marrawclient.LibraryRoot
		for _, r := range g.roots {
			if match(rootName(r)) || match(name) {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		isOpen := open(g.parent)
		items = append(items, RailItem{Key: "group:" + strings.ToLower(g.parent), Path: g.parent, Name: name, Kind: "group",
			Group: true, Open: isOpen, Sub: g.parent, Count: len(g.roots), First: first, Last: last})
		if !isOpen {
			continue
		}
		for _, r := range rows {
			st := l.status[strings.ToLower(r.Path)]
			title, line := shootLabels(rootName(r), st.EarliestTakenAt, r.PhotoCount)
			items = append(items, RailItem{Key: r.Path, Path: r.Path, Name: rootName(r), Kind: "root", Count: r.PhotoCount,
				Depth: 1, Offline: !online(r.Path), Missing: !online(r.Path) && missing(r.Path), Shared: shared(r.Path), Subfolders: r.IncludeSubfolders,
				Title: title, Line: line, Picked: st.Picked, Rejected: st.Rejected, ref: st.Cover})
		}
	}
	// A folder opened that the library does not hold shows at the top (a
	// folder's photos as one are of the library's shoots, so never).
	if cu.folderPath != "" && cu.set == nil && q == "" && !cu.libraryHolds(cu.folderPath) {
		title, line := shootLabels(baseName(cu.folderPath), 0, len(cu.all))
		items = append([]RailItem{{Key: cu.folderPath, Path: cu.folderPath, Name: baseName(cu.folderPath), Kind: "root", Count: len(cu.all), Outside: true,
			Title: title, Line: line}}, items...)
	}
	return items
}

// libraryHolds reports whether the library lists the folder at path: as
// a root, or as a shoot of a library folder.
func (cu *culler) libraryHolds(path string) bool {
	for _, r := range cu.lib.roots {
		if strings.EqualFold(r.Path, path) {
			return true
		}
	}
	for _, shoots := range cu.lib.shoots {
		for _, s := range shoots {
			if strings.EqualFold(s.Path, path) {
				return true
			}
		}
	}
	return false
}

// regroup is the folder at path made one shoot or split while the shoot
// open, folder, is it or lies in it. before is the shape of the shoots of
// the library folder parent, so the change shows when they come again.
type regroup struct {
	parent, path, folder, before string
}

// followRegroup opens the shoot that holds the open folder's photos now,
// once the shoots of the library folder key show the change: the one
// shoot a group was made, or the first of a split folder's shoots. A
// folder still a shoot stays open, read again so its photos follow.
func (cu *culler) followRegroup(key string) {
	g := cu.lib.regroup
	if g == nil || g.parent != key || shootsShape(cu.lib.shoots[key]) == g.before {
		return
	}
	cu.lib.regroup = nil
	if cu.set != nil || cu.opening != "" || !strings.EqualFold(cu.folderPath, g.folder) {
		return
	}
	if cu.libraryHolds(g.folder) {
		// Still a shoot, read now with or without its folders: read it
		// again and show the photos it holds now.
		if strings.EqualFold(g.folder, g.path) {
			if cu.culling {
				cu.cullNext = cu.folderPath
			}
			cu.loadShoot(cu.folderPath)
		}
		return
	}
	sort := ""
	if cu.ui != nil {
		sort = string(cu.ui.ShootSort)
	}
	to := ""
	shoots := sortShoots(cu.lib.shoots[key], sort)
	for _, s := range shoots {
		// The shoot made of a group the folder is in.
		if !s.IsSelf && !s.Split && hasPathUnder(g.folder, s.Path) && len(s.Path) > len(to) {
			to = s.Path
		}
	}
	for _, s := range shoots {
		// Or the first of the folder's own, as the sidebar orders them.
		if to == "" && hasPathUnder(s.Path, g.folder) {
			to = s.Path
		}
	}
	if to == "" {
		return
	}
	if cu.culling {
		cu.openShootCull(to)
		return
	}
	cu.openShoot(to)
}

// shootsShape names which shoots there are and which are split.
func shootsShape(shoots []marrawclient.Shoot) string {
	var b strings.Builder
	for _, s := range shoots {
		fmt.Fprintf(&b, "%s|%v\n", strings.ToLower(s.Path), s.Split)
	}
	return b.String()
}

// hasPathUnder reports whether path lies inside dir, in any case.
func hasPathUnder(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(dir), strings.ToLower(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hasKey reports whether m holds k.
func hasKey(m map[string]bool, k string) bool {
	_, ok := m[k]
	return ok
}

// railBlock is a block of the sidebar: a library folder, or the shoots
// added by hand that share the folder parent.
type railBlock struct {
	parent *marrawclient.LibraryRoot
	group  *rootGroup
}

// rootGroup is the shoots added by hand in one folder.
type rootGroup struct {
	parent string
	roots  []marrawclient.LibraryRoot
}

// railBlocks are the library's blocks in the order their first folders
// are stored in, as marraw's rail orders them.
func railBlocks(roots []marrawclient.LibraryRoot) []railBlock {
	groups := map[string]*rootGroup{}
	var blocks []railBlock
	for i := range roots {
		r := roots[i]
		if r.IsParent {
			blocks = append(blocks, railBlock{parent: &roots[i]})
			continue
		}
		key := strings.ToLower(parentDir(r.Path))
		if g, ok := groups[key]; ok {
			g.roots = append(g.roots, r)
			continue
		}
		g := &rootGroup{parent: parentDir(r.Path), roots: []marrawclient.LibraryRoot{r}}
		groups[key] = g
		blocks = append(blocks, railBlock{group: g})
	}
	return blocks
}

// blockRoots are the roots a block holds, in their order.
func (b railBlock) blockRoots() []marrawclient.LibraryRoot {
	if b.parent != nil {
		return []marrawclient.LibraryRoot{*b.parent}
	}
	return b.group.roots
}

// key is the block's row key.
func (b railBlock) key() string {
	if b.parent != nil {
		return "parent:" + b.parent.Path
	}
	return "group:" + strings.ToLower(b.group.parent)
}

// baseName is the last part of path; a drive keeps its letter.
func baseName(path string) string {
	t := strings.TrimRight(path, `\/`)
	if i := strings.LastIndexAny(t, `\/`); i >= 0 && i < len(t)-1 {
		return t[i+1:]
	}
	if t == "" {
		return path
	}
	return t
}

// parentDir is the folder holding path.
func parentDir(path string) string {
	t := strings.TrimRight(path, `\/`)
	if i := strings.LastIndexAny(t, `\/`); i > 0 {
		return t[:i]
	}
	return t
}

// rootName is a root's alias, or its folder's name.
func rootName(r marrawclient.LibraryRoot) string {
	if r.Alias != "" {
		return r.Alias
	}
	return baseName(r.Path)
}

// sortShoots orders shoots as by says: by name, or by date with those
// with none last; a library folder's own loose photos stay first.
// shootNode is a folder in a library folder's tree of shoots: a shoot, a
// group of them, as a year's or a client's folder, or both, as a split
// shoot with shoots in its subfolders.
type shootNode struct {
	name, rel string
	shoot     *marrawclient.Shoot
	kids      []*shootNode
	// count is the photos under it, and latest the newest capture, for
	// the date sort.
	count  int
	latest int64
}

// shootTree arranges a library folder's shoots by their folders, each
// level in the order by says.
func shootTree(shoots []marrawclient.Shoot, by string) *shootNode {
	root := &shootNode{}
	child := func(n *shootNode, name string) *shootNode {
		for _, k := range n.kids {
			if strings.EqualFold(k.name, name) {
				return k
			}
		}
		rel := name
		if n.rel != "" {
			rel = n.rel + "/" + name
		}
		k := &shootNode{name: name, rel: rel}
		n.kids = append(n.kids, k)
		return k
	}
	for i := range shoots {
		s := &shoots[i]
		if s.IsSelf {
			continue
		}
		n := root
		if s.Group != "" {
			for _, seg := range strings.Split(s.Group, "/") {
				n = child(n, seg)
			}
		}
		child(n, s.Name).shoot = s
	}
	var settle func(n *shootNode)
	settle = func(n *shootNode) {
		if n.shoot != nil {
			n.count, n.latest = n.shoot.PhotoCount, n.shoot.EarliestTakenAt
		}
		for _, k := range n.kids {
			settle(k)
			n.count += k.count
			n.latest = max(n.latest, k.latest)
		}
		slices.SortStableFunc(n.kids, func(a, b *shootNode) int { return compareNodes(a, b, by) })
	}
	settle(root)
	return root
}

// compareNodes orders two folders of one level as by orders shoots: by
// name, or by date, a group by its newest shoot, the undated last.
func compareNodes(a, b *shootNode, by string) int {
	byName := strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	switch by {
	case "nameDesc":
		return -byName
	case "dateAsc", "dateDesc":
		if a.latest == 0 || b.latest == 0 {
			switch {
			case a.latest == b.latest:
				return byName
			case a.latest == 0:
				return 1
			}
			return -1
		}
		d := a.latest - b.latest
		if by == "dateDesc" {
			d = -d
		}
		switch {
		case d < 0:
			return -1
		case d > 0:
			return 1
		}
		return byName
	}
	return byName
}

func sortShoots(shoots []marrawclient.Shoot, by string) []marrawclient.Shoot {
	out := slices.Clone(shoots)
	byName := func(a, b marrawclient.Shoot) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	}
	slices.SortStableFunc(out, func(a, b marrawclient.Shoot) int {
		if a.IsSelf != b.IsSelf {
			if a.IsSelf {
				return -1
			}
			return 1
		}
		switch by {
		case "nameDesc":
			return -byName(a, b)
		case "dateAsc", "dateDesc":
			if a.EarliestTakenAt == 0 || b.EarliestTakenAt == 0 {
				if a.EarliestTakenAt == b.EarliestTakenAt {
					return byName(a, b)
				}
				if a.EarliestTakenAt == 0 {
					return 1
				}
				return -1
			}
			d := a.EarliestTakenAt - b.EarliestTakenAt
			if by == "dateDesc" {
				d = -d
			}
			if d != 0 {
				return int(max(-1, min(1, d)))
			}
			return byName(a, b)
		}
		return byName(a, b)
	})
	return out
}

// timeGroup is the shoots of one year, month or day.
type timeGroup struct {
	id, label string
	year      int
	shoots    []marrawclient.Shoot
}

// groupShoots buckets sorted shoots by when they were taken, as by says,
// in their order, the undated last; a library folder's own loose photos
// are left out, as they show above the buckets.
func groupShoots(sorted []marrawclient.Shoot, by string) []timeGroup {
	var groups []timeGroup
	index := map[string]int{}
	var none *timeGroup
	for _, s := range sorted {
		if s.IsSelf {
			continue
		}
		if s.EarliestTakenAt == 0 {
			if none == nil {
				none = &timeGroup{id: "no-date", label: "No date"}
			}
			none.shoots = append(none.shoots, s)
			continue
		}
		t := time.Unix(s.EarliestTakenAt, 0)
		id, label := fmt.Sprint(t.Year()), fmt.Sprint(t.Year())
		switch by {
		case "month":
			id, label = t.Format("2006-01"), t.Format("January 2006")
		case "day":
			id, label = t.Format("2006-01-02"), t.Format("Mon 2 Jan 2006")
		}
		i, ok := index[id]
		if !ok {
			i = len(groups)
			index[id] = i
			groups = append(groups, timeGroup{id: id, label: label, year: t.Year()})
		}
		groups[i].shoots = append(groups[i].shoots, s)
	}
	if none != nil {
		groups = append(groups, *none)
	}
	return groups
}

// railToggle opens or closes the header key, as the user's choice kept.
func (cu *culler) railToggle(key string) {
	it, ok := cu.railItem(key)
	if !ok || !it.Group {
		return
	}
	stored := key
	if it.Kind == "group" {
		stored = it.Path
	}
	lower := strings.ToLower(stored)
	if cu.ui != nil {
		if cu.ui.RailGroups == nil {
			cu.ui.RailGroups = map[string]bool{}
		}
		if it.Open {
			cu.ui.RailGroups[lower] = false
		} else {
			delete(cu.ui.RailGroups, lower)
		}
	}
	open := !it.Open
	cu.railChanged()
	cu.call("The sidebar could not keep that", func(ctx context.Context) error {
		return cu.api.Settings.SetRailGroupOpen(ctx, lower, open)
	}, nil)
}

// railItem is the row key.
func (cu *culler) railItem(key string) (RailItem, bool) {
	for _, it := range cu.rail.Items {
		if it.Key == key {
			return it, true
		}
	}
	return RailItem{}, false
}

// railOrder sorts and buckets the shoots, or collapses the years past.
func (cu *culler) railOrder(in RailOrder) {
	if cu.ui == nil {
		return
	}
	ui := cu.ui
	switch {
	case in.CollapseYears:
		if ui.RailGroups == nil {
			ui.RailGroups = map[string]bool{}
		}
		year := time.Now().Year()
		var writes []func(ctx context.Context) error
		for _, r := range cu.lib.roots {
			if !r.IsParent {
				continue
			}
			sorted := sortShoots(cu.lib.shoots[strings.ToLower(r.Path)], string(ui.ShootSort))
			for _, g := range groupShoots(sorted, string(ui.ShootGroup)) {
				if g.year == 0 {
					continue
				}
				key := strings.ToLower("parent:" + r.Path + "|tg:" + g.id)
				open := g.year >= year
				if open {
					delete(ui.RailGroups, key)
				} else {
					ui.RailGroups[key] = false
				}
				writes = append(writes, func(ctx context.Context) error { return cu.api.Settings.SetRailGroupOpen(ctx, key, open) })
			}
		}
		cu.call("The sidebar could not keep that", func(ctx context.Context) error {
			for _, w := range writes {
				if err := w(ctx); err != nil {
					return err
				}
			}
			return nil
		}, nil)
	case in.Sort != "":
		ui.ShootSort = marrawclient.ShootSort(in.Sort)
		cu.call("The sidebar could not keep that", func(ctx context.Context) error {
			return cu.api.Settings.SetShootSort(ctx, marrawclient.ShootSort(in.Sort))
		}, nil)
	case in.GroupBy != "":
		ui.ShootGroup = marrawclient.ShootGroup(in.GroupBy)
		cu.call("The sidebar could not keep that", func(ctx context.Context) error {
			return cu.api.Settings.SetShootGroup(ctx, marrawclient.ShootGroup(in.GroupBy))
		}, nil)
	}
	cu.railChanged()
}

// saveRoots stores the library's folders as roots; the backend's
// listing then shows them.
func (cu *culler) saveRoots(roots []marrawclient.LibraryRoot, after func()) {
	cu.lib.roots = roots
	cu.railChanged()
	cu.call("The library could not be saved", func(ctx context.Context) error {
		return cu.api.Library.SetLibraryRoots(ctx, roots)
	}, after)
}

// railAct does what a row's menu offers.
func (cu *culler) railAct(in RailAct) {
	it, ok := cu.railItem(in.Key)
	if !ok {
		return
	}
	roots := cu.lib.roots
	switch in.Act {
	case "open":
		cu.openShoot(it.Path)
	case "cull":
		cu.openShootCull(it.Path)
	case "newWindow":
		if cu.newWindow != nil {
			cu.newWindow(it.Path)
		}
	case "reveal":
		if err := cu.c.Reveal(it.Path); err != nil {
			cu.fail("The folder could not be shown", err)
		}
	case "rescan":
		cu.rescan([]string{it.Path})
	case "rescanAll":
		var paths []string
		for _, r := range cu.rail.Items {
			if (r.Kind == "shoot" || r.Kind == "root") && (strings.EqualFold(r.Parent, it.Path) && it.Kind == "parent" ||
				it.Kind == "group" && strings.EqualFold(parentDir(r.Path), it.Path)) {
				paths = append(paths, r.Path)
			}
		}
		if it.Kind == "parent" {
			paths = nil
			for _, s := range cu.lib.shoots[strings.ToLower(it.Path)] {
				paths = append(paths, s.Path)
			}
		}
		cu.rescan(paths)
	case "refresh":
		cu.rootsIn(cu.lib.roots)
	case "fullres":
		cu.call("The folder could not be rendered", func(ctx context.Context) error {
			_, err := cu.api.Library.RenderFolderFullres(ctx, it.Path)
			return err
		}, func() { cu.notify("Rendering " + it.Name + " at 1:1 in the background") })
	case "subfolders":
		next := slices.Clone(roots)
		for i := range next {
			if strings.EqualFold(next[i].Path, it.Path) {
				next[i].IncludeSubfolders = !next[i].IncludeSubfolders
			}
		}
		cu.saveRoots(next, func() { cu.rescan([]string{it.Path}) })
	case "up", "down":
		blocks := railBlocks(roots)
		i := slices.IndexFunc(blocks, func(b railBlock) bool { return b.key() == in.Key })
		j := i - 1
		if in.Act == "down" {
			j = i + 1
		}
		if i < 0 || j < 0 || j >= len(blocks) {
			return
		}
		blocks[i], blocks[j] = blocks[j], blocks[i]
		var next []marrawclient.LibraryRoot
		for _, b := range blocks {
			next = append(next, b.blockRoots()...)
		}
		cu.saveRoots(next, nil)
	case "find":
		cu.findFolder(it)
	case "remove":
		next := slices.DeleteFunc(slices.Clone(roots), func(r marrawclient.LibraryRoot) bool {
			switch it.Kind {
			case "group":
				return !r.IsParent && strings.EqualFold(parentDir(r.Path), it.Path)
			default:
				return strings.EqualFold(r.Path, it.Path)
			}
		})
		cu.saveRoots(next, nil)
		cu.notify("Removed " + it.Name + " from the library. The files stay on disk.")
	case "folderOne", "folderSplit", "folderDefault":
		// How a folder shows: one shoot, its folders as shoots of their
		// own, or as the rules have it.
		mode := map[string]string{"folderOne": "one", "folderSplit": "split"}[in.Act]
		next := slices.Clone(roots)
		for i := range next {
			if !strings.EqualFold(next[i].Path, it.Parent) {
				continue
			}
			m := maps.Clone(next[i].FolderModes)
			if m == nil {
				m = map[string]string{}
			}
			k := strings.ToLower(filepath.Clean(it.Path))
			if mode == "" {
				delete(m, k)
			} else {
				m[k] = mode
			}
			next[i].FolderModes = m
		}
		if cu.set == nil && (strings.EqualFold(cu.folderPath, it.Path) || hasPathUnder(cu.folderPath, it.Path)) {
			key := strings.ToLower(it.Parent)
			cu.lib.regroup = &regroup{parent: key, path: it.Path, folder: cu.folderPath, before: shootsShape(cu.lib.shoots[key])}
		}
		cu.saveRoots(next, nil)
	case "hide":
		next := slices.Clone(roots)
		for i := range next {
			if strings.EqualFold(next[i].Path, it.Parent) {
				next[i].ExcludedChildren = append(slices.Clone(next[i].ExcludedChildren), strings.ToLower(it.Path))
			}
		}
		cu.saveRoots(next, nil)
		cu.notify("Hid " + it.Name + ". The files stay on disk.")
	case "revoke":
		var ids []string
		for _, l := range cu.lib.links {
			if !l.Expired && strings.EqualFold(l.Path, it.Path) {
				ids = append(ids, l.ID)
			}
		}
		cu.call("Sharing could not be withdrawn", func(ctx context.Context) error {
			for _, id := range ids {
				if err := cu.api.Share.RevokeLink(ctx, id); err != nil {
					return err
				}
			}
			return nil
		}, func() { cu.notify(it.Name + " is no longer shared") })
	case "share":
		cu.askShare(it.Path, it.Name)
	case "addLib":
		cu.addFolders([]string{it.Path})
	case "rename", "renameDisk":
		title, label, value := "Rename "+it.Name, "Name in the sidebar", it.Name
		if in.Act == "renameDisk" {
			title, label, value = "Rename on disk", "New folder name on disk", baseName(it.Path)
		}
		cu.askPrompt(PromptAsk{Kind: in.Act, Key: in.Key, Title: title, Label: label, Value: value, OK: "Rename"})
	}
}

// railRenamed takes a new name for a row: an alias in the sidebar, or a
// new name for its folder on disk.
func (cu *culler) railRenamed(kind, key, name string) {
	it, ok := cu.railItem(key)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return
	}
	switch {
	case kind == "renameDisk":
		active := strings.EqualFold(it.Path, cu.folderPath)
		cu.call("The folder could not be renamed", func(ctx context.Context) error {
			res, err := cu.api.Library.RenameFolderOnDisk(ctx, it.Path, name)
			if err == nil && active {
				cu.onDo(func() { cu.folderPath = res.Path; cu.railChanged() })
			}
			return err
		}, func() { cu.notify("Renamed on disk to " + name) })
	case it.Kind == "root":
		next := slices.Clone(cu.lib.roots)
		for i := range next {
			if strings.EqualFold(next[i].Path, it.Path) {
				next[i].Alias = name
				if name == baseName(it.Path) {
					next[i].Alias = ""
				}
			}
		}
		cu.saveRoots(next, nil)
	case it.Kind == "parent" || it.Kind == "group":
		stored := strings.ToLower(it.Path)
		if it.Kind == "parent" {
			stored = strings.ToLower("parent:" + it.Path)
		}
		alias := name
		if name == baseName(it.Path) {
			alias = ""
		}
		if cu.ui != nil {
			if cu.ui.GroupAliases == nil {
				cu.ui.GroupAliases = map[string]string{}
			}
			if alias == "" {
				delete(cu.ui.GroupAliases, stored)
			} else {
				cu.ui.GroupAliases[stored] = alias
			}
		}
		cu.railChanged()
		cu.call("The name could not be kept", func(ctx context.Context) error {
			return cu.api.Settings.SetGroupAlias(ctx, stored, alias)
		}, nil)
	}
}

// rescan reads the folders at paths again for photos new or gone.
func (cu *culler) rescan(paths []string) {
	if len(paths) == 0 {
		return
	}
	cu.notify(fmt.Sprintf("Rescanning %d folder%s", len(paths), map[bool]string{true: "", false: "s"}[len(paths) == 1]))
	go func() {
		for _, p := range paths {
			ctx, cancel := context.WithTimeout(cu.ctx, 5*time.Minute)
			_, err := cu.api.Library.OpenFolder(ctx, p)
			cancel()
			if err != nil && cu.ctx.Err() == nil {
				log.Printf("rescan %s: %v", p, err)
			}
		}
	}()
}

// railWidth keeps the sidebar's width, as its edge is let go.
//
// The bounds are the rail's own, which are the ones the server keeps.
// A width that has not moved is not saved again: the sidebar reports
// its width whenever a gesture ends on it, and saving the same number
// over and over only risks an error about it.
func (cu *culler) railWidth(px int) {
	px = max(railMin, min(px, railMax))
	if cu.ui != nil {
		if cu.ui.RailWidth == px {
			return
		}
		cu.ui.RailWidth = px
	}
	cu.railChanged()
	cu.call("The sidebar's width could not be kept", func(ctx context.Context) error {
		return cu.api.Settings.SetRailWidth(ctx, px)
	}, nil)
}

// railHide puts the sidebar away, or brings it back.
func (cu *culler) railHide(hidden bool) {
	if cu.sheet {
		// The contact sheet keeps the window to itself; the setting is
		// the library's.
		return
	}
	if cu.ui != nil {
		cu.ui.RailHidden = hidden
	}
	cu.railChanged()
	cu.call("The sidebar could not keep that", func(ctx context.Context) error {
		return cu.api.Settings.SetRailHidden(ctx, hidden)
	}, nil)
}

// addFolders adds the folders at paths to the library, each as a library
// folder or a shoot as its photos lie, and opens the last.
func (cu *culler) addFolders(paths []string) {
	// A folder inside a library folder is in the library already: added
	// as a folder of its own, it would show twice. Its shoot opens, where
	// it is one.
	paths = slices.DeleteFunc(slices.Clone(paths), func(p string) bool {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			// A photo dragged in: folders join the library, files not.
			cu.notify(baseName(p) + " is a file: drop a folder to add it to the library")
			return true
		}
		for _, r := range cu.lib.roots {
			if r.IsParent && hasPathUnder(p, r.Path) {
				cu.notify(baseName(p) + " is in the library already")
				if cu.libraryHolds(p) {
					cu.openShoot(p)
				}
				return true
			}
		}
		return false
	})
	if len(paths) == 0 {
		return
	}
	cu.notify("Adding to the library…")
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 2*time.Minute)
		defer cancel()
		var first error
		for _, p := range paths {
			if err := addLibraryFolder(ctx, cu.api, p); err != nil && first == nil {
				first = err
			}
		}
		cu.onDo(func() {
			if first != nil {
				cu.fail("The folder could not be added", first)
				return
			}
			n := len(paths)
			cu.notify(fmt.Sprintf("Added %d folder%s to the library", n, map[bool]string{true: "", false: "s"}[n == 1]))
		})
	}()
}

// addLibraryFolder adds dir to the library: as a library folder, its
// subfolders its shoots, where its photos are in subfolders, and as a shoot
// of its own where they are in it. A folder the library holds already is
// set right the same way.
func addLibraryFolder(ctx context.Context, api *marrawclient.Client, dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	roots, err := api.Library.GetLibraryRoots(ctx)
	if err != nil {
		return err
	}
	parent := true
	own, err1 := api.Library.CountRaws(ctx, []string{dir}, false)
	all, err2 := api.Library.CountRaws(ctx, []string{dir}, true)
	if err1 == nil && err2 == nil && own != nil && all != nil && own.Files > 0 && all.Files == own.Files {
		parent = false
	}
	for i, r := range roots {
		if strings.EqualFold(filepath.Clean(r.Path), dir) {
			if r.IsParent == parent {
				return nil
			}
			roots[i].IsParent = parent
			return api.Library.SetLibraryRoots(ctx, roots)
		}
	}
	return api.Library.SetLibraryRoots(ctx, append(roots, marrawclient.LibraryRoot{Path: dir, IsParent: parent}))
}

// loadSettings reads marraw's settings, for the folders' remembered views,
// and shows the folder open as its own says.
func (cu *culler) loadSettings() {
	ui, err := cu.api.Settings.GetUISettings(cu.ctx)
	select {
	case cu.do <- func() {
		if err != nil {
			log.Printf("settings: %v", err)
			return
		}
		cu.ui = ui
		if cu.restore {
			// The folder open when marraw last closed, unless one opens
			// already. One that fails to open stays remembered: its drive
			// plugged in again, the next start lands there. Its failing
			// is no news, so the library shows without a word: the rail
			// says where it went.
			cu.restore = false
			if ui.LastFolder != "" && cu.folder == 0 && cu.opening == "" {
				cu.restoring = ui.LastFolder
				cu.openShoot(ui.LastFolder)
				if cu.opening != ui.LastFolder {
					cu.restoring = ""
				}
			}
		}
		if v := viewFor(ui, cu.folderPath); cu.folder != 0 && v != cu.libView && !cu.culling {
			cu.libView = v
			cu.applyView()
			_ = cu.c.Update("grid", cu.gridState())
		}
		cu.railChanged()
	}:
	case <-cu.ctx.Done():
	}
}

// railHas reports whether the sidebar lists path.
func (cu *culler) railHas(path string) bool {
	for _, it := range cu.rail.Items {
		if strings.EqualFold(it.Path, path) {
			return true
		}
	}
	return false
}

// openShoot opens the folder at path in the grid in place of the one
// showing, the cull view closing first if it is open.
func (cu *culler) openShoot(path string) {
	cu.openingSet = ""
	if strings.EqualFold(path, cu.folderPath) && cu.set == nil {
		return
	}
	if it, ok := cu.railItem(path); ok && it.Missing {
		cu.notify(it.Name + " is no longer in " + parentDir(it.Path) + ". If you moved it, right-click it and choose Find folder…")
		return
	} else if ok && it.Offline {
		cu.notify(it.Name + " is offline. Reconnect its drive and it comes back on its own.")
		return
	}
	cu.loadShoot(path)
}

// findFolder asks where the folder of row it is now, and has the library
// read it there. Its ratings and edits come along in its marraw.json.
func (cu *culler) findFolder(it RailItem) {
	opts := chooseFolder("Where is " + it.Name + " now?")
	opts.Folder = parentDir(it.Path)
	go func() {
		paths, err := cu.c.ChooseFiles(cu.ctx, opts)
		if err != nil || len(paths) == 0 {
			return
		}
		cu.onDo(func() { cu.relocate(it.Path, paths[0]) })
	}()
}

// relocate points the library's folder at from to the folder at to,
// where it was moved. A folder the library holds there already keeps
// its place, and the old entry goes.
func (cu *culler) relocate(from, to string) {
	to = filepath.Clean(to)
	i := slices.IndexFunc(cu.lib.roots, func(r marrawclient.LibraryRoot) bool { return strings.EqualFold(r.Path, from) })
	if i < 0 || strings.EqualFold(from, to) {
		return
	}
	if st, err := os.Stat(to); err != nil || !st.IsDir() {
		cu.notify(baseName(to) + " is not a folder")
		return
	}
	old := cu.lib.roots[i]
	held := slices.ContainsFunc(cu.lib.roots, func(r marrawclient.LibraryRoot) bool {
		return strings.EqualFold(r.Path, to) || r.IsParent && !old.IsParent && hasPathUnder(to, r.Path)
	})
	next := slices.Clone(cu.lib.roots)
	if held {
		next = slices.Delete(next, i, i+1)
		cu.saveRoots(next, func() {
			cu.notify(baseName(to) + " is in the library already. The old entry is gone.")
			if !old.IsParent && cu.libraryHolds(to) {
				cu.openShoot(to)
			}
		})
		return
	}
	next[i].Path = to
	// How the folders in a library folder show is kept by their paths,
	// which moved with it.
	if len(old.FolderModes) > 0 {
		modes := make(map[string]string, len(old.FolderModes))
		for k, m := range old.FolderModes {
			if rel, err := filepath.Rel(strings.ToLower(filepath.Clean(from)), k); err == nil && !strings.HasPrefix(rel, "..") {
				k = strings.ToLower(filepath.Join(to, rel))
			}
			modes[k] = m
		}
		next[i].FolderModes = modes
	}
	cu.saveRoots(next, func() {
		cu.notify("Found " + rootName(old) + " in " + parentDir(to))
		if !old.IsParent {
			cu.openShoot(to)
		}
	})
}

// loadShoot reads the folder at path and shows it in the grid, the one
// showing too, as openShoot does.
func (cu *culler) loadShoot(path string) {
	cu.opening = path
	cu.railChanged()
	go func() {
		info, err := cu.api.Library.OpenFolder(cu.ctx, path)
		var photos []marrawclient.Photo
		if err == nil {
			photos, err = cu.api.Library.ListPhotos(cu.ctx, info.FolderID)
		}
		select {
		case cu.do <- func() {
			quiet := strings.EqualFold(cu.restoring, path)
			cu.restoring = ""
			if cu.opening != path {
				// Another shoot was chosen meanwhile.
				return
			}
			cu.opening = ""
			if err != nil {
				cu.railChanged()
				if quiet {
					log.Printf("reopening %s: %v", path, err)
					return
				}
				cu.fail("The folder could not be opened", err)
				return
			}
			cu.showFolder(info.FolderID, path, photos)
			if cu.cullNext == path {
				cu.cullNext = ""
				if len(cu.photos) > 0 {
					cu.openCull(0)
				}
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// openShootCull opens the folder at path in the cull view.
func (cu *culler) openShootCull(path string) {
	if strings.EqualFold(path, cu.folderPath) {
		if !cu.culling && len(cu.photos) > 0 {
			cu.openCull(max(0, cu.cursor))
		}
		return
	}
	cu.cullNext = path
	cu.openShoot(path)
}

// showFolder puts folder id's photos in the grid: what was under way for
// the last folder stops, and the grid's tiles go and the new ones come.
func (cu *culler) showFolder(id int64, path string, photos []marrawclient.Photo) {
	if cu.culling {
		cu.leaveCull()
	}
	for _, stop := range []func(){cu.gridStop, cu.load, cu.warm, cu.stopLive} {
		if stop != nil {
			stop()
		}
	}
	cu.gridStop, cu.load, cu.warm, cu.stopLive = nil, nil, nil, nil
	cu.stopTiles()
	if cu.sheet && id != cu.folder {
		// Another shoot: the contact sheet was this one's.
		cu.sheet = false
	}
	cu.folder, cu.folderPath = id, path
	if id > 0 && !cu.secondary {
		// Opened again on the next start, as marraw does.
		go func() {
			if err := cu.api.Settings.SetLastFolder(cu.ctx, path); err != nil && cu.ctx.Err() == nil {
				log.Printf("last folder: %v", err)
			}
		}()
	}
	cu.stopSetRead()
	cu.set = nil
	cu.setAll(photos)
	cu.libView = viewFor(cu.ui, path)
	cu.applyView()
	clear(cu.thumbsWanted)
	cu.at, cu.gen, cu.sel, cu.cursor = 0, cu.gen+1, nil, -1
	cu.stopLive = cu.followFolder()
	_ = cu.c.Update("grid", cu.gridState())
	cu.railChanged()
	cu.ensureFolderMaps()
}

// AskAddFolder opens the dialog for adding a folder to the library.
type AskAddFolder struct{}
