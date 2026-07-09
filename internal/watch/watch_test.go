package watch

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSource replaces fsnotify so the coalescer can be exercised without real
// kernel watches — their timing is unreproducible and would make these tests
// flaky rather than merely slow.
type fakeSource struct {
	events chan Event
	errors chan error

	mu    sync.Mutex
	added map[string]bool
}

// Unbuffered: a send completes only once the run goroutine has taken the value,
// which is what makes barrier() a real ordering guarantee.
func newFakeSource() *fakeSource {
	return &fakeSource{
		events: make(chan Event),
		errors: make(chan error),
		added:  map[string]bool{},
	}
}

func (f *fakeSource) Add(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added[strings.ToLower(dir)] = true
	return nil
}

func (f *fakeSource) Remove(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.added, strings.ToLower(dir))
	return nil
}

func (f *fakeSource) watching(dir string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.added[strings.ToLower(dir)]
}

func (f *fakeSource) Events() <-chan Event { return f.events }
func (f *fakeSource) Errors() <-chan error { return f.errors }
func (f *fakeSource) Close() error         { return nil }

type record struct {
	mu      sync.Mutex
	parents []string
	folders []string
}

func newRecord() *record { return &record{} }

func (r *record) ParentChanged(p string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parents = append(r.parents, p)
}

func (r *record) FolderChanged(p string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.folders = append(r.folders, p)
}

func (r *record) snapshot() (parents, folders []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.parents...), append([]string(nil), r.folders...)
}

// harness wires a watcher to a fake clock and a manually pumped tick channel,
// so "two seconds passed" is an assignment rather than a sleep.
type harness struct {
	t    *testing.T
	w    *Watcher
	src  *fakeSource
	sink *record

	mu      sync.Mutex
	now     time.Time
	parents []string
	tick    chan time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:    t,
		src:  newFakeSource(),
		sink: newRecord(),
		now:  time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
		tick: make(chan time.Time),
	}
	opts := DefaultOptions(isRawTest, skipDirTest)
	opts.now = h.clock
	opts.tick = h.tick
	opts.newSource = func() (source, error) { return h.src, nil }

	w, err := New(h.sink, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.w = w
	t.Cleanup(func() { w.Close() })
	return h
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}

// sweep forces one coalescer pass and waits for it to complete.
func (h *harness) sweep() {
	h.tick <- h.clock()
	h.barrier()
}

// barrier blocks until the run goroutine has finished whatever it was doing.
// Every channel it selects on is unbuffered, so the first send proves the loop
// reached select (i.e. the previous item was handled) and the second proves the
// first command's own work is done.
func (h *harness) barrier() {
	h.w.SetParents(h.currentParents())
	h.w.SetParents(h.currentParents())
}

// touch writes a file and back-dates its mtime relative to the *fake* clock —
// stillWriting compares against opts.now(), so a real-time mtime would make the
// SettleAge guard untestable.
func (h *harness) touch(path string, age time.Duration) {
	h.t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		h.t.Fatal(err)
	}
	when := h.clock().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) currentParents() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.parents
}

func (h *harness) setParents(p ...string) {
	h.mu.Lock()
	h.parents = p
	h.mu.Unlock()
	h.w.SetParents(p)
	h.barrier()
}

func isRawTest(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".arw")
}

func skipDirTest(name string) bool { return strings.EqualFold(name, "export") }

// emit pushes an event and waits for the run loop to have consumed it.
func (h *harness) emit(name string, op Op) {
	h.src.events <- Event{Name: name, Op: op}
	h.barrier()
}

// --- tests -----------------------------------------------------------------

// A card copy fires one event per file. The folder must be reported once, after
// the copy settles — not 500 times, and not mid-copy.
func TestBurstCoalescesToOneFolderChange(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t)
	h.setParents(dir)
	h.w.FocusShoot(dir, false)
	h.barrier()

	for i := range 500 {
		name := filepath.Join(dir, "DSC0"+string(rune('0'+i%10))+".ARW")
		h.touch(name, 10*time.Second)
		h.emit(name, OpCreate)
	}

	// Not yet quiet.
	h.advance(time.Second)
	h.sweep()
	if _, folders := h.sink.snapshot(); len(folders) != 0 {
		t.Fatalf("reported before quiescence: %v", folders)
	}

	h.advance(2 * time.Second)
	h.sweep()
	_, folders := h.sink.snapshot()
	if len(folders) != 1 || !strings.EqualFold(folders[0], dir) {
		t.Fatalf("want exactly one FolderChanged for %s, got %v", dir, folders)
	}
}

// The scan a notification triggers writes `.marraw.json` sidecars back into the
// folder it just scanned. If those writes were reported, the loop would never
// terminate. This is the single cut that breaks the cycle.
func TestSidecarWritesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t)
	h.setParents(dir)
	h.w.FocusShoot(dir, false)
	h.barrier()

	for _, name := range []string{"DSC01.ARW.marraw.json", "out.jpg", "out.tif"} {
		p := filepath.Join(dir, name)
		h.touch(p, 10*time.Second)
		h.emit(p, OpCreate|OpWrite)
	}

	h.advance(5 * time.Second)
	h.sweep()
	parents, folders := h.sink.snapshot()
	if len(folders) != 0 || len(parents) != 0 {
		t.Fatalf("non-RAW writes must not trigger a rescan; got parents=%v folders=%v", parents, folders)
	}
}

// mkdir under a managed parent must both announce the parent and start watching
// the new directory — the RAWs land inside it, and nothing else would ever see
// them.
func TestNewSubdirAnnouncesParentAndGetsWatched(t *testing.T) {
	parent := t.TempDir()
	h := newHarness(t)
	h.setParents(parent)

	child := filepath.Join(parent, "NewShoot")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	h.emit(child, OpCreate)

	h.advance(3 * time.Second)
	h.sweep()

	parents, _ := h.sink.snapshot()
	if len(parents) != 1 || !strings.EqualFold(parents[0], parent) {
		t.Fatalf("want ParentChanged(%s), got %v", parent, parents)
	}
	if !h.src.watching(child) {
		t.Fatalf("new subdirectory %s must be watched, else RAWs copied into it are never seen", child)
	}
}

// The whole point of watching children: a folder created empty is not a shoot,
// so only an event from inside it can bring it into the library.
func TestRawInNewSubdirReportsIt(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "Shoot")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.setParents(parent)

	raw := filepath.Join(child, "DSC01.ARW")
	h.touch(raw, 10*time.Second)
	h.emit(raw, OpCreate)

	h.advance(3 * time.Second)
	h.sweep()

	_, folders := h.sink.snapshot()
	if len(folders) != 1 || !strings.EqualFold(folders[0], child) {
		t.Fatalf("want FolderChanged(%s), got %v", child, folders)
	}
}

// A directory that never goes quiet must still be reported eventually.
func TestMaxWaitFiresUnderContinuousEvents(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t)
	h.setParents(dir)
	h.w.FocusShoot(dir, false)
	h.barrier()

	raw := filepath.Join(dir, "DSC01.ARW")
	h.touch(raw, 10*time.Second)

	// Never quiet: an event every second, for longer than MaxWait.
	for range 31 {
		h.emit(raw, OpWrite)
		h.advance(time.Second)
		h.sweep()
	}
	_, folders := h.sink.snapshot()
	if len(folders) == 0 {
		t.Fatal("MaxWait must eventually report a continuously-busy directory")
	}
}

// When MaxWait fires, the mtime guard is the only thing left between us and a
// half-copied RAW — whose size and mtime would be baked into a cache key that
// never changes again.
func TestStillWritingDefersReport(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t)
	h.setParents(dir)
	h.w.FocusShoot(dir, false)
	h.barrier()

	raw := filepath.Join(dir, "DSC01.ARW")

	// A single large file being copied: its mtime keeps advancing as it grows,
	// and the directory never goes quiet. Run well past MaxWait.
	for range 80 {
		h.touch(raw, 0) // still growing: mtime == now
		h.emit(raw, OpWrite)
		h.advance(500 * time.Millisecond)
		h.sweep()
	}
	if _, folders := h.sink.snapshot(); len(folders) != 0 {
		t.Fatalf("a RAW younger than SettleAge must not be ingested, even past MaxWait; got %v", folders)
	}

	h.touch(raw, 10*time.Second) // copy finished, file aged out
	h.advance(3 * time.Second)
	h.sweep()
	if _, folders := h.sink.snapshot(); len(folders) != 1 {
		t.Fatalf("want one FolderChanged once the file settled, got %v", folders)
	}
}

// A kernel-buffer overflow gives an error with no path attached, so the only
// safe response is to resync everything watched.
func TestErrorTriggersFullResync(t *testing.T) {
	parent := t.TempDir()
	h := newHarness(t)
	h.setParents(parent)

	h.src.errors <- os.ErrDeadlineExceeded
	h.barrier()

	h.advance(3 * time.Second)
	h.sweep()

	parents, folders := h.sink.snapshot()
	if len(parents) == 0 {
		t.Fatal("a lost-events error must resync the managed parent")
	}
	if len(folders) == 0 {
		t.Fatal("a lost-events error must resync the parent's own folder rows")
	}
}

// Two windows can sit on two folders; a single "current folder" slot would stop
// watching one of them.
func TestFocusKeepsSeveralFolders(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	h := newHarness(t)
	h.w.FocusShoot(a, false)
	h.w.FocusShoot(b, false)
	h.barrier()

	if !h.src.watching(a) || !h.src.watching(b) {
		t.Fatalf("both focused folders must stay watched (a=%v b=%v)", h.src.watching(a), h.src.watching(b))
	}
}

// Focus is bounded; the oldest folder is dropped.
func TestFocusEvictsBeyondLimit(t *testing.T) {
	h := newHarness(t)
	dirs := make([]string, 0, DefaultOptions(isRawTest, skipDirTest).RecentFocusN+1)
	for range cap(dirs) {
		dirs = append(dirs, t.TempDir())
	}
	for _, d := range dirs {
		h.w.FocusShoot(d, false)
	}
	h.barrier()

	if h.src.watching(dirs[0]) {
		t.Fatalf("oldest focused folder %s should have been evicted", dirs[0])
	}
	if !h.src.watching(dirs[len(dirs)-1]) {
		t.Fatal("most recent focused folder must be watched")
	}
}

// Noise directories are neither walked nor watched.
func TestSkipDirNotWatched(t *testing.T) {
	parent := t.TempDir()
	export := filepath.Join(parent, "export")
	if err := os.Mkdir(export, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.setParents(parent)

	if h.src.watching(export) {
		t.Fatal("export/ must not be watched")
	}
}

func TestUNCPathNotWatched(t *testing.T) {
	h := newHarness(t)
	h.setParents(`\\server\share\photos`)
	if h.src.watching(`\\server\share\photos`) {
		t.Fatal("UNC paths must not be watched: ReadDirectoryChangesW is unreliable over SMB")
	}
}
