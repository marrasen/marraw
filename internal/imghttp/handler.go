// Package imghttp serves pyramid cache images over plain HTTP so the client
// can use <img> tags with free browser caching. aprot stays JSON-only.
package imghttp

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// Access is what a credential is allowed to see. The owner's own tokens carry
// the zero value, which is unrestricted; a share link is confined.
type Access struct {
	// FolderID confines the credential to one folder's photos. Zero is the
	// whole library. Photo IDs are sequential integers anyone can count
	// through, so this check is the only thing standing between a shared
	// folder and the rest of the library.
	FolderID int64
	// BaseEditOnly serves the unedited rendition whatever edit state was
	// asked for: a share made without the "show my edits" capability.
	BaseEditOnly bool
	// Downloads permits taking full renders away (see download.go). Only
	// consulted for confined credentials — the owner may always download.
	Downloads bool
	// Download is how those renders are produced: what the owner's chosen
	// export preset resolved to when the share was minted. The zero value
	// means the endpoint's own defaults.
	Download DownloadSpec
}

// DownloadSpec is the render settings for a share's downloads. Plain fields
// rather than the export package's request type: this package serves cache
// files and knows nothing about developing, and the caller that supplies the
// render func is the one that owns that vocabulary.
type DownloadSpec struct {
	// LongEdge resizes the output; 0 is full resolution.
	LongEdge int
	// JpegQuality is 1-100; 0 lets the renderer choose.
	JpegQuality    int
	ColorSpace     string
	SharpenTarget  string
	SharpenAmount  string
	ExifMode       string
	RemoveLocation bool
	WatermarkID    string
}

// Authorizer resolves the ?t=/X-Marraw-Token credential to what it may see.
// A func rather than a token string so regenerating a token, or revoking a
// share, takes effect without re-wiring handlers.
type Authorizer func(tok string) (Access, bool)

type Handler struct {
	DB    *store.DB
	Cache *pyramid.Cache
	// Authorize checks the request credential. Nil disables the check and
	// grants unrestricted access (dev mode).
	Authorize Authorizer
}

// access resolves the request's credential, accepting either the ?t= query
// param or the X-Marraw-Token header.
func access(auth Authorizer, r *http.Request) (Access, bool) {
	if auth == nil {
		return Access{}, true
	}
	if a, ok := auth(r.URL.Query().Get("t")); ok {
		return a, true
	}
	return auth(r.Header.Get("X-Marraw-Token"))
}

// authorized reports whether the request carries a credential with
// unrestricted access — what the endpoints that are not photo-scoped need.
func authorized(auth Authorizer, r *http.Request) bool {
	a, ok := access(auth, r)
	return ok && a.FolderID == 0
}

// photoFor authorizes the request and resolves the photo record and edit
// hash shared by both endpoints. On failure it writes the error response and
// returns ok=false.
//
// The v query param makes URLs content-addressed: a changed file gets a new
// cache key, hence a new URL, so responses are immutable and cacheable
// forever. A stale v yields 409 so the client refetches the photo record.
func (h *Handler) photoFor(w http.ResponseWriter, r *http.Request) (photo store.Photo, editHash string, acc Access, ok bool) {
	acc, allowed := access(h.Authorize, r)
	if !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad photo id", http.StatusBadRequest)
		return
	}
	editHash = r.URL.Query().Get("e")
	if editHash == "" {
		editHash = edit.BaseHash
	}
	// A share without the edits capability asks for the current hash like any
	// client — the photo record it read carries one — and is answered with the
	// base rendition regardless.
	if acc.BaseEditOnly {
		editHash = edit.BaseHash
	}
	photo, err = h.DB.GetPhoto(r.Context(), id)
	if err != nil {
		http.Error(w, "unknown photo", http.StatusNotFound)
		return
	}
	if acc.FolderID != 0 && photo.FolderID != acc.FolderID {
		// Same status as an unknown photo would give a confined caller no
		// information either way; 404 keeps the id space opaque.
		http.Error(w, "unknown photo", http.StatusNotFound)
		return
	}
	if v := r.URL.Query().Get("v"); v != "" && v != photo.CacheKey {
		http.Error(w, "stale cache key", http.StatusConflict)
		return
	}
	return photo, editHash, acc, true
}

// generatable reports whether the edit state can be rendered on demand: only
// current states are; other hashes exist solely as files PreviewEdit already
// wrote.
func generatable(photo store.Photo, editHash string) bool {
	return editHash == edit.BaseHash || editHash == photo.EditHash
}

// ServeHTTP handles GET /img/{id}/{level}?v={cacheKey}&e={editHash}.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	level := r.PathValue("level")
	if !pyramid.ValidLevel(level) {
		http.Error(w, "bad level", http.StatusBadRequest)
		return
	}
	photo, editHash, acc, ok := h.photoFor(w, r)
	if !ok {
		return
	}
	path := h.Cache.PathFor(photo.CacheKey, level, editHash)
	if _, err := os.Stat(path); err != nil {
		// stale=1 callers (the loupe's low-res bridge) prefer the RIGHT PHOTO
		// immediately over the right edit state: when the exact rendition is
		// missing (a superseded commit settle never wrote it, the janitor
		// evicted it), serve the photo's freshest rendition of this level
		// under any edit hash instead of blocking on a RAW decode — the sharp
		// layer revalidates against the exact hash on top. Served no-store:
		// the URL names the exact state, and an immutably-cached stale body
		// would impersonate it forever.
		//
		// Never for a base-only share: "freshest rendition under any edit
		// hash" is exactly what that share is not allowed to see.
		if r.URL.Query().Get("stale") != "" && !acc.BaseEditOnly {
			if alt := h.Cache.NewestLevel(photo.CacheKey, level); alt != "" {
				h.serveFileHeaders(w, r, alt, "no-store")
				return
			}
			// No rendition of this photo+level exists at all (fresh import):
			// fall through to the ordinary render path.
		}
		// fast callers (the cull/loupe cold-frame fallback) want pixels NOW,
		// without a RAW decode: the cached file, a downscale of an existing
		// 2048, or the camera's embedded JPEG. A provisional (thumb-derived,
		// base-look) body must never enter the browser's immutable cache —
		// the content-addressed URL's promise is the real render, which will
		// exist later under this exact URL. 404 means "nothing derivable"
		// (no usable embedded JPEG); the client keeps its previous frame and
		// the dwell-kicked render fills in, exactly as before fast existed.
		if r.URL.Query().Get("fast") != "" {
			if p, provisional, err := h.Cache.EnsureFast(r.Context(), photo, level, editHash); err == nil {
				if provisional {
					w.Header().Set("X-Marraw-Provisional", "1")
					h.serveFileHeaders(w, r, p, "no-store")
				} else {
					h.serveFile(w, r, p)
				}
				return
			}
			http.Error(w, "not derivable", http.StatusNotFound)
			return
		}
		// cacheOnly callers (the fit loupe) want the pre-rendered rendition or
		// nothing — never an on-demand RAW decode. Browsing then paints the
		// warm low-res underlay instead of blocking on a full render, and the
		// background pre-render pass is what fills the cache. A 404 here is the
		// expected "not warm yet" signal, not an error.
		if r.URL.Query().Get("cacheOnly") != "" {
			http.Error(w, "not cached", http.StatusNotFound)
			return
		}
		if !generatable(photo, editHash) {
			http.Error(w, "unknown edit state", http.StatusNotFound)
			return
		}
		if path, err = h.Cache.Ensure(r.Context(), photo, level, editHash, decode.PriorityVisible); err != nil {
			// The client walked away (navigation aborts the fetch, which
			// cancels the render): nobody reads the response, don't log a 500.
			if r.Context().Err() != nil {
				return
			}
			http.Error(w, "render failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	h.serveFile(w, r, path)
}

// ServeTile handles GET /img/{id}/tile/{tx}/{ty}?v={cacheKey}&e={editHash}:
// one full-resolution tile of the pyramid.TileSize grid. A miss renders the
// photo's whole tile set in one decode; coordinates outside the image yield
// 404.
func (h *Handler) ServeTile(w http.ResponseWriter, r *http.Request) {
	tx, errX := strconv.Atoi(r.PathValue("tx"))
	ty, errY := strconv.Atoi(r.PathValue("ty"))
	if errX != nil || errY != nil {
		http.Error(w, "bad tile coordinates", http.StatusBadRequest)
		return
	}
	photo, editHash, _, ok := h.photoFor(w, r)
	if !ok {
		return
	}
	path := h.Cache.PathForTile(photo.CacheKey, tx, ty, editHash)
	if _, err := os.Stat(path); err != nil {
		// cacheOnly: the fit loupe probing whether the tile set is warm —
		// 404 means "not rendered", never a multi-second on-demand render.
		if r.URL.Query().Get("cacheOnly") != "" {
			http.Error(w, "not cached", http.StatusNotFound)
			return
		}
		if !generatable(photo, editHash) {
			http.Error(w, "unknown edit state", http.StatusNotFound)
			return
		}
		if path, err = h.Cache.EnsureTile(r.Context(), photo, tx, ty, editHash, decode.PriorityVisible); err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "tile outside image", http.StatusNotFound)
				return
			}
			// Aborted request ⇒ cancelled render; the response is unread.
			if r.Context().Err() != nil {
				return
			}
			http.Error(w, "render failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	h.serveFile(w, r, path)
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, path string) {
	h.serveFileHeaders(w, r, path, "private, max-age=31536000, immutable")
}

func (h *Handler) serveFileHeaders(w http.ResponseWriter, r *http.Request, path, cacheControl string) {
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "cache read failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	// Wide-open CORS so the client can fetch() pixels for the histogram —
	// including from Electron's file:// origin ("null"). Access control is
	// the token, which rides in the URL, not the origin.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, "", time.Time{}, f)
	// mtime ≈ last served, so the janitor evicts cold files first.
	now := time.Now()
	os.Chtimes(path, now, now)
}
