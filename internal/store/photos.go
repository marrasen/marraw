package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	// PHash is the 64-bit perceptual hash of the embedded thumb
	// (pyramid.DHash bits as int64), measured by the calibrate pass.
	// Near-duplicate burst groups are derived from it at list time.
	// Invalid = not yet measured.
	PHash sql.NullInt64
	// EyesClosed is the highest closed-eye probability across the detected
	// faces of the embedded thumb (eyes.Score), measured once the eye models
	// are on disk. Invalid = not yet measured; -1 = measured but no
	// judgeable face/eyes.
	EyesClosed sql.NullFloat64
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
// whether the folder has ever been scanned (a folder made to hold photos
// handed over from another has not). Comparing paths case-insensitively
// matches how the rest of the app treats Windows paths.
func (db *DB) FolderPhotoCount(ctx context.Context, path string) (int, bool, error) {
	path = filepath.Clean(path)
	// The folder lookup is separate from the count: an aggregate over a
	// non-existent folder still yields one row holding 0, which would report an
	// unscanned folder as an empty one.
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM folders WHERE path = ? COLLATE NOCASE AND last_scanned_at IS NOT NULL`, path).Scan(&id)
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

// FolderSummary is what the library rail shows of one scanned folder row:
// its earliest known capture time (0 when none is read yet), how many of
// its photos are picked and rejected, and its cover photo.
type FolderSummary struct {
	Path              string
	Earliest          int64 // unix seconds
	Picked, Rejected  int
	CoverID           int64
	CoverKey, CoverEd string // the cover's cache_key and edit_hash
}

// FolderSummaries returns a FolderSummary for every scanned folder with
// photos. The cover is the folder's first pick, or its first photo when
// nothing is picked, first by capture time, then by file name. Like
// EarliestTakenByFolder, it is one pass over the catalog, and callers
// match paths to shoots in Go.
func (db *DB) FolderSummaries(ctx context.Context) ([]FolderSummary, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT f.path, s.earliest, s.picked, s.rejected, c.id, c.cache_key, COALESCE(c.edit_hash, '')
		FROM folders f
		JOIN (SELECT folder_id,
				COALESCE(MIN(CASE WHEN taken_at > 0 THEN taken_at END), 0) AS earliest,
				SUM(flag > 0) AS picked, SUM(flag < 0) AS rejected
			FROM photos GROUP BY folder_id) s ON s.folder_id = f.id
		JOIN (SELECT folder_id, id, cache_key, edit_hash,
				ROW_NUMBER() OVER (PARTITION BY folder_id
					ORDER BY flag > 0 DESC, taken_at <= 0, taken_at, file_name) AS rn
			FROM photos) c ON c.folder_id = f.id AND c.rn = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderSummary
	for rows.Next() {
		var s FolderSummary
		if err := rows.Scan(&s.Path, &s.Earliest, &s.Picked, &s.Rejected, &s.CoverID, &s.CoverKey, &s.CoverEd); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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
	n, _, err := db.SyncFolderWith(ctx, folderID, folderPath, entries, nil)
	return n, err
}

// SyncFolderWith is SyncFolder, keeping the rows of photos read another way
// than before, and all they hold. home names the catalog folder, by path,
// a photo's row belongs in by the library's rules ("" where they say
// none), as of before the sync: it is called with the database busy, so
// it must not use it, and it is asked of every photo first, before the
// write lock is taken, so it can keep its answers for then. A folder that is a photo's home takes over the row
// another catalog folder above or below it has of the photo; a photo the
// folder no longer takes in, still on disk, has its row moved to its home,
// made unread where the catalog has none. With home nil no row moves. It
// also returns the other catalog folders that gained or lost rows, whose
// listings are out of date.
func (db *DB) SyncFolderWith(ctx context.Context, folderID int64, folderPath string, entries []FileEntry, home func(file string) string) (int, []int64, error) {
	seen := make(map[string]bool, len(entries))
	seenFold := make(map[string]bool, len(entries))
	for _, e := range entries {
		seen[e.Name] = true
		seenFold[strings.ToLower(e.Name)] = true
	}
	// dropped reports whether the row name, which the listing does not
	// have, is of a file still on disk in a subfolder: on a disk that
	// ignores case, a name the listing has in another case is the same
	// file, no other.
	dropped := func(name string) bool {
		if seenFold[strings.ToLower(name)] || !strings.ContainsAny(name, `/\`) {
			return false
		}
		_, err := os.Stat(filepath.Join(folderPath, name))
		return err == nil
	}
	// Asked of the disk before the write lock is taken, for the rows the
	// folder has now; a row another sync moves in meanwhile is asked
	// after.
	stays := map[string]bool{}
	if home != nil {
		// The homes asked for too, which a rule may answer from the disk,
		// so the transaction finds them known.
		for _, e := range entries {
			home(filepath.Join(folderPath, e.Name))
		}
		if names, err := db.photoNames(ctx, folderID); err == nil {
			for _, name := range names {
				if !seen[name] {
					if stays[name] = dropped(name); stays[name] {
						home(filepath.Join(folderPath, name))
					}
				}
			}
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()

	m := &rowMover{tx: tx, folders: map[string]int64{}, home: home, touched: map[int64]bool{}}
	if home != nil {
		for _, e := range entries {
			if err := m.adopt(ctx, folderID, folderPath, e.Name); err != nil {
				return 0, nil, err
			}
		}
	}

	for _, e := range entries {
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
					THEN photos.meta_loaded ELSE 0 END,
				fingerprint = CASE WHEN photos.mtime_ns = excluded.mtime_ns
					AND photos.file_size = excluded.file_size
					THEN photos.fingerprint ELSE NULL END`,
			folderID, e.Name, e.Size, e.MtimeNs, key); err != nil {
			return 0, nil, err
		}
	}

	// Delete rows for vanished files, or move them to their home.
	rows, err := tx.QueryContext(ctx, `SELECT id, file_name FROM photos WHERE folder_id = ?`, folderID)
	if err != nil {
		return 0, nil, err
	}
	var gone []goneRow
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return 0, nil, err
		}
		if !seen[name] {
			gone = append(gone, goneRow{id, name})
		}
	}
	rows.Close()
	for _, g := range gone {
		if home != nil {
			still, asked := stays[g.name]
			if !asked {
				still = dropped(g.name)
			}
			if still && strings.EqualFold(home(filepath.Join(folderPath, g.name)), folderPath) {
				// Still on disk and still this folder's, though this read
				// left it out: the rules changed under the read, as a
				// split undone at once does. The row stays, with all it
				// holds, for the next read.
				continue
			}
			if still {
				moved, err := m.handOver(ctx, folderPath, g)
				if err != nil {
					return 0, nil, err
				}
				if moved {
					continue
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM photos WHERE id = ?`, g.id); err != nil {
			return 0, nil, err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE folders SET last_scanned_at = unixepoch() WHERE id = ?`, folderID); err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	var touched []int64
	for id := range m.touched {
		if id != folderID {
			touched = append(touched, id)
		}
	}
	slices.Sort(touched)
	return len(entries), touched, nil
}

// photoNames are the file names of folder folderID's photo rows.
func (db *DB) photoNames(ctx context.Context, folderID int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT file_name FROM photos WHERE folder_id = ?`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// goneRow is a photo row whose file a folder's listing no longer has.
type goneRow struct {
	id   int64
	name string
}

// rowMover moves photo rows between catalog folders in one sync.
type rowMover struct {
	tx      *sql.Tx
	folders map[string]int64 // catalog folder ids by lowercased path; 0 for none
	home    func(file string) string
	touched map[int64]bool
}

// folder is the id of the catalog folder at path, made unread if create is
// set and there is none; nought where there is none and create is not set.
func (m *rowMover) folder(ctx context.Context, path string, create bool) (int64, error) {
	key := strings.ToLower(path)
	if id, ok := m.folders[key]; ok && (id != 0 || !create) {
		return id, nil
	}
	var id int64
	err := m.tx.QueryRowContext(ctx, `SELECT id FROM folders WHERE path = ? COLLATE NOCASE`, path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err, id = nil, 0
		if create {
			err = m.tx.QueryRowContext(ctx, `INSERT INTO folders (path) VALUES (?) RETURNING id`, path).Scan(&id)
		}
	}
	if err != nil {
		return 0, err
	}
	m.folders[key] = id
	return id, nil
}

// adopt moves into folder folderID, at folderPath, the row another catalog
// folder has of the photo at name (a path under folderPath), where this
// folder is the photo's home and has no row of it: the nearest folder
// holding one, under this one or over it.
func (m *rowMover) adopt(ctx context.Context, folderID int64, folderPath, name string) error {
	full := filepath.Join(folderPath, name)
	if !strings.EqualFold(m.home(full), folderPath) {
		return nil
	}
	for dir := filepath.Dir(full); ; {
		if !strings.EqualFold(dir, folderPath) {
			from, err := m.folder(ctx, dir, false)
			if err != nil {
				return err
			}
			if rel, err := filepath.Rel(dir, full); from != 0 && err == nil {
				res, err := m.tx.ExecContext(ctx, `
					UPDATE photos SET folder_id = ?, file_name = ?
					WHERE folder_id = ? AND file_name = ?
					AND NOT EXISTS (SELECT 1 FROM photos WHERE folder_id = ? AND file_name = ?)`,
					folderID, name, from, rel, folderID, name)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n > 0 {
					m.touched[from] = true
					return nil
				}
			}
		}
		up := filepath.Dir(dir)
		if up == dir {
			return nil
		}
		dir = up
	}
}

// handOver moves the row g, of a photo under folderPath that the folder's
// listing no longer takes in, to the photo's home, where that folder has no
// row of it. It reports whether it moved the row.
func (m *rowMover) handOver(ctx context.Context, folderPath string, g goneRow) (bool, error) {
	full := filepath.Join(folderPath, g.name)
	h := m.home(full)
	if h == "" || strings.EqualFold(h, folderPath) {
		return false, nil
	}
	rel, err := filepath.Rel(h, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	to, err := m.folder(ctx, h, true)
	if err != nil || to == 0 {
		return false, err
	}
	res, err := m.tx.ExecContext(ctx, `
		UPDATE photos SET folder_id = ?, file_name = ?
		WHERE id = ? AND NOT EXISTS (SELECT 1 FROM photos WHERE folder_id = ? AND file_name = ?)`,
		to, rel, g.id, to, rel)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		m.touched[to] = true
	}
	return n > 0, nil
}

const photoCols = `p.id, p.folder_id, f.path, p.file_name, p.file_size, p.mtime_ns, p.cache_key,
	p.meta_loaded, p.width, p.height, p.orientation, p.make, p.model,
	p.iso, p.shutter, p.aperture, p.focal_len, p.taken_at, p.rating, p.flag, p.edit_params, p.edit_hash,
	p.look_gamma, p.base_exp_ev, p.updated_at, p.lens, p.gps_lat, p.gps_lon, p.gps_alt, p.sharpness,
	p.subject_sharpness, p.phash, p.eyes_closed`

func scanPhoto(row interface{ Scan(...any) error }) (Photo, error) {
	var p Photo
	err := row.Scan(&p.ID, &p.FolderID, &p.FolderPath, &p.FileName, &p.FileSize, &p.MtimeNs, &p.CacheKey,
		&p.MetaLoaded, &p.Width, &p.Height, &p.Orientation, &p.Make, &p.Model,
		&p.ISO, &p.Shutter, &p.Aperture, &p.FocalLen, &p.TakenAt, &p.Rating, &p.Flag, &p.EditParams, &p.EditHash,
		&p.LookGamma, &p.BaseExpEV, &p.UpdatedAt, &p.Lens, &p.GPSLat, &p.GPSLon, &p.GPSAlt, &p.Sharpness,
		&p.SubjectSharpness, &p.PHash, &p.EyesClosed)
	return p, err
}

// SetDimensions corrects the stored pixel dimensions from a ground-truth
// source (the pyramid's full-resolution render).
func (db *DB) SetDimensions(ctx context.Context, id int64, w, h int) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET width = ?, height = ? WHERE id = ?`, w, h, id)
	return err
}

// SetLookGamma persists the calibrated tone lift for a photo that has none:
// the first measurement stands, so renders made over it stay right.
func (db *DB) SetLookGamma(ctx context.Context, id int64, gamma float64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET look_gamma = ? WHERE id = ? AND look_gamma = 0`, gamma, id)
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

// SetEyesClosed persists the closed-eye probability for a photo
// (-1 = measured but no judgeable face/eyes, see Photo.EyesClosed).
func (db *DB) SetEyesClosed(ctx context.Context, id int64, score float64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET eyes_closed = ? WHERE id = ?`, score, id)
	return err
}

// SetPHash persists the perceptual hash of a photo's embedded thumb
// (pyramid.DHash bits, stored as int64).
func (db *DB) SetPHash(ctx context.Context, id int64, hash uint64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET phash = ? WHERE id = ?`, int64(hash), id)
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
// CountPhotos returns how many photos a folder holds. For a caller that wants
// only the number, this is worth having on its own: the share list refreshes
// on every guest connect and disconnect, and materialising every row of a
// shoot to call len() on it made a guest on a flaky connection expensive to
// have around.
func (db *DB) CountPhotos(ctx context.Context, folderID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM photos WHERE folder_id = ?`, folderID).Scan(&n)
	return n, err
}

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

// GetPhotos reads many photos in the caller's order. One query rather than one
// per ID: several callers pass a whole selection, and one of them (the guest
// scope check) is handed its list by someone outside the library.
//
// A missing ID is an error, not a short result — callers rely on the returned
// slice lining up with the IDs they asked for.
func (db *DB) GetPhotos(ctx context.Context, ids []int64) ([]Photo, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args := int64Placeholders(ids)
	rows, err := db.QueryContext(ctx, `
		SELECT `+photoCols+` FROM photos p JOIN folders f ON f.id = p.folder_id
		WHERE p.id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[int64]Photo, len(ids))
	for rows.Next() {
		p, err := scanPhoto(rows)
		if err != nil {
			return nil, err
		}
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Rebuild in the caller's order, which the IN clause does not preserve.
	// Duplicated IDs are returned as many times as they were asked for, as the
	// per-ID loop this replaced did.
	out := make([]Photo, 0, len(ids))
	for _, id := range ids {
		p, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("photo %d: %w", id, sql.ErrNoRows)
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

// ListCameras returns the distinct camera make/model pairs across the whole
// catalog (blank-metadata photos excluded), for the default-preset picker
// in Settings. The caller composes the "Make Model" key so the
// normalization lives in one place.
func (db *DB) ListCameras(ctx context.Context) ([][2]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT make, model FROM photos
		 WHERE TRIM(make) != '' OR TRIM(model) != '' ORDER BY make, model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var mk, md string
		if err := rows.Scan(&mk, &md); err != nil {
			return nil, err
		}
		out = append(out, [2]string{mk, md})
	}
	return out, rows.Err()
}

// SetEdit stores the edit params JSON (nil clears) and its hash.
func (db *DB) SetEdit(ctx context.Context, id int64, paramsJSON *string, hash string, updatedAtMs int64) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET edit_params = ?, edit_hash = ?, updated_at = ? WHERE id = ?`,
		paramsJSON, hash, updatedAtMs, id)
	return err
}

// SetEditSeed stores an edit only when the photo has never been edited
// (edit_params IS NULL), reporting whether the write landed. The calibrate
// pass seeds default presets through it, so a user edit racing the pass can
// never be clobbered — the condition and the write are one statement.
func (db *DB) SetEditSeed(ctx context.Context, id int64, paramsJSON string, hash string, updatedAtMs int64) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE photos SET edit_params = ?, edit_hash = ?, updated_at = ? WHERE id = ? AND edit_params IS NULL`,
		paramsJSON, hash, updatedAtMs, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
