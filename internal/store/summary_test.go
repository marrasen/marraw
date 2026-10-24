package store

import (
	"context"
	"path/filepath"
	"testing"
)

// FolderSummaries feeds the rail's covers and cull counts: the cover is the
// first pick by capture time, or the first photo when none is picked, and
// an untimed photo never comes before a timed one.
func TestFolderSummaries(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	sync := func(path string, names ...string) int64 {
		t.Helper()
		id, err := db.UpsertFolder(ctx, path)
		if err != nil {
			t.Fatalf("UpsertFolder(%s): %v", path, err)
		}
		files := make([]FileEntry, len(names))
		for i, n := range names {
			files[i] = FileEntry{Name: n, Size: 1, MtimeNs: 1}
		}
		if _, err := db.SyncFolder(ctx, id, path, files); err != nil {
			t.Fatalf("SyncFolder(%s): %v", path, err)
		}
		return id
	}
	photo := func(folderID int64, name string, takenAt int64, flag int) int64 {
		t.Helper()
		p := getByName(t, db, folderID, name)
		if takenAt != 0 {
			if err := db.SetMeta(ctx, p.ID, PhotoMeta{TakenAt: takenAt}); err != nil {
				t.Fatalf("SetMeta(%s): %v", name, err)
			}
		}
		if flag != 0 {
			if err := db.SetFlag(ctx, []int64{p.ID}, flag, 1); err != nil {
				t.Fatalf("SetFlag(%s): %v", name, err)
			}
		}
		return p.ID
	}

	culled := sync(`/photos/culled`, "a.arw", "b.arw", "c.arw", "d.arw")
	photo(culled, "a.arw", 0, 1) // picked but untimed: a timed pick wins
	photo(culled, "b.arw", 3000, 1)
	pick := photo(culled, "c.arw", 2000, 1)
	photo(culled, "d.arw", 1000, -1)
	fresh := sync(`/photos/fresh`, "x.arw", "y.arw", "z.arw")
	photo(fresh, "x.arw", 0, 0)
	first := photo(fresh, "y.arw", 500, 0)
	photo(fresh, "z.arw", 900, 0)
	unread := sync(`/photos/unread`, "q.arw", "p.arw")
	byName := getByName(t, db, unread, "p.arw").ID

	rows, err := db.FolderSummaries(ctx)
	if err != nil {
		t.Fatalf("FolderSummaries: %v", err)
	}
	got := map[string]FolderSummary{}
	for _, r := range rows {
		got[r.Path] = r
	}
	want := map[string]FolderSummary{
		filepath.Clean(`/photos/culled`): {Earliest: 1000, Picked: 3, Rejected: 1, CoverID: pick},
		filepath.Clean(`/photos/fresh`):  {Earliest: 500, CoverID: first},
		filepath.Clean(`/photos/unread`): {CoverID: byName},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p, w := range want {
		g := got[p]
		if g.Earliest != w.Earliest || g.Picked != w.Picked || g.Rejected != w.Rejected || g.CoverID != w.CoverID {
			t.Errorf("%s: got %+v, want %+v", p, g, w)
		}
		if g.CoverKey == "" {
			t.Errorf("%s: the cover has no cache key", p)
		}
	}
}
