package pyramid

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

// fullTestCatalog is a catalog of the one sample RAW with a cache over it.
type fullTestCatalog struct {
	t     *testing.T
	db    *store.DB
	cache *Cache
	id    int64
}

func newFullTestCatalog(t *testing.T) *fullTestCatalog {
	t.Helper()
	raw := sampleRAW(t)
	fi, err := os.Stat(raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pool := decode.NewPool(2)
	t.Cleanup(pool.Close)
	c, err := New(t.TempDir(), pool, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
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
	return &fullTestCatalog{t: t, db: db, cache: c, id: photos[0].ID}
}

// edit saves params as the photo's edit and returns the photo as it is now.
func (f *fullTestCatalog) edit(params edit.Params) store.Photo {
	f.t.Helper()
	params.Normalize()
	b, err := json.Marshal(&params)
	if err != nil {
		f.t.Fatal(err)
	}
	js := string(b)
	if err := f.db.SetEdit(f.t.Context(), f.id, &js, params.Hash(), 1); err != nil {
		f.t.Fatal(err)
	}
	return f.photo()
}

func (f *fullTestCatalog) photo() store.Photo {
	f.t.Helper()
	p, err := f.db.GetPhoto(f.t.Context(), f.id)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

// tile renders the photo's 1:1 tiles under its saved edit and returns the
// first one's bytes.
func (f *fullTestCatalog) tile() []byte {
	f.t.Helper()
	p := f.photo()
	path, err := f.cache.EnsureTile(f.t.Context(), p, 0, 0, p.EditHash, decode.PriorityVisible)
	if err != nil {
		f.t.Fatalf("1:1 render: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

// TestFullRenderReusesKeptDecode: after a 1:1 render, an edit that leaves
// the LibRaw inputs alone renders its tiles from the kept decode — no second
// demosaic — and they are byte for byte the tiles a cold render gives. An
// exposure change is a different decode and decodes again.
func TestFullRenderReusesKeptDecode(t *testing.T) {
	warm := newFullTestCatalog(t)
	warm.edit(edit.Params{Contrast: 0.1})
	warm.tile()
	if n := warm.cache.fullDecodes.Load(); n != 1 {
		t.Fatalf("first 1:1 render ran %d decodes, want 1", n)
	}

	look := edit.Params{Contrast: 0.4, Saturation: 0.2, CropX: 0.1, CropY: 0.1, CropW: 0.7, CropH: 0.7}
	warm.edit(look)
	got := warm.tile()
	if n := warm.cache.fullDecodes.Load(); n != 1 {
		t.Errorf("a look-stage edit's 1:1 render decoded again: %d decodes in all", n)
	}

	cold := newFullTestCatalog(t)
	cold.edit(look)
	if want := cold.tile(); !bytes.Equal(got, want) {
		t.Errorf("tile from the kept decode differs from a cold render's (%d vs %d bytes)", len(got), len(want))
	}

	warm.edit(edit.Params{Contrast: 0.4, ExpEV: 0.5})
	warm.tile()
	if n := warm.cache.fullDecodes.Load(); n != 2 {
		t.Errorf("an exposure change should decode again: %d decodes in all, want 2", n)
	}
}

// TestWarmFullServesTheNextRender: WarmFull decodes once, a second WarmFull
// is free, and the 1:1 render of the next look-stage edit starts from it.
func TestWarmFullServesTheNextRender(t *testing.T) {
	f := newFullTestCatalog(t)
	p := f.edit(edit.Params{Contrast: 0.1})
	start := &edit.Params{Contrast: 0.1}
	start.Normalize()
	for range 2 {
		if err := f.cache.WarmFull(t.Context(), p, start); err != nil {
			t.Fatal(err)
		}
	}
	f.edit(edit.Params{Contrast: 0.3, Vibrance: 0.2})
	f.tile()
	if n := f.cache.fullDecodes.Load(); n != 1 {
		t.Errorf("warm + render ran %d decodes, want 1", n)
	}
}

// TestWarmFullCancelled: a cancelled warm-up keeps nothing, and the render
// after it decodes for itself.
func TestWarmFullCancelled(t *testing.T) {
	f := newFullTestCatalog(t)
	p := f.edit(edit.Params{Contrast: 0.1})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.cache.WarmFull(ctx, p, &edit.Params{}); err == nil {
		t.Fatal("cancelled WarmFull returned no error")
	}
	f.tile()
	if n := f.cache.fullDecodes.Load(); n != 1 {
		t.Errorf("render after a cancelled warm-up ran %d decodes, want 1", n)
	}
}

// TestWarmFullUneditedServesFirstEdit: the editor warms an unedited photo
// from the neutral starting state, and its first look-stage edit renders
// from that decode. Warming the base look would not serve it: no edit
// decodes auto-brightened.
func TestWarmFullUneditedServesFirstEdit(t *testing.T) {
	f := newFullTestCatalog(t)
	if err := f.cache.WarmFull(t.Context(), f.photo(), &edit.Params{}); err != nil {
		t.Fatal(err)
	}
	f.edit(edit.Params{Contrast: 0.3})
	f.tile()
	if n := f.cache.fullDecodes.Load(); n != 1 {
		t.Errorf("first edit after warming an unedited photo: %d decodes, want 1", n)
	}
}

// TestBackgroundRenderKeepsEditorsDecode: a background 1:1 render (the
// pre-render pass) decodes for itself and leaves the kept decode alone.
func TestBackgroundRenderKeepsEditorsDecode(t *testing.T) {
	f := newFullTestCatalog(t)
	f.edit(edit.Params{Contrast: 0.1})
	f.tile()

	p := f.edit(edit.Params{ExpEV: 0.7})
	if _, err := f.cache.EnsureTile(t.Context(), p, 0, 0, p.EditHash, decode.PriorityBackground); err != nil {
		t.Fatal(err)
	}
	if n := f.cache.fullDecodes.Load(); n != 2 {
		t.Fatalf("background render of a new decode: %d decodes in all, want 2", n)
	}

	f.edit(edit.Params{Contrast: 0.5})
	f.tile()
	if n := f.cache.fullDecodes.Load(); n != 2 {
		t.Errorf("the background render pushed out the kept decode: %d decodes in all, want 2", n)
	}
}

// TestRenderAndWarmFullDecodeOnce: a 1:1 render and a warm-up of the same
// decode at the same time decode once, whichever starts first, and a
// warm-up called off midway leaves the render to decode for itself.
func TestRenderAndWarmFullDecodeOnce(t *testing.T) {
	for _, cancelWarm := range []bool{false, true} {
		f := newFullTestCatalog(t)
		start := &edit.Params{Contrast: 0.1}
		start.Normalize()
		p := f.edit(*start)
		ctx, cancel := context.WithCancel(t.Context())
		warmed := make(chan error, 1)
		go func() { warmed <- f.cache.WarmFull(ctx, p, start) }()
		if cancelWarm {
			time.Sleep(100 * time.Millisecond) // the warm-up's decode is under way
			cancel()
		}
		f.tile()
		err := <-warmed
		cancel()
		if !cancelWarm && err != nil {
			t.Fatalf("WarmFull: %v", err)
		}
		if n := f.cache.fullDecodes.Load(); n != 1 {
			t.Errorf("cancelWarm=%v: %d finished decodes, want 1", cancelWarm, n)
		}
	}
}
