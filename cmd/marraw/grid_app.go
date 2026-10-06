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
		Folder string
		Photos []GridPhoto
	}
	// GridPhoto is one photo as a tile shows it.
	GridPhoto struct {
		ID     int64
		Name   string
		Aspect float32
		Rating int
		Flag   string
	}
	// ThumbIn is the small picture of the photo at Index, arrived, or,
	// with no Img, let go.
	ThumbIn struct {
		Index int
		Img   *paint.Image
	}
	// PhotoMarked is the rating and the flag of the photo at Index, changed.
	PhotoMarked struct {
		Index  int
		Rating int
		Flag   string
	}
	// GridAt puts the grid's keyboard on the photo at Index, in view, as
	// the cull view steps through the folder over it.
	GridAt struct{ Index int }

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
)

// gridState is the grid's state: every photo in the folder.
func (cu *culler) gridState() GridState {
	st := GridState{Folder: filepath.Base(cu.folderPath)}
	for _, p := range cu.photos {
		s := size(p)
		a := float32(1.5)
		if s.Y > 0 {
			a = float32(s.X) / float32(s.Y)
		}
		st.Photos = append(st.Photos, GridPhoto{ID: p.ID, Name: p.FileName, Aspect: a, Rating: p.Rating, Flag: string(p.Flag)})
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
	cu.culling = true
	cu.dev.mounted = false
	// With no pipeline, goTo starts one even on the photo it is on.
	cu.at, cu.load = i, nil
	_ = cu.c.Mount(gunim.Root, "cull", "cull", cu.state())
	_ = cu.c.Focus("cull")
	cu.goTo(i)
}

// leaveCull closes the cull view, the photo flying back to its tile, and
// stops what it fetched.
func (cu *culler) leaveCull() {
	if !cu.culling {
		return
	}
	if cu.dev.mounted {
		cu.dev.mounted = false
		_ = cu.c.Unmount("develop")
	}
	if cu.dev.stop != nil {
		cu.dev.stop()
	}
	cu.dev.live = nil
	cu.culling = false
	for _, stop := range []context.CancelFunc{cu.load, cu.warm, cu.tileStop, cu.probeStop} {
		if stop != nil {
			stop()
		}
	}
	cu.load, cu.warm, cu.tileStop, cu.probeStop, cu.probing = nil, nil, nil, nil, 0
	_ = cu.c.Patch("grid", GridAt{Index: cu.at})
	_ = cu.c.Unmount("cull")
	_ = cu.c.Focus("grid")
}

// showCull shows the cull view its state, while it is open.
func (cu *culler) showCull() {
	if cu.culling {
		_ = cu.c.Update("cull", cu.state())
	}
}
