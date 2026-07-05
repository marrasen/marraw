// Package store persists folders, photos, culling state, and edit params
// in a SQLite database (pure-Go driver).
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const schemaVersion = 4

type DB struct {
	*sql.DB
}

// Open opens (creating if needed) the marraw database at dir/marraw.db.
func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)",
		filepath.ToSlash(filepath.Join(dir, "marraw.db")))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite allows one writer; a single connection avoids
	// SQLITE_BUSY churn and is plenty for a desktop app.
	db.SetMaxOpenConns(1)
	s := &DB{db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (db *DB) migrate(ctx context.Context) error {
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v >= schemaVersion {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if v < 1 {
		// Fresh database: schema.sql is always current.
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("store: apply schema: %w", err)
		}
	} else {
		if v < 2 {
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE photos ADD COLUMN look_gamma REAL NOT NULL DEFAULT 0`); err != nil {
				return fmt.Errorf("store: migrate v2: %w", err)
			}
		}
		if v < 3 {
			// Metadata written by older builds can hold wrong (half-size)
			// pixel dimensions, which mis-size the loupe box and the 1:1 tile
			// grid. Re-run the metadata pass for everything: it is cheap
			// (file open only, no decode) and runs as the usual background
			// task on the next folder open. Ratings, flags, and edits keep.
			if _, err := tx.ExecContext(ctx, `UPDATE photos SET meta_loaded = 0`); err != nil {
				return fmt.Errorf("store: migrate v3: %w", err)
			}
		}
		if v < 4 {
			// Camera-mimic exposure compensation, measured by the background
			// calibrate pass; NULL = not yet measured.
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE photos ADD COLUMN base_exp_ev REAL`); err != nil {
				return fmt.Errorf("store: migrate v4: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}
