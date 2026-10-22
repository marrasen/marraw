package pyramid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"io"
	"math"
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
// It settles on them as the editor does (a no-op unless the photo is held
// open).
func (f *fullTestCatalog) edit(i int, params edit.Params) store.Photo {
	f.t.Helper()
	params.Normalize()
	f.cache.WantFull(f.photo(i), &params)
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

// TestLeftPhotoNotKept: a photo the editor has left is not warmed, and
// its 1:1 renders keep nothing.
func TestLeftPhotoNotKept(t *testing.T) {
	f := newFullTestCatalog(t)
	release := f.open(0)
	release()
	start := &edit.Params{Contrast: 0.1}
	start.Normalize()
	if err := f.cache.WarmFull(t.Context(), f.photo(0), start); err != nil {
		t.Fatal(err)
	}
	f.decodes(0, "a warm-up of a photo no longer open")
	f.edit(0, *start)
	f.tile(0, decode.PriorityVisible)
	if f.cache.fullEntry != nil {
		t.Error("a render of a photo no longer open kept its decode")
	}
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

// TestKeepsWhatEditorIsOn: of the decodes of the editor's photo, only the
// one the editor asked for last is kept, whichever finishes last — a
// warm-up of the start state, or the decode of a state the user has undone
// away from, finishing late does not push it out.
func TestKeepsWhatEditorIsOn(t *testing.T) {
	c := &Cache{}
	photo := store.Photo{CacheKey: "k"}
	defer c.HoldEditor(photo)()
	px := image.NewRGBA(image.Rect(0, 0, 1, 1))
	x := &fullDecode{cacheKey: "k", key: "x", rgba: px}
	y := &fullDecode{cacheKey: "k", key: "y", rgba: px}
	c.fullMu.Lock()
	c.wantLocked("k", "x")
	c.fullMu.Unlock()
	if !c.keepFull(x) {
		t.Fatal("the decode the editor is on was not kept")
	}
	if c.keepFull(y) || c.fullEntry != x {
		t.Error("a decode of a state the editor is not on replaced the one it is on")
	}
	c.fullMu.Lock()
	c.wantLocked("k", "y")
	c.fullMu.Unlock()
	if !c.keepFull(y) || c.fullEntry != y {
		t.Error("the editor moved to y, but y's decode was not kept")
	}
	c.closeFull()
}

// TestFlightFailureShared: when the decode others wait on fails, they fail
// the same instead of each reading and decoding the file again in turn.
func TestFlightFailureShared(t *testing.T) {
	c := &Cache{}
	photo := store.Photo{CacheKey: "k"}
	edits := &edit.Params{}
	broken := errors.New("unreadable file")
	inOpen, fail := make(chan struct{}), make(chan struct{})
	owner := make(chan error, 1)
	go func() {
		_, _, _, _, err := c.sharedFullDecode(t.Context(), nil, photo, edits, func() error {
			close(inOpen)
			<-fail
			return broken
		})
		owner <- err
	}()
	<-inOpen
	waiter := make(chan error, 1)
	reopened := errors.New("the waiter opened the file again")
	go func() {
		_, _, _, _, err := c.sharedFullDecode(t.Context(), nil, photo, edits, func() error {
			return reopened
		})
		waiter <- err
	}()
	for waiting := false; !waiting; time.Sleep(time.Millisecond) {
		c.fullMu.Lock()
		for _, fl := range c.fullFlights {
			waiting = fl.waiters > 0
		}
		c.fullMu.Unlock()
	}
	close(fail)
	if err := <-owner; !errors.Is(err, broken) {
		t.Fatalf("owner: %v", err)
	}
	if err := <-waiter; !errors.Is(err, broken) {
		t.Errorf("waiter: %v, want the owner's error", err)
	}
}

// TestWantCallsOffWarm: the editor asking for another state calls off a
// warm-up of the photo; asking for the warm-up's own state does not, and
// leaving the photo calls off any.
func TestWantCallsOffWarm(t *testing.T) {
	c := &Cache{}
	photo := store.Photo{CacheKey: "k"}
	release := c.HoldEditor(photo)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c.fullWarms = map[string][]*fullWarm{"k": {{key: "start", cancel: cancel}}}
	c.fullMu.Lock()
	c.wantLocked("k", "start")
	c.fullMu.Unlock()
	if ctx.Err() != nil {
		t.Fatal("asking for the warm-up's own state called it off")
	}
	c.fullMu.Lock()
	c.wantLocked("k", "edit")
	c.fullMu.Unlock()
	if ctx.Err() == nil {
		t.Error("asking for another state left the warm-up running")
	}

	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	c.fullWarms = map[string][]*fullWarm{"k": {{key: "edit", cancel: cancel2}}}
	release()
	if ctx2.Err() == nil {
		t.Error("leaving the photo left its warm-up running")
	}
}

// TestReleaseRestartsIdle: the kept decode goes fullDecodeIdle after the
// editor leaves its photo, not after its last render, which may be long
// before.
func TestReleaseRestartsIdle(t *testing.T) {
	c := &Cache{}
	photo := store.Photo{CacheKey: "k"}
	release := c.HoldEditor(photo)
	f := &fullDecode{cacheKey: "k", key: "x", rgba: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	c.fullMu.Lock()
	c.wantLocked("k", "x")
	c.fullMu.Unlock()
	c.keepFull(f)
	f.used = time.Now().Add(-10 * time.Minute)
	release()
	c.expireFull(f)
	if c.fullEntry != f {
		t.Error("the decode went the moment the editor left")
	}
	c.closeFull()
}

// TestRenderDoesNotAsk: a 1:1 render of the held photo under a state the
// editor is not on — a share-link guest's, the pre-render pass's — neither
// moves what the editor is on nor calls off its warm-up.
func TestRenderDoesNotAsk(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	f.edit(0, edit.Params{Contrast: 0.1})
	want := f.cache.fullEditors[f.photo(0).CacheKey].want
	p := f.photo(0)
	other := edit.Params{ExpEV: 0.8}
	other.Normalize()
	b, _ := json.Marshal(&other)
	js := string(b)
	if err := f.db.SetEdit(t.Context(), f.ids[0], &js, other.Hash(), 1); err != nil {
		t.Fatal(err)
	}
	p = f.photo(0)
	if _, err := f.cache.EnsureTile(t.Context(), p, 0, 0, p.EditHash, decode.PriorityVisible); err != nil {
		t.Fatal(err)
	}
	if got := f.cache.fullEditors[p.CacheKey].want; got != want {
		t.Error("a render moved what the editor is on")
	}
	if f.cache.fullEntry != nil {
		t.Error("a render of a state the editor is not on was kept")
	}
}

// TestKeptForWindow: a window renders from the kept decode under the same
// LibRaw inputs, or the same but for exposure, which it folds in.
func TestKeptForWindow(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	start := &edit.Params{Contrast: 0.1, ExpEV: 0.5}
	start.Normalize()
	p := f.photo(0)
	if err := f.cache.WarmFull(t.Context(), p, start); err != nil {
		t.Fatal(err)
	}
	src, base, exact, ok := f.cache.KeptForWindow(p, start)
	if !ok || !exact || src == nil {
		t.Fatalf("own state: ok %v exact %v", ok, exact)
	}
	look := *start
	look.Contrast, look.Clarity = 0.5, 0.3
	if _, ev, exact, ok := f.cache.KeptForWindow(p, &look); !ok || !exact || ev != base {
		t.Errorf("look edit: ok %v exact %v expEV %v, want %v", ok, exact, ev, base)
	}
	brighter := *start
	brighter.ExpEV = 1.2
	if _, ev, exact, ok := f.cache.KeptForWindow(p, &brighter); !ok || exact || math.Abs(ev-base-0.7) > 1e-9 {
		t.Errorf("exposure edit: ok %v exact %v expEV %v, want %v", ok, exact, ev, base+0.7)
	}
	warmer := *start
	warmer.WBMode, warmer.WBKelvin = edit.WBKelvin, 7000
	warmer.Normalize()
	if _, _, _, ok := f.cache.KeptForWindow(p, &warmer); ok {
		t.Error("a white balance edit renders from the kept decode")
	}
	if _, _, _, ok := f.cache.KeptForWindow(f.photo(1), start); ok {
		t.Error("another photo renders from the kept decode")
	}
	if _, _, _, ok := f.cache.KeptForWindow(p, nil); ok {
		t.Error("the base look renders from the kept decode")
	}
}

// TestEditorWantsWindow: the state the editor settled on, its decode not
// kept yet, is one a window waits for; WarmFull then keeps it.
func TestEditorWantsWindow(t *testing.T) {
	f := newFullTestCatalog(t)
	f.open(0)
	p := f.photo(0)
	start := &edit.Params{Contrast: 0.1}
	start.Normalize()
	if err := f.cache.WarmFull(t.Context(), p, start); err != nil {
		t.Fatal(err)
	}
	warmer := *start
	warmer.WBMode, warmer.WBKelvin = edit.WBKelvin, 7000
	warmer.Normalize()
	if f.cache.EditorWants(p, &warmer) {
		t.Error("a drag frame's state is wanted")
	}
	f.cache.WantFull(p, &warmer)
	if !f.cache.EditorWants(p, &warmer) {
		t.Fatal("the settled state is not wanted")
	}
	drag := warmer
	drag.Contrast = 0.37
	if f.cache.EditorWants(p, &drag) {
		t.Error("a look drag sharing the settled state's decode is wanted")
	}
	if _, _, _, ok := f.cache.KeptForWindow(p, &warmer); ok {
		t.Fatal("kept before it was decoded")
	}
	if err := f.cache.WarmFull(t.Context(), p, &warmer); err != nil {
		t.Fatal(err)
	}
	if _, _, exact, ok := f.cache.KeptForWindow(p, &warmer); !ok || !exact {
		t.Errorf("after the wait: ok %v exact %v", ok, exact)
	}
	if f.cache.EditorWants(f.photo(1), &warmer) {
		t.Error("a photo not held is wanted")
	}
}
