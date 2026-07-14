package pyramid

import (
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

func writeCacheFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCacheStatAndClear(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeCacheFile(t, filepath.Join(dir, "ab", "f1.jpg"), "hello")  // 5
	writeCacheFile(t, filepath.Join(dir, "cd", "f2.jpg"), "worldd") // 6

	if b, n := c.Stat(); b != 11 || n != 2 {
		t.Fatalf("Stat = %d bytes / %d files, want 11/2", b, n)
	}
	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if b, n := c.Stat(); b != 0 || n != 0 {
		t.Fatalf("after Clear Stat = %d/%d, want 0/0", b, n)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cache dir should survive Clear: %v", err)
	}
}

func TestCacheRelocateWipesOld(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "old")
	c, err := New(old, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeCacheFile(t, filepath.Join(old, "ab", "f.jpg"), "x")

	newDir := filepath.Join(base, "new")
	if err := c.Relocate(newDir); err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	if c.Dir() != filepath.Clean(newDir) {
		t.Fatalf("Dir = %q, want %q", c.Dir(), filepath.Clean(newDir))
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old cache should be wiped after relocate (err=%v)", err)
	}
	writeCacheFile(t, filepath.Join(newDir, "cd", "g.jpg"), "yy")
	if b, _ := c.Stat(); b != 2 {
		t.Fatalf("new cache Stat = %d bytes, want 2", b)
	}
}

// TestCacheRelocateNestedGuard verifies the wipe never deletes an ancestor of
// the new location (which would take the new cache down with it).
func TestCacheRelocateNestedGuard(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "cache")
	c, err := New(old, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(old, "ab", "f.jpg")
	writeCacheFile(t, keep, "data")

	inner := filepath.Join(old, "marraw-previews") // new dir inside old
	if err := c.Relocate(inner); err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	if c.Dir() != filepath.Clean(inner) {
		t.Fatalf("Dir = %q, want %q", c.Dir(), filepath.Clean(inner))
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("nested relocate must not wipe the ancestor cache: %v", err)
	}
}

// TestGenerateDerivesSmallLevelsFrom2048: a fixed-level request for an edit
// whose 2048 rendition exists must be served by downscaling that JPEG — the
// nil processor proves the RAW route is never reached. This is the browse
// path for freshly edited photos: the commit settle writes only the 2048.
func TestGenerateDerivesSmallLevelsFrom2048(t *testing.T) {
	c, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	params := &edit.Params{Contrast: 0.3}
	hash := params.Hash()
	raw, _ := json.Marshal(params)
	photo := store.Photo{
		ID: 1, CacheKey: "aabbccddeeff00112233445566778899",
		EditHash: hash, EditParams: sql.NullString{String: string(raw), Valid: true},
	}

	// Warm 2048 (the state right after a commit settle).
	src := image.NewRGBA(image.Rect(0, 0, 2048, 1365))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 31)
	}
	if err := c.writeJPEG(src, photo.CacheKey, "2048", hash, 80); err != nil {
		t.Fatal(err)
	}

	// nil processor: reaching proc.Open (the RAW route) would panic.
	if err := c.generate(context.Background(), nil, photo, "512", hash, decode.PriorityVisible); err != nil {
		t.Fatalf("derive-from-2048 failed: %v", err)
	}
	for _, level := range []string{"1024", "512", "256"} {
		if _, err := os.Stat(c.PathFor(photo.CacheKey, level, hash)); err != nil {
			t.Errorf("level %s not derived: %v", level, err)
		}
	}
}
