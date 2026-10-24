package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// homeAt is a home rule: every photo under one of dirs (the deepest that
// holds it) belongs there, and any other in folder.
func homeAt(folder string, dirs ...string) func(string) string {
	return func(file string) string {
		best := ""
		for _, d := range dirs {
			if strings.HasPrefix(file, d+string(filepath.Separator)) && len(d) > len(best) {
				best = d
			}
		}
		if best == "" {
			return folder
		}
		return best
	}
}

func photoIn(t *testing.T, db *DB, folder int64, name string) (Photo, bool) {
	t.Helper()
	ps, err := db.ListPhotos(context.Background(), folder)
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

func folderAt(t *testing.T, db *DB, path string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM folders WHERE path = ?`, path).Scan(&id); err != nil {
		t.Fatalf("no catalog folder at %s: %v", path, err)
	}
	return id
}

// A folder read without its subfolders hands their photos' rows to the
// folder the rules make their home, and read with them again takes the rows
// back: the photo keeps its id, so its rating, edit and all keyed by it
// stay.
func TestAFolderReadAnotherWayKeepsItsPhotos(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	root := t.TempDir()
	writeRaws(t, root, "x.ARW", filepath.Join("Sub", "y.ARW"))
	sub := filepath.Join(root, "Sub")
	nested := filepath.Join("Sub", "y.ARW")
	id, _ := db.UpsertFolder(ctx, root)
	all := []FileEntry{{Name: "x.ARW", Size: 3, MtimeNs: 1}, {Name: nested, Size: 3, MtimeNs: 1}}
	whole := homeAt(root)
	if _, _, err := db.SyncFolderWith(ctx, id, root, all, whole); err != nil {
		t.Fatal(err)
	}
	y, ok := photoIn(t, db, id, nested)
	if !ok {
		t.Fatal("the nested photo was not read")
	}
	if err := db.SetRating(ctx, []int64{y.ID}, 4, 1); err != nil {
		t.Fatal(err)
	}

	// Split: the folder alone, its subfolder a shoot of its own.
	_, touched, err := db.SyncFolderWith(ctx, id, root, all[:1], homeAt(root, sub))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := photoIn(t, db, id, nested); ok {
		t.Fatal("the split folder kept its subfolder's photo")
	}
	if _, _, read, err := db.FolderByPath(ctx, sub); err != nil || read {
		t.Fatalf("the subfolder's folder reads as scanned before it was: %v, %v", read, err)
	}
	sid := folderAt(t, db, sub)
	got, ok := photoIn(t, db, sid, "y.ARW")
	if !ok || got.ID != y.ID || got.Rating != 4 || got.CacheKey != y.CacheKey {
		t.Fatalf("the subfolder's photo: %+v, %v; want id %d rated 4, key kept", got, ok, y.ID)
	}
	if len(touched) != 1 || touched[0] != sid {
		t.Fatalf("touched %v, want the subfolder %d", touched, sid)
	}

	// One shoot again: the row comes back.
	if _, _, err := db.SyncFolderWith(ctx, id, root, all, whole); err != nil {
		t.Fatal(err)
	}
	got, ok = photoIn(t, db, id, nested)
	if !ok || got.ID != y.ID || got.Rating != 4 {
		t.Fatalf("taken back: %+v, %v; want id %d rated 4", got, ok, y.ID)
	}
	if _, ok := photoIn(t, db, sid, "y.ARW"); ok {
		t.Fatal("the subfolder kept a row of the photo taken back")
	}

	// A file gone from the disk is still deleted.
	if err := os.Remove(filepath.Join(root, nested)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SyncFolderWith(ctx, id, root, all[:1], homeAt(root, sub)); err != nil {
		t.Fatal(err)
	}
	if _, ok := photoIn(t, db, sid, "y.ARW"); ok {
		t.Fatal("a photo gone from the disk was handed over")
	}
}

// A folder read as one shoot and then made a group is never read again, so
// its subfolders, read on their own as the photos' homes now, take their
// rows from it.
func TestASubfolderTakesItsRowsFromAFolderAboveIt(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	trip := t.TempDir()
	writeRaws(t, trip, filepath.Join("Day1", "x.ARW"), filepath.Join("Day2", "y.ARW"))
	day1 := filepath.Join(trip, "Day1")
	id, _ := db.UpsertFolder(ctx, trip)
	if _, _, err := db.SyncFolderWith(ctx, id, trip, []FileEntry{
		{Name: filepath.Join("Day1", "x.ARW"), Size: 3, MtimeNs: 1},
		{Name: filepath.Join("Day2", "y.ARW"), Size: 3, MtimeNs: 1},
	}, homeAt(trip)); err != nil {
		t.Fatal(err)
	}
	x, _ := photoIn(t, db, id, filepath.Join("Day1", "x.ARW"))
	if err := db.SetRating(ctx, []int64{x.ID}, 5, 1); err != nil {
		t.Fatal(err)
	}
	d1, _ := db.UpsertFolder(ctx, day1)
	_, touched, err := db.SyncFolderWith(ctx, d1, day1, []FileEntry{{Name: "x.ARW", Size: 3, MtimeNs: 1}},
		homeAt(trip, day1, filepath.Join(trip, "Day2")))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := photoIn(t, db, d1, "x.ARW")
	if got.ID != x.ID || got.Rating != 5 {
		t.Fatalf("Day1's photo = %+v; want photo %d rated 5", got, x.ID)
	}
	if len(touched) != 1 || touched[0] != id {
		t.Fatalf("touched %v, want the folder above, %d", touched, id)
	}
}

// A folder that is not a photo's home takes nothing: a subfolder of a shoot
// opened on its own reads the photos with rows of its own, and the shoot
// keeps its.
func TestAFolderNotThePhotosHomeTakesNothing(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	trip := t.TempDir()
	writeRaws(t, trip, filepath.Join("Day1", "x.ARW"))
	day1 := filepath.Join(trip, "Day1")
	id, _ := db.UpsertFolder(ctx, trip)
	nested := filepath.Join("Day1", "x.ARW")
	whole := homeAt(trip)
	if _, _, err := db.SyncFolderWith(ctx, id, trip, []FileEntry{{Name: nested, Size: 3, MtimeNs: 1}}, whole); err != nil {
		t.Fatal(err)
	}
	d1, _ := db.UpsertFolder(ctx, day1)
	if _, _, err := db.SyncFolderWith(ctx, d1, day1, []FileEntry{{Name: "x.ARW", Size: 3, MtimeNs: 1}}, whole); err != nil {
		t.Fatal(err)
	}
	if _, ok := photoIn(t, db, id, nested); !ok {
		t.Fatal("the shoot lost its photo to a subfolder opened on its own")
	}
	if _, ok := photoIn(t, db, d1, "x.ARW"); !ok {
		t.Fatal("the subfolder did not read its photo")
	}
	// And the shoot read again keeps its row, taking nothing back.
	_, touched, err := db.SyncFolderWith(ctx, id, trip, []FileEntry{{Name: nested, Size: 3, MtimeNs: 1}}, whole)
	if err != nil || len(touched) != 0 {
		t.Fatalf("the shoot read again touched %v, %v", touched, err)
	}
}

// A row whose name the listing has in another case stays put rather than
// going to a subfolder: on a disk that ignores case it is the same file.
func TestACaseOnlyRenameHandsNothingOver(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	other := t.TempDir()
	writeRaws(t, other, filepath.Join("CamA", "x.ARW"))
	oid, _ := db.UpsertFolder(ctx, other)
	split := homeAt(other, filepath.Join(other, "CamA"), filepath.Join(other, "cama"))
	if _, _, err := db.SyncFolderWith(ctx, oid, other, []FileEntry{{Name: filepath.Join("CamA", "x.ARW"), Size: 3, MtimeNs: 1}}, homeAt(other)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SyncFolderWith(ctx, oid, other, []FileEntry{{Name: filepath.Join("cama", "x.ARW"), Size: 3, MtimeNs: 1}}, split); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM folders WHERE path = ? COLLATE NOCASE`, filepath.Join(other, "CamA")).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a case-only rename handed a row to a folder of its own: %d, %v", n, err)
	}
}

// A read that leaves out a photo still on disk whose home the rules say is
// this very folder keeps its row: the rules changed under the read, as a
// split undone at once does, and deleting it would lose its rating.
func TestARowWhoseHomeIsThisFolderStays(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	trip := t.TempDir()
	writeRaws(t, trip, filepath.Join("Day1", "x.ARW"))
	nested := filepath.Join("Day1", "x.ARW")
	id, _ := db.UpsertFolder(ctx, trip)
	whole := homeAt(trip)
	if _, _, err := db.SyncFolderWith(ctx, id, trip, []FileEntry{{Name: nested, Size: 3, MtimeNs: 1}}, whole); err != nil {
		t.Fatal(err)
	}
	x, _ := photoIn(t, db, id, nested)
	if err := db.SetRating(ctx, []int64{x.ID}, 5, 1); err != nil {
		t.Fatal(err)
	}
	// Read without its subfolders, as a split would, but under rules
	// that make it one shoot again.
	if _, _, err := db.SyncFolderWith(ctx, id, trip, nil, whole); err != nil {
		t.Fatal(err)
	}
	got, ok := photoIn(t, db, id, nested)
	if !ok || got.ID != x.ID || got.Rating != 5 {
		t.Fatalf("the row = %+v, %v; want photo %d rated 5", got, ok, x.ID)
	}
}
