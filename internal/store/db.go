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

const schemaVersion = 11

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
		if v < 5 {
			// Last-writer-wins timestamp (unix millis) for portable edit
			// sidecars; NULL = never touched, so any sidecar wins on import.
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE photos ADD COLUMN updated_at INTEGER`); err != nil {
				return fmt.Errorf("store: migrate v5: %w", err)
			}
		}
		if v < 6 {
			// A pool worker's LibRaw handle kept params across jobs, so a photo
			// whose metadata was read right after a calibration decode recorded
			// half its real width and height (libraw.Processor.Open now resets
			// params first). Any row could be affected; re-read them. The pass
			// is metadata-only — no pixel decode — and runs in the background.
			if _, err := tx.ExecContext(ctx, `UPDATE photos SET meta_loaded = 0`); err != nil {
				return fmt.Errorf("store: migrate v6: %w", err)
			}
		}
		if v < 7 {
			// Lens model and GPS, newly read by the metadata pass for export
			// EXIF. NULL gps = no fix recorded. Resetting meta_loaded backfills
			// existing rows lazily on the next folder open (the v3/v6 pattern).
			for _, stmt := range []string{
				`ALTER TABLE photos ADD COLUMN lens TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE photos ADD COLUMN gps_lat REAL`,
				`ALTER TABLE photos ADD COLUMN gps_lon REAL`,
				`ALTER TABLE photos ADD COLUMN gps_alt REAL`,
				`UPDATE photos SET meta_loaded = 0`,
			} {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("store: migrate v7: %w", err)
				}
			}
		}
		if v < 8 {
			// Sharpness score (Laplacian variance of the embedded thumb, see
			// pyramid.SharpnessScore), measured by the calibrate pass for the
			// grid's soft-photo badge; NULL = not yet measured.
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE photos ADD COLUMN sharpness REAL`); err != nil {
				return fmt.Errorf("store: migrate v8: %w", err)
			}
		}
		if v < 9 {
			// Subject-weighted sharpness (pyramid.SubjectSharpnessScore),
			// measured only for photos whose AI subject matte already exists;
			// NULL = not yet measured, -1 = measured but unscoreable (no
			// meaningful subject coverage).
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE photos ADD COLUMN subject_sharpness REAL`); err != nil {
				return fmt.Errorf("store: migrate v9: %w", err)
			}
		}
		if v < 10 {
			// Perceptual hash (pyramid.DHash of the embedded thumb), measured
			// by the calibrate pass; near-duplicate burst groups are derived
			// from it at list time. NULL = not yet measured.
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE photos ADD COLUMN phash INTEGER`); err != nil {
				return fmt.Errorf("store: migrate v10: %w", err)
			}
		}
		if v < 11 {
			// Closed-eye probability (eyes.Score over the embedded thumb),
			// measured once the eye models are on disk; NULL = not yet
			// measured, -1 = measured but no judgeable face/eyes.
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE photos ADD COLUMN eyes_closed REAL`); err != nil {
				return fmt.Errorf("store: migrate v11: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}
