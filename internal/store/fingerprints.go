package store

import (
	"context"
	"database/sql"
	"errors"
)

// deletedPhotosSQL makes the table of photos deleted on purpose; schema.sql
// makes the same for a new catalog.
const deletedPhotosSQL = `CREATE TABLE deleted_photos (
    fingerprint TEXT PRIMARY KEY,
    file_name   TEXT    NOT NULL,
    folder_path TEXT    NOT NULL,
    taken_at    INTEGER NOT NULL DEFAULT 0,
    deleted_at  INTEGER NOT NULL
)`

// Unfingerprinted are a folder's photos with no fingerprint yet.
func (db *DB) Unfingerprinted(ctx context.Context, folderID int64) ([]Photo, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT p.id, p.file_name, p.file_size, f.path FROM photos p
		JOIN folders f ON f.id = p.folder_id
		WHERE p.folder_id = ? AND p.fingerprint IS NULL ORDER BY p.file_name`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.ID, &p.FileName, &p.FileSize, &p.FolderPath); err != nil {
			return nil, err
		}
		p.FolderID = folderID
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetFingerprint records photo id's fingerprint.
func (db *DB) SetFingerprint(ctx context.Context, id int64, fp string) error {
	_, err := db.ExecContext(ctx, `UPDATE photos SET fingerprint = ? WHERE id = ?`, fp, id)
	return err
}

// Fingerprint is photo id's fingerprint, or "" where it has none yet.
func (db *DB) Fingerprint(ctx context.Context, id int64) (string, error) {
	var fp sql.NullString
	err := db.QueryRowContext(ctx, `SELECT fingerprint FROM photos WHERE id = ?`, id).Scan(&fp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return fp.String, err
}

// DeletedPhoto is a photo deleted on purpose.
type DeletedPhoto struct {
	Fingerprint, FileName, FolderPath string
	TakenAt, DeletedAt                int64
}

// RecordDeleted keeps that the photo d was deleted on purpose.
func (db *DB) RecordDeleted(ctx context.Context, d DeletedPhoto) error {
	if d.Fingerprint == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO deleted_photos (fingerprint, file_name, folder_path, taken_at, deleted_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET file_name = excluded.file_name,
			folder_path = excluded.folder_path, deleted_at = excluded.deleted_at`,
		d.Fingerprint, d.FileName, d.FolderPath, d.TakenAt, d.DeletedAt)
	return err
}

// WasDeleted is the record of the photo of fingerprint fp deleted on
// purpose, and whether there is one.
func (db *DB) WasDeleted(ctx context.Context, fp string) (DeletedPhoto, bool, error) {
	d := DeletedPhoto{Fingerprint: fp}
	err := db.QueryRowContext(ctx, `SELECT file_name, folder_path, taken_at, deleted_at FROM deleted_photos WHERE fingerprint = ?`, fp).
		Scan(&d.FileName, &d.FolderPath, &d.TakenAt, &d.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// FolderByPath is the catalog's folder at path: its id and how many photos
// it holds, and false where the catalog has not read it.
func (db *DB) FolderByPath(ctx context.Context, path string) (id int64, photos int, ok bool, err error) {
	err = db.QueryRowContext(ctx, `
		SELECT f.id, (SELECT COUNT(*) FROM photos p WHERE p.folder_id = f.id)
		FROM folders f WHERE f.path = ? COLLATE NOCASE`, path).Scan(&id, &photos)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	return id, photos, err == nil, err
}
