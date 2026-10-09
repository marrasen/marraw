package main

import (
	"context"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	}
	// RailItem is one row: a shoot, or a library folder of shoots with
	// its shoots under it, one deeper.
	RailItem struct {
		Path, Name string
		Count      int
		Depth      int
		// Group says the row is a library folder, which holds shoots.
		Group bool
	}
	// OpenShoot opens the folder at Path in the grid.
	OpenShoot struct{ Path string }
)

// loadLibrary reads the library's folders and their shoots, as marraw's
// rail lists them, for the sidebar: each library folder with its shoots
// newest first under it.
func (cu *culler) loadLibrary() {
	ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
	defer cancel()
	roots, err := cu.api.Library.GetLibraryRoots(ctx)
	if err != nil {
		log.Printf("library: %v", err)
		return
	}
	var items []RailItem
	for _, r := range roots {
		name := r.Alias
		if name == "" {
			name = filepath.Base(r.Path)
		}
		if !r.IsParent {
			items = append(items, RailItem{Path: r.Path, Name: name, Count: r.PhotoCount})
			continue
		}
		items = append(items, RailItem{Path: r.Path, Name: name, Group: true})
		shoots, err := cu.api.Library.ListShoots(ctx, r.Path)
		if err != nil {
			log.Printf("library: %s: %v", r.Path, err)
			continue
		}
		excluded := map[string]bool{}
		for _, x := range r.ExcludedChildren {
			excluded[x] = true
		}
		sort.SliceStable(shoots, func(i, j int) bool {
			if shoots[i].IsSelf != shoots[j].IsSelf {
				return shoots[i].IsSelf
			}
			return shoots[i].EarliestTakenAt > shoots[j].EarliestTakenAt
		})
		for _, s := range shoots {
			if excluded[strings.ToLower(s.Path)] {
				continue
			}
			n := s.Name
			if s.IsSelf {
				n = "Loose photos"
			}
			items = append(items, RailItem{Path: s.Path, Name: n, Count: s.PhotoCount, Depth: 1})
		}
	}
	select {
	case cu.do <- func() {
		cu.rail.Items, cu.rail.Loaded = items, true
		// A folder opened that the library does not hold shows at the top.
		if cu.folderPath != "" && !cu.railHas(cu.folderPath) {
			cu.rail.Items = append([]RailItem{{Path: cu.folderPath, Name: filepath.Base(cu.folderPath), Count: len(cu.photos)}}, cu.rail.Items...)
		}
		cu.rail.Current = cu.folderPath
		_ = cu.c.Patch("grid", cu.rail)
	}:
	case <-cu.ctx.Done():
	}
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
		if v := viewFor(ui, cu.folderPath); cu.folder != 0 && v != cu.libView && !cu.culling {
			cu.libView = v
			cu.applyView()
			_ = cu.c.Update("grid", cu.gridState())
		}
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
	if strings.EqualFold(path, cu.folderPath) {
		return
	}
	for _, it := range cu.rail.Items {
		if it.Path == path && it.Group {
			// A library folder holds shoots; it opens none itself.
			return
		}
	}
	cu.rail.Current = path
	_ = cu.c.Patch("grid", cu.rail)
	go func() {
		info, err := cu.api.Library.OpenFolder(cu.ctx, path)
		var photos []marrawclient.Photo
		if err == nil {
			photos, err = cu.api.Library.ListPhotos(cu.ctx, info.FolderID)
		}
		select {
		case cu.do <- func() {
			if err != nil {
				log.Printf("library: open %s: %v", path, err)
				return
			}
			if cu.rail.Current != path {
				// Another shoot was chosen meanwhile.
				return
			}
			cu.showFolder(info.FolderID, path, photos)
		}:
		case <-cu.ctx.Done():
		}
	}()
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
	cu.folder, cu.folderPath = id, path
	cu.setAll(photos)
	cu.libView = viewFor(cu.ui, path)
	cu.applyView()
	clear(cu.thumbsWanted)
	cu.at, cu.gen, cu.sel, cu.cursor = 0, cu.gen+1, nil, -1
	cu.stopLive = cu.followFolder()
	_ = cu.c.Update("grid", cu.gridState())
	for i := range cu.rail.Items {
		if strings.EqualFold(cu.rail.Items[i].Path, path) {
			cu.rail.Items[i].Count = len(photos)
		}
	}
	_ = cu.c.Patch("grid", cu.rail)
}
