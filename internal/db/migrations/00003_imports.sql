-- +goose Up
-- One row per uploaded Yamtrack export. state: pending, running, done, failed
-- or cancelled. total counts the stored rows, ignored the rows of other media
-- types that were dropped at upload time.
CREATE TABLE imports (
    id          INTEGER PRIMARY KEY,
    filename    TEXT    NOT NULL DEFAULT '',
    state       TEXT    NOT NULL DEFAULT 'pending',
    total       INTEGER NOT NULL DEFAULT 0,
    ignored     INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    finished_at TEXT
);

-- The parsed rows of an import. outcome: pending (not processed yet),
-- imported (also used for adjusted rows, the adjustment is in message),
-- unmatched (saved as a manual entry), skipped or failed. data holds the
-- parsed row as JSON so an interrupted job can resume.
CREATE TABLE import_rows (
    import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
    seq       INTEGER NOT NULL,
    mal_id    TEXT    NOT NULL DEFAULT '',
    title     TEXT    NOT NULL DEFAULT '',
    outcome   TEXT    NOT NULL DEFAULT 'pending',
    anime_id  INTEGER REFERENCES anime(id) ON DELETE SET NULL,
    message   TEXT    NOT NULL DEFAULT '',
    data      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (import_id, seq)
);
CREATE INDEX idx_import_rows_outcome ON import_rows(import_id, outcome, seq);

-- +goose Down
DROP TABLE import_rows;
DROP TABLE imports;
