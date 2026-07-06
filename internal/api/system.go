package api

import (
	"context"
	"path/filepath"

	"github.com/marrasen/aprot"
)

// System exposes app-level maintenance: preview-cache inspection, clearing,
// and relocation.
type System struct {
	deps *Deps
}

const cacheInfoKey = "cacheInfo"

// customPreviewsSubdir is created inside a user-picked folder so the cache is
// self-contained and safe to wipe without touching the user's other files.
const customPreviewsSubdir = "marraw-previews"

// CacheInfo describes the preview cache's location and disk usage.
type CacheInfo struct {
	Dir      string `json:"dir"`
	Bytes    int64  `json:"bytes"`
	Files    int64  `json:"files"`
	IsCustom bool   `json:"isCustom"` // false when the default location is in use
}

// GetCacheInfo returns the cache location and current disk usage. Subscription
// query: ClearCache and SetCacheDir push an update. The size is measured by
// walking the cache, so it is fetched on demand rather than folded into the
// always-live app settings.
func (s *System) GetCacheInfo(ctx context.Context) (*CacheInfo, error) {
	aprot.RegisterRefreshTrigger(ctx, cacheInfoKey)
	return s.cacheInfo(ctx), nil
}

func (s *System) cacheInfo(ctx context.Context) *CacheInfo {
	bytes, files := s.deps.Cache.Stat()
	return &CacheInfo{
		Dir:      s.deps.Cache.Dir(),
		Bytes:    bytes,
		Files:    files,
		IsCustom: s.deps.DB.CacheDir(ctx) != "",
	}
}

// ClearCache deletes every cached rendition; they regenerate on demand.
func (s *System) ClearCache(ctx context.Context) (*CacheInfo, error) {
	if err := s.deps.Cache.Clear(); err != nil {
		return nil, err
	}
	aprot.TriggerRefresh(ctx, cacheInfoKey)
	return s.cacheInfo(ctx), nil
}

// SetCacheDir relocates the preview cache. An empty path restores the default
// location; otherwise the cache moves into "<path>/marraw-previews". The
// previous cache is wiped (its previews are regenerable), and the change takes
// effect immediately as well as persisting for the next launch.
func (s *System) SetCacheDir(ctx context.Context, path string) (*CacheInfo, error) {
	target := s.deps.DefaultCacheDir
	persist := ""
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, aprot.ErrInvalidParams("invalid cache folder: " + err.Error())
		}
		target = filepath.Join(abs, customPreviewsSubdir)
		persist = target
	}
	if err := s.deps.Cache.Relocate(target); err != nil {
		return nil, aprot.ErrInvalidParams("cannot use that cache folder: " + err.Error())
	}
	if err := s.deps.DB.SetCacheDirSetting(ctx, persist); err != nil {
		return nil, err
	}
	aprot.TriggerRefresh(ctx, cacheInfoKey)
	return s.cacheInfo(ctx), nil
}
