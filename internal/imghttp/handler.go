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

// ServeHTTP handles GET /img/{id}/{level}?v={cacheKey}&e={editHash}.
//
// v makes URLs content-addressed: a changed file gets a new cache key, hence
// a new URL, so responses are immutable and cacheable forever. A stale v
// yields 409 so the client refetches the photo record.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Token != "" && r.URL.Query().Get("t") != h.Token && r.Header.Get("X-Marraw-Token") != h.Token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad photo id", http.StatusBadRequest)
		return
	}
	level := r.PathValue("level")
	if !pyramid.ValidLevel(level) {
		http.Error(w, "bad level", http.StatusBadRequest)
		return
	}
	editHash := r.URL.Query().Get("e")
	if editHash == "" {
		editHash = edit.BaseHash
	}

	photo, err := h.DB.GetPhoto(r.Context(), id)
	if err != nil {
		http.Error(w, "unknown photo", http.StatusNotFound)
		return
	}
	if v := r.URL.Query().Get("v"); v != "" && v != photo.CacheKey {
		http.Error(w, "stale cache key", http.StatusConflict)
		return
	}

	path := h.Cache.PathFor(photo.CacheKey, level, editHash)
	if _, err := os.Stat(path); err != nil {
		// Only current states are generatable; other hashes exist solely as
		// files PreviewEdit already wrote.
		if editHash != edit.BaseHash && editHash != photo.EditHash {
			http.Error(w, "unknown edit state", http.StatusNotFound)
			return
		}
		if path, err = h.Cache.Ensure(r.Context(), photo, level, editHash, decode.PriorityVisible); err != nil {
			http.Error(w, "render failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "cache read failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, f)
	// mtime ≈ last served, so the janitor evicts cold files first.
	now := time.Now()
	os.Chtimes(path, now, now)
}
