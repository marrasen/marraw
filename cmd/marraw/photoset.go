package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// A folder's photos as one view: clicking a library folder, a year's or a
// client's, shows the photos of all its shoots in the grid, as if they
// were one big shoot, under a header for each shoot. The filters, the
// sort, culling, Develop and export all work across them, so "every five
// star photo of 2026" is two clicks. The same folder's overview, a card
// a shoot, is one click away on the switch in the head.
//
// The photos come from the shoots the catalog has read; a shoot never
// opened shows once it has been. Each shoot keeps its own live listing,
// as a single shoot's photos do, so a change in one re-lists that one.

type (
	// FolderView switches a folder between its photos and its overview.
	FolderView struct{ Photos bool }
)

// photoSet is the shoots whose photos the grid shows together.
type photoSet struct {
	// key is the folder row's, title and path the folder's.
	key, title, path string
	// shoots are the shoots, by their catalog folder's id: each one's
	// path, name and place in the order.
	shoots map[int64]setShoot
	// unread are the paths of the shoots the catalog has not read yet,
	// which the backend reads as the set shows, and byPath every shoot by
	// its path. stopRead stops the reading; catching says the set is
	// looking for shoots read since, and again that it should look once
	// more when done.
	unread          []string
	byPath          map[string]setShoot
	stopRead        context.CancelFunc
	catching, again bool
}

// setShoot is one shoot of a set.
type setShoot struct {
	path, name string
	earliest   int64
	rank       int
}

// setSeq numbers the sets shown, for the grid to tell one from another:
// a set's folder is minus its number, where a shoot's is its own id.
var setSeq int64

// libraryPrefs are what the library keeps between runs here.
type libraryPrefs struct {
	// FolderView is how a folder opens: "photos" or "shoots".
	FolderView string `json:"folderView,omitempty"`
}

func libraryPrefsPath() string { return filepath.Join(defaultDataDir(), "library.json") }

func loadLibraryPrefs() libraryPrefs {
	var p libraryPrefs
	if b, err := os.ReadFile(libraryPrefsPath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func saveLibraryPrefs(p libraryPrefs) {
	if b, err := json.Marshal(p); err == nil {
		_ = os.MkdirAll(filepath.Dir(libraryPrefsPath()), 0o755)
		_ = os.WriteFile(libraryPrefsPath(), b, 0o644)
	}
}

// openFolderView opens the folder row key heads as the user last had a
// folder: its photos, or its overview.
func (cu *culler) openFolderView(key string) {
	if loadLibraryPrefs().FolderView == "shoots" {
		cu.openOverview(key)
		return
	}
	cu.openSet(key)
}

// folderView switches the folder showing to its photos or its overview,
// and keeps the choice for the next folder.
func (cu *culler) folderView(photos bool) {
	key := ""
	switch {
	case cu.ov != nil:
		key = cu.ov.key
	case cu.set != nil:
		key = cu.set.key
	}
	p := loadLibraryPrefs()
	p.FolderView = map[bool]string{false: "shoots", true: "photos"}[photos]
	saveLibraryPrefs(p)
	if key == "" {
		return
	}
	if photos {
		cu.openSet(key)
	} else {
		cu.openOverview(key)
	}
}

// setShoots are the shoots of the folder row it heads, in the sidebar's
// order, each with its path, name and earliest capture.
func (cu *culler) setShoots(it RailItem) []setShoot {
	sort := "nameAsc"
	if cu.ui != nil && cu.ui.ShootSort != "" {
		sort = string(cu.ui.ShootSort)
	}
	var out []setShoot
	switch it.Kind {
	case "parent", "dir":
		parent := it.Path
		if it.Kind == "dir" {
			parent = it.Parent
		}
		for _, s := range sortShoots(cu.lib.shoots[strings.ToLower(parent)], sort) {
			if !strings.EqualFold(s.Path, it.Path) && !hasPrefixFold(s.Path, it.Path) || s.IsSelf && it.Kind != "parent" {
				continue
			}
			name := s.Name
			if s.IsSelf {
				name = "Loose photos"
			}
			out = append(out, setShoot{path: s.Path, name: name, earliest: s.EarliestTakenAt})
		}
	case "group":
		for _, r := range cu.lib.roots {
			if !r.IsParent && strings.EqualFold(filepath.Dir(r.Path), it.Path) {
				out = append(out, setShoot{path: r.Path, name: rootName(r), earliest: cu.lib.status[strings.ToLower(r.Path)].EarliestTakenAt})
			}
		}
	}
	return out
}

// openSet shows the photos of every shoot of the folder row key heads, of
// those the catalog has read, in the grid.
func (cu *culler) openSet(key string) {
	it, ok := cu.railItem(key)
	if !ok || !it.Group || it.Kind == "time" || it.Kind == "note" {
		return
	}
	shoots := cu.setShoots(it)
	paths := make([]string, len(shoots))
	byPath := map[string]setShoot{}
	for i, s := range shoots {
		paths[i] = s.path
		s.rank = i
		byPath[strings.ToLower(filepath.Clean(s.path))] = s
	}
	// Away from the overview, with no way back: this is the folder.
	if cu.ov != nil || cu.ovBack != nil {
		cu.ov, cu.ovBack = nil, nil
		_ = cu.c.Patch("grid", OverviewIn{})
	}
	title := it.Name
	cu.opening = it.Path
	cu.openingSet = key
	cu.railChanged()
	go func() {
		set := &photoSet{key: key, title: title, path: it.Path, shoots: map[int64]setShoot{}}
		folders, err := cu.api.Library.ScannedFolders(cu.ctx, paths)
		var photos []marrawclient.Photo
		if err == nil {
			photos, err = cu.setPhotos(folders, set, byPath)
		}
		cu.onDo(func() {
			if cu.openingSet != key {
				// Another folder or shoot was chosen meanwhile.
				return
			}
			cu.opening, cu.openingSet = "", ""
			if err != nil {
				cu.railChanged()
				cu.fail("The folder's photos could not be read", err)
				return
			}
			setSeq++
			cu.showFolder(-setSeq, set.path, photos)
			cu.set = set
			cu.stopLive()
			cu.stopLive = cu.followSet()
			set.byPath = byPath
			read := map[string]bool{}
			for _, f := range folders {
				read[strings.ToLower(filepath.Clean(f.Path))] = true
			}
			for _, p := range paths {
				if !read[strings.ToLower(filepath.Clean(p))] {
					set.unread = append(set.unread, p)
				}
			}
			cu.readSet(set)
			_ = cu.c.Update("grid", cu.gridState())
			cu.railChanged()
		})
	}()
}

// readSet has the backend read the set's shoots not read yet, under a task
// in the tray; their photos join the set as each is read (setCatchUp).
// Leaving the set stops it.
func (cu *culler) readSet(set *photoSet) {
	if len(set.unread) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	set.stopRead = cancel
	paths := slices.Clone(set.unread)
	go func() {
		err := cu.api.Library.ReadShoots(ctx, set.path, paths)
		cu.onDo(func() {
			if err != nil && ctx.Err() == nil {
				log.Printf("reading the shoots of %s: %v", set.path, err)
			}
			set.stopRead = nil
			cancel()
			if cu.set == set {
				_ = cu.c.Update("grid", cu.gridState())
				cu.setCatchUp()
			}
		})
	}()
}

// stopSetRead stops the reading of the set showing's shoots, as it goes.
func (cu *culler) stopSetRead() {
	if cu.set != nil && cu.set.stopRead != nil {
		cu.set.stopRead()
		cu.set.stopRead = nil
	}
}

// setCatchUp adds to the set showing the shoots the backend has read since
// it looked: their photos, in their places, and their live listings.
func (cu *culler) setCatchUp() {
	set := cu.set
	if set == nil || len(set.unread) == 0 {
		return
	}
	if set.catching {
		set.again = true
		return
	}
	set.catching = true
	paths := slices.Clone(set.unread)
	go func() {
		got := &photoSet{shoots: map[int64]setShoot{}}
		folders, err := cu.api.Library.ScannedFolders(cu.ctx, paths)
		var photos []marrawclient.Photo
		if err == nil && len(folders) > 0 {
			photos, err = cu.setPhotos(folders, got, set.byPath)
		}
		cu.onDo(func() {
			set.catching = false
			if cu.set != set {
				return
			}
			if err != nil {
				log.Printf("adding read shoots to %s: %v", set.path, err)
			} else if len(folders) > 0 {
				cu.addToSet(folders, photos, got)
			}
			if set.again {
				set.again = false
				cu.setCatchUp()
			}
		})
	}()
}

// addToSet adds the shoots of folders, just read, to the set showing: their
// photos, but a file the set holds already, and their live listings.
func (cu *culler) addToSet(folders []marrawclient.FolderInfo, photos []marrawclient.Photo, got *photoSet) {
	set := cu.set
	done := map[string]bool{}
	for _, f := range folders {
		if _, ok := set.shoots[f.FolderID]; ok {
			continue
		}
		set.shoots[f.FolderID] = got.shoots[f.FolderID]
		done[strings.ToLower(filepath.Clean(f.Path))] = true
		stop, follow := cu.stopLive, cu.followFolderID(f.FolderID)
		cu.stopLive = func() {
			if stop != nil {
				stop()
			}
			follow()
		}
	}
	set.unread = slices.DeleteFunc(set.unread, func(p string) bool { return done[strings.ToLower(filepath.Clean(p))] })
	held := make(map[string]bool, len(cu.all))
	for _, p := range cu.all {
		held[strings.ToLower(filepath.Join(cu.folderOf(p), p.FileName))] = true
	}
	for _, p := range photos {
		if held[strings.ToLower(filepath.Join(cu.folderOf(p), p.FileName))] {
			continue
		}
		cu.allIndex[p.ID] = len(cu.all)
		cu.all = append(cu.all, p)
	}
	cu.refilter()
	_ = cu.c.Update("grid", cu.gridState())
	cu.railChanged()
}

// setPhotos lists the photos of folders, a few at a time, as one list, a
// file in two folders' rows once, and fills set's shoots.
func (cu *culler) setPhotos(folders []marrawclient.FolderInfo, set *photoSet, byPath map[string]setShoot) ([]marrawclient.Photo, error) {
	lists := make([][]marrawclient.Photo, len(folders))
	errs := make([]error, len(folders))
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, f := range folders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			lists[i], errs[i] = cu.api.Library.ListPhotos(cu.ctx, f.FolderID)
		}()
	}
	wg.Wait()
	var out []marrawclient.Photo
	seen := map[string]bool{}
	for i, f := range folders {
		if errs[i] != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, errs[i])
		}
		s := byPath[strings.ToLower(filepath.Clean(f.Path))]
		set.shoots[f.FolderID] = s
		for _, p := range lists[i] {
			k := strings.ToLower(filepath.Join(f.Path, p.FileName))
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// followSet follows the live listings of the set's shoots, each its own.
func (cu *culler) followSet() func() {
	var stops []func()
	for id := range cu.set.shoots {
		stops = append(stops, cu.followFolderID(id))
	}
	return func() {
		for _, s := range stops {
			s()
		}
	}
}

// folderOf is the folder photo p's file is in: the open folder, or with
// a folder's photos as one, p's own shoot.
func (cu *culler) folderOf(p marrawclient.Photo) string {
	if s, ok := cu.shootOf(p); ok {
		return s.path
	}
	return cu.folderPath
}

// shootOf is the shoot of the set that photo p is in, and whether it is in
// one.
func (cu *culler) shootOf(p marrawclient.Photo) (setShoot, bool) {
	if cu.set == nil {
		return setShoot{}, false
	}
	s, ok := cu.set.shoots[p.FolderID]
	return s, ok
}

// setOrder orders a set's photos: by shoot, in the photos' sort's
// direction, then as the view sorts them within it.
func (cu *culler) setOrder(out []marrawclient.Photo) {
	desc := cu.libView.Sort == "captureDesc" || cu.libView.Sort == "nameDesc"
	byName := cu.libView.Sort == "nameAsc" || cu.libView.Sort == "nameDesc"
	shootLess := func(a, b setShoot) int {
		c := 0
		switch {
		case byName:
			c = strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
		case a.earliest != b.earliest:
			// Undated last either way.
			switch {
			case a.earliest == 0:
				return 1
			case b.earliest == 0:
				return -1
			case a.earliest < b.earliest:
				c = -1
			default:
				c = 1
			}
		}
		if c == 0 {
			c = strings.Compare(strings.ToLower(a.path), strings.ToLower(b.path))
		}
		if desc {
			c = -c
		}
		return c
	}
	slices.SortStableFunc(out, func(a, b marrawclient.Photo) int {
		sa, _ := cu.shootOf(a)
		sb, _ := cu.shootOf(b)
		if c := shootLess(sa, sb); c != 0 {
			return c
		}
		switch {
		case cu.libView.less(a, b):
			return -1
		case cu.libView.less(b, a):
			return 1
		}
		return 0
	})
}

// shootGroups splits a set's photos, in the view's order, into a group a
// shoot, each titled with the shoot's name.
func (cu *culler) shootGroups(photos []marrawclient.Photo) []GapGroup {
	var out []GapGroup
	last := int64(-1)
	for i, p := range photos {
		if p.FolderID != last || len(out) == 0 {
			s, _ := cu.shootOf(p)
			title, _ := shootLabels(s.name, 0, 0)
			out = append(out, GapGroup{Start: i, GapBefore: -1, Title: title})
			last = p.FolderID
		}
		g := &out[len(out)-1]
		g.Count++
		if t := p.TakenAt; t > 0 {
			if g.From == 0 || t < g.From {
				g.From = t
			}
			g.To = max(g.To, t)
		}
	}
	return out
}

// setLive is the cull counts and covers of a set's shoots as culled here,
// newer than the backend's listing, by lowercased path.
func (cu *culler) setLive() map[string]liveSum {
	out := map[string]liveSum{}
	if cu.set == nil {
		return out
	}
	cu.syncAll()
	byFolder := map[int64][]marrawclient.Photo{}
	for _, p := range cu.all {
		byFolder[p.FolderID] = append(byFolder[p.FolderID], p)
	}
	for id, ps := range byFolder {
		s, ok := cu.set.shoots[id]
		if !ok {
			continue
		}
		out[strings.ToLower(s.path)] = summaryOf(ps)
	}
	return out
}
