package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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
	// Lens is the lens model string, "" = unknown.
	Lens string
	// GPS position in signed decimal degrees (S/W negative), altitude in
	// meters. Invalid = the RAW carried no fix (or no altitude).
	GPSLat sql.NullFloat64
	GPSLon sql.NullFloat64
	GPSAlt sql.NullFloat64
	// Sharpness is the Laplacian-variance focus score of the embedded thumb
	// (pyramid.SharpnessScore), measured by the calibrate pass and rendered
	// as the grid's soft-photo badge. Invalid = not yet measured.
	Sharpness sql.NullFloat64
	// SubjectSharpness is the focus score restricted to the AI subject matte
	// (pyramid.SubjectSharpnessScore), measured only once a matte exists so
	// "background sharp, subject soft" frames still badge. Invalid = not yet
	// measured; -1 = measured but unscoreable (no meaningful subject).
	SubjectSharpness sql.NullFloat64
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

// FolderPhotoCount returns the catalogued photo count for a folder path, and
// whether the folder has ever been scanned. Comparing paths case-insensitively
// matches how the rest of the app treats Windows paths.
func (db *DB) FolderPhotoCount(ctx context.Context, path string) (int, bool, error) {
	path = filepath.Clean(path)
	// The folder lookup is separate from the count: an aggregate over a
	// non-existent folder still yields one row holding 0, which would report an
	// unscanned folder as an empty one.
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM folders WHERE path = ? COLLATE NOCASE`, path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM photos WHERE folder_id = ?`, id).Scan(&n); err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// FolderEarliest is one folder row's earliest known capture time.
type FolderEarliest struct {
	Path     string
	Earliest int64 // unix seconds
}

// EarliestTakenByFolder returns, for every scanned folder, the earliest
// non-zero taken_at among its photos. Folders whose photos all lack a capture
// time (taken_at = 0, not yet backfilled or absent from EXIF) are omitted.
// One aggregate over the whole folders table: callers match paths to shoots
// in Go, for the same reason RenameFolderPaths does its prefix math there.
func (db *DB) EarliestTakenByFolder(ctx context.Context) ([]FolderEarliest, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT f.path, MIN(p.taken_at) FROM folders f
		JOIN photos p ON p.folder_id = f.id
		WHERE p.taken_at > 0 GROUP BY f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderEarliest
	for rows.Next() {
		var fe FolderEarliest
		if err := rows.Scan(&fe.Path, &fe.Earliest); err != nil {
			return nil, err
		}
		out = append(out, fe)
	}
	return out, rows.Err()
}

// RenameFolderPaths moves a folder row (and any rows for folders nested
// beneath it) to a new path after an on-disk rename. Photo rows are keyed by
// folder id and are untouched; their cache keys are recomputed on the next
// SyncFolder.
func (db *DB) RenameFolderPaths(ctx context.Context, oldPath, newPath string) error {
	oldPath = filepath.Clean(oldPath)
	newPath = filepath.Clean(newPath)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE folders SET path = ? WHERE path = ? COLLATE NOCASE`, newPath, oldPath); err != nil {
		return err
	}
	// Descendant rows: rewrite the prefix in Go (SQL substr counts
	// characters, not bytes — unsafe for the byte-length prefix math).
	rows, err := tx.QueryContext(ctx, `SELECT id, path FROM folders`)
	if err != nil {
		return err
	}
	prefix := oldPath + string(filepath.Separator)
	type move struct {
		id   int64
		path string
	}
	var moves []move
	for rows.Next() {
		var m move
		if err := rows.Scan(&m.id, &m.path); err != nil {
			rows.Close()
			return err
		}
		if len(m.path) > len(prefix) && strings.EqualFold(m.path[:len(prefix)], prefix) {
			moves = append(moves, move{id: m.id, path: newPath + string(filepath.Separator) + m.path[len(prefix):]})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range moves {
		if _, err := tx.ExecContext(ctx, `UPDATE folders SET path = ? WHERE id = ?`, m.path, m.id); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	p.look_gamma, p.base_exp_ev, p.updated_at, p.lens, p.gps_lat, p.gps_lon, p.gps_alt, p.sharpness,
	p.subject_sharpness`

func scanPhoto(row interface{ Scan(...any) error }) (Photo, error) {
	var p Photo
	err := row.Scan(&p.ID, &p.FolderID, &p.FolderPath, &p.FileName, &p.FileSize, &p.MtimeNs, &p.CacheKey,
		&p.MetaLoaded, &p.Width, &p.Height, &p.Orientation, &p.Make, &p.Model,
		&p.ISO, &p.Shutter, &p.Aperture, &p.FocalLen, &p.TakenAt, &p.Rating, &p.Flag, &p.EditParams, &p.EditHash,
		&p.LookGamma, &p.BaseExpEV, &p.UpdatedAt, &p.Lens, &p.GPSLat, &p.GPSLon, &p.GPSAlt, &p.Sharpness,
		&p.SubjectSharpness)
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

// SetSharpness persists the measured focus score for a photo.
func (db *DB) SetSharpness(ctx context.Context, id int64, score float64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET sharpness = ? WHERE id = ?`, score, id)
	return err
}

// SetSubjectSharpness persists the subject-weighted focus score for a photo
// (-1 = measured but unscoreable, see Photo.SubjectSharpness).
func (db *DB) SetSubjectSharpness(ctx context.Context, id int64, score float64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET subject_sharpness = ? WHERE id = ?`, score, id)
	return err
}

// ListPhotos returns a folder's photos in capture order.
//
// Ordering by taken_at rather than file_name is what makes time-gap grouping
// trustworthy: two bodies shooting the same event, or a counter rolling over
// from DSC09999 to DSC00001, put file_name order badly out of step with
// capture order, and a gap group computed from a mis-ordered list is nonsense.
//
// taken_at is 0 until the background metadata pass reaches a photo, so a
// freshly scanned folder sorts by file_name exactly as before, then settles
// into capture order as the backfill lands. Untimed photos (no EXIF date) sort
// last, among themselves by name; file_name breaks ties so the order is total
// and stable across queries.
func (db *DB) ListPhotos(ctx context.Context, folderID int64) ([]Photo, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+photoCols+` FROM photos p JOIN folders f ON f.id = p.folder_id
		WHERE p.folder_id = ?
		ORDER BY (p.taken_at = 0), p.taken_at, p.file_name`, folderID)
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
	Lens                       string
	GPSLat, GPSLon, GPSAlt     sql.NullFloat64
}

func (db *DB) SetMeta(ctx context.Context, id int64, m PhotoMeta) error {
	_, err := db.ExecContext(ctx, `
		UPDATE photos SET meta_loaded = 1, width = ?, height = ?, orientation = ?,
			make = ?, model = ?, iso = ?, shutter = ?, aperture = ?, focal_len = ?, taken_at = ?,
			lens = ?, gps_lat = ?, gps_lon = ?, gps_alt = ?
		WHERE id = ?`,
		m.Width, m.Height, m.Orientation, m.Make, m.Model, m.ISO, m.Shutter, m.Aperture, m.FocalLen, m.TakenAt,
		m.Lens, m.GPSLat, m.GPSLon, m.GPSAlt, id)
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
