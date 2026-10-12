package main

import (
	"slices"
	"testing"

	"github.com/marrasen/marraw/internal/marrawclient"
)

func TestFolderPhotosGroupByShoot(t *testing.T) {
	cu := &culler{set: &photoSet{shoots: map[int64]setShoot{
		1: {path: "/lib/Launch", name: "Launch party", earliest: 300},
		2: {path: "/lib/Concert", name: "2019-11-08 Concert", earliest: 100},
		3: {path: "/lib/Odd", name: "Odd", earliest: 0},
	}}}
	photos := []marrawclient.Photo{
		{ID: 1, FolderID: 1, FileName: "b.arw", TakenAt: 320},
		{ID: 2, FolderID: 3, FileName: "x.arw"},
		{ID: 3, FolderID: 2, FileName: "c.arw", TakenAt: 150},
		{ID: 4, FolderID: 1, FileName: "a.arw", TakenAt: 310},
		{ID: 5, FolderID: 2, FileName: "d.arw", TakenAt: 120},
	}
	ids := func(ps []marrawclient.Photo) []int64 {
		var out []int64
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return out
	}

	cu.setOrder(photos)
	if got, want := ids(photos), []int64{5, 3, 4, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("oldest first: got %v, want %v", got, want)
	}
	gs := cu.shootGroups(photos)
	if len(gs) != 3 {
		t.Fatalf("got %d groups, want 3: %+v", len(gs), gs)
	}
	if gs[0].Title != "Concert" || gs[0].Count != 2 || gs[0].From != 120 || gs[0].To != 150 {
		t.Errorf("first group %+v: want Concert, 2 photos, 120–150 (date stripped from the name)", gs[0])
	}
	if gs[1].Start != 2 || gs[1].Title != "Launch party" || gs[2].Title != "Odd" {
		t.Errorf("groups %+v", gs)
	}

	// Newest first flips the shoots and the photos in them, and a shoot
	// without a date stays last.
	cu.libView.Sort = "captureDesc"
	cu.setOrder(photos)
	if got, want := ids(photos), []int64{1, 4, 3, 5, 2}; !slices.Equal(got, want) {
		t.Fatalf("newest first: got %v, want %v", got, want)
	}
}
