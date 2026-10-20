package pyramid

import (
	"encoding/json"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

// TestLookGammaIndependentOfRenderOrder: two catalogs, one photo, one edit.
// One draws the photo unedited first, as a catalog that had it before it was
// edited; the other meets it with the edit already on, as one that read the
// edit from a sidecar. Both must measure the same tone lift, and so render
// the same picture. The second used to render over FallbackLookGamma, much
// brighter for a dark scene, and an edit made to look right there came out
// dark in the first.
func TestLookGammaIndependentOfRenderOrder(t *testing.T) {
	raw := sampleRAW(t)
	fi, err := os.Stat(raw)
	if err != nil {
		t.Fatal(err)
	}
	params := &edit.Params{ExpEV: 0.5, Contrast: 0.2}
	params.Normalize()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	editJSON, hash := string(b), params.Hash()

	render := func(baseFirst bool) (float64, *image.RGBA) {
		ctx := t.Context()
		db, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		pool := decode.NewPool(1)
		defer pool.Close()
		c, err := New(t.TempDir(), pool, db)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		dir := filepath.Dir(raw)
		folderID, err := db.UpsertFolder(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		entry := store.FileEntry{Name: filepath.Base(raw), Size: fi.Size(), MtimeNs: fi.ModTime().UnixNano()}
		if _, err := db.SyncFolder(ctx, folderID, dir, []store.FileEntry{entry}); err != nil {
			t.Fatal(err)
		}
		photos, err := db.ListPhotos(ctx, folderID)
		if err != nil || len(photos) != 1 {
			t.Fatalf("ListPhotos = %d photos, %v", len(photos), err)
		}
		photo := photos[0]
		if baseFirst {
			if _, err := c.Ensure(ctx, photo, "2048", edit.BaseHash, decode.PriorityVisible); err != nil {
				t.Fatalf("base render: %v", err)
			}
		}
		if err := db.SetEdit(ctx, photo.ID, &editJSON, hash, 1); err != nil {
			t.Fatal(err)
		}
		if photo, err = db.GetPhoto(ctx, photo.ID); err != nil {
			t.Fatal(err)
		}
		path, err := c.Ensure(ctx, photo, "2048", hash, decode.PriorityVisible)
		if err != nil {
			t.Fatalf("edited render: %v", err)
		}
		if photo, err = db.GetPhoto(ctx, photo.ID); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img, err := jpeg.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		return photo.LookGamma, toRGBA(img)
	}

	gBase, imgBase := render(true)
	gEdit, imgEdit := render(false)
	t.Logf("look gamma: base render first %.4f, edit first %.4f; mean luma %.1f vs %.1f",
		gBase, gEdit, MeanLuma(imgBase), MeanLuma(imgEdit))
	if gBase == 0 || gEdit == 0 {
		t.Fatalf("look gamma not stored: base first %v, edit first %v", gBase, gEdit)
	}
	// The base render measures on its own 2048 decode and the edited one on
	// a half-size base decode of its own; the same scene either way.
	if math.Abs(gBase-gEdit) > 0.01 {
		t.Errorf("look gamma differs by render order: %.4f with a base render first, %.4f without", gBase, gEdit)
	}
	if d := math.Abs(MeanLuma(imgBase) - MeanLuma(imgEdit)); d > 1 {
		t.Errorf("the same edit renders %.1f levels apart by render order", d)
	}
}
