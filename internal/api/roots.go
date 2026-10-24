package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/store"
)

const (
	settingLibraryRoots = "libraryRoots"
	libraryRootsKey     = "libraryRoots"
)

// shootsKey is the subscription refresh key for one managed parent's child
// list, mirroring photosKey(folderID) for a folder's photo list.
func shootsKey(parent string) string {
	return "shoots:" + strings.ToLower(filepath.Clean(parent))
}

// GetLibraryRoots returns the curated library: every shoot folder the user
// added, in display order. Subscription query — SetLibraryRoots and on-disk
// renames push updates.
func (l *Library) GetLibraryRoots(ctx context.Context) ([]LibraryRoot, error) {
	aprot.RegisterRefreshTrigger(ctx, libraryRootsKey)
	return l.libraryRoots(ctx), nil
}

// SetLibraryRoots replaces the stored root list (add, remove, alias-rename,
// toggle include-subfolders, and reorder are all "send the new list").
func (l *Library) SetLibraryRoots(ctx context.Context, roots []LibraryRoot) error {
	l.rootsMu.Lock()
	defer l.rootsMu.Unlock()
	return l.setRoots(ctx, roots)
}

// setRoots is SetLibraryRoots, rootsMu held.
func (l *Library) setRoots(ctx context.Context, roots []LibraryRoot) error {
	if roots == nil {
		roots = []LibraryRoot{}
	}
	for i := range roots {
		if strings.TrimSpace(roots[i].Path) == "" {
			return aprot.ErrInvalidParams("root path must not be empty")
		}
		roots[i].Path = filepath.Clean(roots[i].Path)
	}
	raw, err := json.Marshal(roots)
	if err != nil {
		return err
	}
	if err := l.deps.DB.SetSetting(ctx, settingLibraryRoots, string(raw)); err != nil {
		return err
	}
	aprot.TriggerRefresh(ctx, libraryRootsKey)
	// A newly added root has no cached reachability yet; GetRootStatus stats it
	// on demand, and the poller adopts it on its next tick.
	aprot.TriggerRefresh(ctx, rootStatusKey)
	l.syncWatchedParents(roots)
	for _, r := range roots {
		if r.IsParent {
			aprot.TriggerRefresh(ctx, shootsKey(r.Path))
		}
	}
	return nil
}

// syncWatchedParents keeps the filesystem watcher's parent set in step with the
// stored roots. Offline parents are left out: a directory that does not exist
// cannot be watched, and retrying would only log. The availability poller calls
// this again when the storage reappears.
func (l *Library) syncWatchedParents(roots []LibraryRoot) {
	if l.deps.Watch == nil {
		return
	}
	var parents []string
	l.deps.groupMu.Lock()
	for _, r := range roots {
		if r.IsParent && l.rootOnline(r.Path) {
			parents = append(parents, r.Path)
			// The group folders under it, where shoots appear too.
			parents = append(parents, l.deps.groupDirs[strings.ToLower(filepath.Clean(r.Path))]...)
		}
	}
	l.deps.groupMu.Unlock()
	l.deps.Watch.SetParents(parents)
}

// scanRecursionFor decides how deep a folder is scanned.
//
// An exact stored root always wins: if the user configured D:\Shoots\Wedding as
// a shoot with include-subfolders off, that beats the fact that D:\Shoots is a
// managed parent. Otherwise a shoot at any depth under a parent scans
// recursively, so its own subfolders roll into its grid rather than becoming
// shoots of their own, unless the user split it, which leaves it its own RAWs.
func (l *Library) scanRecursionFor(ctx context.Context, path string) bool {
	clean := filepath.Clean(path)
	// An older catalog's shoots are kept before a folder of one is read.
	l.upgradeShootTree(ctx)
	roots := l.libraryRoots(ctx)
	for _, r := range roots {
		if strings.EqualFold(r.Path, clean) {
			if r.IsParent {
				// The parent's own row holds only the RAWs loose in it; the
				// nested ones belong to its children.
				return false
			}
			return r.IncludeSubfolders
		}
	}
	if r, ok := l.libraryFolderOf(ctx, clean); ok {
		return shootRecursion(r, clean)
	}
	return false
}

// ListShoots lists the folders of a managed parent: its immediate subdirectories
// that hold RAWs anywhere beneath them, plus the parent itself when RAWs sit
// loose in it. Subscription query — the watcher pushes updates as folders and
// photos appear on disk.
func (l *Library) ListShoots(ctx context.Context, parentPath string) ([]Shoot, error) {
	aprot.RegisterRefreshTrigger(ctx, shootsKey(parentPath))

	parent := filepath.Clean(parentPath)
	// A disconnected drive is a normal state, not an error: the rail renders the
	// parent with an Offline badge and no children, and picks them up again when
	// the storage reappears. Only a folder that exists but cannot be read is a
	// real failure worth surfacing.
	if !l.rootOnline(parent) {
		return []Shoot{}, nil
	}
	if _, err := os.ReadDir(parent); err != nil {
		return nil, aprot.ErrInvalidParams(fmt.Sprintf("cannot read %s: %v", parent, err))
	}

	l.upgradeShootTree(ctx)
	roots := l.libraryRoots(ctx)
	var root LibraryRoot
	excluded := map[string]bool{}
	for _, r := range roots {
		if r.IsParent && strings.EqualFold(r.Path, parent) {
			root = r
			for _, e := range r.ExcludedChildren {
				excluded[strings.ToLower(e)] = true
			}
		}
	}
	isStoredRoot := func(p string) bool {
		for _, r := range roots {
			if strings.EqualFold(r.Path, p) {
				return true
			}
		}
		return false
	}

	// Capture dates are display-only (they feed the rail's date sort and time
	// grouping), so a failed lookup degrades to "no date" rather than failing
	// the listing.
	summaries, err := l.deps.DB.FolderSummaries(ctx)
	if err != nil {
		summaries = nil
	}
	earliest := folderEarliest(summaries)

	out := []Shoot{}
	// The parent's own loose RAWs are a flat count: anything nested belongs to
	// one of the shoots below — so its date, like its count, is exact-match
	// only.
	if n, oldest := directRaws(parent); n > 0 {
		self := Shoot{Path: parent, Name: filepath.Base(parent), PhotoCount: n, IsSelf: true,
			EarliestTakenAt: orEstimate(earliestFor(earliest, parent, true), oldest)}
		summarize(&self, summaries, true)
		out = append(out, self)
	}

	// The shoots at any depth, the folders above them as groups.
	w := &shootWalk{l: l, ctx: ctx, root: root, excluded: excluded, stored: isStoredRoot, earliest: earliest, sums: summaries}
	in, _ := l.deps.dirs.info(parent)
	for _, s := range in.subdirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		w.visit(filepath.Join(parent, s), "", 1)
	}
	l.watchGroups(parent, w.groups)
	sort.Slice(w.shoots, func(i, j int) bool {
		return strings.ToLower(w.shoots[i].Path) < strings.ToLower(w.shoots[j].Path)
	})
	return append(out, w.shoots...), nil
}

// watchGroups keeps the group folders found under the library folder parent,
// and has the watcher watch them too where they changed, so a shoot appearing
// in a year's folder shows as one appearing in the library folder does.
func (l *Library) watchGroups(parent string, groups []string) {
	key := strings.ToLower(filepath.Clean(parent))
	sort.Strings(groups)
	l.deps.groupMu.Lock()
	if l.deps.groupDirs == nil {
		l.deps.groupDirs = map[string][]string{}
	}
	same := slices.Equal(l.deps.groupDirs[key], groups)
	l.deps.groupDirs[key] = groups
	l.deps.groupMu.Unlock()
	if !same {
		l.syncWatchedParents(l.libraryRoots(context.Background()))
	}
}

// libraryFolderOf is the library folder path lies in or is, and whether
// there is one.
func (l *Library) libraryFolderOf(ctx context.Context, path string) (LibraryRoot, bool) {
	clean := filepath.Clean(path)
	var best LibraryRoot
	found := false
	for _, r := range l.libraryRoots(ctx) {
		if r.IsParent && (strings.EqualFold(r.Path, clean) || hasPathPrefixFold(clean, r.Path)) && len(r.Path) >= len(best.Path) {
			best, found = r, true
		}
	}
	return best, found
}

// ShootPreview is the cover of a folder not scanned yet, for the library
// sidebar: the camera's preview embedded in its first RAW, read without
// cataloguing anything. The folder must be in the library, a root or a
// shoot under one, and its drive connected.
func (l *Library) ShootPreview(ctx context.Context, path string) (*ShootPreview, error) {
	path = filepath.Clean(path)
	inLibrary := false
	for _, r := range l.libraryRoots(ctx) {
		if strings.EqualFold(r.Path, path) || hasPathPrefixFold(path, r.Path) {
			inLibrary = true
			break
		}
	}
	if !inLibrary {
		return nil, aprot.ErrInvalidParams(fmt.Sprintf("%s is not in the library", path))
	}
	if !l.rootOnline(path) {
		return &ShootPreview{}, nil
	}
	raw := scan.FirstRaw(ctx, path)
	if raw == "" {
		return &ShootPreview{}, nil
	}
	b, err := l.deps.Cache.FilePreview(ctx, raw)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A RAW with no preview, or one that will not open: no cover.
		return &ShootPreview{}, nil
	}
	return &ShootPreview{JPEG: b}, nil
}

// ScannedFolders are the folders among paths the catalog has read, each with
// its id and how many photos it holds; the rest are left out. A folder's
// photos as one view, as a year's or a client's, is the photos of its
// shoots, which the client names here.
func (l *Library) ScannedFolders(ctx context.Context, paths []string) ([]FolderInfo, error) {
	out := []FolderInfo{}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p = filepath.Clean(p)
		id, n, ok, err := l.deps.DB.FolderByPath(ctx, p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, FolderInfo{FolderID: id, Path: p, PhotoCount: n})
		}
	}
	return out, nil
}

// summarize fills in a shoot's cull counts and cover from the per-folder
// summaries: its own folder row and, unless selfOnly, those beneath it, as
// earliestFor matches them.
func summarize(sh *Shoot, rows []store.FolderSummary, selfOnly bool) {
	sh.Picked, sh.Rejected, sh.Cover = summaryFor(rows, sh.Path, selfOnly)
}

// summaryFor sums the cull counts of the folder at path and, unless
// selfOnly, those beneath it, and picks their cover: the folder's own, or
// else the first nested folder's by path.
func summaryFor(rows []store.FolderSummary, path string, selfOnly bool) (picked, rejected int, cover *ShootCover) {
	var own, nested *ShootCover
	var nestedPath string
	for _, r := range rows {
		isOwn := strings.EqualFold(r.Path, path)
		if !isOwn && (selfOnly || !hasPathPrefixFold(r.Path, path)) {
			continue
		}
		picked += r.Picked
		rejected += r.Rejected
		c := &ShootCover{PhotoID: r.CoverID, CacheKey: r.CoverKey, EditHash: r.CoverEd}
		switch {
		case isOwn:
			own = c
		case nested == nil || r.Path < nestedPath:
			nested, nestedPath = c, r.Path
		}
	}
	if own != nil {
		return picked, rejected, own
	}
	return picked, rejected, nested
}

// folderEarliest is the summaries' earliest capture times, for earliestFor.
func folderEarliest(rows []store.FolderSummary) []store.FolderEarliest {
	out := make([]store.FolderEarliest, 0, len(rows))
	for _, s := range rows {
		if s.Earliest > 0 {
			out = append(out, store.FolderEarliest{Path: s.Path, Earliest: s.Earliest})
		}
	}
	return out
}

// earliestFor picks a shoot's earliest capture time from the per-folder
// aggregate: the shoot's own folder row plus, unless selfOnly, any folder rows
// beneath it (a nested subdirectory opened directly gets its own row). The
// parent's IsSelf row matches exactly — its descendants belong to the child
// shoots. Returns 0 when nothing under the path has a known capture time.
func earliestFor(rows []store.FolderEarliest, shootPath string, selfOnly bool) int64 {
	var min int64
	for _, r := range rows {
		if !strings.EqualFold(r.Path, shootPath) && (selfOnly || !hasPathPrefixFold(r.Path, shootPath)) {
			continue
		}
		if min == 0 || r.Earliest < min {
			min = r.Earliest
		}
	}
	return min
}

// shootCount returns a child's photo count and whether it qualifies as a shoot,
// and, for a folder not scanned yet, the oldest modification time of its direct
// RAWs (unix seconds, 0 when unknown), to stand in for its capture date.
//
// A scanned folder answers from the catalog, which is both authoritative and
// free — the steady-state case, since the rail's folders are the ones being
// opened. An unscanned folder falls back to its direct RAW count (one ReadDir,
// exact for the usual flat folder of ARWs). Only a folder with no direct RAWs
// pays for a walk, and HasRaw stops at the first file it finds.
func (l *Library) shootCount(ctx context.Context, path string) (int, int64, bool) {
	if n, scanned, err := l.deps.DB.FolderPhotoCount(ctx, path); err == nil && scanned {
		// Its date comes from the catalog; until the metadata pass has read
		// it, the files' times stand in, which costs the one ReadDir.
		_, oldest := directRaws(path)
		return n, oldest, n > 0
	}
	if n, oldest := directRaws(path); n > 0 {
		return n, oldest, true
	}
	if scan.HasRaw(ctx, path) {
		return 0, 0, true
	}
	return 0, 0, false
}

// orEstimate is a shoot's capture date: the catalog's when it knows one, else
// the estimate from its files' modification times. A fresh catalog (a new data
// folder, another machine) has dates only for the shoots opened so far, and the
// rail's date sort and time groups would put every other shoot under "No date";
// a card copy keeps the cameras' file times, so the estimate is close, and the
// real capture time replaces it once the shoot is scanned.
func orEstimate(catalog, estimate int64) int64 {
	if catalog != 0 {
		return catalog
	}
	return estimate
}

func (l *Library) libraryRoots(ctx context.Context) []LibraryRoot {
	raw, err := l.deps.DB.GetSetting(ctx, settingLibraryRoots)
	if err != nil || raw == "" {
		return []LibraryRoot{}
	}
	var roots []LibraryRoot
	if json.Unmarshal([]byte(raw), &roots) != nil || roots == nil {
		return []LibraryRoot{}
	}
	return roots
}

// rootFor finds the stored root config for a path (case-insensitive, as
// Windows paths are).
func (l *Library) rootFor(ctx context.Context, path string) (LibraryRoot, bool) {
	clean := filepath.Clean(path)
	for _, r := range l.libraryRoots(ctx) {
		if strings.EqualFold(r.Path, clean) {
			return r, true
		}
	}
	return LibraryRoot{}, false
}

// ListDirRaws lists the subdirectories of path for the Add-folder picker,
// with each folder's direct (non-recursive) RAW file count.
func (l *Library) ListDirRaws(ctx context.Context, path string) ([]PickEntry, error) {
	dirents, err := os.ReadDir(path)
	if err != nil {
		return nil, aprot.ErrInvalidParams(fmt.Sprintf("cannot read %s: %v", path, err))
	}
	out := []PickEntry{}
	for _, de := range dirents {
		if !de.IsDir() {
			continue
		}
		name := de.Name()
		if len(name) > 0 && name[0] == '.' || name == "$RECYCLE.BIN" || name == "System Volume Information" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		full := filepath.Join(path, name)
		out = append(out, PickEntry{
			Name:       name,
			Path:       full,
			HasSubdirs: hasSubdirs(full),
			RawCount:   countDirectRaws(full),
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func countDirectRaws(path string) int {
	n, _ := directRaws(path)
	return n
}

// directRaws counts the RAW files directly in path, and finds the oldest of
// their modification times, in unix seconds (0 with none). The times come with
// the directory listing on Windows; elsewhere they cost a stat each.
func directRaws(path string) (int, int64) {
	dirents, err := os.ReadDir(path)
	if err != nil {
		return 0, 0
	}
	n := 0
	var oldest int64
	for _, de := range dirents {
		if de.IsDir() || !scan.IsRawFile(de.Name()) {
			continue
		}
		n++
		if info, err := de.Info(); err == nil {
			if t := info.ModTime().Unix(); t > 0 && (oldest == 0 || t < oldest) {
				oldest = t
			}
		}
	}
	return n, oldest
}

// CountRaws totals the RAW files under the given paths for the picker
// footer. Recursive honours the same noise-folder and symlink rules as the
// import scan, so the number shown is the number imported.
func (l *Library) CountRaws(ctx context.Context, paths []string, recursive bool) (*RawTotal, error) {
	total := 0
	for _, p := range paths {
		entries, err := scan.CollectEntries(ctx, p, recursive)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue // unreadable roots just contribute zero
		}
		total += len(entries)
	}
	return &RawTotal{Files: total}, nil
}

// RenameFolderOnDisk renames the folder itself (not the display alias). The
// catalog rows and any stored roots follow the new path; preview cache keys
// change with the path and regenerate lazily.
func (l *Library) RenameFolderOnDisk(ctx context.Context, path string, newName string) (*RenameResult, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" || strings.ContainsAny(newName, `\/:*?"<>|`) {
		return nil, aprot.ErrInvalidParams("invalid folder name")
	}
	oldPath := filepath.Clean(path)
	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	if strings.EqualFold(oldPath, newPath) {
		return &RenameResult{Path: oldPath}, nil
	}
	if _, err := os.Stat(newPath); err == nil {
		return nil, aprot.ErrInvalidParams(fmt.Sprintf("%s already exists", newPath))
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return nil, aprot.ErrInvalidParams(err.Error())
	}
	if err := l.deps.DB.RenameFolderPaths(ctx, oldPath, newPath); err != nil {
		return nil, err
	}

	// Follow the rename in the stored roots (the renamed folder may be a
	// root itself or the parent of several).
	roots := l.libraryRoots(ctx)
	changed := false
	for i, r := range roots {
		switch {
		case strings.EqualFold(r.Path, oldPath):
			roots[i].Path = newPath
			changed = true
		case hasPathPrefixFold(r.Path, oldPath):
			roots[i].Path = newPath + r.Path[len(oldPath):]
			changed = true
		}
	}
	if changed {
		if err := l.SetLibraryRoots(ctx, roots); err != nil {
			return nil, err
		}
	}
	aprot.TriggerRefresh(ctx, libraryRootsKey)
	// The renamed folder may be a shoot of a library folder, at any depth,
	// or a library folder itself; refresh the listings so the rail follows
	// the rename.
	keys := []string{shootsKey(filepath.Dir(oldPath)), shootsKey(oldPath), shootsKey(newPath)}
	if r, ok := l.libraryFolderOf(ctx, newPath); ok {
		keys = append(keys, shootsKey(r.Path))
	}
	aprot.TriggerRefresh(ctx, keys...)
	return &RenameResult{Path: newPath}, nil
}

// hasPathPrefixFold reports whether path lies strictly beneath prefix
// (case-insensitive, separator-aware).
func hasPathPrefixFold(path, prefix string) bool {
	if len(path) <= len(prefix) || prefix == "" {
		return false
	}
	// A drive's or a share's root ends in its separator already: E:\.
	return strings.EqualFold(path[:len(prefix)], prefix) &&
		(os.IsPathSeparator(path[len(prefix)]) || os.IsPathSeparator(prefix[len(prefix)-1]))
}
