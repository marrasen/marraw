package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/scan"
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
	l.syncWatchedParents(roots)
	for _, r := range roots {
		if r.IsParent {
			aprot.TriggerRefresh(ctx, shootsKey(r.Path))
		}
	}
	return nil
}

// syncWatchedParents keeps the filesystem watcher's parent set in step with the
// stored roots.
func (l *Library) syncWatchedParents(roots []LibraryRoot) {
	if l.deps.Watch == nil {
		return
	}
	var parents []string
	for _, r := range roots {
		if r.IsParent {
			parents = append(parents, r.Path)
		}
	}
	l.deps.Watch.SetParents(parents)
}

// isParentRoot reports whether path is a stored managed parent.
func (l *Library) isParentRoot(ctx context.Context, path string) bool {
	r, ok := l.rootFor(ctx, path)
	return ok && r.IsParent
}

// scanRecursionFor decides how deep a folder is scanned.
//
// An exact stored root always wins: if the user configured D:\Shoots\Wedding as
// a shoot with include-subfolders off, that beats the fact that D:\Shoots is a
// managed parent. Otherwise an immediate child of a parent scans recursively,
// so a shoot's own subfolders roll into its grid rather than becoming shoots of
// their own. Rule 2 compares against filepath.Dir, not a path prefix — a
// grandchild is never a shoot.
func (l *Library) scanRecursionFor(ctx context.Context, path string) bool {
	clean := filepath.Clean(path)
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
	parent := filepath.Dir(clean)
	for _, r := range roots {
		if r.IsParent && strings.EqualFold(r.Path, parent) {
			return true
		}
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
	dirents, err := os.ReadDir(parent)
	if err != nil {
		return nil, aprot.ErrInvalidParams(fmt.Sprintf("cannot read %s: %v", parent, err))
	}

	roots := l.libraryRoots(ctx)
	excluded := map[string]bool{}
	for _, r := range roots {
		if r.IsParent && strings.EqualFold(r.Path, parent) {
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

	out := []Shoot{}
	// The parent's own loose RAWs are a flat count: anything nested belongs to
	// one of the children below.
	if n := countDirectRaws(parent); n > 0 {
		out = append(out, Shoot{Path: parent, Name: filepath.Base(parent), PhotoCount: n, IsSelf: true})
	}

	var children []Shoot
	for _, de := range dirents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !de.IsDir() || scan.SkipDirName(de.Name()) {
			continue
		}
		full := filepath.Join(parent, de.Name())
		// A child that is itself a stored root renders in its own right; listing
		// it here too would show the same folder twice.
		if isStoredRoot(full) || excluded[strings.ToLower(full)] {
			continue
		}
		count, ok := l.shootCount(ctx, full)
		if !ok {
			continue
		}
		children = append(children, Shoot{Path: full, Name: de.Name(), PhotoCount: count})
	}
	sort.Slice(children, func(i, j int) bool {
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})
	return append(out, children...), nil
}

// shootCount returns a child's photo count and whether it qualifies as a shoot.
//
// A scanned folder answers from the catalog, which is both authoritative and
// free — the steady-state case, since the rail's folders are the ones being
// opened. An unscanned folder falls back to its direct RAW count (one ReadDir,
// exact for the usual flat folder of ARWs). Only a folder with no direct RAWs
// pays for a walk, and HasRaw stops at the first file it finds.
func (l *Library) shootCount(ctx context.Context, path string) (int, bool) {
	if n, scanned, err := l.deps.DB.FolderPhotoCount(ctx, path); err == nil && scanned {
		return n, n > 0
	}
	if n := countDirectRaws(path); n > 0 {
		return n, true
	}
	if scan.HasRaw(ctx, path) {
		return 0, true
	}
	return 0, false
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
	dirents, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, de := range dirents {
		if !de.IsDir() && scan.IsRawFile(de.Name()) {
			n++
		}
	}
	return n
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
	// The renamed folder may be a discovered child of a managed parent, or a
	// parent itself; refresh both listings so the rail follows the rename.
	aprot.TriggerRefresh(ctx, shootsKey(filepath.Dir(oldPath)), shootsKey(oldPath), shootsKey(newPath))
	return &RenameResult{Path: newPath}, nil
}

// hasPathPrefixFold reports whether path lies strictly beneath prefix
// (case-insensitive, separator-aware).
func hasPathPrefixFold(path, prefix string) bool {
	if len(path) <= len(prefix) {
		return false
	}
	return strings.EqualFold(path[:len(prefix)], prefix) && os.IsPathSeparator(path[len(prefix)])
}
