package api

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/store"
)

// A library folder's shoots lie at any depth: photographers keep them by
// year (2019 / 2019-11-08 Wedding), by client (Client / Project / Date), or
// mixed. The rules, in order:
//
//   - A folder with RAWs directly in it is a shoot, and its subfolders are
//     part of it, as a shoot split by camera or card is.
//   - A folder without RAWs directly in it whose name starts with a full
//     date, as "2019-11-08 Wedding" holding "CamA" and "CamB", is one shoot
//     of its subfolders.
//   - So is a folder holding a DCIM folder, whatever its name: a memory
//     card copied as it is, with the photos in DCIM/100MSDCF and the
//     camera's own folders, as PRIVATE and AVF_INFO, beside it.
//   - So is a folder right in the library folder that the catalog already
//     read as one shoot, of photos in its subfolders: marraw once made a
//     shoot of every folder there, and splitting one now would leave its
//     catalog rows, and all they hold, behind.
//   - Any other folder without RAWs directly in it is a group, such as a
//     year or a client, and the folders in it are looked at the same way.
//
// The user can say otherwise of any folder, kept in the library folder's
// FolderModes: FolderSplit shows a folder's subfolders as shoots of their
// own (a shoot of its own RAWs where it has some, else a group), and
// FolderOne makes a folder one shoot of everything beneath it.
const (
	FolderSplit = "split"
	FolderOne   = "one"
)

// shootDepth is how deep under a library folder shoots are looked for.
const shootDepth = 8

// datedName is a folder name starting with a full date: 2019-11-08,
// 2019_11_08, 2019.11.08 or 20191108.
var datedName = regexp.MustCompile(`^\d{4}[-_.]?\d{2}[-_.]?\d{2}([\s_.-]|$)`)

// dirInfo is what a folder's listing says, as last read: how many RAWs lie
// directly in it, the oldest of their times, and its subfolders.
type dirInfo struct {
	mtime   time.Time
	raws    int
	oldest  int64
	subdirs []string
}

// dirCache keeps the folders' listings, read again only when a folder's
// modification time changes, as it does when an entry is added or removed:
// listing a library of a thousand shoots on a network share is then a stat
// of each folder, not a listing of each.
type dirCache struct {
	mu   sync.Mutex
	dirs map[string]dirInfo
}

// info is dir's listing, and false where it cannot be read.
func (c *dirCache) info(dir string) (dirInfo, bool) {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return dirInfo{}, false
	}
	c.mu.Lock()
	in, ok := c.dirs[dir]
	c.mu.Unlock()
	if ok && in.mtime.Equal(st.ModTime()) {
		return in, true
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return dirInfo{}, false
	}
	in = dirInfo{mtime: st.ModTime()}
	for _, de := range des {
		if de.IsDir() {
			if !scan.SkipDirName(de.Name()) {
				in.subdirs = append(in.subdirs, de.Name())
			}
			continue
		}
		if !scan.IsRawFile(de.Name()) {
			continue
		}
		in.raws++
		if fi, err := de.Info(); err == nil {
			if t := fi.ModTime().Unix(); t > 0 && (in.oldest == 0 || t < in.oldest) {
				in.oldest = t
			}
		}
	}
	c.mu.Lock()
	if c.dirs == nil {
		c.dirs = map[string]dirInfo{}
	}
	c.dirs[dir] = in
	c.mu.Unlock()
	return in, true
}

// below is how many RAWs lie in dir and everything beneath it, and the
// oldest of their times, to depth more levels.
func (c *dirCache) below(ctx context.Context, dir string, depth int) (n int, oldest int64) {
	in, ok := c.info(dir)
	if !ok || ctx.Err() != nil {
		return 0, 0
	}
	n, oldest = in.raws, in.oldest
	if depth <= 0 {
		return n, oldest
	}
	for _, s := range in.subdirs {
		m, o := c.below(ctx, filepath.Join(dir, s), depth-1)
		n += m
		if o != 0 && (oldest == 0 || o < oldest) {
			oldest = o
		}
	}
	return n, oldest
}

// shootWalk finds the shoots under one library folder.
type shootWalk struct {
	l        *Library
	ctx      context.Context
	root     LibraryRoot
	excluded map[string]bool
	stored   func(string) bool
	earliest []store.FolderEarliest
	sums     []store.FolderSummary
	shoots   []Shoot
	// groups are the group folders found, which are watched as the
	// library folder is, for shoots appearing in them.
	groups []string
}

// visit looks at dir, a folder group deep (its group folders' names, "/"
// between), and reports whether anything under it is a shoot.
func (w *shootWalk) visit(dir, group string, depth int) bool {
	if w.ctx.Err() != nil || w.excluded[strings.ToLower(dir)] || w.stored(dir) {
		return false
	}
	in, ok := w.l.deps.dirs.info(dir)
	if !ok {
		return false
	}
	mode := w.root.FolderModes[strings.ToLower(dir)]
	inner := joinGroup(group, filepath.Base(dir))
	switch {
	case in.raws > 0 && mode != FolderSplit:
		w.add(dir, group, false, len(in.subdirs) > 0)
		return true
	case in.raws > 0:
		// Split: a shoot of its own RAWs, and its subfolders' shoots
		// under it.
		w.add(dir, group, true, true)
		for _, s := range in.subdirs {
			w.visit(filepath.Join(dir, s), inner, depth+1)
		}
		return true
	case mode == FolderOne || mode != FolderSplit && (datedName.MatchString(filepath.Base(dir)) || cardCopy(in)):
		if n, _ := w.l.deps.dirs.below(w.ctx, dir, shootDepth-depth); n == 0 {
			return false
		}
		w.add(dir, group, false, len(in.subdirs) > 0)
		return true
	case depth == 1 && mode != FolderSplit && w.readAsOne(dir):
		w.add(dir, group, false, len(in.subdirs) > 0)
		return true
	}
	if depth >= shootDepth {
		return false
	}
	any := false
	for _, s := range in.subdirs {
		if w.visit(filepath.Join(dir, s), inner, depth+1) {
			any = true
		}
	}
	if any {
		w.groups = append(w.groups, dir)
	}
	return any
}

// readAsOne reports whether the catalog holds photos read under dir, which
// has none directly in it: it was read as one shoot of its subfolders.
func (w *shootWalk) readAsOne(dir string) bool {
	n, scanned, err := w.l.deps.DB.FolderPhotoCount(w.ctx, dir)
	return err == nil && scanned && n > 0
}

// add lists the shoot at dir: split, its own RAWs alone; else everything
// beneath it.
func (w *shootWalk) add(dir, group string, split, subfolders bool) {
	n, oldest := 0, int64(0)
	if c, scanned, err := w.l.deps.DB.FolderPhotoCount(w.ctx, dir); err == nil && scanned {
		n = c
		if in, ok := w.l.deps.dirs.info(dir); ok {
			oldest = in.oldest
		}
	}
	if n == 0 {
		depth := shootDepth
		if split {
			depth = 0
		}
		n, oldest = w.l.deps.dirs.below(w.ctx, dir, depth)
	}
	sh := Shoot{Path: dir, Name: filepath.Base(dir), PhotoCount: n, Group: group, Split: split, Subfolders: subfolders,
		EarliestTakenAt: orEstimate(earliestFor(w.earliest, dir, split), oldest)}
	summarize(&sh, w.sums, split)
	w.shoots = append(w.shoots, sh)
}

// cardCopy says a folder holds a memory card's DCIM folder, as a card copied
// whole does.
func cardCopy(in dirInfo) bool {
	for _, s := range in.subdirs {
		if strings.EqualFold(s, "DCIM") {
			return true
		}
	}
	return false
}

// joinGroup is group with name below it.
func joinGroup(group, name string) string {
	if group == "" {
		return name
	}
	return group + "/" + name
}

// shootRecursion says how deep the folder at path is scanned, where it lies
// under a library folder: a shoot takes in its subfolders unless it is
// split.
func shootRecursion(root LibraryRoot, path string) bool {
	return root.FolderModes[strings.ToLower(path)] != FolderSplit
}
