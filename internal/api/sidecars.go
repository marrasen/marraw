package api

import (
	"context"
	"log"

	"github.com/marrasen/marraw/internal/sidecar"
	"github.com/marrasen/marraw/internal/store"
)

// writeSidecarFor mirrors a photo's portable intent (rating, flag, edit) to
// disk next to its RAW, unless sidecar writes are disabled. Best-effort:
// sidecar I/O must never fail a catalog write, so failures are logged and
// swallowed.
func (d *Deps) writeSidecarFor(ctx context.Context, p store.Photo) {
	if !d.DB.SidecarWritesEnabled(ctx) {
		return
	}
	editJSON := ""
	if p.EditParams.Valid {
		editJSON = p.EditParams.String
	}
	f := sidecar.Build(p.FileName, p.FileSize, p.Rating, p.Flag, editJSON, sidecarUpdatedMs(p))
	if err := sidecar.Write(p.Path(), f); err != nil {
		log.Printf("api: write sidecar %s: %v", p.FileName, err)
	}
}

// writeSidecars refreshes the sidecars of many photos after a bulk mutation.
// The fetch and writes are detached from the request context so a client that
// navigates away mid-batch still leaves consistent sidecars on disk.
func (d *Deps) writeSidecars(ctx context.Context, ids []int64) {
	ctx = context.WithoutCancel(ctx)
	if !d.DB.SidecarWritesEnabled(ctx) {
		return
	}
	photos, err := d.DB.GetPhotos(ctx, ids)
	if err != nil {
		return
	}
	for _, p := range photos {
		d.writeSidecarFor(ctx, p)
	}
}

// sidecarUpdatedMs is the timestamp to stamp into a sidecar. Every intent
// change now sets photos.updated_at, so a just-written row is Valid; the
// fallback only matters for rows that predate the column.
func sidecarUpdatedMs(p store.Photo) int64 {
	if p.UpdatedAt.Valid {
		return p.UpdatedAt.Int64
	}
	return 0
}
