package imghttp

import (
	"archive/zip"
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marrasen/marraw/internal/store"
)

// Downloads serves developed JPEGs to a share visitor: GET /dl/{id} for one
// photo, GET /dl.zip?ids=1,2,3 for a selection.
//
// These are full renders through the develop pipeline, not pyramid cache
// files, so they are expensive in a way the /img endpoints are not — a
// full-resolution frame peaks near a gigabyte while it is being built. Hence
// the semaphore: the export path has its own admission budget for exactly this
// reason, and a visitor tapping "download all" on a phone must not be able to
// walk the daemon out of memory.
type Downloads struct {
	DB *store.DB
	// Authorize checks the credential and reports what it may see. Nil
	// disables the check (dev mode).
	Authorize Authorizer
	// Render produces the finished JPEG for one photo, to the credential's
	// own settings. Injected so this package keeps out of the export/libraw
	// dependency chain, and so the caller owns the render vocabulary.
	Render func(ctx context.Context, photoID int64, spec DownloadSpec) ([]byte, error)

	sem chan struct{}
}

// maxZipPhotos bounds one archive request. Well above any realistic "send me
// these" selection, and low enough that a crafted id list cannot queue hours
// of rendering.
const maxZipPhotos = 200

// NewDownloads builds the handler with a concurrency limit. Two at a time: one
// render saturates several cores already, and a visitor on a phone gains
// nothing from a third running in parallel with their first two.
func NewDownloads(db *store.DB, auth Authorizer, render func(context.Context, int64, DownloadSpec) ([]byte, error)) *Downloads {
	return &Downloads{DB: db, Authorize: auth, Render: render, sem: make(chan struct{}, 2)}
}

// acquire takes a render slot, or gives up if the visitor navigates away
// first.
func (h *Downloads) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Downloads) release() { <-h.sem }

// allowed resolves the credential and confirms it may download at all. A share
// minted without the downloads capability can view and cull but not take
// copies away, which is the point of the capability.
func (h *Downloads) allowed(w http.ResponseWriter, r *http.Request) (Access, bool) {
	acc, ok := access(h.Authorize, r)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return Access{}, false
	}
	if acc.FolderID != 0 && !acc.Downloads {
		http.Error(w, "downloads are not enabled for this link", http.StatusForbidden)
		return Access{}, false
	}
	return acc, true
}

// photosFor resolves and scopes an id list, preserving the caller's order so a
// selection arrives in the order it was made.
//
// It distinguishes the two ways an id can fail to resolve, because they mean
// different things. An id belonging to another folder is a request a confined
// caller had no business making, and the whole request is refused — a shared
// link must not be able to name the rest of the library and quietly receive
// the part of its list that happened to be in scope. An id that resolves to
// nothing is just a race with the owner deleting a frame, and is skipped, so
// "download all" still works on a selection made a moment ago.
func (h *Downloads) photosFor(ctx context.Context, acc Access, ids []int64) ([]store.Photo, error) {
	folders, err := h.DB.PhotoFolders(ctx, ids)
	if err != nil {
		return nil, err
	}
	if acc.FolderID != 0 {
		for _, id := range ids {
			if f, ok := folders[id]; ok && f != acc.FolderID {
				return nil, fmt.Errorf("photo %d is not in this album", id)
			}
		}
	}
	live := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := folders[id]; ok {
			live = append(live, id)
		}
	}
	if len(live) == 0 {
		return nil, fmt.Errorf("no photos")
	}
	return h.DB.GetPhotos(ctx, live)
}

// jpegName is the download's file name: the RAW's name with a .jpg extension.
func jpegName(p store.Photo) string {
	base := strings.TrimSuffix(p.FileName, filepath.Ext(p.FileName))
	return base + ".jpg"
}

// ServeHTTP handles GET /dl/{id}?t=token — one developed JPEG.
func (h *Downloads) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	acc, ok := h.allowed(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad photo id", http.StatusBadRequest)
		return
	}
	photos, err := h.photosFor(r.Context(), acc, []int64{id})
	if err != nil {
		http.Error(w, "unknown photo", http.StatusNotFound)
		return
	}
	if err := h.acquire(r.Context()); err != nil {
		return // visitor navigated away while queued
	}
	defer h.release()
	data, err := h.Render(r.Context(), photos[0].ID, acc.Download)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		http.Error(w, "render failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	// A download, not a render of the current edit state: no-store, because
	// the pixels change whenever the owner develops the shot further.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", contentDisposition(jpegName(photos[0])))
	w.Write(data)
}

// ServeZip handles GET /dl.zip?ids=1,2,3&t=token, streaming the archive as it
// renders. Photos are rendered one at a time and written straight into the
// stream: holding a whole selection of full-resolution frames in memory to
// build the archive first would be the very thing the budget exists to stop.
func (h *Downloads) ServeZip(w http.ResponseWriter, r *http.Request) {
	acc, ok := h.allowed(w, r)
	if !ok {
		return
	}
	var ids []int64
	for _, s := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			http.Error(w, "bad photo id", http.StatusBadRequest)
			return
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		http.Error(w, "no photos requested", http.StatusBadRequest)
		return
	}
	if len(ids) > maxZipPhotos {
		http.Error(w, fmt.Sprintf("too many photos (max %d)", maxZipPhotos), http.StatusBadRequest)
		return
	}
	photos, err := h.photosFor(r.Context(), acc, ids)
	if err != nil {
		http.Error(w, "unknown photos", http.StatusNotFound)
		return
	}

	// Name the archive after the shoot, so what lands in the visitor's
	// downloads folder says which album it came from.
	name := "photos.zip"
	if dir := photos[0].FolderPath; dir != "" {
		name = filepath.Base(dir) + ".zip"
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", contentDisposition(name))

	zw := zip.NewWriter(w)
	for _, p := range photos {
		if err := h.acquire(r.Context()); err != nil {
			return
		}
		data, err := h.Render(r.Context(), p.ID, acc.Download)
		h.release()
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			// The response is already streaming, so the status line is long
			// gone: log it and skip the frame rather than pretending the
			// archive failed. A short archive is recoverable by the visitor;
			// a corrupt one is not.
			log.Printf("download: render %s: %v", p.FileName, err)
			continue
		}
		// Store, not Deflate: JPEG does not compress, and deflating it costs
		// the visitor time for nothing.
		f, err := zw.CreateHeader(&zip.FileHeader{Name: jpegName(p), Method: zip.Store})
		if err != nil {
			return
		}
		if _, err := f.Write(data); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("download: close archive: %v", err)
	}
}

// contentDisposition builds the header, quoting the name and also spelling it
// UTF-8 so non-ASCII file names survive the trip.
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", ascii, urlEscape(name))
}

// urlEscape percent-encodes for RFC 5987, which allows a narrower set than a
// URL path does.
func urlEscape(s string) string {
	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$&+-.^_`|~"
	var b strings.Builder
	for _, c := range []byte(s) {
		if strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
