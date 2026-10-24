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
