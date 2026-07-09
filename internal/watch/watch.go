// Package watch turns raw filesystem notifications into settled, de-noised
// signals about the photo library: a subdirectory appeared under a managed
// parent, or a folder's RAW set changed.
//
// Three properties make it safe to wire straight into the scanner:
//
//   - Only RAW files and directories are ever reacted to. marraw writes
//     `<raw>.marraw.json` sidecars into the folders it watches on every rating,
//     flag, and edit — and the scan that a notification triggers writes more of
//     them. Dropping every non-RAW file event is what stops that from becoming
//     an infinite loop; there is no other cut in the cycle.
//
//   - A directory must go quiet for Quiescence before it is reported. Copying a
//     card streams events continuously, so the folder is reported once, after
//     the last file lands. This matters beyond politeness: photo cache keys are
//     derived from size and mtime, so ingesting a half-copied RAW would cache a
//     truncated decode under a key that never changes again.
//
//   - Notifications are advisory. Every failure mode (a watch that won't
//     attach, a removed drive, an overflowed kernel buffer, a network share)
//     degrades to "no events", and the user's manual rescan still works.
package watch

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Sink receives settled notifications. Both methods may block; they run on the
// watcher's own goroutine.
type Sink interface {
	// ParentChanged reports that a managed parent's child folders may have
	// changed (a subdirectory appeared or vanished, or its own loose RAWs
	// changed).
	ParentChanged(parent string)
	// FolderChanged reports that a folder's RAW files may have changed.
	FolderChanged(path string)
}

// Options tune the watcher. The zero value is not usable; see DefaultOptions.
type Options struct {
	// Quiescence is how long a directory must be event-free before it is
	// reported. Sized to outlast the gaps between per-file events while a card
	// is being copied.
	Quiescence time.Duration
	// MaxWait caps how long a continuously-busy directory can starve. When it
	// fires, SettleAge is the only thing standing between us and a half-written
	// file, so the two must not both be short.
	MaxWait time.Duration
	// SettleAge is the minimum age of a RAW file before its directory may be
	// reported. A file younger than this is assumed to still be growing.
	SettleAge time.Duration
	// Tick is the coalescer's polling interval.
	Tick time.Duration

	// RecentFocusN is how many recently-opened folders stay watched. It is not
	// 1: two windows share one daemon and can sit on two different folders, so
	// a single "current folder" slot would stop watching one of them.
	RecentFocusN int
	// MaxDirsPerShoot bounds the subdirectory fan-out of one recursive folder.
	MaxDirsPerShoot int
	// MaxWatches bounds the total watch count across all roles.
	MaxWatches int

	// IsRaw reports whether a file name is a RAW photo. Everything else is
	// ignored — this is the cut that breaks the sidecar feedback loop.
	IsRaw func(name string) bool
	// SkipDir reports whether a directory name is noise (exports, caches).
	SkipDir func(name string) bool

	// now and tick exist so tests can drive the coalescer deterministically
	// rather than sleeping on real fsnotify timing.
	now       func() time.Time
	tick      <-chan time.Time
	newSource func() (source, error)
}

// DefaultOptions returns the production tuning. isRaw and skipDir must be
// supplied by the caller (they belong to the scanner, which must not be
// imported here).
func DefaultOptions(isRaw, skipDir func(string) bool) Options {
	return Options{
		Quiescence:      2 * time.Second,
		MaxWait:         30 * time.Second,
		SettleAge:       time.Second,
		Tick:            250 * time.Millisecond,
		RecentFocusN:    4,
		MaxDirsPerShoot: 512,
		MaxWatches:      1024,
		IsRaw:           isRaw,
		SkipDir:         skipDir,
	}
}

// Op is the set of filesystem operations the watcher distinguishes.
type Op uint8

const (
	OpCreate Op = 1 << iota
	OpWrite
	OpRemove
	OpRename
)

// Event is one filesystem notification, decoupled from fsnotify so the
// coalescer can be tested without a real kernel watch.
type Event struct {
	Name string
	Op   Op
}

// source is the event feed. fsnotify satisfies it in production; tests inject a
// channel-backed fake.
type source interface {
	Add(dir string) error
	Remove(dir string) error
	Events() <-chan Event
	Errors() <-chan error
	Close() error
}

// dirRole records why a directory is watched. A directory can hold several
// roles at once (a focused folder that is also a managed parent's child).
type dirRole struct {
	path string // original-cased path
	// parent: this directory is a managed library folder.
	parent bool
	// childOf: this directory is an immediate subdirectory of that parent. It
	// is watched so that RAWs copied into a *newly created, still empty* folder
	// are noticed — the create fires on the parent, but the files land here.
	childOf string
	// shootRoot: this directory belongs to that focused folder's tree; events
	// here are reported against the root, not against this directory.
	shootRoot string
}

// dirty is the accumulated, unsettled event state for one directory.
type dirty struct {
	first, last time.Time
	sawRaw      bool
	sawDir      bool
}

type cmdKind int

const (
	cmdSetParents cmdKind = iota
	cmdFocus
	cmdClose
)

type cmd struct {
	kind      cmdKind
	parents   []string
	path      string
	recursive bool
	done      chan struct{}
}

type focusEntry struct {
	path      string
	recursive bool
}

// Watcher watches managed parents and recently-opened folders, and reports
// settled changes to a Sink. All mutating methods are safe for concurrent use;
// they hand work to the single goroutine that owns the watch set.
type Watcher struct {
	opts Options
	sink Sink
	src  source

	cmds chan cmd
	done chan struct{}

	// Owned by the run goroutine only.
	parents  []string
	focus    []focusEntry // most-recent first
	watched  map[string]*dirRole
	pending  map[string]*dirty
	lastLost time.Time
}

// New starts a watcher. It never fails on an unwatchable path; only creating
// the underlying kernel watch handle can fail.
func New(sink Sink, opts Options) (*Watcher, error) {
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.newSource == nil {
		opts.newSource = newFsnotifySource
	}
	if opts.IsRaw == nil {
		opts.IsRaw = func(string) bool { return false }
	}
	if opts.SkipDir == nil {
		opts.SkipDir = func(string) bool { return false }
	}
	src, err := opts.newSource()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		opts:    opts,
		sink:    sink,
		src:     src,
		cmds:    make(chan cmd),
		done:    make(chan struct{}),
		watched: map[string]*dirRole{},
		pending: map[string]*dirty{},
	}
	go w.run()
	return w, nil
}

// SetParents replaces the managed-parent set.
func (w *Watcher) SetParents(parents []string) {
	w.send(cmd{kind: cmdSetParents, parents: parents})
}

// FocusShoot promotes a folder in the focus set, so RAWs dropped into it are
// noticed. Folders stay focused until evicted by RecentFocusN more recent ones;
// there is no "folder closed" signal to key off, and keeping a recently-viewed
// folder watched is what lets a background card-drop ingest at all.
func (w *Watcher) FocusShoot(path string, recursive bool) {
	w.send(cmd{kind: cmdFocus, path: path, recursive: recursive})
}

// Close stops the watcher and releases every watch handle.
func (w *Watcher) Close() error {
	done := make(chan struct{})
	select {
	case w.cmds <- cmd{kind: cmdClose, done: done}:
		<-done
	case <-w.done:
	}
	return nil
}

func (w *Watcher) send(c cmd) {
	if w == nil {
		return
	}
	select {
	case w.cmds <- c:
	case <-w.done:
	}
}

func (w *Watcher) run() {
	defer close(w.done)
	defer w.src.Close()

	tick := w.opts.tick
	if tick == nil {
		t := time.NewTicker(w.opts.Tick)
		defer t.Stop()
		tick = t.C
	}

	for {
		select {
		case c := <-w.cmds:
			switch c.kind {
			case cmdSetParents:
				w.parents = c.parents
				w.applyWatches()
			case cmdFocus:
				w.promoteFocus(c.path, c.recursive)
				w.applyWatches()
			case cmdClose:
				close(c.done)
				return
			}
		case ev, ok := <-w.src.Events():
			if !ok {
				return
			}
			w.onEvent(ev)
		case err, ok := <-w.src.Errors():
			if !ok {
				return
			}
			w.onError(err)
		case <-tick:
			w.sweep()
		}
	}
}

// ---------------------------------------------------------------- focus set

func (w *Watcher) promoteFocus(path string, recursive bool) {
	clean := filepath.Clean(path)
	next := []focusEntry{{path: clean, recursive: recursive}}
	for _, f := range w.focus {
		if strings.EqualFold(f.path, clean) {
			continue
		}
		next = append(next, f)
		if len(next) == w.opts.RecentFocusN {
			break
		}
	}
	w.focus = next
}

// ---------------------------------------------------------------- watch set

func key(p string) string { return strings.ToLower(filepath.Clean(p)) }

// unwatchable rejects paths where ReadDirectoryChangesW is unreliable. A UNC
// share silently delivers nothing (or delivers late and lossily), which would
// be worse than not watching at all: the user would trust it.
func unwatchable(p string) bool {
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//")
}

// desiredWatches computes the full watch set from the parents and focus set.
func (w *Watcher) desiredWatches() map[string]*dirRole {
	out := map[string]*dirRole{}
	budget := w.opts.MaxWatches

	role := func(p string) *dirRole {
		k := key(p)
		r, ok := out[k]
		if !ok {
			if budget <= 0 {
				return nil
			}
			budget--
			r = &dirRole{path: p}
			out[k] = r
		}
		return r
	}

	for _, p := range w.parents {
		p = filepath.Clean(p)
		if unwatchable(p) {
			continue
		}
		r := role(p)
		if r == nil {
			break
		}
		r.parent = true
		// Watch each immediate subdirectory too. Without this, `mkdir` inside
		// the parent fires here, but the RAWs then copied into that new folder
		// fire only inside it — and since it was empty at mkdir time it is not
		// yet a shoot, so nothing would ever bring it into the library.
		for _, sub := range w.subdirs(p) {
			cr := role(sub)
			if cr == nil {
				break
			}
			if cr.childOf == "" {
				cr.childOf = p
			}
		}
	}

	for _, f := range w.focus {
		if unwatchable(f.path) {
			continue
		}
		r := role(f.path)
		if r == nil {
			break
		}
		if r.shootRoot == "" {
			r.shootRoot = f.path
		}
		if !f.recursive {
			continue
		}
		for _, sub := range w.shootSubdirs(f.path) {
			sr := role(sub)
			if sr == nil {
				break
			}
			if sr.shootRoot == "" {
				sr.shootRoot = f.path
			}
		}
	}
	return out
}

func (w *Watcher) subdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() || w.opts.SkipDir(e.Name()) {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// shootSubdirs walks a recursive folder's tree, bounded by MaxDirsPerShoot.
func (w *Watcher) shootSubdirs(root string) []string {
	var out []string
	queue := []string{root}
	for len(queue) > 0 && len(out) < w.opts.MaxDirsPerShoot {
		d := queue[0]
		queue = queue[1:]
		for _, sub := range w.subdirs(d) {
			out = append(out, sub)
			queue = append(queue, sub)
			if len(out) >= w.opts.MaxDirsPerShoot {
				log.Printf("watch: %s exceeds %d subdirectories; deeper folders rely on manual rescan", root, w.opts.MaxDirsPerShoot)
				return out
			}
		}
	}
	return out
}

// applyWatches diffs the desired watch set against the live one.
func (w *Watcher) applyWatches() {
	want := w.desiredWatches()
	for k, r := range w.watched {
		if _, ok := want[k]; !ok {
			_ = w.src.Remove(r.path)
			delete(w.watched, k)
		}
	}
	for k, r := range want {
		if _, ok := w.watched[k]; ok {
			w.watched[k] = r // roles may have changed
			continue
		}
		if err := w.src.Add(r.path); err != nil {
			// A folder that cannot be watched is not an error the user can act
			// on; the manual rescan still covers it.
			log.Printf("watch: cannot watch %s: %v", r.path, err)
			continue
		}
		w.watched[k] = r
	}
}

// ---------------------------------------------------------------- events

// interesting decides whether an event can possibly matter, and how. Only RAW
// files and directories qualify: `.marraw.json` sidecars (which the scan we
// trigger writes) and exported JPEG/TIFF must never round-trip back into a
// rescan.
func (w *Watcher) interesting(ev Event) (isRaw, isDir bool) {
	base := filepath.Base(ev.Name)
	if w.opts.IsRaw(base) {
		return true, false
	}
	// Removals cannot be stat'd. An extensionless name is the only thing that
	// could have been a directory, so treat it as one; a stray extensionless
	// file costs at most one wasted refresh.
	if fi, err := os.Stat(ev.Name); err == nil {
		return false, fi.IsDir()
	}
	return false, filepath.Ext(base) == ""
}

func (w *Watcher) onEvent(ev Event) {
	isRaw, isDir := w.interesting(ev)
	if !isRaw && !isDir {
		return
	}
	// A directory event is reported against its parent (the directory that
	// gained or lost a child); a file event against the directory holding it.
	dir := filepath.Dir(ev.Name)
	if isDir && ev.Op&(OpCreate|OpRemove|OpRename) == 0 {
		return // a plain write to a directory tells us nothing
	}
	k := key(dir)
	if _, ok := w.watched[k]; !ok {
		return
	}
	now := w.opts.now()
	d, ok := w.pending[k]
	if !ok {
		d = &dirty{first: now}
		w.pending[k] = d
	}
	d.last = now
	d.sawRaw = d.sawRaw || isRaw
	d.sawDir = d.sawDir || isDir
}

// onError treats any watcher error as "we lost track". The kernel buffer can
// overflow during a burst, and fsnotify cannot say which directory was
// affected — so resync everything. Since the settled action is itself a full
// rescan, this degrades to "rescan slightly early".
func (w *Watcher) onError(err error) {
	now := w.opts.now()
	if now.Sub(w.lastLost) < 5*time.Second {
		return
	}
	w.lastLost = now
	log.Printf("watch: lost events (%v); resyncing", err)
	for k, r := range w.watched {
		d, ok := w.pending[k]
		if !ok {
			d = &dirty{first: now}
			w.pending[k] = d
		}
		d.last = now
		d.sawRaw = true
		d.sawDir = d.sawDir || r.parent
	}
}

// stillWriting reports whether any RAW directly in dir is younger than
// SettleAge. It is the backstop for MaxWait: quiescence normally guarantees the
// copy finished, but a directory that never goes quiet would otherwise be
// scanned mid-copy, caching a truncated decode under a key derived from the
// truncated file's size and mtime.
func (w *Watcher) stillWriting(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	cutoff := w.opts.now().Add(-w.opts.SettleAge)
	for _, e := range ents {
		if e.IsDir() || !w.opts.IsRaw(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().After(cutoff) {
			return true
		}
	}
	return false
}

func (w *Watcher) sweep() {
	now := w.opts.now()
	for k, d := range w.pending {
		quiet := now.Sub(d.last) >= w.opts.Quiescence
		starved := now.Sub(d.first) >= w.opts.MaxWait
		if !quiet && !starved {
			continue
		}
		r, ok := w.watched[k]
		if !ok {
			delete(w.pending, k)
			continue
		}
		if d.sawRaw && w.stillWriting(r.path) {
			// Hold it: re-arm the quiescence window rather than ingesting a
			// file that is still growing.
			d.last = now
			d.first = now
			continue
		}
		delete(w.pending, k)
		w.dispatch(r, d)
	}
}

func (w *Watcher) dispatch(r *dirRole, d *dirty) {
	rescanWatches := false

	if r.parent && (d.sawDir || d.sawRaw) {
		// sawDir: a child folder appeared or vanished.
		// sawRaw: the parent's own loose-RAW row changed count.
		w.sink.ParentChanged(r.path)
		rescanWatches = rescanWatches || d.sawDir
	}

	if d.sawRaw {
		// Report each affected folder once. A directory can be both a managed
		// parent's child and a focused folder.
		seen := map[string]bool{}
		emit := func(p string) {
			if p == "" || seen[key(p)] {
				return
			}
			seen[key(p)] = true
			w.sink.FolderChanged(p)
		}
		if r.parent {
			emit(r.path) // the self-shoot: RAWs loose in the parent
		}
		if r.childOf != "" {
			emit(r.path)
		}
		emit(r.shootRoot)
	}

	if d.sawDir && r.shootRoot != "" {
		rescanWatches = true
	}
	if rescanWatches {
		// A new subdirectory needs its own watch before anything is copied in.
		w.applyWatches()
	}
}

// ---------------------------------------------------------------- fsnotify

type fsnotifySource struct {
	w      *fsnotify.Watcher
	events chan Event
	done   chan struct{}
}

func newFsnotifySource() (source, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	s := &fsnotifySource{w: fw, events: make(chan Event, 256), done: make(chan struct{})}
	go s.pump()
	return s, nil
}

func (s *fsnotifySource) pump() {
	defer close(s.events)
	for {
		select {
		case ev, ok := <-s.w.Events:
			if !ok {
				return
			}
			var op Op
			if ev.Has(fsnotify.Create) {
				op |= OpCreate
			}
			if ev.Has(fsnotify.Write) {
				op |= OpWrite
			}
			if ev.Has(fsnotify.Remove) {
				op |= OpRemove
			}
			if ev.Has(fsnotify.Rename) {
				op |= OpRename
			}
			if op == 0 {
				continue // chmod and friends
			}
			select {
			case s.events <- Event{Name: ev.Name, Op: op}:
			case <-s.done:
				return
			}
		case <-s.done:
			return
		}
	}
}

func (s *fsnotifySource) Add(dir string) error    { return s.w.Add(dir) }
func (s *fsnotifySource) Remove(dir string) error { return s.w.Remove(dir) }
func (s *fsnotifySource) Events() <-chan Event    { return s.events }
func (s *fsnotifySource) Errors() <-chan error    { return s.w.Errors }
func (s *fsnotifySource) Close() error            { close(s.done); return s.w.Close() }
