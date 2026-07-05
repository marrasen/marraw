// Package api defines the aprot handler surface of marrawd.
package api

import (
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

	mu     sync.RWMutex
	server *aprot.Server
}

// SetServer wires the aprot server in after construction (the registry must
// exist before the server does).
func (d *Deps) SetServer(s *aprot.Server) {
	d.mu.Lock()
	d.server = s
	d.mu.Unlock()
}

// Broadcast pushes an event to all connected clients (no-op before SetServer).
func (d *Deps) Broadcast(event any) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.Broadcast(event)
	}
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

func photosKey(folderID int64) string { return fmt.Sprintf("photos:%d", folderID) }
func editKey(photoID int64) string    { return fmt.Sprintf("edit:%d", photoID) }

// NewRegistry builds the aprot registry with all marraw handler groups.
func NewRegistry(deps *Deps) (*aprot.Registry, *Library, *Edits, *Export) {
	registry := aprot.NewRegistry()
	registry.SetValidator(aprot.NewPlaygroundValidator())

	library := &Library{deps: deps}
	edits := &Edits{deps: deps}
	export := &Export{deps: deps}
	registry.Register(library)
	registry.Register(edits)
	registry.Register(export)

	registry.RegisterEnumFor(library, FlagValues())
	registry.RegisterEnumFor(edits, edit.WBModeValues())
	registry.RegisterEnumFor(export, ExportFormatValues())
	registry.RegisterPushEventFor(library, PhotoPatchEvent{})

	tasks.Enable(registry)
	return registry, library, edits, export
}
