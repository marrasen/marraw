package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/sidecar"
	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/trash"
)

// Library handles folder browsing and culling.
type Library struct {
	deps *Deps
}

// ListDrives returns the filesystem roots to seed the folder tree.
func (l *Library) ListDrives(ctx context.Context) ([]DriveInfo, error) {
	var out []DriveInfo
	if home, err := os.UserHomeDir(); err == nil {
		pictures := filepath.Join(home, "Pictures")
		if _, err := os.Stat(pictures); err == nil {
			out = append(out, DriveInfo{Path: pictures, Name: "Pictures"})
		}
	}
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err == nil {
			out = append(out, DriveInfo{Path: root, Name: string(letter) + ":"})
		}
	}
	return out, nil
}

// ListDir lists the subdirectories of path for the folder tree.
func (l *Library) ListDir(ctx context.Context, path string) ([]DirEntry, error) {
	dirents, err := os.ReadDir(path)
	if err != nil {
		return nil, aprot.ErrInvalidParams(fmt.Sprintf("cannot read %s: %v", path, err))
	}
	out := []DirEntry{}
	for _, de := range dirents {
		if !de.IsDir() {
			continue
		}
		name := de.Name()
		if len(name) > 0 && name[0] == '.' || name == "$RECYCLE.BIN" || name == "System Volume Information" {
			continue
		}
		full := filepath.Join(path, name)
		out = append(out, DirEntry{Name: name, Path: full, HasSubdirs: hasSubdirs(full)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func hasSubdirs(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	// Cheap peek: scan a limited batch rather than the whole directory.
	for range 4 {
		ents, err := f.ReadDir(64)
		for _, e := range ents {
			if e.IsDir() {
				return true
			}
		}
		if err != nil || len(ents) == 0 {
			return false
		}
	}
	return false
}

// OpenFolder scans a folder into the library and returns its id and photo
// count. Fast: no decoding happens here — the metadata backfill and the
// pre-render pass start in the background as cancellable shared tasks.
func (l *Library) OpenFolder(ctx context.Context, path string) (*FolderInfo, error) {
	// Library roots carry a per-root include-subfolders flag; folders opened
	// outside the curated library scan flat.
	recursive := false
	if root, ok := l.rootFor(ctx, path); ok {
		recursive = root.IncludeSubfolders
	}
	folderID, count, err := l.deps.Scanner.OpenFolder(ctx, path, recursive)
	if err != nil {
		return nil, aprot.ErrInvalidParams(err.Error())
	}
	aprot.TriggerRefresh(ctx, photosKey(folderID))
	l.rememberRecent(ctx, path)
	l.startFolderJobs(ctx, folderID, path)
	return &FolderInfo{FolderID: folderID, Path: path, PhotoCount: count}, nil
}

const (
	settingFavoriteFolders = "favoriteFolders"
	settingRecentFolders   = "recentFolders"
	folderPrefsKey         = "folderPrefs"
	appSettingsKey         = "appSettings"
	maxRecentFolders       = 8
)

// GetAppSettings returns the application preferences. Subscription query: a
// SetSidecarWrites call pushes an update.
func (l *Library) GetAppSettings(ctx context.Context) (*AppSettings, error) {
	aprot.RegisterRefreshTrigger(ctx, appSettingsKey)
	return &AppSettings{
		SidecarWrites: l.deps.DB.SidecarWritesEnabled(ctx),
	}, nil
}

// SetSidecarWrites toggles whether edits are mirrored to portable sidecars.
func (l *Library) SetSidecarWrites(ctx context.Context, enabled bool) error {
	if err := l.deps.DB.SetSidecarWrites(ctx, enabled); err != nil {
		return err
	}
	aprot.TriggerRefresh(ctx, appSettingsKey)
	return nil
}

// GetFolderPrefs returns favourite and recently opened folders. Subscription
// query: adding a favourite or opening a folder pushes an update.
func (l *Library) GetFolderPrefs(ctx context.Context) (*FolderPrefs, error) {
	aprot.RegisterRefreshTrigger(ctx, folderPrefsKey)
	return &FolderPrefs{
		Favorites: l.pathListSetting(ctx, settingFavoriteFolders),
		Recents:   l.pathListSetting(ctx, settingRecentFolders),
	}, nil
}

// SetFavoriteFolders replaces the favourite list (add, remove, and reorder are
// all "send the new list").
func (l *Library) SetFavoriteFolders(ctx context.Context, paths []string) error {
	if paths == nil {
		paths = []string{}
	}
	if err := l.savePathListSetting(ctx, settingFavoriteFolders, paths); err != nil {
		return err
	}
	aprot.TriggerRefresh(ctx, folderPrefsKey)
	return nil
}

// rememberRecent moves path to the front of the recent-folders list. Best
// effort: an opened folder must never fail because bookkeeping did.
func (l *Library) rememberRecent(ctx context.Context, path string) {
	recents := []string{path}
	for _, p := range l.pathListSetting(ctx, settingRecentFolders) {
		if !strings.EqualFold(p, path) && len(recents) < maxRecentFolders {
			recents = append(recents, p)
		}
	}
	if err := l.savePathListSetting(ctx, settingRecentFolders, recents); err == nil {
		aprot.TriggerRefresh(ctx, folderPrefsKey)
	}
}

func (l *Library) pathListSetting(ctx context.Context, key string) []string {
	raw, err := l.deps.DB.GetSetting(ctx, key)
	if err != nil || raw == "" {
		return []string{}
	}
	var paths []string
	if json.Unmarshal([]byte(raw), &paths) != nil || paths == nil {
		return []string{}
	}
	return paths
}

func (l *Library) savePathListSetting(ctx context.Context, key string, paths []string) error {
	raw, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	return l.deps.DB.SetSetting(ctx, key, string(raw))
}

// ListPhotos returns all photos of a folder, sorted by file name. It is a
// subscription query: scan progress and structural changes push new results.
func (l *Library) ListPhotos(ctx context.Context, folderID int64) ([]Photo, error) {
	aprot.RegisterRefreshTrigger(ctx, photosKey(folderID))
	rows, err := l.deps.DB.ListPhotos(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := make([]Photo, len(rows))
	for i, p := range rows {
		out[i] = toAPIPhoto(p)
	}
	return out, nil
}

func toAPIPhoto(p store.Photo) Photo {
	return Photo{
		ID:          p.ID,
		FileName:    p.FileName,
		CacheKey:    p.CacheKey,
		EditHash:    p.EditHash,
		Rating:      p.Rating,
		Flag:        FlagFromInt(p.Flag),
		MetaLoaded:  p.MetaLoaded,
		Width:       p.Width,
		Height:      p.Height,
		Orientation: p.Orientation,
		ISO:         p.ISO,
		Shutter:     p.Shutter,
		Aperture:    p.Aperture,
		FocalLen:    p.FocalLen,
		TakenAt:     p.TakenAt,
		Make:        p.Make,
		Model:       p.Model,
	}
}

// SetRating rates the given photos 0-5. Subscribers learn via a granular
// patch event, not a full-list refresh.
func (l *Library) SetRating(ctx context.Context, ids []int64, rating int) error {
	if rating < 0 || rating > 5 {
		return aprot.ErrInvalidParams("rating must be 0..5")
	}
	if err := l.deps.DB.SetRating(ctx, ids, rating, time.Now().UnixMilli()); err != nil {
		return err
	}
	patches := make([]PhotoPatch, len(ids))
	for i, id := range ids {
		patches[i] = PhotoPatch{ID: id, Rating: &rating}
	}
	l.deps.PatchPhotos(ctx, patches)
	l.deps.writeSidecars(ctx, ids)
	return nil
}

// SetFlag sets the cull flag of the given photos.
func (l *Library) SetFlag(ctx context.Context, ids []int64, flag Flag) error {
	if err := l.deps.DB.SetFlag(ctx, ids, FlagToInt(flag), time.Now().UnixMilli()); err != nil {
		return err
	}
	patches := make([]PhotoPatch, len(ids))
	for i, id := range ids {
		patches[i] = PhotoPatch{ID: id, Flag: &flag}
	}
	l.deps.PatchPhotos(ctx, patches)
	l.deps.writeSidecars(ctx, ids)
	return nil
}

// DeletePhotos moves the given photos' files to the OS recycle bin and
// removes them from the library. Structural change: subscribers get a full
// refresh, not a patch.
func (l *Library) DeletePhotos(ctx context.Context, ids []int64) (*DeleteResult, error) {
	photos, err := l.deps.DB.GetPhotos(ctx, ids)
	if err != nil {
		return nil, aprot.ErrInvalidParams(err.Error())
	}
	paths := make([]string, len(photos))
	folders := map[int64]bool{}
	for i, p := range photos {
		paths[i] = p.Path()
		folders[p.FolderID] = true
	}
	if err := trash.MoveToTrash(paths); err != nil {
		return nil, err
	}
	if err := l.deps.DB.DeletePhotos(ctx, ids); err != nil {
		return nil, err
	}
	// Remove orphaned sidecars so a deleted photo leaves nothing behind. Best
	// effort: a stray sidecar is harmless (no matching RAW is ever read).
	for _, p := range photos {
		if scPath := sidecar.PathFor(p.Path()); scPath != "" {
			if err := os.Remove(scPath); err != nil && !os.IsNotExist(err) {
				log.Printf("api: remove sidecar %s: %v", p.FileName, err)
			}
		}
	}
	for f := range folders {
		aprot.TriggerRefresh(ctx, photosKey(f))
	}
	return &DeleteResult{Deleted: len(photos)}, nil
}

// SetVisible hints which photos the client's viewport shows so their
// thumbnails are generated ahead of scroll. Fire-and-forget.
func (l *Library) SetVisible(ctx context.Context, folderID int64, ids []int64) error {
	if len(ids) > 256 {
		ids = ids[:256]
	}
	photos, err := l.deps.DB.GetPhotos(context.WithoutCancel(ctx), ids)
	if err != nil {
		return nil // vanished rows are not the client's problem
	}
	for _, p := range photos {
		go l.deps.Cache.Ensure(context.Background(), p, "512", currentHash(p), decode.PriorityPrefetch)
	}
	return nil
}

// currentHash is the edit hash the client will request for this photo.
func currentHash(p store.Photo) string {
	if p.EditHash == "" {
		return edit.BaseHash
	}
	return p.EditHash
}
