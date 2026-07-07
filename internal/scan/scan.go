// Package scan discovers RAW files in folders and backfills their metadata
// and grid thumbnails in the background.
package scan

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/sidecar"
	"github.com/marrasen/marraw/internal/store"
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

// skipDirName filters noise directories out of recursive walks: exports,
// select copies, thumbnail caches, our own preview cache, and dot/system
// folders.
func skipDirName(name string) bool {
	l := strings.ToLower(name)
	if strings.HasPrefix(l, ".") {
		return true
	}
	switch l {
	case "export", "exports", "_selects", "marraw-previews",
		"$recycle.bin", "system volume information":
		return true
	}
	return false
}

// CollectEntries lists the RAW files of root. Non-recursive is a flat
// ReadDir; recursive is a breadth-first walk that skips noise directories,
// follows directory symlinks one hop with a resolved-path loop guard, and
// names each file by its path relative to root.
func CollectEntries(ctx context.Context, root string, recursive bool) ([]store.FileEntry, error) {
	type dirItem struct {
		abs, rel string
		depth    int
	}
	const maxDepth = 12

	visited := map[string]bool{}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		visited[strings.ToLower(r)] = true
	}
	queue := []dirItem{{abs: root, rel: "", depth: 0}}
	entries := []store.FileEntry{}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := queue[0]
		queue = queue[1:]
		dirents, err := os.ReadDir(d.abs)
		if err != nil {
			if d.rel == "" {
				return nil, err
			}
			continue // an unreadable subfolder must not fail the whole scan
		}
		for _, de := range dirents {
			name := de.Name()
			isSymlink := de.Type()&os.ModeSymlink != 0
			if de.IsDir() || isSymlink {
				if !recursive || d.depth >= maxDepth || skipDirName(name) {
					continue
				}
				child := filepath.Join(d.abs, name)
				info, err := os.Stat(child) // resolves symlinks
				if err != nil || !info.IsDir() {
					continue
				}
				resolved, err := filepath.EvalSymlinks(child)
				if err != nil {
					continue
				}
				key := strings.ToLower(resolved)
				if visited[key] {
					continue
				}
				visited[key] = true
				queue = append(queue, dirItem{abs: child, rel: filepath.Join(d.rel, name), depth: d.depth + 1})
				continue
			}
			if !IsRawFile(name) {
				continue
			}
			info, err := de.Info()
			if err != nil {
				continue
			}
			entries = append(entries, store.FileEntry{
				Name:    filepath.Join(d.rel, name),
				Size:    info.Size(),
				MtimeNs: info.ModTime().UnixNano(),
			})
		}
	}
	return entries, nil
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
// decoding). With recursive set, RAWs anywhere beneath the folder are
// registered under it with their relative subpath as the file name — the
// nested structure stays visible in the name, and Photo.Path() still joins
// cleanly. The metadata backfill is driven separately by the API layer so
// it can surface as a cancellable shared task.
func (s *Scanner) OpenFolder(ctx context.Context, path string, recursive bool) (int64, int, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, 0, err
	}
	entries, err := CollectEntries(ctx, abs, recursive)
	if err != nil {
		return 0, 0, err
	}
	folderID, err := s.DB.UpsertFolder(ctx, abs)
	if err != nil {
		return 0, 0, err
	}
	count, err := s.DB.SyncFolder(ctx, folderID, abs, entries)
	if err != nil {
		return 0, 0, err
	}
	// Reconcile portable sidecars: adopt edits copied in with the folder, and
	// backfill sidecars for catalog-only intent. Best-effort — a folder must
	// still open if sidecar I/O fails.
	if err := s.importSidecars(ctx, folderID, abs, entries); err != nil {
		log.Printf("scan: import sidecars %s: %v", abs, err)
	}
	return folderID, count, nil
}

// importSidecars reconciles each RAW's on-disk sidecar with its catalog row.
// When a sidecar is present and newer it overwrites the row (last-writer-wins,
// enforced in the store); when a sidecar is absent but the row carries intent,
// a sidecar is written so the folder becomes self-contained. Returns after
// notifying subscribers if any row changed.
func (s *Scanner) importSidecars(ctx context.Context, folderID int64, folderPath string, entries []store.FileEntry) error {
	photos, err := s.DB.ListPhotos(ctx, folderID)
	if err != nil {
		return err
	}
	byName := make(map[string]store.Photo, len(photos))
	for _, p := range photos {
		byName[p.FileName] = p
	}

	var applied int
	for _, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rawPath := filepath.Join(folderPath, e.Name)
		sc, err := sidecar.Read(rawPath)
		if err != nil {
			log.Printf("scan: read sidecar %s: %v", e.Name, err)
			continue
		}
		if sc != nil {
			if s.applySidecar(ctx, folderID, e, sc) {
				applied++
			}
			continue
		}
		// No sidecar on disk: backfill one from existing catalog intent, unless
		// sidecar writes are disabled (importing above still runs regardless).
		if p, ok := byName[e.Name]; ok && hasIntent(p) && s.DB.SidecarWritesEnabled(ctx) {
			editJSON := ""
			if p.EditParams.Valid {
				editJSON = p.EditParams.String
			}
			f := sidecar.Build(p.FileName, p.FileSize, p.Rating, p.Flag, editJSON, sidecarUpdatedMs(p))
			if err := sidecar.Write(rawPath, f); err != nil {
				log.Printf("scan: backfill sidecar %s: %v", e.Name, err)
			}
		}
	}
	if applied > 0 && s.OnPhotosChanged != nil {
		s.OnPhotosChanged(folderID)
	}
	return nil
}

// applySidecar validates a sidecar against the file it names and, if it wins
// the last-writer-wins check, writes its intent into the catalog. Returns
// whether the row changed.
func (s *Scanner) applySidecar(ctx context.Context, folderID int64, e store.FileEntry, sc *sidecar.File) bool {
	// A sidecar whose recorded size disagrees with the file it sits beside was
	// almost certainly left over from a different file; ignore it rather than
	// apply the wrong edit.
	if sc.FileSize != 0 && sc.FileSize != e.Size {
		log.Printf("scan: sidecar %s size mismatch (%d vs %d), ignoring", e.Name, sc.FileSize, e.Size)
		return false
	}

	var editJSON *string
	editHash := edit.BaseHash
	if len(sc.Edit) > 0 {
		ep, err := edit.Parse(string(sc.Edit))
		if err != nil {
			log.Printf("scan: sidecar %s malformed edit, importing rating/flag only: %v", e.Name, err)
		} else {
			ep.Normalize()
			if !ep.IsNeutral() {
				// Re-marshal the normalized params so the stored JSON and edit
				// hash match exactly what the editor would have written.
				if b, err := json.Marshal(ep); err == nil {
					str := string(b)
					editJSON = &str
					editHash = ep.Hash()
				}
			}
		}
	}

	ok, err := s.DB.ApplyImportedEdit(ctx, folderID, e.Name, sc.Rating, sc.Flag, editJSON, editHash, sc.UpdatedAt)
	if err != nil {
		log.Printf("scan: apply sidecar %s: %v", e.Name, err)
		return false
	}
	return ok
}

// hasIntent reports whether a photo carries portable intent worth mirroring to
// a sidecar (a rating, a cull flag, or a non-neutral edit).
func hasIntent(p store.Photo) bool {
	return p.Rating != 0 || p.Flag != 0 || p.EditParams.Valid
}

// sidecarUpdatedMs is the timestamp to record in a backfilled sidecar; 0 for a
// row that predates the updated_at column.
func sidecarUpdatedMs(p store.Photo) int64 {
	if p.UpdatedAt.Valid {
		return p.UpdatedAt.Int64
	}
	return 0
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
