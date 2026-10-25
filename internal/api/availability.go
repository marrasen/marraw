package api

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/aprot"
)

// rootStatusKey is the subscription key for the online/offline state of every
// stored root.
const rootStatusKey = "rootStatus"

// pollAvailability is how often the daemon rechecks whether each root's storage
// is reachable. A removable drive appearing is the event we care about, and
// nothing in the OS tells us about it through a path watch — the directory
// cannot be watched while it does not exist. Polling a handful of paths costs a
// stat each.
const pollAvailability = 3 * time.Second

// statTimeout bounds one reachability check. A disconnected network share can
// leave os.Stat blocked for a long time, and one dead share must not stall the
// status of every other root.
const statTimeout = 2 * time.Second

// statGate keeps at most one outstanding stat per path.
//
// A stat that outlives its timeout is still running: it ends when the kernel
// finally answers, which on a hard-hung NFS or SMB mount can be minutes, or
// never. The poller comes back every few seconds regardless, so without this
// one wedged share added a permanently blocked goroutine — and the OS thread
// it sits in, since it is parked in a syscall — on every tick, for the life of
// the daemon. That is roughly a thousand an hour for one unplugged drive.
type statGate struct {
	mu      sync.Mutex
	pending map[string]bool
}

var statsInFlight = statGate{pending: map[string]bool{}}

// enter reports whether the caller may start a stat for path.
func (g *statGate) enter(path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending[path] {
		return false
	}
	g.pending[path] = true
	return true
}

func (g *statGate) leave(path string) {
	g.mu.Lock()
	delete(g.pending, path)
	g.mu.Unlock()
}

// look is what one check of a path found: ok, that it is an existing
// directory (or, for mountLeftEmpty, a folder with something in it), and
// gone, that the system said there is nothing there. A check that did not
// answer in time knows neither.
type look struct{ ok, gone bool }

// probe runs check for key on its own goroutine, one at a time per key,
// and gives up on it after statTimeout.
//
// The goroutine outlives the timeout — it ends when the kernel finally
// answers — which is why the result channel is buffered: nobody may be left
// to receive it.
func probe(key string, check func() look) look {
	if !statsInFlight.enter(key) {
		// The previous check is still stuck in the kernel. What it is stuck
		// on is the path not answering, so that is the answer — and piling a
		// second goroutine onto it would not learn anything the first one
		// will not.
		return look{}
	}
	done := make(chan look, 1)
	go func() {
		defer statsInFlight.leave(key)
		done <- check()
	}()
	timer := time.NewTimer(statTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r
	case <-timer.C:
		return look{}
	}
}

// lookAt checks whether path is an existing directory right now.
func lookAt(path string) look {
	return probe(path, func() look {
		fi, err := os.Stat(path)
		return look{ok: err == nil && fi.IsDir(), gone: errors.Is(err, fs.ErrNotExist)}
	})
}

// reachable reports whether path is an existing directory right now.
func reachable(path string) bool { return lookAt(path).ok }

// lookAll checks paths concurrently, keyed by lowercased path.
func lookAll(paths []string, check func(string) look) map[string]look {
	out := make(map[string]look, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := check(p)
			mu.Lock()
			out[strings.ToLower(p)] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// presence is what the poller knows of a root: there, on storage that is
// not connected, or gone from storage that is.
type presence int8

const (
	present presence = iota
	disconnected
	missing
)

// mountHolder reports whether dir is where the system mounts drives, so a
// folder missing from it is a drive unplugged, not a folder moved:
// /Volumes on macOS, /media, /media/<user>, /run/media/<user> and /mnt on
// Linux. A drive's letter on Windows goes with the drive.
func mountHolder(dir string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	switch dir {
	case "/Volumes", "/media", "/mnt":
		return true
	}
	up := filepath.Dir(dir)
	return up == "/media" || up == "/run/media"
}

// holdsSomething checks whether dir has anything in it, away from Windows:
// an empty folder is more likely a drive's mount point with the drive
// unplugged than the folder a shoot moved out of. On Windows every folder
// counts as holding something.
func holdsSomething(dir string) look {
	if runtime.GOOS == "windows" {
		return look{ok: true}
	}
	return probe("list:"+dir, func() look {
		f, err := os.Open(dir)
		if err != nil {
			return look{}
		}
		defer f.Close()
		names, _ := f.Readdirnames(1)
		return look{ok: len(names) > 0}
	})
}

// presenceAll checks paths concurrently, keyed by lowercased path. Sequential
// checks would cost statTimeout *per* dead path; this costs it a few times
// at most.
//
// A folder the system says is not there is missing where the nearest folder
// above it that is there holds something: a folder moved or deleted, or one
// above it, leaves the rest of the tree behind; an unplugged drive takes it
// all, the drive's own top folder too, up to the folder drives are mounted
// in. A folder that
// does not answer, or answers with another error, as a dropped network share
// does, is disconnected. The folders above are checked a level at a time,
// each once: the stat gate would fail a second check of one while the first
// runs.
func presenceAll(paths []string) map[string]presence {
	looks := lookAll(paths, lookAt)
	out := make(map[string]presence, len(paths))
	// above is the folder over each gone path still to check.
	above := map[string]string{}
	var pending []string
	for _, p := range paths {
		r := looks[strings.ToLower(p)]
		out[strings.ToLower(p)] = disconnected
		if r.ok {
			out[strings.ToLower(p)] = present
		} else if r.gone {
			above[p] = filepath.Dir(p)
			pending = append(pending, p)
		}
	}
	// anchor is the folder above each gone path that is there.
	anchor := map[string]string{}
	for len(pending) > 0 {
		seen := map[string]bool{}
		var dirs []string
		var left []string
		for _, p := range pending {
			d := above[p]
			if mountHolder(d) {
				continue // where drives are mounted
			}
			left = append(left, p)
			if !seen[strings.ToLower(d)] {
				seen[strings.ToLower(d)] = true
				dirs = append(dirs, d)
			}
		}
		there := lookAll(dirs, lookAt)
		pending = nil
		for _, p := range left {
			d := above[p]
			switch r := there[strings.ToLower(d)]; {
			case r.ok:
				anchor[p] = d
			case r.gone && filepath.Dir(d) != d:
				above[p] = filepath.Dir(d)
				pending = append(pending, p)
			}
		}
	}
	seen := map[string]bool{}
	var anchors []string
	for _, d := range anchor {
		if !seen[strings.ToLower(d)] {
			seen[strings.ToLower(d)] = true
			anchors = append(anchors, d)
		}
	}
	holding := lookAll(anchors, holdsSomething)
	for p, d := range anchor {
		if holding[strings.ToLower(d)].ok {
			out[strings.ToLower(p)] = missing
		}
	}
	return out
}

// availability caches the last known presence of each root so the poller
// only pushes when something actually changed, and so a request handler can
// answer without touching the disk.
type availability struct {
	mu     sync.RWMutex
	online map[string]presence // lowercased path -> presence
}

func newAvailability() *availability { return &availability{online: map[string]presence{}} }

// get is the root at path's presence, and whether the poller has seen it.
func (a *availability) get(path string) (presence, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	v, ok := a.online[strings.ToLower(path)]
	return v, ok
}

// snapshot replaces the cache and reports which paths changed state.
func (a *availability) snapshot(next map[string]presence) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var changed []string
	for k, v := range next {
		if old, ok := a.online[k]; !ok || old != v {
			changed = append(changed, k)
		}
	}
	a.online = next
	return changed
}

// GetRootStatus reports which roots are currently reachable. Subscription
// query: the availability poller pushes an update whenever a drive appears or
// disappears.
//
// Status is deliberately not a field on LibraryRoot. That struct round-trips
// through SetLibraryRoots — a derived field would be written back into the
// stored config, and a folder that happened to be offline when the user
// reordered the rail would persist as offline.
func (l *Library) GetRootStatus(ctx context.Context) ([]RootStatus, error) {
	aprot.RegisterRefreshTrigger(ctx, rootStatusKey)
	roots := l.libraryRoots(ctx)

	// Roots the poller has not seen yet — a root added moments ago. Stat them
	// together rather than one after another.
	var unknown []string
	for _, r := range roots {
		if _, cached := l.deps.Avail.get(r.Path); !cached {
			unknown = append(unknown, r.Path)
		}
	}
	fresh := presenceAll(unknown)
	// What the rail shows of each root from the catalog; a failed lookup
	// leaves it out rather than failing the status.
	summaries, err := l.deps.DB.FolderSummaries(ctx)
	if err != nil {
		summaries = nil
	}
	earliest := folderEarliest(summaries)

	out := make([]RootStatus, 0, len(roots))
	for _, r := range roots {
		at, cached := l.deps.Avail.get(r.Path)
		if !cached {
			at = fresh[strings.ToLower(r.Path)]
		}
		st := RootStatus{Path: r.Path, Online: at == present, Missing: at == missing,
			EarliestTakenAt: earliestFor(earliest, r.Path, !r.IncludeSubfolders)}
		st.Picked, st.Rejected, st.Cover = summaryFor(summaries, r.Path, !r.IncludeSubfolders)
		out = append(out, st)
	}
	return out, nil
}

// pollRootAvailability rechecks every root until ctx is done, pushing a refresh
// when any of them appears or disappears. A root that comes back online also
// gets its child listing refreshed and its filesystem watch re-attached — the
// watch could not exist while the directory did not.
func (l *Library) pollRootAvailability(ctx context.Context) {
	tick := time.NewTicker(pollAvailability)
	defer tick.Stop()
	for {
		l.refreshAvailability(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (l *Library) refreshAvailability(ctx context.Context) {
	roots := l.libraryRoots(ctx)
	paths := make([]string, 0, len(roots))
	for _, r := range roots {
		paths = append(paths, r.Path)
	}
	changed := l.deps.Avail.snapshot(presenceAll(paths))
	if len(changed) == 0 {
		return
	}

	l.deps.TriggerRefresh(rootStatusKey)
	// A parent that just appeared has children to list; one that just vanished
	// has none. Either way its listing is stale.
	for _, r := range roots {
		if r.IsParent && contains(changed, strings.ToLower(r.Path)) {
			l.deps.TriggerRefresh(shootsKey(r.Path))
		}
	}
	// Re-attach watches for whatever is reachable now, and drop the rest.
	l.syncWatchedParents(roots)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// rootOnline reports whether a stored root's storage is reachable, preferring
// the poller's cache over a fresh stat.
func (l *Library) rootOnline(path string) bool {
	if l.deps.Avail != nil {
		if at, cached := l.deps.Avail.get(path); cached {
			return at == present
		}
	}
	return reachable(path)
}
