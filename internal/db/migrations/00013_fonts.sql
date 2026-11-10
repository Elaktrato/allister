-- +goose Up
-- Fonts the user uploaded for Settings > Appearance. The file lives in
-- DATA_DIR/fonts under its content hash (file); name is the display name
-- the user typed (never used in CSS, the family name is derived from id).
CREATE TABLE fonts (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    file        TEXT    NOT NULL UNIQUE,
    format      TEXT    NOT NULL CHECK (format IN ('woff2', 'woff', 'truetype', 'opentype')),
    size        INTEGER NOT NULL,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- +goose Down
DROP TABLE fonts;
