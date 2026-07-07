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
	return nil
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
