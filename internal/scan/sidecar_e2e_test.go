package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/foldermeta"
	"github.com/marrasen/marraw/internal/sidecar"
	"github.com/marrasen/marraw/internal/store"
)

// TestCopyFolderCarriesEditsViaSidecars is the end-to-end proof of the whole
// feature: edit a folder on one machine, copy the folder (RAWs and its
// marraw.json) to another machine with an independent catalog, and confirm
// the edits reappear.
// It drives the real production entry point (Scanner.OpenFolder) throughout —
// no test-only shortcuts around the import/backfill logic.
func TestCopyFolderCarriesEditsViaSidecars(t *testing.T) {
	ctx := context.Background()

	// --- "Desktop" machine: catalog + a folder of RAWs ---
	deskDB := openDB(t)
	desk := &Scanner{DB: deskDB, Meta: foldermeta.NewWriter(time.Hour)}

	folderA := filepath.Join(t.TempDir(), "shoot")
	if err := os.MkdirAll(folderA, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeRaw(t, folderA, "IMG1.ARW", "the-first-frame")
	writeFakeRaw(t, folderA, "IMG2.ARW", "second")

	fidA, countA, err := desk.OpenFolder(ctx, folderA, false)
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

	// Re-open folder A: the catalog's intent goes into the folder's
	// marraw.json, and no per-photo sidecar is made.
	if _, _, err := desk.OpenFolder(ctx, folderA, false); err != nil {
		t.Fatal(err)
	}
	desk.Meta.Flush()
	mf, err := foldermeta.Read(folderA)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := mf.Photos["IMG1.ARW"]; !ok || e.Rating != 4 || len(e.Edit) == 0 || e.UpdatedAt == 0 {
		t.Fatalf("marraw.json holds %+v for the edited photo", e)
	}
	if _, ok := mf.Photos["IMG2.ARW"]; ok {
		t.Fatal("marraw.json holds an entry for the photo nobody touched")
	}
	for _, n := range []string{"IMG1.ARW", "IMG2.ARW"} {
		if _, err := os.Stat(sidecar.PathFor(filepath.Join(folderA, n))); !os.IsNotExist(err) {
			t.Fatalf("a per-photo sidecar was made for %s (err=%v)", n, err)
		}
	}

	// --- "Laptop" machine: copy the folder to a new path, fresh catalog ---
	folderB := filepath.Join(t.TempDir(), "shoot-copy")
	copyDir(t, folderA, folderB)

	lapDB := openDB(t)
	lap := &Scanner{DB: lapDB}
	fidB, countB, err := lap.OpenFolder(ctx, folderB, false)
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

// An old per-photo sidecar is still read, wins where it is the newest, and is
// carried into marraw.json; the old file itself is left where it is.
func TestAnOldSidecarIsReadAndCarriedIntoTheFolderFile(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s := &Scanner{DB: db, Meta: foldermeta.NewWriter(time.Hour)}
	dir := filepath.Join(t.TempDir(), "shoot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeRaw(t, dir, "OLD.ARW", "old-frame")
	writeFakeRaw(t, dir, "NEW.ARW", "new-frame")
	old := sidecar.Build("OLD.ARW", int64(len("old-frame")), 3, 1, "", 500)
	if err := sidecar.Write(filepath.Join(dir, "OLD.ARW"), old); err != nil {
		t.Fatal(err)
	}
	// marraw.json already has a newer word on NEW.ARW, and an older one
	// on OLD.ARW than its sidecar.
	mf := foldermeta.Empty()
	mf.SetPhoto("NEW.ARW", foldermeta.Photo{Rating: 5, UpdatedAt: 900})
	mf.SetPhoto("OLD.ARW", foldermeta.Photo{Rating: 1, UpdatedAt: 100})
	if err := foldermeta.Write(dir, mf); err != nil {
		t.Fatal(err)
	}

	fid, _, err := s.OpenFolder(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if p := photoByName(t, db, fid, "OLD.ARW"); p.Rating != 3 || p.Flag != 1 {
		t.Fatalf("the old sidecar, the newest word on OLD.ARW, gave %+v", p)
	}
	if p := photoByName(t, db, fid, "NEW.ARW"); p.Rating != 5 {
		t.Fatalf("marraw.json gave NEW.ARW rating %d, want 5", p.Rating)
	}
	s.Meta.Flush()
	got, err := foldermeta.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := got.Photos["OLD.ARW"]; e.Rating != 3 || e.Flag != 1 || e.UpdatedAt != 500 {
		t.Fatalf("marraw.json now holds %+v for OLD.ARW, want the old sidecar's", e)
	}
	if _, err := os.Stat(sidecar.PathFor(filepath.Join(dir, "OLD.ARW"))); err != nil {
		t.Fatalf("the old sidecar was removed: %v", err)
	}
}
