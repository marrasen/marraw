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

type Handler struct {
	DB    *store.DB
	Cache *pyramid.Cache
	Token string // empty disables the check (dev mode)
}

// photoFor authorizes the request and resolves the photo record and edit
// hash shared by both endpoints. On failure it writes the error response and
// returns ok=false.
//
// The v query param makes URLs content-addressed: a changed file gets a new
// cache key, hence a new URL, so responses are immutable and cacheable
// forever. A stale v yields 409 so the client refetches the photo record.
func (h *Handler) photoFor(w http.ResponseWriter, r *http.Request) (photo store.Photo, editHash string, ok bool) {
	if h.Token != "" && r.URL.Query().Get("t") != h.Token && r.Header.Get("X-Marraw-Token") != h.Token {
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
	photo, err = h.DB.GetPhoto(r.Context(), id)
	if err != nil {
		http.Error(w, "unknown photo", http.StatusNotFound)
		return
	}
	if v := r.URL.Query().Get("v"); v != "" && v != photo.CacheKey {
		http.Error(w, "stale cache key", http.StatusConflict)
		return
	}
	return photo, editHash, true
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
	photo, editHash, ok := h.photoFor(w, r)
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
		if r.URL.Query().Get("stale") != "" {
			if alt := h.Cache.NewestLevel(photo.CacheKey, level); alt != "" {
				h.serveFileHeaders(w, r, alt, "no-store")
				return
			}
			// No rendition of this photo+level exists at all (fresh import):
			// fall through to the ordinary render path.
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
	photo, editHash, ok := h.photoFor(w, r)
	if !ok {
		return
	}
	path := h.Cache.PathForTile(photo.CacheKey, tx, ty, editHash)
	if _, err := os.Stat(path); err != nil {
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
