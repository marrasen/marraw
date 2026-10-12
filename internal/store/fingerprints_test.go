package store

import (
	"context"
	"testing"
)

func TestFingerprintsAndDeletedPhotos(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	id, err := db.UpsertFolder(ctx, "/photos/shoot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SyncFolder(ctx, id, "/photos/shoot", []FileEntry{{Name: "A.ARW", Size: 1, MtimeNs: 1}, {Name: "B.ARW", Size: 2, MtimeNs: 1}}); err != nil {
		t.Fatal(err)
	}
	todo, err := db.Unfingerprinted(ctx, id)
	if err != nil || len(todo) != 2 || todo[0].FileName != "A.ARW" || todo[0].FolderPath == "" {
		t.Fatalf("Unfingerprinted = %+v, %v", todo, err)
	}
	if err := db.SetFingerprint(ctx, todo[0].ID, "1:aa"); err != nil {
		t.Fatal(err)
	}
	if fp, _ := db.Fingerprint(ctx, todo[0].ID); fp != "1:aa" {
		t.Fatalf("Fingerprint = %q", fp)
	}
	if left, _ := db.Unfingerprinted(ctx, id); len(left) != 1 || left[0].FileName != "B.ARW" {
		t.Fatalf("after one, Unfingerprinted = %+v", left)
	}

	if _, ok, _ := db.WasDeleted(ctx, "1:aa"); ok {
		t.Fatal("a photo never deleted was taken as deleted")
	}
	d := DeletedPhoto{Fingerprint: "1:aa", FileName: "A.ARW", FolderPath: "/photos/shoot", TakenAt: 5, DeletedAt: 10}
	if err := db.RecordDeleted(ctx, d); err != nil {
		t.Fatal(err)
	}
	// Deleted again later, as from a second copy: one record, the latest.
	d.DeletedAt = 20
	if err := db.RecordDeleted(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.WasDeleted(ctx, "1:aa")
	if err != nil || !ok || got.DeletedAt != 20 || got.FileName != "A.ARW" || got.TakenAt != 5 {
		t.Fatalf("WasDeleted = %+v, %v, %v", got, ok, err)
	}
}

func TestFolderByPath(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	id, err := db.UpsertFolder(ctx, "/photos/shoot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SyncFolder(ctx, id, "/photos/shoot", []FileEntry{{Name: "A.ARW", Size: 1, MtimeNs: 1}, {Name: "B.ARW", Size: 1, MtimeNs: 1}}); err != nil {
		t.Fatal(err)
	}
	if got, n, ok, err := db.FolderByPath(ctx, "/PHOTOS/shoot"); err != nil || !ok || got != id || n != 2 {
		t.Fatalf("FolderByPath = %d, %d, %v, %v; want %d, 2, true", got, n, ok, err, id)
	}
	if _, _, ok, err := db.FolderByPath(ctx, "/photos/never"); err != nil || ok {
		t.Fatalf("a folder never read was found: %v, %v", ok, err)
	}
}
