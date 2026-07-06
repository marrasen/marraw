package store

import (
	"context"
	"testing"
)

// getByName is a small test helper: the folder's photo row for a file name.
func getByName(t *testing.T, db *DB, folderID int64, name string) Photo {
	t.Helper()
	photos, err := db.ListPhotos(context.Background(), folderID)
	if err != nil {
		t.Fatalf("ListPhotos: %v", err)
	}
	for _, p := range photos {
		if p.FileName == name {
			return p
		}
	}
	t.Fatalf("photo %q not found", name)
	return Photo{}
}

func TestApplyImportedEditLastWriterWins(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	const folderPath = `C:\photos`
	folderID, err := db.UpsertFolder(ctx, folderPath)
	if err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	if _, err := db.SyncFolder(ctx, folderID, folderPath, []FileEntry{
		{Name: "a.arw", Size: 100, MtimeNs: 1},
	}); err != nil {
		t.Fatalf("SyncFolder: %v", err)
	}

	// Fresh row has NULL updated_at, so any sidecar wins (the copied-in case).
	editJSON := `{"expEV":0.5}`
	ok, err := db.ApplyImportedEdit(ctx, folderID, "a.arw", 3, 1, &editJSON, "hash1", 1000)
	if err != nil {
		t.Fatalf("apply #1: %v", err)
	}
	if !ok {
		t.Fatal("first sidecar should apply to a fresh row")
	}
	if p := getByName(t, db, folderID, "a.arw"); p.Rating != 3 || p.Flag != 1 ||
		!p.EditParams.Valid || p.EditParams.String != editJSON || p.EditHash != "hash1" ||
		!p.UpdatedAt.Valid || p.UpdatedAt.Int64 != 1000 {
		t.Fatalf("row after apply #1: %+v", p)
	}

	// An older sidecar must not clobber the newer catalog state.
	ok, err = db.ApplyImportedEdit(ctx, folderID, "a.arw", 5, 0, nil, "base", 500)
	if err != nil {
		t.Fatalf("apply #2: %v", err)
	}
	if ok {
		t.Fatal("older sidecar should not apply")
	}
	if p := getByName(t, db, folderID, "a.arw"); p.Rating != 3 || p.UpdatedAt.Int64 != 1000 {
		t.Fatalf("older sidecar altered the row: %+v", p)
	}

	// A newer sidecar wins and can clear the edit (a reset is a write too).
	ok, err = db.ApplyImportedEdit(ctx, folderID, "a.arw", 4, 0, nil, "base", 2000)
	if err != nil {
		t.Fatalf("apply #3: %v", err)
	}
	if !ok {
		t.Fatal("newer sidecar should apply")
	}
	if p := getByName(t, db, folderID, "a.arw"); p.Rating != 4 || p.Flag != 0 ||
		p.EditParams.Valid || p.EditHash != "base" || p.UpdatedAt.Int64 != 2000 {
		t.Fatalf("row after apply #3: %+v", p)
	}

	// An equal timestamp is not "newer" — no change (idempotent re-import).
	ok, err = db.ApplyImportedEdit(ctx, folderID, "a.arw", 1, 0, nil, "base", 2000)
	if err != nil {
		t.Fatalf("apply #4: %v", err)
	}
	if ok {
		t.Fatal("equal timestamp should not apply")
	}
}
