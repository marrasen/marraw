package api

import (
	"context"
	"log"
	"os"

	"github.com/marrasen/marraw/internal/foldermeta"
	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/sidecar"
	"github.com/marrasen/marraw/internal/store"
)

// writeSidecarFor records a photo's portable intent (rating, flag, edit) in
// the marraw.json of its folder, unless sidecar writes are disabled. An old
// per-photo sidecar already beside the RAW is kept in step too, so the
// Electron app still reads the same until the user removes those; no new
// one is made. Best-effort: sidecar I/O must never fail a catalog write, so
// failures are logged and swallowed.
func (d *Deps) writeSidecarFor(ctx context.Context, p store.Photo) {
	if !d.DB.SidecarWritesEnabled(ctx) {
		return
	}
	if d.Meta != nil {
		fp, _ := d.DB.Fingerprint(ctx, p.ID)
		entry := scan.MetaEntry(p, fp)
		dir, name := scan.MetaLoc(p.Path())
		d.Meta.Change(dir, func(f *foldermeta.File) { f.SetPhoto(name, entry) })
	}
	if _, err := os.Stat(sidecar.PathFor(p.Path())); err != nil {
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
