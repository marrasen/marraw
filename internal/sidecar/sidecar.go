// Package sidecar mirrors a photo's portable edit intent — rating, cull flag,
// and develop parameters — to a small JSON file next to its RAW. The SQLite
// catalog stays the fast index and preview cache; the sidecar is the durable,
// path-independent record, so copying a folder to another machine carries the
// work along with the pixels.
//
// Sidecars hold intent only. Derived cache — calibrated look gamma, measured
// base exposure, pixel dimensions, EXIF — is deliberately excluded: it is
// cheap to recompute and tied to the render pipeline version, so carrying it
// would risk pinning stale values across a version bump.
package sidecar

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Ext is appended to a RAW file name to form its sidecar name, e.g.
// "IMG1234.ARW" -> "IMG1234.ARW.marraw.json".
const Ext = ".marraw.json"

// Version is the sidecar schema version. Bumped only on an incompatible
// change; readers still import an unknown-but-present file best-effort.
const Version = 1

// File is the on-disk sidecar payload.
type File struct {
	V         int             `json:"v"`
	File      string          `json:"file"`
	FileSize  int64           `json:"fileSize"`
	Rating    int             `json:"rating,omitempty"`
	Flag      int             `json:"flag,omitempty"`
	Edit      json.RawMessage `json:"edit,omitempty"` // edit.Params JSON; absent = neutral
	UpdatedAt int64           `json:"updatedAt"`      // unix millis, for last-writer-wins
}

// PathFor returns the sidecar path for a RAW file path.
func PathFor(rawPath string) string { return rawPath + Ext }

// Build assembles a sidecar payload from a photo's portable fields. editJSON
// is the stored edit-params JSON ("" = neutral).
func Build(fileName string, fileSize int64, rating, flag int, editJSON string, updatedAtMs int64) File {
	f := File{
		V:         Version,
		File:      fileName,
		FileSize:  fileSize,
		Rating:    rating,
		Flag:      flag,
		UpdatedAt: updatedAtMs,
	}
	if editJSON != "" {
		f.Edit = json.RawMessage(editJSON)
	}
	return f
}

// Read loads and validates the sidecar for a RAW path. It returns (nil, nil)
// when no sidecar exists, and an error only for a present-but-unreadable or
// malformed file — a corrupt sidecar must never block opening a folder, so
// callers log and skip.
func Read(rawPath string) (*File, error) {
	b, err := os.ReadFile(PathFor(rawPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.V <= 0 {
		return nil, errors.New("sidecar: missing schema version")
	}
	return &f, nil
}

// Write atomically writes the sidecar next to the RAW (temp file + rename), so
// a concurrent reader never observes a half-written file.
func Write(rawPath string, f File) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dst := PathFor(rawPath)
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".marraw-sidecar-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	// os.Rename replaces an existing destination on both POSIX and Windows.
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
