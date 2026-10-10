package main

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"

	"github.com/marrasen/marraw/internal/marrawclient"
)

type (
	// AddFolderState is what the Add folder dialog shows: the drives, the
	// folder open and its folders, and how many RAW files adding it
	// would bring.
	AddFolderState struct {
		Drives  []marrawclient.DriveInfo
		Path    string
		Entries []marrawclient.PickEntry
		Loading bool
		Err     string
		// Count is the RAW files the folder holds as the mode reads it,
		// or below nought while it is counted; Already says the library
		// holds it, and Drive that it is a whole drive.
		Count   int
		Already bool
		Drive   bool
	}
	// AddFolderNav opens the folder at Path in the dialog.
	AddFolderNav struct{ Path string }
	// AddFolderMode says how the folder would be added: as a library
	// folder, or as a shoot, with its subfolders or not.
	AddFolderMode struct{ Library, Subfolders bool }
	// AddFolderGo adds the folder open, as the mode says, or with OK
	// false closes the dialog.
	AddFolderGo struct {
		OK                  bool
		Library, Subfolders bool
	}
	// AddFolderSystem asks for the folder with the system's dialog.
	AddFolderSystem struct{}
)

// addFolder is the Add folder dialog's state on the controller's side.
type addFolder struct {
	open                bool
	st                  AddFolderState
	library, subfolders bool
	stopCount           context.CancelFunc
	listGen             int
}

// driveRoot matches a bare drive, as C:\, which is never added whole.
var driveRoot = regexp.MustCompile(`^[a-zA-Z]:[\\/]?$|^/$`)

// askAddFolder opens the Add folder dialog on the first drive, or on the
// folder showing.
func (cu *culler) askAddFolder() {
	a := &cu.addf
	if a.open || cu.asking {
		return
	}
	a.open, cu.asking = true, true
	a.st, a.library, a.subfolders = AddFolderState{Loading: true, Count: -1}, false, true
	_ = cu.c.Mount(gunim.Root, "addfolder", "addfolder", a.st)
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		drives, err := cu.api.Library.ListDrives(ctx)
		cu.onDo(func() {
			if !a.open {
				return
			}
			if err != nil {
				a.st.Err = err.Error()
			}
			a.st.Drives = drives
			start := ""
			if cu.folderPath != "" {
				start = parentDir(cu.folderPath)
			} else if len(drives) > 0 {
				start = drives[0].Path
			}
			cu.addFolderNav(start)
		})
	}()
}

// addFolderNav opens the folder at path in the dialog.
func (cu *culler) addFolderNav(path string) {
	a := &cu.addf
	if !a.open || path == "" {
		cu.addFolderShow()
		return
	}
	a.listGen++
	gen := a.listGen
	a.st.Path, a.st.Entries, a.st.Loading, a.st.Err = path, nil, true, ""
	cu.addFolderShow()
	cu.addFolderCount()
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		entries, err := cu.api.Library.ListDirRaws(ctx, path)
		cu.onDo(func() {
			if !a.open || a.listGen != gen {
				return
			}
			a.st.Loading = false
			if err != nil {
				a.st.Err = err.Error()
			}
			a.st.Entries = entries
			cu.addFolderShow()
		})
	}()
}

// addFolderShow shows the dialog its state.
func (cu *culler) addFolderShow() {
	a := &cu.addf
	if !a.open {
		return
	}
	a.st.Already = slices.ContainsFunc(cu.lib.roots, func(r marrawclient.LibraryRoot) bool { return strings.EqualFold(r.Path, a.st.Path) })
	a.st.Drive = driveRoot.MatchString(a.st.Path)
	_ = cu.c.Update("addfolder", a.st)
}

// addFolderCount counts the RAW files adding the folder would bring, a
// moment after the folder or the mode last changed, the count before
// stopped.
func (cu *culler) addFolderCount() {
	a := &cu.addf
	if a.stopCount != nil {
		a.stopCount()
		a.stopCount = nil
	}
	a.st.Count = -1
	path := a.st.Path
	if path == "" || driveRoot.MatchString(path) {
		return
	}
	recursive := a.library || a.subfolders
	ctx, cancel := context.WithCancel(cu.ctx)
	a.stopCount = cancel
	go func() {
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		n, err := cu.api.Library.CountRaws(ctx, []string{path}, recursive)
		if err != nil || n == nil {
			return
		}
		cu.onDo(func() {
			if a.open && a.st.Path == path && ctx.Err() == nil {
				a.st.Count = n.Files
				cu.addFolderShow()
			}
		})
	}()
}

// addFolderMode takes the mode chosen.
func (cu *culler) addFolderMode(in AddFolderMode) {
	a := &cu.addf
	if !a.open || (a.library == in.Library && a.subfolders == in.Subfolders) {
		return
	}
	a.library, a.subfolders = in.Library, in.Subfolders
	cu.addFolderCount()
	cu.addFolderShow()
}

// addFolderGo adds the folder open to the library and closes the dialog.
// A library folder takes over the shoots added by hand that sit right
// in it, as marraw's does, so none shows twice.
func (cu *culler) addFolderGo(in AddFolderGo) {
	a := &cu.addf
	if !a.open {
		return
	}
	path := a.st.Path
	a.open, cu.asking = false, false
	if a.stopCount != nil {
		a.stopCount()
		a.stopCount = nil
	}
	_ = cu.c.Unmount("addfolder")
	cu.refocus()
	if !in.OK || path == "" || driveRoot.MatchString(path) {
		return
	}
	if slices.ContainsFunc(cu.lib.roots, func(r marrawclient.LibraryRoot) bool { return strings.EqualFold(r.Path, path) }) {
		return
	}
	root := marrawclient.LibraryRoot{Path: path, IncludeSubfolders: !in.Library && in.Subfolders, IsParent: in.Library}
	var kept []marrawclient.LibraryRoot
	absorbed := 0
	for _, r := range cu.lib.roots {
		if in.Library && !r.IsParent && strings.EqualFold(parentDir(r.Path), path) {
			absorbed++
			continue
		}
		kept = append(kept, r)
	}
	what := "folder"
	if in.Library {
		what = "library folder"
	}
	cu.saveRoots(append(kept, root), func() {
		note := "Added the " + what + " to the library"
		if absorbed > 0 {
			note += fmt.Sprintf(". %d folder%s already there now sit under it.", absorbed, map[bool]string{true: "", false: "s"}[absorbed == 1])
		}
		cu.notify(note)
		if !in.Library {
			cu.openShoot(path)
		}
	})
}

// addFolderSystem asks for the folder with the system's dialog, and
// opens it in the dialog.
func (cu *culler) addFolderSystem() {
	go func() {
		paths, err := cu.c.ChooseFiles(cu.ctx, chooseFolder("Choose a folder to add"))
		if err != nil || len(paths) == 0 {
			return
		}
		cu.onDo(func() { cu.addFolderNav(paths[0]) })
	}()
}
