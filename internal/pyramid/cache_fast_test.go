package pyramid

import (
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

// TestEnsureFastThumbProvisional: a cold 2048 request served from the embedded
// JPEG. The nil decode pool proves the RAW route is never reached. The
// critical invariant: the provisional must live OUTSIDE PathFor's namespace —
// the pre-render pass selects work by Stat(PathFor(ck, "2048", hash)), and a
// provisional that satisfied it would silently disable pre-rendering for the
// photo, making the base-look stand-in permanent.
func TestEnsureFastThumbProvisional(t *testing.T) {
	raw := sampleRAW(t)
	c, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	photo := store.Photo{
		FolderPath: filepath.Dir(raw),
		FileName:   filepath.Base(raw),
		CacheKey:   "fasttest0011223344556677",
	}
	ctx := context.Background()

	path, provisional, err := c.EnsureFast(ctx, photo, "2048", edit.BaseHash)
	if err != nil {
		t.Fatalf("EnsureFast(2048): %v", err)
	}
	if !provisional {
		t.Fatalf("EnsureFast(2048) = %q provisional=false, want a provisional on a cold cache", path)
	}
	if want := c.PathForProvisional(photo.CacheKey, 2048); path != want {
		t.Fatalf("EnsureFast(2048) = %q, want %q", path, want)
	}
	if _, err := os.Stat(c.PathFor(photo.CacheKey, "2048", edit.BaseHash)); err == nil {
		t.Fatal("provisional derivation wrote the REAL 2048 path — the pre-render pass would skip this photo forever")
	}

	// Canonical thumb territory (base, ≤1024) comes back as a real cache file
	// from the same derivation — identical semantics to the plain thumb route.
	path, provisional, err = c.EnsureFast(ctx, photo, "512", edit.BaseHash)
	if err != nil {
		t.Fatalf("EnsureFast(512): %v", err)
	}
	if provisional {
		t.Errorf("EnsureFast(512, base) = provisional, want the real thumb-route file")
	}
	if want := c.PathFor(photo.CacheKey, "512", edit.BaseHash); path != want {
		t.Errorf("EnsureFast(512) = %q, want %q", path, want)
	}
}

// TestEnsureFastDerivesFrom2048: with the edit's 2048 already on disk, a
// smaller level is a pure-Go downscale into the REAL rendition — again with a
// nil decode pool, and without touching the photo's file at all (the path
// points nowhere).
func TestEnsureFastDerivesFrom2048(t *testing.T) {
	c, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	params := &edit.Params{Contrast: 0.3}
	hash := params.Hash()
	raw, _ := json.Marshal(params)
	photo := store.Photo{
		ID: 1, CacheKey: "aabbccddeeff00112233445566778899",
		EditHash: hash, EditParams: sql.NullString{String: string(raw), Valid: true},
	}
	src := image.NewRGBA(image.Rect(0, 0, 2048, 1365))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 31)
	}
	if err := c.writeJPEG(src, photo.CacheKey, "2048", hash, 80); err != nil {
		t.Fatal(err)
	}

	path, provisional, err := c.EnsureFast(context.Background(), photo, "512", hash)
	if err != nil {
		t.Fatalf("EnsureFast: %v", err)
	}
	if provisional {
		t.Error("EnsureFast = provisional, want the real derived rendition")
	}
	if want := c.PathFor(photo.CacheKey, "512", hash); path != want {
		t.Errorf("EnsureFast = %q, want %q", path, want)
	}
}

// TestEnsureFastNothingDerivable: a file LibRaw can't open yields an error
// (the handler's 404), not a hang or a decode attempt.
func TestEnsureFastNothingDerivable(t *testing.T) {
	c, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	dir := t.TempDir()
	junk := filepath.Join(dir, "x.arw")
	if err := os.WriteFile(junk, []byte("not a raw file"), 0o644); err != nil {
		t.Fatal(err)
	}
	photo := store.Photo{FolderPath: dir, FileName: "x.arw", CacheKey: "junktest0011223344556677"}
	if _, _, err := c.EnsureFast(context.Background(), photo, "2048", edit.BaseHash); err == nil {
		t.Fatal("EnsureFast on an unreadable file = nil error, want failure")
	}
}
