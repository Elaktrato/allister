-- +goose Up
-- Hand-built, ordered series of list entries. One series holds items of one
-- plugin and kind; an item is in at most one series. Positions are dense
-- (1..n) and rewritten by the library on every change.
CREATE TABLE series (
    id         INTEGER PRIMARY KEY,
    uid        TEXT NOT NULL UNIQUE,
    plugin_id  TEXT NOT NULL,
    kind       TEXT NOT NULL,
    title      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE series_parts (
    series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    item_id   INTEGER NOT NULL UNIQUE REFERENCES items(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    PRIMARY KEY (series_id, item_id)
);
CREATE INDEX series_parts_order ON series_parts(series_id, position);

-- +goose Down
DROP INDEX series_parts_order;
DROP TABLE series_parts;
DROP TABLE series;
