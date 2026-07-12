package store

import (
	"context"
	"path/filepath"
	"testing"
)

// EarliestTakenByFolder feeds the rail's date sort/grouping: MIN must skip
// taken_at = 0 (metadata not yet read), folders with no timed photos must be
// absent rather than reported as epoch, and nested folder rows each report
// their own minimum.
func TestEarliestTakenByFolder(t *testing.T) {
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
	setTaken := func(folderID int64, name string, takenAt int64) {
		t.Helper()
		p := getByName(t, db, folderID, name)
		if err := db.SetMeta(ctx, p.ID, PhotoMeta{TakenAt: takenAt}); err != nil {
			t.Fatalf("SetMeta(%s): %v", name, err)
		}
	}

	timed := sync(`/photos/shoot`, "a.arw", "b.arw", "c.arw")
	setTaken(timed, "b.arw", 2000)
	setTaken(timed, "c.arw", 5000) // a.arw stays 0: must not win the MIN
	nested := sync(`/photos/shoot/second-body`, "d.arw")
	setTaken(nested, "d.arw", 1000)
	sync(`/photos/unread`, "e.arw") // all taken_at = 0 → omitted

	rows, err := db.EarliestTakenByFolder(ctx)
	if err != nil {
		t.Fatalf("EarliestTakenByFolder: %v", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Path] = r.Earliest
	}
	// UpsertFolder stores cleaned paths (OS separators), so expectations must
	// be cleaned the same way to hold on Windows.
	want := map[string]int64{
		filepath.Clean(`/photos/shoot`):             2000,
		filepath.Clean(`/photos/shoot/second-body`): 1000,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p, e := range want {
		if got[p] != e {
			t.Fatalf("earliest[%s] = %d, want %d (all: %v)", p, got[p], e, got)
		}
	}
}
