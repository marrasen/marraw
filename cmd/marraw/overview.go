package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/jpegturbo"
	"github.com/marrasen/marraw/internal/marrawclient"
)

// The overview of a folder: clicking a folder in the library, a library
// folder, a year's or a client's, shows its shoots where the photos show,
// one card each, grouped by the folders between, with their covers, dates,
// counts and how far each is culled. A card opens its shoot, and the
// shoot's head then has a way back to the overview.

type (
	// OpenOverview shows the overview of the folder row Key heads.
	OpenOverview struct{ Key string }
	// OpenFromOverview opens the shoot at Path from the overview, with a
	// way back to it.
	OpenFromOverview struct{ Path string }
	// OverviewIn is the overview to show, or, with On false, none: then
	// BackKey and BackTitle name the overview the shoot showing was opened
	// from, for the way back, or are empty.
	OverviewIn struct {
		On                 bool
		Key, Title, Sub    string
		Sections           []OverviewSection
		BackKey, BackTitle string
	}
	// OverviewSection is the shoots of one folder under the one shown:
	// Title is the folders between, "" for the shoots right in it.
	OverviewSection struct {
		Title string
		Cards []OverviewCard
	}
	// OverviewCard is one shoot: its name as it shows, the line under it,
	// its photos, its cull counts, and its cover's key.
	OverviewCard struct {
		Path, Title, Line, Cover string
		Count, Picked, Rejected  int
		Offline                  bool
	}
	// WantBigCovers asks for the large covers of the cards in view, by
	// their keys; BigCoverIn is one arrived.
	WantBigCovers struct{ Keys []string }
	BigCoverIn    struct {
		Key string
		Img *paint.Image
	}
)

// The large covers: cards are ovCardW by ovCoverH, and their covers are cut
// at twice that, for a sharp picture on a scaled screen. bigKeep is how
// many are held, the ones asked for last kept.
const (
	ovCardW, ovCoverH = 220, 146
	bigKeep           = 160
)

// overviewSel is the overview open: its row's key, and what it shows.
type overviewSel struct {
	key, kind, title, path, parent string
}

// coverSource is where a cover's picture comes from: a photo, or the
// preview in a folder's first RAW.
type coverSource struct {
	ref  *marrawclient.ShootCover
	path string
}

// openOverview shows the overview of the folder row key heads.
func (cu *culler) openOverview(key string) {
	it, ok := cu.railItem(key)
	if !ok || !it.Group || it.Kind == "time" || it.Kind == "note" {
		return
	}
	cu.ov = &overviewSel{key: key, kind: it.Kind, title: it.Name, path: it.Path, parent: it.Parent}
	if it.Kind == "parent" {
		cu.ov.parent = it.Path
	}
	cu.showOverview()
	// The info panel has no photo to show meanwhile.
	_ = cu.c.Patch("grid", LibInfo{Folder: cu.folder})
	cu.railChanged()
}

// closeOverview puts the overview away, leaving the shoot's head a way
// back to it where back is set.
func (cu *culler) closeOverview(back bool) {
	if cu.ov == nil {
		if !back {
			cu.ovBack = nil
			_ = cu.c.Patch("grid", OverviewIn{})
		}
		return
	}
	cu.ovBack = nil
	if back {
		cu.ovBack = cu.ov
	}
	cu.ov = nil
	in := OverviewIn{}
	if cu.ovBack != nil {
		in.BackKey, in.BackTitle = cu.ovBack.key, cu.ovBack.title
	}
	_ = cu.c.Patch("grid", in)
	cu.sendLibInfo()
	cu.railChanged()
}

// showOverview shows the overview open as the library stands now.
func (cu *culler) showOverview() {
	ov := cu.ov
	if ov == nil {
		return
	}
	in := OverviewIn{On: true, Key: ov.key, Title: ov.title}
	sections := map[string]*OverviewSection{}
	var order []string
	add := func(section string, c OverviewCard) {
		s, ok := sections[section]
		if !ok {
			s = &OverviewSection{Title: section}
			sections[section] = s
			order = append(order, section)
		}
		s.Cards = append(s.Cards, c)
	}
	if cu.ovSources == nil {
		cu.ovSources = map[string]coverSource{}
	}
	shoots, photos := 0, 0
	card := func(path, name string, earliest int64, count, picked, rejected int, ref *marrawclient.ShootCover, offline bool) OverviewCard {
		title, line := shootLabels(name, earliest, count)
		if s, ok := cu.lib.live[strings.ToLower(path)]; ok {
			picked, rejected, ref = s.picked, s.rejected, s.cover
		}
		c := OverviewCard{Path: path, Title: title, Line: line, Count: count, Picked: picked, Rejected: rejected, Offline: offline}
		switch {
		case ref != nil:
			c.Cover = coverKey(ref)
			cu.wantCover(ref)
			cu.ovSources[c.Cover] = coverSource{ref: ref}
		case !offline:
			c.Cover = previewKey(path)
			cu.wantPreview(path)
			cu.ovSources[c.Cover] = coverSource{path: path}
		}
		shoots++
		photos += count
		return c
	}
	sort := "nameAsc"
	if cu.ui != nil && cu.ui.ShootSort != "" {
		sort = string(cu.ui.ShootSort)
	}
	switch ov.kind {
	case "parent", "dir":
		offline := !cu.online(ov.parent)
		for _, s := range sortShoots(cu.lib.shoots[strings.ToLower(ov.parent)], sort) {
			if !strings.EqualFold(s.Path, ov.path) && !hasPrefixFold(s.Path, ov.path) || s.IsSelf && ov.kind != "parent" {
				continue
			}
			name := s.Name
			if s.IsSelf {
				name = "Loose photos"
			}
			section := ""
			if !s.IsSelf {
				if rel, err := filepath.Rel(ov.path, filepath.Dir(s.Path)); err == nil && rel != "." {
					section = strings.ReplaceAll(filepath.ToSlash(rel), "/", " / ")
				}
			}
			add(section, card(s.Path, name, s.EarliestTakenAt, s.PhotoCount, s.Picked, s.Rejected, s.Cover, offline))
		}
	case "group":
		for _, r := range cu.lib.roots {
			if r.IsParent || !strings.EqualFold(filepath.Dir(r.Path), ov.path) {
				continue
			}
			st := cu.lib.status[strings.ToLower(r.Path)]
			add("", card(r.Path, rootName(r), st.EarliestTakenAt, r.PhotoCount, st.Picked, st.Rejected, st.Cover, !cu.online(r.Path)))
		}
	}
	// The shoots right in the folder first, then the folders under it,
	// in the order the sidebar's sort gives names.
	slices.SortStableFunc(order, func(a, b string) int {
		switch {
		case a == "":
			return -1
		case b == "":
			return 1
		}
		c := strings.Compare(strings.ToLower(a), strings.ToLower(b))
		if sort == "nameDesc" {
			c = -c
		}
		return c
	})
	for _, k := range order {
		in.Sections = append(in.Sections, *sections[k])
	}
	in.Sub = fmt.Sprintf("%d shoots · %d photos", shoots, photos)
	if shoots == 1 {
		in.Sub = fmt.Sprintf("1 shoot · %d photos", photos)
	}
	_ = cu.c.Patch("grid", in)
}

// online says the folder at path is on a drive that is connected.
func (cu *culler) online(path string) bool {
	on, ok := cu.lib.online[strings.ToLower(path)]
	return on || !ok
}

// openFromOverview opens the shoot at path, with a way back to the
// overview.
func (cu *culler) openFromOverview(path string) {
	cu.closeOverview(true)
	cu.openShoot(path)
}

// wantBigCovers fetches the large covers of the cards in view, a few at a
// time, and keeps the bigKeep asked for last.
func (cu *culler) wantBigCovers(keys []string) {
	if cu.lib.big == nil {
		cu.lib.big, cu.lib.bigWant = map[string]*paint.Image{}, map[string]bool{}
	}
	for _, key := range keys {
		// Asked for again: kept longer.
		if i := slices.Index(cu.lib.bigOrder, key); i >= 0 {
			cu.lib.bigOrder = append(slices.Delete(cu.lib.bigOrder, i, i+1), key)
		}
		src, ok := cu.ovSources[key]
		if !ok || cu.lib.big[key] != nil || cu.lib.bigWant[key] {
			continue
		}
		cu.lib.bigWant[key] = true
		go func() {
			var img *paint.Image
			select {
			case cu.lib.previewSlots <- struct{}{}:
				img = cu.fetchBigCover(src)
				<-cu.lib.previewSlots
			case <-cu.ctx.Done():
				return
			}
			cu.onDo(func() {
				delete(cu.lib.bigWant, key)
				if img == nil {
					return
				}
				cu.lib.big[key] = img
				cu.lib.bigOrder = append(cu.lib.bigOrder, key)
				for len(cu.lib.bigOrder) > bigKeep {
					delete(cu.lib.big, cu.lib.bigOrder[0])
					_ = cu.c.Patch("grid", BigCoverIn{Key: cu.lib.bigOrder[0]})
					cu.lib.bigOrder = cu.lib.bigOrder[1:]
				}
				_ = cu.c.Patch("grid", BigCoverIn{Key: key, Img: img})
			})
		}()
	}
}

// fetchBigCover is a large cover's picture, or nil.
func (cu *culler) fetchBigCover(src coverSource) *paint.Image {
	if src.ref != nil {
		p := marrawclient.Photo{ID: src.ref.PhotoID, CacheKey: src.ref.CacheKey, EditHash: src.ref.EditHash}
		m, err := cu.im.rgba(cu.ctx, p, want{level: "512", stale: true, fast: true})
		if err != nil {
			return nil
		}
		return paint.NewImage(coverCut(m, ovCardW*coverScale, ovCoverH*coverScale))
	}
	res, err := cu.api.Library.ShootPreview(cu.ctx, src.path)
	if err != nil || res == nil || len(res.JPEG) == 0 {
		return nil
	}
	m, err := jpegturbo.DecodeRGBA(res.JPEG)
	if err != nil {
		return nil
	}
	return paint.NewImage(coverCut(m, ovCardW*coverScale, ovCoverH*coverScale))
}
