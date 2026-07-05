package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
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
	folderID, count, err := l.deps.Scanner.OpenFolder(ctx, path)
	if err != nil {
		return nil, aprot.ErrInvalidParams(err.Error())
	}
	aprot.TriggerRefresh(ctx, photosKey(folderID))
	l.startFolderJobs(ctx, folderID, path)
	return &FolderInfo{FolderID: folderID, Path: path, PhotoCount: count}, nil
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
	if err := l.deps.DB.SetRating(ctx, ids, rating); err != nil {
		return err
	}
	patches := make([]PhotoPatch, len(ids))
	for i, id := range ids {
		patches[i] = PhotoPatch{ID: id, Rating: &rating}
	}
	l.deps.PatchPhotos(ctx, patches)
	return nil
}

// SetFlag sets the cull flag of the given photos.
func (l *Library) SetFlag(ctx context.Context, ids []int64, flag Flag) error {
	if err := l.deps.DB.SetFlag(ctx, ids, FlagToInt(flag)); err != nil {
		return err
	}
	patches := make([]PhotoPatch, len(ids))
	for i, id := range ids {
		patches[i] = PhotoPatch{ID: id, Flag: &flag}
	}
	l.deps.PatchPhotos(ctx, patches)
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
