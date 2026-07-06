package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// Photo mirrors a photos row joined with its folder path.
type Photo struct {
	ID          int64
	FolderID    int64
	FolderPath  string
	FileName    string
	FileSize    int64
	MtimeNs     int64
	CacheKey    string
	MetaLoaded  bool
	Width       int
	Height      int
	Orientation int
	Make        string
	Model       string
	ISO         float64
	Shutter     float64
	Aperture    float64
	FocalLen    float64
	TakenAt     int64
	Rating      int
	Flag        int
	EditParams  sql.NullString
	EditHash    string
	// LookGamma is the per-photo adaptive tone lift calibrated against the
	// camera's embedded JPEG during the first RAW render; 0 = not yet known.
	LookGamma float64
	// BaseExpEV is the measured EV equivalent of the base look's
	// auto-brighten lift, used to seed the exposure dial so the camera-mimic
	// compensation is visible in the develop values. Invalid = not measured.
	BaseExpEV sql.NullFloat64
	// UpdatedAt is the unix-millis timestamp of the last intent change
	// (rating, flag, or edit), used to reconcile against portable sidecars by
	// last-writer-wins. Invalid = never touched.
	UpdatedAt sql.NullInt64
}

// Path returns the absolute file path of the photo.
func (p *Photo) Path() string {
	return filepath.Join(p.FolderPath, p.FileName)
}

// CacheKeyFor derives the pyramid cache key from file identity.
// Any change to the file yields a new key, invalidating cached previews.
func CacheKeyFor(absPath string, size, mtimeNs int64) string {
	s := fmt.Sprintf("%s|%d|%d", strings.ToLower(filepath.Clean(absPath)), size, mtimeNs)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:32]
}

func (db *DB) UpsertFolder(ctx context.Context, path string) (int64, error) {
	path = filepath.Clean(path)
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO folders (path) VALUES (?)
		ON CONFLICT(path) DO UPDATE SET path = excluded.path
		RETURNING id`, path).Scan(&id)
	return id, err
}

// FileEntry is one on-disk RAW file observed during a folder scan.
type FileEntry struct {
	Name    string
	Size    int64
	MtimeNs int64
}

// SyncFolder reconciles the photos table with the current directory listing:
// inserts new files, refreshes changed ones (new cache key, metadata reset),
// and deletes rows whose files vanished. Returns the number of photos.
func (db *DB) SyncFolder(ctx context.Context, folderID int64, folderPath string, entries []FileEntry) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		seen[e.Name] = true
		key := CacheKeyFor(filepath.Join(folderPath, e.Name), e.Size, e.MtimeNs)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO photos (folder_id, file_name, file_size, mtime_ns, cache_key)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(folder_id, file_name) DO UPDATE SET
				file_size = excluded.file_size,
				mtime_ns  = excluded.mtime_ns,
				cache_key = excluded.cache_key,
				meta_loaded = CASE WHEN photos.mtime_ns = excluded.mtime_ns
					AND photos.file_size = excluded.file_size
					THEN photos.meta_loaded ELSE 0 END`,
			folderID, e.Name, e.Size, e.MtimeNs, key); err != nil {
			return 0, err
		}
	}

	// Delete rows for vanished files.
	rows, err := tx.QueryContext(ctx, `SELECT id, file_name FROM photos WHERE folder_id = ?`, folderID)
	if err != nil {
		return 0, err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return 0, err
		}
		if !seen[name] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM photos WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE folders SET last_scanned_at = unixepoch() WHERE id = ?`, folderID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(entries), nil
}

const photoCols = `p.id, p.folder_id, f.path, p.file_name, p.file_size, p.mtime_ns, p.cache_key,
	p.meta_loaded, p.width, p.height, p.orientation, p.make, p.model,
	p.iso, p.shutter, p.aperture, p.focal_len, p.taken_at, p.rating, p.flag, p.edit_params, p.edit_hash,
	p.look_gamma, p.base_exp_ev, p.updated_at`

func scanPhoto(row interface{ Scan(...any) error }) (Photo, error) {
	var p Photo
	err := row.Scan(&p.ID, &p.FolderID, &p.FolderPath, &p.FileName, &p.FileSize, &p.MtimeNs, &p.CacheKey,
		&p.MetaLoaded, &p.Width, &p.Height, &p.Orientation, &p.Make, &p.Model,
		&p.ISO, &p.Shutter, &p.Aperture, &p.FocalLen, &p.TakenAt, &p.Rating, &p.Flag, &p.EditParams, &p.EditHash,
		&p.LookGamma, &p.BaseExpEV, &p.UpdatedAt)
	return p, err
}

// SetDimensions corrects the stored pixel dimensions from a ground-truth
// source (the pyramid's full-resolution render).
func (db *DB) SetDimensions(ctx context.Context, id int64, w, h int) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET width = ?, height = ? WHERE id = ?`, w, h, id)
	return err
}

// SetLookGamma persists the calibrated tone lift for a photo.
func (db *DB) SetLookGamma(ctx context.Context, id int64, gamma float64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET look_gamma = ? WHERE id = ?`, gamma, id)
	return err
}

// SetBaseExpEV persists the measured auto-brighten EV for a photo.
func (db *DB) SetBaseExpEV(ctx context.Context, id int64, ev float64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET base_exp_ev = ? WHERE id = ?`, ev, id)
	return err
}

func (db *DB) ListPhotos(ctx context.Context, folderID int64) ([]Photo, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+photoCols+` FROM photos p JOIN folders f ON f.id = p.folder_id
		WHERE p.folder_id = ? ORDER BY p.file_name`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		p, err := scanPhoto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) GetPhoto(ctx context.Context, id int64) (Photo, error) {
	row := db.QueryRowContext(ctx, `
		SELECT `+photoCols+` FROM photos p JOIN folders f ON f.id = p.folder_id
		WHERE p.id = ?`, id)
	return scanPhoto(row)
}

func (db *DB) GetPhotos(ctx context.Context, ids []int64) ([]Photo, error) {
	out := make([]Photo, 0, len(ids))
	for _, id := range ids {
		p, err := db.GetPhoto(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("photo %d: %w", id, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// PhotosNeedingMeta returns photos in the folder whose metadata pass is pending.
func (db *DB) PhotosNeedingMeta(ctx context.Context, folderID int64) ([]Photo, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+photoCols+` FROM photos p JOIN folders f ON f.id = p.folder_id
		WHERE p.folder_id = ? AND p.meta_loaded = 0 ORDER BY p.file_name`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		p, err := scanPhoto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PhotoMeta is the result of the background metadata pass.
type PhotoMeta struct {
	Width, Height, Orientation int
	Make, Model                string
	ISO, Shutter, Aperture     float64
	FocalLen                   float64
	TakenAt                    int64
}

func (db *DB) SetMeta(ctx context.Context, id int64, m PhotoMeta) error {
	_, err := db.ExecContext(ctx, `
		UPDATE photos SET meta_loaded = 1, width = ?, height = ?, orientation = ?,
			make = ?, model = ?, iso = ?, shutter = ?, aperture = ?, focal_len = ?, taken_at = ?
		WHERE id = ?`,
		m.Width, m.Height, m.Orientation, m.Make, m.Model, m.ISO, m.Shutter, m.Aperture, m.FocalLen, m.TakenAt, id)
	return err
}

func int64Placeholders(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.Repeat("?,", len(ids)-1) + "?", args
}

func (db *DB) SetRating(ctx context.Context, ids []int64, rating int, updatedAtMs int64) error {
	if len(ids) == 0 {
		return nil
	}
	ph, args := int64Placeholders(ids)
	_, err := db.ExecContext(ctx, `UPDATE photos SET rating = ?, updated_at = ? WHERE id IN (`+ph+`)`,
		append([]any{rating, updatedAtMs}, args...)...)
	return err
}

func (db *DB) SetFlag(ctx context.Context, ids []int64, flag int, updatedAtMs int64) error {
	if len(ids) == 0 {
		return nil
	}
	ph, args := int64Placeholders(ids)
	_, err := db.ExecContext(ctx, `UPDATE photos SET flag = ?, updated_at = ? WHERE id IN (`+ph+`)`,
		append([]any{flag, updatedAtMs}, args...)...)
	return err
}

// PhotoFolders maps each given photo id to its folder id.
func (db *DB) PhotoFolders(ctx context.Context, ids []int64) (map[int64]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args := int64Placeholders(ids)
	rows, err := db.QueryContext(ctx, `SELECT id, folder_id FROM photos WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int64, len(ids))
	for rows.Next() {
		var id, folderID int64
		if err := rows.Scan(&id, &folderID); err != nil {
			return nil, err
		}
		out[id] = folderID
	}
	return out, rows.Err()
}

// DeletePhotos removes photo rows (the files are the caller's problem).
func (db *DB) DeletePhotos(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	ph, args := int64Placeholders(ids)
	_, err := db.ExecContext(ctx, `DELETE FROM photos WHERE id IN (`+ph+`)`, args...)
	return err
}

// SetEdit stores the edit params JSON (nil clears) and its hash.
func (db *DB) SetEdit(ctx context.Context, id int64, paramsJSON *string, hash string, updatedAtMs int64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET edit_params = ?, edit_hash = ?, updated_at = ? WHERE id = ?`,
		paramsJSON, hash, updatedAtMs, id)
	return err
}

// ApplyImportedEdit applies portable intent read from a sidecar to a photo,
// but only when the incoming timestamp is newer than the stored one
// (last-writer-wins). A NULL stored timestamp — a freshly scanned row, or one
// that predates sidecars — always loses, so a copied-in folder adopts its
// sidecars. Returns whether the row was updated.
func (db *DB) ApplyImportedEdit(ctx context.Context, folderID int64, fileName string,
	rating, flag int, editJSON *string, editHash string, updatedAtMs int64) (bool, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE photos SET rating = ?, flag = ?, edit_params = ?, edit_hash = ?, updated_at = ?
		WHERE folder_id = ? AND file_name = ?
		  AND (updated_at IS NULL OR updated_at < ?)`,
		rating, flag, editJSON, editHash, updatedAtMs,
		folderID, fileName, updatedAtMs)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
