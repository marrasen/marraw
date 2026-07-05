CREATE TABLE folders (
    id              INTEGER PRIMARY KEY,
    path            TEXT NOT NULL UNIQUE,
    last_scanned_at INTEGER
);

CREATE TABLE photos (
    id          INTEGER PRIMARY KEY,
    folder_id   INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    file_name   TEXT    NOT NULL,
    file_size   INTEGER NOT NULL,
    mtime_ns    INTEGER NOT NULL,
    cache_key   TEXT    NOT NULL,
    meta_loaded INTEGER NOT NULL DEFAULT 0,
    width       INTEGER NOT NULL DEFAULT 0,
    height      INTEGER NOT NULL DEFAULT 0,
    orientation INTEGER NOT NULL DEFAULT 0,
    make        TEXT    NOT NULL DEFAULT '',
    model       TEXT    NOT NULL DEFAULT '',
    iso         REAL    NOT NULL DEFAULT 0,
    shutter     REAL    NOT NULL DEFAULT 0,
    aperture    REAL    NOT NULL DEFAULT 0,
    focal_len   REAL    NOT NULL DEFAULT 0,
    taken_at    INTEGER NOT NULL DEFAULT 0,
    rating      INTEGER NOT NULL DEFAULT 0,
    flag        INTEGER NOT NULL DEFAULT 0,
    edit_params TEXT,
    edit_hash   TEXT    NOT NULL DEFAULT 'base',
    look_gamma  REAL    NOT NULL DEFAULT 0,
    UNIQUE(folder_id, file_name)
);
CREATE INDEX idx_photos_folder ON photos(folder_id, file_name);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
