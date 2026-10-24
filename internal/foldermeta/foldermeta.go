// Package foldermeta keeps what marraw knows about the photos of a folder in
// one file in that folder, marraw.json: each photo's rating, flag and edit,
// its fingerprint, and the photos deleted on purpose. The file travels with
// the photos, so backing up a folder backs up the work done on it, and the
// catalog is a cache rebuilt from the disk.
//
// One readable file per folder, not a file per photo and not a database: it
// keeps the folder tidy, loads in one read, and merges. Every photo's entry
// carries the time of its last change, so two copies of the file, written on
// two computers or left behind by a sync tool as a "conflicted copy", merge
// photo by photo, the newest winning. A database in the folder would not
// survive file locking over a network share, nor merge.
package foldermeta

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Name is the file's name in each folder.
const Name = "marraw.json"

// Version is the file's schema version, bumped only for a change older
// readers cannot take; a reader takes what it knows of a newer file.
const Version = 1

// File is marraw.json.
type File struct {
	V int `json:"v"`
	// Photos are the folder's photos by file name.
	Photos map[string]Photo `json:"photos"`
	// Deleted are the photos deleted on purpose from the folder, so an
	// import from a card does not bring them back.
	Deleted []Deleted `json:"deleted,omitempty"`
}

// Photo is one photo's entry.
type Photo struct {
	// Size is the file's size, and FP its fingerprint: the same for a
	// copy of the file anywhere, whatever it is called. See fingerprint.Of.
	Size int64  `json:"size,omitempty"`
	FP   string `json:"fp,omitempty"`
	// Rating, Flag and Edit are what the user made of it: Edit is the
	// develop settings' JSON, absent for none.
	Rating int             `json:"rating,omitempty"`
	Flag   int             `json:"flag,omitempty"`
	Edit   json.RawMessage `json:"edit,omitempty"`
	// UpdatedAt is when Rating, Flag or Edit last changed, in unix
	// milliseconds: the newer of two entries wins a merge.
	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

// HasIntent reports whether the user made anything of the photo.
func (p Photo) HasIntent() bool { return p.Rating != 0 || p.Flag != 0 || len(p.Edit) > 0 }

// Deleted is a photo deleted on purpose.
type Deleted struct {
	FP        string `json:"fp"`
	Name      string `json:"name"`
	TakenAt   int64  `json:"takenAt,omitempty"`
	DeletedAt int64  `json:"deletedAt"`
}

// Empty is a file with nothing in it yet.
func Empty() *File { return &File{V: Version, Photos: map[string]Photo{}} }

// Read reads dir's marraw.json, merged with any conflicted copies a sync
// tool left beside it, or an empty file where there is none. A copy that
// will not parse is skipped: a broken file must never keep a folder from
// opening. The error is the main file's, where it is there but unreadable.
func Read(dir string) (*File, error) {
	f := Empty()
	main, err := readOne(filepath.Join(dir, Name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return f, err
	}
	if main != nil {
		f.merge(main)
	}
	for _, c := range conflicted(dir) {
		if cf, err := readOne(c); err == nil {
			f.merge(cf)
		}
	}
	return f, nil
}

func readOne(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Photos == nil {
		f.Photos = map[string]Photo{}
	}
	return &f, nil
}

// conflicted are the copies of marraw.json sync tools leave beside it when
// two computers changed it: Dropbox's "marraw (Name's conflicted copy
// 2026-10-12).json", OneDrive's "marraw-COMPUTER.json", and the like.
func conflicted(dir string) []string {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range des {
		n := de.Name()
		l := strings.ToLower(n)
		if de.IsDir() || l == Name || !strings.HasPrefix(l, "marraw") || !strings.HasSuffix(l, ".json") {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(l, "marraw"), ".json")
		// A sidecar of the old kind is "IMG.ARW.marraw.json", which
		// does not start so; a conflicted copy has more after the name.
		if strings.HasPrefix(mid, " (") || strings.HasPrefix(mid, "-") || strings.HasPrefix(mid, "_") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}

// merge takes o into f: each photo's newer entry, every deletion, and the
// fingerprint and size where only o has them.
func (f *File) merge(o *File) {
	for name, p := range o.Photos {
		f.SetPhoto(name, p)
	}
	for _, d := range o.Deleted {
		f.AddDeleted(d)
	}
	f.dropDeleted()
}

// SetPhoto sets name's entry to p where p is the newer, keeping a
// fingerprint and size either one has.
func (f *File) SetPhoto(name string, p Photo) {
	cur, ok := f.Photos[name]
	if !ok {
		f.Photos[name] = p
		return
	}
	if p.UpdatedAt > cur.UpdatedAt || p.UpdatedAt == cur.UpdatedAt && !cur.HasIntent() {
		if p.FP == "" {
			p.FP, p.Size = cur.FP, cur.Size
		}
		f.Photos[name] = p
		return
	}
	if cur.FP == "" && p.FP != "" {
		cur.FP, cur.Size = p.FP, p.Size
		f.Photos[name] = cur
	}
}

// SetFingerprint records name's size and fingerprint, leaving what the
// user made of it as it is.
func (f *File) SetFingerprint(name string, size int64, fp string) {
	p := f.Photos[name]
	p.Size, p.FP = size, fp
	f.Photos[name] = p
}

// AddDeleted records a photo deleted on purpose, once by its fingerprint.
func (f *File) AddDeleted(d Deleted) {
	if d.FP == "" {
		return
	}
	for i, e := range f.Deleted {
		if e.FP == d.FP {
			if d.DeletedAt > e.DeletedAt {
				f.Deleted[i] = d
			}
			return
		}
	}
	f.Deleted = append(f.Deleted, d)
	slices.SortFunc(f.Deleted, func(a, b Deleted) int { return strings.Compare(a.Name, b.Name) })
}

// Delete drops name's entry and records it as deleted on purpose.
func (f *File) Delete(name string, d Deleted) {
	delete(f.Photos, name)
	f.AddDeleted(d)
}

// dropDeleted drops the entries of photos deleted after their last change:
// a merge must not bring back a photo deleted on another computer.
func (f *File) dropDeleted() {
	for name, p := range f.Photos {
		if p.FP == "" {
			continue
		}
		for _, d := range f.Deleted {
			if d.FP == p.FP && d.DeletedAt >= p.UpdatedAt {
				delete(f.Photos, name)
				break
			}
		}
	}
}

// Write writes f as dir's marraw.json, whole beside it first and then put in
// place, so a crash or a reader never sees half a file.
func Write(dir string, f *File) error {
	f.V = Version
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, ".marraw-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	// Readable as the photos are, as a temporary file is not: a share or
	// a backup reads it with them.
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, filepath.Join(dir, Name)); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
