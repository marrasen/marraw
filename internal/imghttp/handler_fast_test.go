package imghttp

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// TestServeFastCaching: a provisional (thumb-derived) body must go out
// no-store and flagged — its content-addressed URL will later name the real
// render, and an immutably-cached provisional would impersonate it forever.
// Once the real file exists, the same fast URL serves it immutable. A photo
// with nothing derivable 404s instead of rendering.
func TestServeFastCaching(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	folderPath := t.TempDir() // photo files point here; none exist
	folderID, err := db.UpsertFolder(ctx, folderPath)
	if err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	if _, err := db.SyncFolder(ctx, folderID, folderPath, []store.FileEntry{{Name: "a.arw", Size: 1, MtimeNs: 1}}); err != nil {
		t.Fatalf("SyncFolder: %v", err)
	}
	photos, err := db.ListPhotos(ctx, folderID)
	if err != nil || len(photos) != 1 {
		t.Fatalf("ListPhotos: %v (%d photos)", err, len(photos))
	}
	p := photos[0]

	cache, err := pyramid.New(t.TempDir(), nil, db)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	h := &Handler{DB: db, Cache: cache}

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", fmt.Sprintf("/img/%d/2048?fast=1", p.ID), nil)
		req.SetPathValue("id", fmt.Sprint(p.ID))
		req.SetPathValue("level", "2048")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	writeJPEG := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing on disk, no readable photo file: fast must 404, never render.
	if w := get(); w.Code != 404 {
		t.Fatalf("fast with nothing derivable = %d, want 404", w.Code)
	}

	// A provisional on disk is served no-store + flagged.
	writeJPEG(cache.PathForProvisional(p.CacheKey, 2048))
	w := get()
	if w.Code != 200 {
		t.Fatalf("fast provisional = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("provisional Cache-Control = %q, want no-store", cc)
	}
	if w.Header().Get("X-Marraw-Provisional") != "1" {
		t.Error("provisional response missing X-Marraw-Provisional")
	}

	// The real render, once it lands, wins under the same URL — immutable.
	writeJPEG(cache.PathFor(p.CacheKey, "2048", edit.BaseHash))
	w = get()
	if w.Code != 200 {
		t.Fatalf("fast real = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
		t.Errorf("real Cache-Control = %q, want immutable", cc)
	}
	if w.Header().Get("X-Marraw-Provisional") != "" {
		t.Error("real response carries X-Marraw-Provisional")
	}
}

// The edit hash is caller-chosen and is composed into a cache file name, so
// only the shapes this server mints may get that far. A rendition already on
// disk is served before generatable() runs, so that check is not the guard.
func TestValidEditHash(t *testing.T) {
	for _, ok := range []string{"base", "0123456789ab", "abcdef012345"} {
		if !validEditHash(ok) {
			t.Errorf("validEditHash(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"",                    // absent is substituted earlier, never validated
		"../../../etc/passwd", // the shape the constraint exists for
		"..",                  //
		"0123456789AB",        // Hash() is lower-case hex
		"0123456789ab0",       // too long
		"0123456789a",         // too short
		"0123456789a/",        // a separator of any kind
		"base ",               //
		"g123456789ab",        // out of the hex alphabet
	} {
		if validEditHash(bad) {
			t.Errorf("validEditHash(%q) = true, want false", bad)
		}
	}
}
