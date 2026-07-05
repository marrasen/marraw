// Package scan discovers RAW files in folders and backfills their metadata
// and grid thumbnails in the background.
package scan

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
	"os"
)

var rawExts = map[string]bool{
	".arw": true, ".sr2": true, ".srf": true, // Sony
	".cr2": true, ".cr3": true, ".crw": true, // Canon
	".nef": true, ".nrw": true, // Nikon
	".raf": true, // Fuji
	".orf": true, // Olympus
	".rw2": true, // Panasonic
	".pef": true, // Pentax
	".srw": true, // Samsung
	".dng": true, // Adobe/various
	".x3f": true, // Sigma
	".3fr": true, ".fff": true, // Hasselblad
	".iiq": true, // Phase One
	".erf": true, ".mef": true, ".mos": true, ".mrw": true, ".rwl": true,
}

// IsRawFile reports whether the name has a known RAW extension.
func IsRawFile(name string) bool {
	return rawExts[strings.ToLower(filepath.Ext(name))]
}

type Scanner struct {
	DB    *store.DB
	Cache *pyramid.Cache
	Pool  *decode.Pool
	// OnPhotosChanged is called (already throttled) when a folder's photo
	// rows changed and subscribers should refresh.
	OnPhotosChanged func(folderID int64)

	backfillGen atomic.Int64 // invalidates in-flight backfills on rescan
}

// OpenFolder syncs the folder's directory listing into the store (fast; no
// decoding) and kicks off background metadata + thumbnail backfill.
func (s *Scanner) OpenFolder(ctx context.Context, path string) (int64, int, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, 0, err
	}
	dirents, err := os.ReadDir(abs)
	if err != nil {
		return 0, 0, err
	}
	var entries []store.FileEntry
	for _, de := range dirents {
		if de.IsDir() || !IsRawFile(de.Name()) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, store.FileEntry{
			Name:    de.Name(),
			Size:    info.Size(),
			MtimeNs: info.ModTime().UnixNano(),
		})
	}
	folderID, err := s.DB.UpsertFolder(ctx, abs)
	if err != nil {
		return 0, 0, err
	}
	count, err := s.DB.SyncFolder(ctx, folderID, abs, entries)
	if err != nil {
		return 0, 0, err
	}
	go s.backfill(folderID)
	return folderID, count, nil
}

// backfill reads metadata for photos that lack it and pre-generates grid
// thumbnails, notifying subscribers in batches.
func (s *Scanner) backfill(folderID int64) {
	gen := s.backfillGen.Add(1)
	ctx := context.Background()
	photos, err := s.DB.PhotosNeedingMeta(ctx, folderID)
	if err != nil {
		log.Printf("scan: backfill query: %v", err)
		return
	}
	if len(photos) == 0 {
		return
	}

	var doneCount atomic.Int64
	lastNotify := time.Now()
	notify := func(force bool) {
		if s.OnPhotosChanged == nil {
			return
		}
		if force || time.Since(lastNotify) > 1500*time.Millisecond {
			lastNotify = time.Now()
			s.OnPhotosChanged(folderID)
		}
	}

	for _, ph := range photos {
		if s.backfillGen.Load() != gen {
			return // a newer rescan superseded this pass
		}
		ph := ph
		err := s.Pool.Do(ctx, ph.CacheKey+"|meta", decode.PriorityBackground, func(proc *libraw.Processor) error {
			if err := proc.Open(ph.Path()); err != nil {
				return err
			}
			md := proc.Metadata()
			return s.DB.SetMeta(ctx, ph.ID, store.PhotoMeta{
				Width: md.Width, Height: md.Height, Orientation: md.Orientation,
				Make: md.Make, Model: md.Model,
				ISO: md.ISO, Shutter: md.Shutter, Aperture: md.Aperture,
				FocalLen: md.FocalLen, TakenAt: md.TakenAt.Unix(),
			})
		})
		if err != nil {
			log.Printf("scan: meta %s: %v", ph.FileName, err)
			continue
		}
		doneCount.Add(1)
		notify(false)

		// Queue the grid thumbnail; Ensure dedups against on-demand requests.
		// Refresh orientation from the metadata we just wrote.
		if p2, err := s.DB.GetPhoto(ctx, ph.ID); err == nil {
			go s.Cache.Ensure(ctx, p2, "512", edit.BaseHash, decode.PriorityBackground)
		}
	}
	notify(true)
}
