package api

import (
	"context"
	"log"
	"path/filepath"

	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/watch"
)

// StartWatcher wires the filesystem watcher to the library, seeds it with the
// stored managed parents, and starts the availability poller that notices an
// external drive coming or going. A watcher that cannot start is not fatal:
// every folder still has a manual rescan.
//
// The poller starts either way — offline detection must work even without
// watches, and it is what re-attaches them when storage reappears.
func StartWatcher(ctx context.Context, lib *Library) (*watch.Watcher, error) {
	bg := context.WithoutCancel(ctx)
	lib.deps.Avail = newAvailability()

	sink := &watchSink{lib: lib, bg: bg}
	w, err := watch.New(sink, watch.DefaultOptions(scan.IsRawFile, scan.SkipDirName))
	if err != nil {
		go lib.pollRootAvailability(ctx)
		return nil, err
	}
	lib.deps.Watch = w
	// The first tick seeds the cache and calls syncWatchedParents, so offline
	// parents never get a watch attempt.
	go lib.pollRootAvailability(ctx)
	return w, nil
}

// watchSink turns settled filesystem signals into catalog work.
type watchSink struct {
	lib *Library
	// bg backs folders that are not open. Shared tasks started on it degrade to
	// detached no-ops (no tray chip), which is what we want for a folder nobody
	// is looking at.
	bg context.Context
}

// ParentChanged re-lists a managed parent's children. No scan: ListShoots reads
// the disk itself.
func (s *watchSink) ParentChanged(parent string) {
	s.lib.deps.TriggerRefresh(shootsKey(parent))
}

// FolderChanged syncs a folder's photo rows, then — only if the folder is open
// — runs the metadata, calibration, and pre-render passes over whatever is new.
func (s *watchSink) FolderChanged(path string) {
	ctx, focused := s.lib.focusCtx(path)
	if !focused {
		ctx = s.bg
	}

	// Scanner.OpenFolder, not Library.OpenFolder: the latter calls
	// startFolderJobs, which cancels the previous folder's passes through a
	// single global slot. With two windows on two folders, a file landing in
	// one would kill the other's pre-render.
	folderID, _, err := s.lib.deps.Scanner.OpenFolder(ctx, path, s.lib.scanRecursionFor(ctx, path))
	if err != nil {
		log.Printf("watch: rescan %s: %v", path, err)
		return
	}
	s.lib.deps.TriggerRefresh(photosKey(folderID))
	if parent := filepath.Dir(path); s.lib.isParentRoot(ctx, parent) {
		s.lib.deps.TriggerRefresh(shootsKey(parent)) // the rail's photo count
	}

	if focused {
		s.lib.ingestFolder(ctx, folderID, path)
	}
}
