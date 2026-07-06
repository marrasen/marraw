package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetSetting returns the value stored under key, or "" if unset.
func (db *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetSetting stores value under key, replacing any previous value.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

const settingSidecarWrites = "sidecarWrites"

// SidecarWritesEnabled reports whether portable edit sidecars should be written
// next to RAWs. Enabled by default; only an explicit "false" turns it off.
// Importing existing sidecars is unaffected by this toggle — a folder authored
// elsewhere always loads.
func (db *DB) SidecarWritesEnabled(ctx context.Context) bool {
	v, err := db.GetSetting(ctx, settingSidecarWrites)
	if err != nil {
		return true
	}
	return v != "false"
}

// SetSidecarWrites persists the sidecar-writes toggle.
func (db *DB) SetSidecarWrites(ctx context.Context, enabled bool) error {
	v := "true"
	if !enabled {
		v = "false"
	}
	return db.SetSetting(ctx, settingSidecarWrites, v)
}

const settingCacheDir = "cacheDir"

// CacheDir returns the user's custom preview-cache directory, or "" when the
// default location (under the app data dir) is in use.
func (db *DB) CacheDir(ctx context.Context) string {
	v, _ := db.GetSetting(ctx, settingCacheDir)
	return v
}

// SetCacheDirSetting persists the custom cache directory ("" restores default).
func (db *DB) SetCacheDirSetting(ctx context.Context, dir string) error {
	return db.SetSetting(ctx, settingCacheDir, dir)
}
