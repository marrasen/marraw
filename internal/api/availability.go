package api

import (
	"context"
	"os"
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

// reachable reports whether path is an existing directory right now.
//
// The stat runs on its own goroutine so a wedged filesystem cannot hold the
// poller. That goroutine outlives the timeout — it ends when the kernel finally
// answers — which is why the result channel is buffered: nobody may be left to
// receive it.
func reachable(path string) bool {
	done := make(chan bool, 1)
	go func() {
		fi, err := os.Stat(path)
		done <- err == nil && fi.IsDir()
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(statTimeout):
		return false
	}
}

// reachableAll checks paths concurrently, keyed by lowercased path. Sequential
// checks would cost statTimeout *per* dead path; this costs it once.
func reachableAll(paths []string) map[string]bool {
	out := make(map[string]bool, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := reachable(p)
			mu.Lock()
			out[strings.ToLower(p)] = ok
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// availability caches the last known reachability of each root so the poller
// only pushes when something actually changed, and so a request handler can
// answer without touching the disk.
type availability struct {
	mu     sync.RWMutex
	online map[string]bool // lowercased path -> reachable
}

func newAvailability() *availability { return &availability{online: map[string]bool{}} }

func (a *availability) get(path string) (bool, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	v, ok := a.online[strings.ToLower(path)]
	return v, ok
}

// snapshot replaces the cache and reports which paths changed state.
func (a *availability) snapshot(next map[string]bool) []string {
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
	fresh := reachableAll(unknown)

	out := make([]RootStatus, 0, len(roots))
	for _, r := range roots {
		online, cached := l.deps.Avail.get(r.Path)
		if !cached {
			online = fresh[strings.ToLower(r.Path)]
		}
		out = append(out, RootStatus{Path: r.Path, Online: online})
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
	changed := l.deps.Avail.snapshot(reachableAll(paths))
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
		if online, cached := l.deps.Avail.get(path); cached {
			return online
		}
	}
	return reachable(path)
}
