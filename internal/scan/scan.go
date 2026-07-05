// Package scan discovers RAW files in folders and backfills their metadata
// and grid thumbnails in the background.
package scan

import (
	"context"
	"log"
	"path/filepath"
	"strings"
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
}

// OpenFolder syncs the folder's directory listing into the store (fast; no
// decoding). The metadata backfill is driven separately by the API layer so
// it can surface as a cancellable shared task.
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
	return folderID, count, nil
}

// MetaCount returns how many photos in the folder still need metadata.
func (s *Scanner) MetaCount(ctx context.Context, folderID int64) (int, error) {
	photos, err := s.DB.PhotosNeedingMeta(ctx, folderID)
	return len(photos), err
}

// Backfill reads metadata for photos that lack it and pre-generates grid
// thumbnails, notifying subscribers in batches. It stops when ctx is
// canceled (a rescan or a client cancel supersedes it).
func (s *Scanner) Backfill(ctx context.Context, folderID int64, onProgress func(done, total int)) error {
	photos, err := s.DB.PhotosNeedingMeta(ctx, folderID)
	if err != nil {
		return err
	}
	if len(photos) == 0 {
		return nil
	}

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

	for i, ph := range photos {
		if ctx.Err() != nil {
			notify(true)
			return ctx.Err()
		}
		// Prefetch priority: metadata reads are cheap (no decode) and unblock
		// culling info + correctly-oriented thumbs; don't let them starve
		// behind queued pre-render decodes.
		err := s.Pool.Do(ctx, ph.CacheKey+"|meta", decode.PriorityPrefetch, func(jctx context.Context, proc *libraw.Processor) error {
			if err := jctx.Err(); err != nil {
				return err
			}
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
		} else {
			notify(false)
			// Queue the grid thumbnail; Ensure dedups against on-demand
			// requests.
			if p2, err := s.DB.GetPhoto(ctx, ph.ID); err == nil {
				go s.Cache.Ensure(context.WithoutCancel(ctx), p2, "512", edit.BaseHash, decode.PriorityBackground)
			}
		}
		if onProgress != nil {
			onProgress(i+1, len(photos))
		}
	}
	notify(true)
	return nil
}
