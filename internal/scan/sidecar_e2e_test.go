package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/sidecar"
	"github.com/marrasen/marraw/internal/store"
)

// TestCopyFolderCarriesEditsViaSidecars is the end-to-end proof of the whole
// feature: edit a folder on one machine, copy the folder (RAWs + sidecars) to
// another machine with an independent catalog, and confirm the edits reappear.
// It drives the real production entry point (Scanner.OpenFolder) throughout —
// no test-only shortcuts around the import/backfill logic.
func TestCopyFolderCarriesEditsViaSidecars(t *testing.T) {
	ctx := context.Background()

	// --- "Desktop" machine: catalog + a folder of RAWs ---
	deskDB := openDB(t)
	desk := &Scanner{DB: deskDB}

	folderA := filepath.Join(t.TempDir(), "shoot")
	if err := os.MkdirAll(folderA, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeRaw(t, folderA, "IMG1.ARW", "the-first-frame")
	writeFakeRaw(t, folderA, "IMG2.ARW", "second")

	fidA, countA, err := desk.OpenFolder(ctx, folderA)
	if err != nil {
		t.Fatalf("open folder A: %v", err)
	}
	if countA != 2 {
		t.Fatalf("expected 2 photos, got %d", countA)
	}

	// Edit + rate IMG1 exactly as the API layer persists a commit.
	img1 := photoByName(t, deskDB, fidA, "IMG1.ARW")
	editJSON := `{"expEV":0.75,"contrast":0.2}`
	if err := deskDB.SetEdit(ctx, img1.ID, &editJSON, "deskhash", 1000); err != nil {
		t.Fatal(err)
	}
	if err := deskDB.SetRating(ctx, []int64{img1.ID}, 4, 1000); err != nil {
		t.Fatal(err)
	}

	// Re-open folder A: the backfill path writes sidecars for catalog intent.
	if _, _, err := desk.OpenFolder(ctx, folderA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecar.PathFor(filepath.Join(folderA, "IMG1.ARW"))); err != nil {
		t.Fatalf("sidecar for edited photo not written: %v", err)
	}
	if _, err := os.Stat(sidecar.PathFor(filepath.Join(folderA, "IMG2.ARW"))); !os.IsNotExist(err) {
		t.Fatalf("un-edited photo should have no sidecar (err=%v)", err)
	}

	// --- "Laptop" machine: copy the folder to a new path, fresh catalog ---
	folderB := filepath.Join(t.TempDir(), "shoot-copy")
	copyDir(t, folderA, folderB)

	lapDB := openDB(t)
	lap := &Scanner{DB: lapDB}
	fidB, countB, err := lap.OpenFolder(ctx, folderB)
	if err != nil {
		t.Fatalf("open folder B: %v", err)
	}
	if countB != 2 {
		t.Fatalf("expected 2 photos in copy, got %d", countB)
	}

	// The edit and rating rode along with the folder.
	img1B := photoByName(t, lapDB, fidB, "IMG1.ARW")
	if img1B.Rating != 4 {
		t.Fatalf("rating not carried: got %d", img1B.Rating)
	}
	if !img1B.EditParams.Valid {
		t.Fatal("edit not carried: edit_params is NULL")
	}
	ep, err := edit.Parse(img1B.EditParams.String)
	if err != nil {
		t.Fatalf("imported edit unparseable: %v", err)
	}
	if ep.ExpEV != 0.75 || ep.Contrast != 0.2 {
		t.Fatalf("edit values wrong after import: %+v", ep)
	}
	// The hash is recomputed canonically on import, not the desktop's opaque one.
	if img1B.EditHash == "base" || img1B.EditHash == "deskhash" {
		t.Fatalf("edit hash not canonicalized on import: %q", img1B.EditHash)
	}
	if img1B.EditHash != ep.Hash() {
		t.Fatalf("edit hash mismatch: row=%q want=%q", img1B.EditHash, ep.Hash())
	}

	// The un-edited photo stayed neutral.
	if img2B := photoByName(t, lapDB, fidB, "IMG2.ARW"); img2B.Rating != 0 || img2B.EditParams.Valid {
		t.Fatalf("un-edited photo picked up state: %+v", img2B)
	}
}

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeFakeRaw(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func photoByName(t *testing.T, db *store.DB, folderID int64, name string) store.Photo {
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
	t.Fatalf("photo %q not found in folder %d", name, folderID)
	return store.Photo{}
}

// copyDir copies every regular file from src to a freshly created dst,
// mirroring a "copy the folder to the other machine" operation (sidecars
// included).
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
