package pyramid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

// fullTestCatalog is a catalog of two copies of the sample RAW, photos 0
// and 1, with a cache over it.
type fullTestCatalog struct {
	t     *testing.T
	db    *store.DB
	cache *Cache
	ids   [2]int64
}

func newFullTestCatalog(t *testing.T) *fullTestCatalog {
	t.Helper()
	raw := sampleRAW(t)
	dir := t.TempDir()
	var entries []store.FileEntry
	for _, name := range []string{"a", "b"} {
		name += filepath.Ext(raw)
		copyFile(t, raw, filepath.Join(dir, name))
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, store.FileEntry{Name: name, Size: fi.Size(), MtimeNs: fi.ModTime().UnixNano()})
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
	folderID, err := db.UpsertFolder(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SyncFolder(ctx, folderID, dir, entries); err != nil {
		t.Fatal(err)
	}
	photos, err := db.ListPhotos(ctx, folderID)
	if err != nil || len(photos) != 2 {
		t.Fatalf("ListPhotos = %d photos, %v", len(photos), err)
	}
	f := &fullTestCatalog{t: t, db: db, cache: c}
	for _, p := range photos {
		if p.FileName == entries[0].Name {
			f.ids[0] = p.ID
		} else {
			f.ids[1] = p.ID
		}
	}
	return f
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	if os.Link(from, to) == nil {
		return
	}
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}

// edit saves params as photo i's edit and returns the photo as it is now.
func (f *fullTestCatalog) edit(i int, params edit.Params) store.Photo {
	f.t.Helper()
	params.Normalize()
	b, err := json.Marshal(&params)
	if err != nil {
		f.t.Fatal(err)
	}
	js := string(b)
	if err := f.db.SetEdit(f.t.Context(), f.ids[i], &js, params.Hash(), 1); err != nil {
		f.t.Fatal(err)
	}
	return f.photo(i)
}

func (f *fullTestCatalog) photo(i int) store.Photo {
	f.t.Helper()
	p, err := f.db.GetPhoto(f.t.Context(), f.ids[i])
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

// open holds photo i open in the editor, as Edits.OpenInEditor does, until
// the test ends or release is called.
func (f *fullTestCatalog) open(i int) (release func()) {
	f.t.Helper()
	release = f.cache.HoldEditor(f.photo(i))
	f.t.Cleanup(release)
	return release
}

// tile renders photo i's 1:1 tiles under its saved edit at prio and returns
// the first one's bytes.
func (f *fullTestCatalog) tile(i int, prio decode.Priority) []byte {
	f.t.Helper()
	p := f.photo(i)
	path, err := f.cache.EnsureTile(f.t.Context(), p, 0, 0, p.EditHash, prio)
	if err != nil {
		f.t.Fatalf("1:1 render: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *fullTestCatalog) decodes(want int64, what string) {
	f.t.Helper()
	if n := f.cache.fullDecodes.Load(); n != want {
		f.t.Errorf("%s: %d decodes in all, want %d", what, n, want)
	}
}

// TestFullRenderReusesKeptDecode: after a 1:1 render of the editor's
// photo, an edit that leaves the LibRaw inputs alone renders its tiles from
// the kept decode — no second demosaic — and they are byte for byte the
// tiles a cold render gives. An exposure change is a different decode and
// decodes again.
func TestFullRenderReusesKeptDecode(t *testing.T) {
	warm := newFullTestCatalog(t)
	warm.open(0)
	warm.edit(0, edit.Params{Contrast: 0.1})
	warm.tile(0, decode.PriorityVisible)
	warm.decodes(1, "first 1:1 render")

	look := edit.Params{Contrast: 0.4, Saturation: 0.2, CropX: 0.1, CropY: 0.1, CropW: 0.7, CropH: 0.7}
	warm.edit(0, look)
	got := warm.tile(0, decode.PriorityVisible)
	warm.decodes(1, "a look-stage edit's 1:1 render")

	cold := newFullTestCatalog(t)
	cold.edit(0, look)
	if want := cold.tile(0, decode.PriorityVisible); !bytes.Equal(got, want) {
		t.Errorf("tile from the kept decode differs from a cold render's (%d vs %d bytes)", len(got), len(want))
	}

	warm.edit(0, edit.Params{Contrast: 0.4, ExpEV: 0.5})
	warm.tile(0, decode.PriorityVisible)
	warm.decodes(2, "an exposure change")
}

// TestWarmFullServesTheNextRender: WarmFull decodes once, a second WarmFull
// is free, and the 1:1 render of the next look-stage edit starts from it.
func TestWarmFullServesTheNextRender(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	p := f.edit(0, edit.Params{Contrast: 0.1})
	start := &edit.Params{Contrast: 0.1}
	start.Normalize()
	for range 2 {
		if err := f.cache.WarmFull(t.Context(), p, start); err != nil {
			t.Fatal(err)
		}
	}
	f.edit(0, edit.Params{Contrast: 0.3, Vibrance: 0.2})
	f.tile(0, decode.PriorityVisible)
	f.decodes(1, "warm-ups and a render")
}

// TestWarmFullCancelled: a cancelled warm-up keeps nothing, and the render
// after it decodes for itself.
func TestWarmFullCancelled(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	p := f.edit(0, edit.Params{Contrast: 0.1})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.cache.WarmFull(ctx, p, &edit.Params{}); err == nil {
		t.Fatal("cancelled WarmFull returned no error")
	}
	f.tile(0, decode.PriorityVisible)
	f.decodes(1, "a render after a cancelled warm-up")
}

// TestWarmFullUneditedServesFirstEdit: the editor warms an unedited photo
// from the neutral starting state, and its first look-stage edit renders
// from that decode. Warming the base look would not serve it: no edit
// decodes auto-brightened.
func TestWarmFullUneditedServesFirstEdit(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	if err := f.cache.WarmFull(t.Context(), f.photo(0), &edit.Params{}); err != nil {
		t.Fatal(err)
	}
	f.edit(0, edit.Params{Contrast: 0.3})
	f.tile(0, decode.PriorityVisible)
	f.decodes(1, "first edit after warming an unedited photo")
}

// TestOtherRendersKeepEditorsDecode: 1:1 renders of another photo — at any
// priority, as the pre-render pass, the loupe's neighbour prefetch and a
// share-link guest make them — leave the editor's kept decode alone.
func TestOtherRendersKeepEditorsDecode(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	f.edit(0, edit.Params{Contrast: 0.1})
	f.tile(0, decode.PriorityVisible)

	f.edit(1, edit.Params{Contrast: 0.2})
	f.tile(1, decode.PriorityVisible)
	f.decodes(2, "another photo's render")
	f.edit(1, edit.Params{Contrast: 0.25})
	f.tile(1, decode.PriorityBackground)
	f.decodes(3, "another photo's background render: nothing of it was kept")

	f.edit(0, edit.Params{Contrast: 0.5})
	f.tile(0, decode.PriorityVisible)
	f.decodes(3, "the editor's photo after other photos' renders")
}

// TestLeftPhotoNotKept: a decode of a photo the editor has left by the time
// it finishes is not kept, and the hold's end lets the kept decode go idle.
func TestLeftPhotoNotKept(t *testing.T) {
	f := newFullTestCatalog(t)
	release := f.open(0)
	release()
	start := &edit.Params{Contrast: 0.1}
	start.Normalize()
	if err := f.cache.WarmFull(t.Context(), f.photo(0), start); err != nil {
		t.Fatal(err)
	}
	f.edit(0, *start)
	f.tile(0, decode.PriorityVisible)
	f.decodes(2, "a render after a warm-up of a photo no longer open")
}

// TestBaseRenderNotKept: the editor's photo's base look is not kept: it is
// a decode no edit can start from, and keeping it would push out the one an
// edit can.
func TestBaseRenderNotKept(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	if err := f.cache.WarmFull(t.Context(), f.photo(0), &edit.Params{}); err != nil {
		t.Fatal(err)
	}
	f.tile(0, decode.PriorityVisible) // unedited: the base look
	f.decodes(2, "warm-up and a base render")
	f.edit(0, edit.Params{Contrast: 0.3})
	f.tile(0, decode.PriorityVisible)
	f.decodes(2, "first edit after a base render")
}

// TestRenderAndWarmFullDecodeOnce: a 1:1 render and a warm-up of the same
// decode at the same time decode once, whichever starts first, and a
// warm-up called off midway leaves the render to decode for itself.
func TestRenderAndWarmFullDecodeOnce(t *testing.T) {
	for _, cancelWarm := range []bool{false, true} {
		f := newFullTestCatalog(t)
		f.open(0)
		start := &edit.Params{Contrast: 0.1}
		start.Normalize()
		p := f.edit(0, *start)
		ctx, cancel := context.WithCancel(t.Context())
		warmed := make(chan error, 1)
		go func() { warmed <- f.cache.WarmFull(ctx, p, start) }()
		if cancelWarm {
			time.Sleep(100 * time.Millisecond) // the warm-up's decode is under way
			cancel()
		}
		f.tile(0, decode.PriorityVisible)
		err := <-warmed
		cancel()
		if !cancelWarm && err != nil {
			t.Fatalf("WarmFull: %v", err)
		}
		what := "render and warm-up"
		if cancelWarm {
			what = "render and a called-off warm-up"
		}
		f.decodes(1, what)
	}
}

// TestKeepFullPrefersLaterAsk: of two decodes of the editor's photo, the one
// asked for later stays kept whichever finishes last — a warm-up of the
// starting state finishing after the decode of an edit made since does not
// push that one out.
func TestKeepFullPrefersLaterAsk(t *testing.T) {
	c := &Cache{}
	photo := store.Photo{CacheKey: "k"}
	defer c.HoldEditor(photo)()
	px := image.NewRGBA(image.Rect(0, 0, 1, 1))
	older := &fullDecode{cacheKey: "k", key: "start", seq: 1, rgba: px}
	newer := &fullDecode{cacheKey: "k", key: "edit", seq: 2, rgba: px}
	c.keepFull(newer)
	c.keepFull(older)
	if c.fullEntry != newer {
		t.Errorf("kept %q, want the later-asked %q", c.fullEntry.key, newer.key)
	}
	c.closeFull()
}

// TestFlightFailureShared: when the decode others wait on fails, they fail
// the same instead of each reading and decoding the file again in turn.
func TestFlightFailureShared(t *testing.T) {
	c := &Cache{}
	photo := store.Photo{CacheKey: "k"}
	defer c.HoldEditor(photo)()
	edits := &edit.Params{}
	broken := errors.New("unreadable file")
	inOpen, fail := make(chan struct{}), make(chan struct{})
	owner := make(chan error, 1)
	go func() {
		_, _, err := c.keptFullDecode(t.Context(), nil, photo, edits, func() error {
			close(inOpen)
			<-fail
			return broken
		})
		owner <- err
	}()
	<-inOpen
	waiter := make(chan error, 1)
	opened := false
	go func() {
		_, _, err := c.keptFullDecode(t.Context(), nil, photo, edits, func() error {
			opened = true
			return nil
		})
		waiter <- err
	}()
	time.Sleep(50 * time.Millisecond) // the waiter waits on the flight
	close(fail)
	if err := <-owner; !errors.Is(err, broken) {
		t.Fatalf("owner: %v", err)
	}
	if err := <-waiter; !errors.Is(err, broken) {
		t.Errorf("waiter: %v, want the owner's error", err)
	}
	if opened {
		t.Error("the waiter opened the file again")
	}
}
