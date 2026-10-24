package main

import (
	"context"
	"path/filepath"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// The vocabulary of the library grid, between the window and the culler.
type (
	// GridState is what the grid shows: the folder's photos, in order.
	GridState struct {
		// FolderID is the folder's, nought for none chosen yet, and Folder
		// its name.
		FolderID int64
		Folder   string
		Photos   []GridPhoto
		// View is how the photos are filtered and sorted, ViewSeq which
		// making of it this is, and Total how many the folder holds.
		View    LibView
		ViewSeq int
		Total   int
		// Groups are the runs of photos taken close together, as View's
		// Gap groups them, or none.
		Groups []GapGroup
		// Off are the culling aids turned off, by their ids, and Crop
		// says the tiles fill their cells, cropped.
		Off  map[string]bool
		Crop bool
		// Natural lays the tiles out in rows of their own shapes.
		Natural bool
		// Presets are the presets the batch card can lay over a
		// selection.
		Presets []PresetCard
		// Sheet says the grid is Cull's contact sheet: the shoot's photos
		// as a sheet, the sidebar away, until Esc goes back to the photo.
		Sheet bool
		// Set says the photos are a folder's, of Shoots shoots, grouped
		// a shoot each.
		Set    bool
		Shoots int
		// Reading says the backend is reading shoots of the set not read
		// yet, and Unread how many are left unread.
		Reading bool
		Unread  int
		// Path is the folder's path, for the status bar, and Aside says
		// the info panel is out.
		Path  string
		Aside bool
	}
	// AsideIn shows the info panel, or puts it away.
	AsideIn struct{ On bool }
	// Covered says the cull view covers the grid, or no longer does.
	Covered struct{ On bool }
	// GridPhoto is one photo as a tile shows it.
	GridPhoto struct {
		ID     int64
		Name   string
		Aspect float32
		Rating int
		Flag   string
		Aids   Aids
	}
	// ThumbIn is the small picture of the photo at Index, arrived, or,
	// with no Img, let go.
	ThumbIn struct {
		Folder int64
		Index  int
		Img    *paint.Image
	}
	// PhotoMarked is the rating and the flag of the photo at Index, changed.
	PhotoMarked struct {
		Folder int64
		Index  int
		Rating int
		Flag   string
	}
	// GridSel selects Runs of the grid's tiles, with its keyboard on
	// Cursor, as a filter takes photos out from among them.
	GridSel struct {
		Folder int64
		Runs   [][2]int
		Cursor int
	}
	// GridAt puts the grid's keyboard on the photo at Index, in view, as
	// the cull view steps through the folder over it.
	GridAt struct {
		Folder int64
		Index  int
	}

	// NeedThumbs asks for the small pictures of the tiles built, Count of
	// them from First.
	NeedThumbs struct{ First, Count int }
	// Selected is the grid's selection, and the tile its keyboard is on.
	Selected struct {
		Runs   [][2]int
		Cursor int
	}
	// OpenCull opens the cull view on the photo at Index, and LeaveCull
	// goes back to the grid.
	OpenCull  struct{ Index int }
	LeaveCull struct{}
	// ContactSheet leaves the photo for Cull's contact sheet, as G does.
	ContactSheet struct{}
)

// gridState is the grid's state: every photo in the folder.
func (cu *culler) gridState() GridState {
	st := GridState{FolderID: cu.folder, Folder: filepath.Base(cu.folderPath), View: cu.libView, ViewSeq: cu.viewSeq, Total: len(cu.all)}
	if cu.folder == 0 {
		st.Folder = ""
	}
	st.Groups = gapGroups(cu.photos, cu.libView.Gap, cu.libView.Sort)
	if cu.set != nil {
		// A folder's photos: a group a shoot, the folder's name over them.
		st.Groups = cu.shootGroups(cu.photos)
		st.Folder, st.Shoots, st.Set = cu.set.title, len(cu.set.shoots), true
		st.Reading, st.Unread = cu.set.stopRead != nil && len(cu.set.unread) > 0, len(cu.set.unread)
	}
	st.Off = cu.featuresOff()
	st.Crop = cu.ui != nil && cu.ui.ThumbFit == "crop"
	st.Natural = cu.ui != nil && cu.ui.ThumbFit == "natural"
	st.Presets = cu.presetCards()
	st.Path = cu.folderPath
	st.Sheet = cu.sheet
	st.Aside = cu.ui == nil || !cu.ui.EditPanelHidden
	for _, p := range cu.photos {
		st.Photos = append(st.Photos, GridPhoto{ID: p.ID, Name: p.FileName, Aspect: cu.aspectOf(p), Rating: p.Rating, Flag: string(p.Flag),
			Aids: cu.aids.of(p)})
	}
	return st
}

// needThumbs fetches the small pictures of the tiles built and a screen
// beyond, nearest the middle first; the last ask's fetches stop.
func (cu *culler) needThumbs(n NeedThumbs) {
	if cu.gridStop != nil {
		cu.gridStop()
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	cu.gridStop = cancel
	// Fetches cancelled on the way leave their photos wanted no longer.
	for id := range cu.thumbsWanted {
		delete(cu.thumbsWanted, id)
	}
	first, last := max(0, n.First-n.Count/2), min(len(cu.photos)-1, n.First+n.Count+n.Count/2)
	mid := n.First + n.Count/2
	for d := 0; mid+d <= last || mid-d >= first; d++ {
		for _, i := range []int{mid + d, mid - d} {
			if i >= first && i <= last && (d > 0 || i == mid) {
				// One held already goes again: the grid may have let it
				// go, as on coming back to the folder.
				if img := cu.thumbs[cu.photos[i].ID]; img != nil {
					_ = cu.c.Patch("grid", ThumbIn{Folder: cu.folder, Index: i, Img: img})
					continue
				}
				cu.loadThumb(ctx, i)
			}
		}
	}
}

// openCull opens the cull view over the grid on photo i: it grows out of
// its tile.
func (cu *culler) openCull(i int) {
	if cu.culling || i < 0 || i >= len(cu.photos) {
		return
	}
	if cu.sheet {
		cu.sheet = false
		cu.railChanged()
		_ = cu.c.Update("grid", cu.gridState())
	}
	cu.culling = true
	cu.dev.mounted = false
	// With no pipeline, goTo starts one even on the photo it is on.
	cu.at, cu.load = i, nil
	_ = cu.c.Mount(gunim.Root, "cull", "cull", cu.state())
	_ = cu.c.Focus("cull")
	_ = cu.c.Patch("grid", Covered{On: true})
	if runsLen(cu.sel) > 1 && inRuns(cu.sel, i) {
		// The photos selected in the grid stay selected.
		cu.goToSel(i, cu.sel)
	} else {
		cu.goTo(i)
	}
	// Opened on purpose, not stepped past: its maps are seen to at once.
	cu.ensureAIMaps(cu.photos[i].ID)
}

// leaveCull closes the cull view, the photo flying back to its tile, and
// stops what it fetched.
func (cu *culler) leaveCull() {
	if !cu.culling {
		return
	}
	cu.wbFinish(true)
	cu.cropDone()
	if cu.dev.mounted {
		cu.dev.mounted = false
		_ = cu.c.Unmount("develop")
	}
	if cu.dev.stop != nil {
		cu.dev.stop()
	}
	cu.dev.live = nil
	if cu.batchShown {
		cu.batchShown = false
		_ = cu.c.Unmount("batchpanel")
	}
	cu.culling = false
	for _, stop := range []context.CancelFunc{cu.load, cu.warm, cu.tileStop, cu.probeStop} {
		if stop != nil {
			stop()
		}
	}
	cu.load, cu.warm, cu.tileStop, cu.probeStop, cu.probing = nil, nil, nil, nil, 0
	_ = cu.c.Patch("grid", GridAt{Folder: cu.folder, Index: cu.at})
	if runsLen(cu.sel) > 1 {
		// The photos selected in the cull view stay selected.
		_ = cu.c.Patch("grid", GridSel{Folder: cu.folder, Runs: cu.sel, Cursor: cu.at})
	}
	_ = cu.c.Unmount("cull")
	_ = cu.c.Focus("grid")
	_ = cu.c.Patch("grid", Covered{})
	// Back in the library, the cull view starts again without the panel,
	// as marraw's Cull mode does.
	if cu.dev.open {
		cu.dev.open = false
		cu.closeDevelop()
	}
	cu.sendLibInfo()
}

// showCull shows the cull view its state, while it is open.
func (cu *culler) showCull() {
	if cu.culling {
		_ = cu.c.Update("cull", cu.state())
	}
}
