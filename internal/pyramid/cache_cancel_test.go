package pyramid

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

// TestEnsurePreCancelled: an Ensure whose ctx is already cancelled surfaces
// context.Canceled and leaves the cache empty — the pool drops the abandoned
// job before it starts, or the render bails at its first checkpoint (worst
// case inside the LibRaw decode, which cannot produce cache files).
func TestEnsurePreCancelled(t *testing.T) {
	raw := sampleRAW(t)
	dir := t.TempDir()
	pool := decode.NewPool(1)
	defer pool.Close()
	c, err := New(dir, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	photo := store.Photo{
		FolderPath: filepath.Dir(raw),
		FileName:   filepath.Base(raw),
		CacheKey:   "canceltest00",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Ensure(ctx, photo, "2048", edit.BaseHash, decode.PriorityVisible); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ensure = %v, want context.Canceled", err)
	}
	// The deferred pool.Close above hasn't run yet, so wait here: it joins the
	// worker, guaranteeing any started render has bailed before we count files.
	pool.Close()
	files := 0
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files++
		}
		return nil
	})
	if files != 0 {
		t.Fatalf("cancelled Ensure left %d cache files", files)
	}
}
