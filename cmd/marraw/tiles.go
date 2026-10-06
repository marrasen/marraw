package main

import (
	"context"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/jpegturbo"
	"github.com/marrasen/marraw/internal/marrawclient"
)

// tileSize must match pyramid.TileSize: the full resolution is served as a
// grid of square tiles this many pixels on a side.
const tileSize = 1024

// tileURL is the content-addressed URL of p's tile at t, as
// client/src/lib/backend.ts builds it.
func (im *images) tileURL(p marrawclient.Photo, t image.Point, cacheOnly bool) string {
	q := url.Values{"v": {p.CacheKey}, "r": {renderVersion}}
	if p.EditHash != "" && p.EditHash != "base" {
		q.Set("e", p.EditHash)
	}
	if cacheOnly {
		q.Set("cacheOnly", "1")
	}
	q.Set("t", im.token)
	return fmt.Sprintf("%s/img/%d/tile/%d/%d?%s", im.base, p.ID, t.X, t.Y, q.Encode())
}

// tile fetches and decodes p's tile at t. With cacheOnly the server answers
// from the rendered set or 404s; without, it renders the full resolution
// first, which takes seconds, and cancelling ctx cancels it.
func (im *images) tile(ctx context.Context, p marrawclient.Photo, t image.Point, cacheOnly bool) (*paint.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, im.tileURL(p, t, cacheOnly), nil)
	if err != nil {
		return nil, err
	}
	resp, err := im.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, errMissing
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tile %v: %s", t, resp.Status)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	m, err := jpegturbo.DecodeRGBA(raw)
	if err != nil {
		return nil, err
	}
	return paint.NewImage(m), nil
}

// wantTiles takes the view's ask for the tiles in a range of the photo
// showing. The tiles engage only once the set is rendered: a cold photo
// gets one full render, and only after the user has stayed on it zoomed in
// for dwell, so zooming while skimming never starts one.
func (cu *culler) wantTiles(w WantTiles) {
	if w.Index != cu.at {
		return
	}
	cu.tileWant = w
	if w.Range.Empty() {
		if cu.tileStop != nil {
			cu.tileStop()
			cu.tileStop = nil
		}
		return
	}
	p := cu.photos[cu.at]
	if cu.editing(p.ID) {
		// The edit under way is not the one the tiles would show; they
		// come once it is saved.
		cu.stopTiles()
		return
	}
	if !cu.tileWarm[tileSet(p)] {
		cu.probeTiles(p)
		return
	}
	cu.fetchTiles(p)
}

// tileSet names p's tiles as its edit has them.
func tileSet(p marrawclient.Photo) string { return fmt.Sprint(p.ID, "|", p.EditHash) }

// stopTiles stops the tile fetches and the probe under way.
func (cu *culler) stopTiles() {
	if cu.tileStop != nil {
		cu.tileStop()
		cu.tileStop = nil
	}
	if cu.probeStop != nil {
		cu.probeStop()
		cu.probing, cu.probeStop = 0, nil
	}
}

// probeTiles finds whether p's tile set is rendered, and renders it once
// the user has stayed for dwell. Moving on cancels it, render and all.
func (cu *culler) probeTiles(p marrawclient.Photo) {
	if cu.probing == p.ID {
		return
	}
	if cu.probeStop != nil {
		cu.probeStop()
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	cu.probing, cu.probeStop = p.ID, cancel
	cu.setTileNote("tiles: looking")
	at, gen := cu.at, cu.gen
	go func() {
		start := time.Now()
		_, err := cu.im.tile(ctx, p, image.Point{}, true)
		rendered := false
		if err == errMissing {
			cu.say(gen, "tiles: rendering the full resolution")
			select {
			case <-time.After(dwell):
				_, err = cu.im.tile(ctx, p, image.Point{}, false)
				rendered = err == nil
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		took := time.Since(start)
		select {
		case cu.do <- func() {
			if cu.probing == p.ID {
				cu.probing, cu.probeStop = 0, nil
			}
			if err != nil {
				if cu.gen == gen {
					cu.setTileNote("tiles: " + err.Error())
				}
				return
			}
			cu.tileWarm[tileSet(p)] = true
			note := "tiles: rendered already"
			if rendered {
				note = fmt.Sprintf("tiles: full resolution rendered in %d ms", took.Milliseconds())
			}
			if cu.gen == gen {
				cu.setTileNote(note)
				if !cu.tileWant.Range.Empty() && cu.tileWant.Index == at {
					cu.fetchTiles(p)
				}
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// fetchTiles fetches the wanted tiles of p not fetched yet, a few at a time,
// and shows each as it comes. A new ask replaces the fetches under way.
func (cu *culler) fetchTiles(p marrawclient.Photo) {
	if cu.tileStop != nil {
		cu.tileStop()
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	cu.tileStop = cancel
	var need []image.Point
	r := cu.tileWant.Range
	for ty := r.Min.Y; ty < r.Max.Y; ty++ {
		for tx := r.Min.X; tx < r.Max.X; tx++ {
			t := image.Pt(tx, ty)
			if !cu.tiles.has(p.ID, p.EditHash, t) {
				need = append(need, t)
			}
		}
	}
	// The middle of the view first.
	mid := image.Pt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
	slices.SortFunc(need, func(a, b image.Point) int {
		da := (a.X-mid.X)*(a.X-mid.X) + (a.Y-mid.Y)*(a.Y-mid.Y)
		db := (b.X-mid.X)*(b.X-mid.X) + (b.Y-mid.Y)*(b.Y-mid.Y)
		return da - db
	})
	at := cu.at
	work := make(chan image.Point)
	for range 4 {
		go func() {
			for t := range work {
				img, err := cu.im.tile(ctx, p, t, true)
				if err != nil {
					continue
				}
				select {
				case cu.do <- func() {
					// Under the edit it shows: a tile of an edit since
					// replaced never shows over the new one.
					cu.tiles.put(p.ID, p.EditHash, t, img)
					if cu.at == at {
						cu.showCull()
					}
				}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, t := range need {
			select {
			case work <- t:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// say sets the tile note from a goroutine, if the user is still on the
// photo of gen.
func (cu *culler) say(gen int, note string) {
	select {
	case cu.do <- func() {
		if cu.gen == gen {
			cu.setTileNote(note)
		}
	}:
	case <-cu.ctx.Done():
	}
}

func (cu *culler) setTileNote(note string) {
	cu.tileNote = note
	cu.showCull()
}

// tileCache keeps the tiles fetched last, of any photo.
type tileCache struct {
	mu    sync.Mutex
	limit int
	order []tileKey
	m     map[tileKey]*paint.Image
}

type tileKey struct {
	id   int64
	hash string
	t    image.Point
}

func newTileCache(limit int) *tileCache {
	return &tileCache{limit: limit, m: map[tileKey]*paint.Image{}}
}

func (tc *tileCache) has(id int64, hash string, t image.Point) bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	_, ok := tc.m[tileKey{id, hash, t}]
	return ok
}

func (tc *tileCache) put(id int64, hash string, t image.Point, img *paint.Image) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	k := tileKey{id, hash, t}
	if _, ok := tc.m[k]; !ok {
		tc.order = append(tc.order, k)
	}
	tc.m[k] = img
	for len(tc.order) > tc.limit {
		delete(tc.m, tc.order[0])
		tc.order = tc.order[1:]
	}
}

// drop forgets photo id's tiles, as after an edit.
func (tc *tileCache) drop(id int64) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	keep := tc.order[:0]
	for _, k := range tc.order {
		if k.id == id {
			delete(tc.m, k)
			continue
		}
		keep = append(keep, k)
	}
	tc.order = keep
}

// of is a new map of photo id's tiles of edit hash, the window's to keep.
func (tc *tileCache) of(id int64, hash string) map[image.Point]*paint.Image {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	out := map[image.Point]*paint.Image{}
	for k, img := range tc.m {
		if k.id == id && k.hash == hash {
			out[k.t] = img
		}
	}
	return out
}
