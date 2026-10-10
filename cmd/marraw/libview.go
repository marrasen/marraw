package main

import (
	"log"
	"sort"
	"strings"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// LibView is how the grid shows a folder, as marraw's filter bar sets it:
// the order, and which photos.
type LibView struct {
	// Sort is one of marraw's orders: "captureAsc", "captureDesc",
	// "nameAsc" or "nameDesc".
	Sort string
	// MinRating shows photos rated at least so many stars; nought shows
	// all.
	MinRating int
	// Flag is "all", "pick", "not-excluded" or "exclude".
	Flag string
	// Soft shows only the soft photos, Blinks only those with eyes
	// closed, and Collapse only each burst's sharpest frame, as marraw's
	// filter bar's toggles do, for a folder while it is open.
	Soft, Blinks, Collapse bool
	// Gap groups the photos wherever more than so many minutes pass
	// between frames, nought for no groups.
	Gap int
}

// SetLibView is the grid's filter bar changed.
type SetLibView struct{ View LibView }

// defaultView is a folder's view where nothing is remembered.
func defaultView(sortBy string, gap int) LibView {
	if sortBy == "" {
		sortBy = "captureAsc"
	}
	return LibView{Sort: sortBy, Flag: "all", Gap: gap}
}

// shows reports whether v shows photo p, whose shoot's aids are a.
func (v LibView) shows(p marrawclient.Photo, a aidsOf) bool {
	if p.Rating < v.MinRating {
		return false
	}
	pa := a.of(p)
	if v.Soft && !pa.Soft || v.Blinks && !pa.Eyes {
		return false
	}
	if v.Collapse && p.GroupID != nil {
		if b := a.bursts[*p.GroupID]; b != nil && b.lead() != p.ID {
			return false
		}
	}
	switch v.Flag {
	case "pick":
		return p.Flag == "pick"
	case "not-excluded":
		return p.Flag != "exclude"
	case "exclude":
		return p.Flag == "exclude"
	}
	return true
}

// less orders photos as v sorts them.
func (v LibView) less(a, b marrawclient.Photo) bool {
	switch v.Sort {
	case "captureDesc":
		if a.TakenAt != b.TakenAt {
			return a.TakenAt > b.TakenAt
		}
		return a.FileName > b.FileName
	case "nameAsc":
		return strings.ToLower(a.FileName) < strings.ToLower(b.FileName)
	case "nameDesc":
		return strings.ToLower(a.FileName) > strings.ToLower(b.FileName)
	}
	if a.TakenAt != b.TakenAt {
		return a.TakenAt < b.TakenAt
	}
	return a.FileName < b.FileName
}

// syncAll copies what changed of the photos showing into the folder's
// whole list, which the view is made from.
func (cu *culler) syncAll() {
	for _, p := range cu.photos {
		if i, ok := cu.allIndex[p.ID]; ok {
			cu.all[i] = p
		}
	}
}

// viewList is the photos the view shows, from the folder's whole list,
// filtered and sorted.
func (cu *culler) viewList() []marrawclient.Photo {
	cu.syncAll()
	cu.aids = newAids(cu.all, cu.libView)
	cu.aids.off = cu.featuresOff()
	var out []marrawclient.Photo
	for _, p := range cu.all {
		if cu.libView.shows(p, cu.aids) {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return cu.libView.less(out[i], out[j]) })
	return out
}

// applyView makes the photos showing from the folder's whole list, as the
// view filters and sorts them.
func (cu *culler) applyView() { cu.showList(cu.viewList()) }

// showList makes out the photos showing.
func (cu *culler) showList(out []marrawclient.Photo) {
	cu.photos = out
	cu.index = make(map[int64]int, len(out))
	for i, p := range out {
		cu.index[p.ID] = i
	}
	cu.viewSeq++
}

// refilter takes the photos that no longer pass the view's filter out of
// it, and those that pass it again back in, at once, as marraw does: a
// photo rejected under Not rejected goes. The grid's tiles make room, the
// keyboard keeps to its photo or, the photo gone, to the one in its place,
// and the cull view shows that one.
func (cu *culler) refilter() {
	if cu.folder == 0 {
		return
	}
	out := cu.viewList()
	same := len(out) == len(cu.photos)
	for i := 0; same && i < len(out); i++ {
		same = out[i].ID == cu.photos[i].ID
	}
	if same {
		return
	}
	// What the keyboard is on and what is selected, by photo.
	at, cursor := cu.at, cu.cursor
	var atID, cursorID int64
	if at >= 0 && at < len(cu.photos) {
		atID = cu.photos[at].ID
	}
	if cursor >= 0 && cursor < len(cu.photos) {
		cursorID = cu.photos[cursor].ID
	}
	selected := map[int64]bool{}
	for _, i := range cu.targets() {
		selected[cu.photos[i].ID] = true
	}
	cu.showList(out)
	slot := func(id int64, was int) int {
		if i, ok := cu.index[id]; ok {
			return i
		}
		return min(was, len(out)-1)
	}
	cu.cursor = -1
	if cursor >= 0 {
		cu.cursor = slot(cursorID, cursor)
	}
	cu.sel = nil
	for i, p := range out {
		if selected[p.ID] {
			cu.sel = append(cu.sel, [2]int{i, i + 1})
		}
	}
	if len(cu.sel) == 0 && cu.cursor >= 0 {
		cu.sel = [][2]int{{cu.cursor, cu.cursor + 1}}
	}
	_ = cu.c.Update("grid", cu.gridState())
	_ = cu.c.Patch("grid", GridSel{Folder: cu.folder, Runs: cu.sel, Cursor: cu.cursor})
	if !cu.culling {
		return
	}
	if len(out) == 0 {
		cu.leaveCull()
		return
	}
	i := slot(atID, at)
	if i < len(out) && out[i].ID == atID {
		// The photo showing stays: only its place changed.
		cu.at = i
		cu.showCull()
		cu.loadStrip()
		return
	}
	cu.at, cu.load = -1, nil
	cu.goTo(i)
}

// setAll takes a folder's whole list.
func (cu *culler) setAll(photos []marrawclient.Photo) {
	cu.all = photos
	cu.allIndex = make(map[int64]int, len(photos))
	for i, p := range photos {
		cu.allIndex[p.ID] = i
	}
	cu.photos = nil
}

// setView shows the folder as v: the grid's tiles go and come back in the
// new order, the cull view closing first. The view is remembered for the
// folder, as marraw remembers it.
func (cu *culler) setView(v LibView) {
	if v == cu.libView {
		return
	}
	if cu.culling {
		cu.leaveCull()
	}
	gapWas := cu.libView.Gap
	cu.libView = v
	cu.applyView()
	cu.at, cu.sel, cu.cursor = 0, nil, -1
	_ = cu.c.Update("grid", cu.gridState())
	path, view := strings.ToLower(cu.folderPath), v
	// Kept here too, for coming back to the folder while the window is open.
	if ui := cu.ui; ui != nil {
		if ui.FolderViews == nil {
			ui.FolderViews = map[string]marrawclient.FolderView{}
		}
		srt, flag := marrawclient.LibrarySort(v.Sort), marrawclient.FlagFilter(v.Flag)
		ui.FolderViews[path] = marrawclient.FolderView{MinRating: &view.MinRating, FlagFilter: &flag, LibrarySort: &srt, GapMinutes: &view.Gap}
		if v.Gap != gapWas {
			ui.GapMinutes = v.Gap
		}
	}
	go func() {
		// A gap chosen is the one new folders take too, as in marraw.
		if view.Gap != gapWas {
			if err := cu.api.Settings.SetGapMinutes(cu.ctx, view.Gap); err != nil {
				log.Printf("gap: %v", err)
			}
		}
		srt := marrawclient.LibrarySort(view.Sort)
		flag := marrawclient.FlagFilter(view.Flag)
		patch := marrawclient.FolderView{MinRating: &view.MinRating, FlagFilter: &flag, LibrarySort: &srt, GapMinutes: &view.Gap}
		if err := cu.api.Settings.SetFolderView(cu.ctx, path, patch); err != nil {
			log.Printf("view: %v", err)
		}
	}()
}

// viewFor is folder path's remembered view, from marraw's settings.
func viewFor(ui *marrawclient.UISettings, path string) LibView {
	if ui == nil {
		return defaultView("", defaultGap)
	}
	v := defaultView(string(ui.LibrarySort), ui.GapMinutes)
	fv, ok := ui.FolderViews[strings.ToLower(path)]
	if !ok {
		return v
	}
	if fv.MinRating != nil {
		v.MinRating = *fv.MinRating
	}
	if fv.FlagFilter != nil && *fv.FlagFilter != "" {
		v.Flag = string(*fv.FlagFilter)
	}
	if fv.LibrarySort != nil && *fv.LibrarySort != "" {
		v.Sort = string(*fv.LibrarySort)
	}
	if fv.GapMinutes != nil {
		v.Gap = *fv.GapMinutes
	}
	return v
}
