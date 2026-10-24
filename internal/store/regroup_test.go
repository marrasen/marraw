package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A folder read without its subfolders hands their photos' rows to them,
// and read with them again takes the rows back: the photo keeps its id, so
// its rating, edit and all keyed by it stay.
func TestAFolderReadAnotherWayKeepsItsPhotos(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	root := t.TempDir()
	for _, p := range []string{"x.ARW", filepath.Join("Sub", "y.ARW")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte("raw"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join("Sub", "y.ARW")
	id, err := db.UpsertFolder(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	all := []FileEntry{{Name: "x.ARW", Size: 3, MtimeNs: 1}, {Name: nested, Size: 3, MtimeNs: 1}}
	if _, err := db.SyncFolder(ctx, id, root, all); err != nil {
		t.Fatal(err)
	}
	photoIn := func(folder int64, name string) (Photo, bool) {
		ps, err := db.ListPhotos(ctx, folder)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if p.FileName == name {
				return p, true
			}
		}
		return Photo{}, false
	}
	y, ok := photoIn(id, nested)
	if !ok {
		t.Fatal("the nested photo was not read")
	}
	if err := db.SetRating(ctx, []int64{y.ID}, 4, 1); err != nil {
		t.Fatal(err)
	}

	// Split: the folder alone.
	if _, err := db.SyncFolder(ctx, id, root, all[:1]); err != nil {
		t.Fatal(err)
	}
	if _, ok := photoIn(id, nested); ok {
		t.Fatal("the split folder kept its subfolder's photo")
	}
	if _, _, read, err := db.FolderByPath(ctx, filepath.Join(root, "Sub")); err != nil || read {
		t.Fatalf("the subfolder's folder reads as scanned before it was: %v, %v", read, err)
	}
	var sub int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM folders WHERE path = ?`, filepath.Join(root, "Sub")).Scan(&sub); err != nil {
		t.Fatalf("no catalog folder for the subfolder: %v", err)
	}
	got, ok := photoIn(sub, "y.ARW")
	if !ok || got.ID != y.ID || got.Rating != 4 {
		t.Fatalf("the subfolder's photo: %+v, %v; want id %d rated 4", got, ok, y.ID)
	}
	if got.CacheKey != y.CacheKey {
		t.Fatalf("cache key changed: %s, was %s", got.CacheKey, y.CacheKey)
	}

	// One shoot again: the row comes back.
	if _, err := db.SyncFolder(ctx, id, root, all); err != nil {
		t.Fatal(err)
	}
	got, ok = photoIn(id, nested)
	if !ok || got.ID != y.ID || got.Rating != 4 {
		t.Fatalf("taken back: %+v, %v; want id %d rated 4", got, ok, y.ID)
	}
	if _, ok := photoIn(sub, "y.ARW"); ok {
		t.Fatal("the subfolder kept a row of the photo taken back")
	}

	// A file gone from the disk is still deleted.
	if err := os.Remove(filepath.Join(root, nested)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SyncFolder(ctx, id, root, all[:1]); err != nil {
		t.Fatal(err)
	}
	if _, ok := photoIn(sub, "y.ARW"); ok {
		t.Fatal("a photo gone from the disk was handed over")
	}
}

// writeRaws makes the files at paths under root.
func writeRaws(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte("raw"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A folder read as one shoot and then made a group is never read again, so
// its subfolders, read on their own, take their photos' rows from it.
func TestASubfolderTakesItsRowsFromAFolderAboveIt(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	trip := t.TempDir()
	writeRaws(t, trip, filepath.Join("Day1", "x.ARW"), filepath.Join("Day2", "y.ARW"))
	id, _ := db.UpsertFolder(ctx, trip)
	if _, err := db.SyncFolder(ctx, id, trip, []FileEntry{
		{Name: filepath.Join("Day1", "x.ARW"), Size: 3, MtimeNs: 1},
		{Name: filepath.Join("Day2", "y.ARW"), Size: 3, MtimeNs: 1},
	}); err != nil {
		t.Fatal(err)
	}
	ps, _ := db.ListPhotos(ctx, id)
	var x Photo
	for _, p := range ps {
		if p.FileName == filepath.Join("Day1", "x.ARW") {
			x = p
		}
	}
	if err := db.SetRating(ctx, []int64{x.ID}, 5, 1); err != nil {
		t.Fatal(err)
	}
	day1 := filepath.Join(trip, "Day1")
	d1, _ := db.UpsertFolder(ctx, day1)
	_, touched, err := db.SyncFolderWith(ctx, d1, day1, []FileEntry{{Name: "x.ARW", Size: 3, MtimeNs: 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := db.ListPhotos(ctx, d1)
	if len(got) != 1 || got[0].ID != x.ID || got[0].Rating != 5 {
		t.Fatalf("Day1 = %+v; want photo %d rated 5", got, x.ID)
	}
	if len(touched) != 1 || touched[0] != id {
		t.Fatalf("touched %v, want the folder above, %d", touched, id)
	}
}

// Rows move only between folders said to be related, and a row whose name
// the listing has in another case stays put rather than going to a
// subfolder: on a disk that ignores case it is the same file.
func TestRowsMoveOnlyBetweenRelatedFolders(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	root := t.TempDir()
	writeRaws(t, root, filepath.Join("Sub", "y.ARW"))
	sub := filepath.Join(root, "Sub")
	sid, _ := db.UpsertFolder(ctx, sub)
	if _, err := db.SyncFolder(ctx, sid, sub, []FileEntry{{Name: "y.ARW", Size: 3, MtimeNs: 1}}); err != nil {
		t.Fatal(err)
	}
	id, _ := db.UpsertFolder(ctx, root)
	nested := filepath.Join("Sub", "y.ARW")
	unrelated := func(p string) bool { return !filepath.IsAbs(p) || p != sub }
	if _, _, err := db.SyncFolderWith(ctx, id, root, []FileEntry{{Name: nested, Size: 3, MtimeNs: 1}}, unrelated); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ListPhotos(ctx, sid); len(got) != 1 {
		t.Fatalf("an unrelated folder gave up its row: %+v", got)
	}

	// The same file listed in another case: the old row is not handed on.
	other := t.TempDir()
	writeRaws(t, other, filepath.Join("CamA", "x.ARW"))
	oid, _ := db.UpsertFolder(ctx, other)
	if _, err := db.SyncFolder(ctx, oid, other, []FileEntry{{Name: filepath.Join("CamA", "x.ARW"), Size: 3, MtimeNs: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SyncFolder(ctx, oid, other, []FileEntry{{Name: filepath.Join("cama", "x.ARW"), Size: 3, MtimeNs: 1}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM folders WHERE path = ? COLLATE NOCASE`, filepath.Join(other, "CamA")).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a case-only rename handed a row to a folder of its own: %d, %v", n, err)
	}
}
