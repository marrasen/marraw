// Package api defines the aprot handler surface of marrawd.
package api

import (
	"context"
	"fmt"
	"sync"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/store"
)

// Deps carries the backend services handlers operate on. For TypeScript
// generation a zero-value Deps is fine — handlers are never invoked.
type Deps struct {
	DB      *store.DB
	Pool    *decode.Pool
	Cache   *pyramid.Cache
	Handles *decode.HandleCache
	Scanner *scan.Scanner
	// Janitor bounds the preview cache; Settings adjusts its cap live.
	Janitor *pyramid.Janitor
	// DefaultCacheDir is the built-in preview-cache location (under the app
	// data dir); System.SetCacheDir("") restores it.
	DefaultCacheDir string

	mu     sync.RWMutex
	server *aprot.Server

	// jobMu guards the single folder-jobs slot: opening a folder cancels the
	// previous folder's metadata/pre-render passes.
	jobMu            sync.Mutex
	folderJobsCancel context.CancelFunc
}

// SetServer wires the aprot server in after construction (the registry must
// exist before the server does).
func (d *Deps) SetServer(s *aprot.Server) {
	d.mu.Lock()
	d.server = s
	d.mu.Unlock()
}

// TriggerRefresh fires subscription refresh keys from background goroutines.
func (d *Deps) TriggerRefresh(keys ...string) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.TriggerRefresh(keys...)
	}
}

// PatchPhotos pushes granular photo patches to the folder-list subscribers —
// O(patch) on the wire instead of a full list refresh. Subscribers without a
// patch reducer fall back to a full refresh automatically.
func (d *Deps) PatchPhotos(ctx context.Context, patches []PhotoPatch) {
	if len(patches) == 0 {
		return
	}
	ids := make([]int64, len(patches))
	for i, p := range patches {
		ids[i] = p.ID
	}
	folders, err := d.DB.PhotoFolders(ctx, ids)
	if err != nil {
		return
	}
	byFolder := map[int64][]PhotoPatch{}
	for _, p := range patches {
		f := folders[p.ID]
		byFolder[f] = append(byFolder[f], p)
	}
	for f, ps := range byFolder {
		d.patchFolderPhotos(f, ps)
	}
}

// patchFolderPhotos pushes patches to one folder's subscription key.
func (d *Deps) patchFolderPhotos(folderID int64, patches []PhotoPatch) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.PatchSubscription(PhotoPatchEvent{Patches: patches}, photosKey(folderID))
	}
}

func photosKey(folderID int64) string { return fmt.Sprintf("photos:%d", folderID) }
func editKey(photoID int64) string    { return fmt.Sprintf("edit:%d", photoID) }

// NewRegistry builds the aprot registry with all marraw handler groups.
func NewRegistry(deps *Deps) (*aprot.Registry, *Library, *Edits, *Export) {
	registry := aprot.NewRegistry()
	registry.SetValidator(aprot.NewPlaygroundValidator())

	library := &Library{deps: deps}
	edits := &Edits{deps: deps}
	export := &Export{deps: deps}
	settings := &Settings{deps: deps}
	registry.Register(library)
	registry.Register(edits)
	registry.Register(export)
	registry.Register(&System{deps: deps})
	registry.Register(settings)

	registry.RegisterEnumFor(library, FlagValues())
	registry.RegisterEnumFor(settings, ThemeValues())
	registry.RegisterEnumFor(edits, edit.WBModeValues())
	registry.RegisterEnumFor(edits, edit.DemosaicValues())
	registry.RegisterEnumFor(export, ExportFormatValues())
	registry.RegisterEnumFor(export, ColorSpaceValues())
	// PhotoPatchEvent is no longer broadcast as a push event — it is the
	// payload of subscription patches — but registering it keeps the
	// TypeScript types generated for the client-side patch reducer.
	registry.RegisterPushEventFor(library, PhotoPatchEvent{})

	tasks.EnableWithMeta[TaskMeta](registry)
	return registry, library, edits, export
}
