-- +goose Up
-- Series carry their own data: a cover, a description, dates, the owner's
-- "finished" marker, the expected number of parts (NULL = not set), an
-- optional linked entry and an optional parent series (spin-off series, one
-- level only; the rules are checked in the library). Deleting the linked
-- item or the parent leaves the series with the reference cleared.
ALTER TABLE series ADD COLUMN cover_path TEXT;
ALTER TABLE series ADD COLUMN description TEXT;
ALTER TABLE series ADD COLUMN started_on TEXT;
ALTER TABLE series ADD COLUMN ended_on TEXT;
ALTER TABLE series ADD COLUMN finished INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN parts_total INTEGER;
ALTER TABLE series ADD COLUMN linked_item_id INTEGER REFERENCES items(id) ON DELETE SET NULL;
ALTER TABLE series ADD COLUMN parent_id INTEGER REFERENCES series(id) ON DELETE SET NULL;
CREATE INDEX series_linked_item ON series(linked_item_id);
CREATE INDEX series_parent ON series(parent_id);

-- Main parts and spin-off parts keep separate dense positions (1..n each).
ALTER TABLE series_parts ADD COLUMN spin_off INTEGER NOT NULL DEFAULT 0;

-- Extra fields of a series, shaped like item_fields.
CREATE TABLE series_fields (
    series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    key       TEXT NOT NULL,
    vtype     TEXT NOT NULL,
    text      TEXT,
    num       REAL,
    date      TEXT,
    seq       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (series_id, key, seq)
);

-- +goose Down
-- Columns with a foreign key cannot be dropped, so series is rebuilt. Its
-- parts are copied aside first: dropping series (foreign keys on) would
-- cascade-delete them.
DROP TABLE series_fields;
CREATE TABLE series_parts_keep AS SELECT series_id, item_id, position, spin_off FROM series_parts;
DROP INDEX series_parts_order;
DROP TABLE series_parts;
DROP INDEX series_parent;
DROP INDEX series_linked_item;
CREATE TABLE series_old (
    id         INTEGER PRIMARY KEY,
    uid        TEXT NOT NULL UNIQUE,
    plugin_id  TEXT NOT NULL,
    kind       TEXT NOT NULL,
    title      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO series_old(id, uid, plugin_id, kind, title, created_at, updated_at)
    SELECT id, uid, plugin_id, kind, title, created_at, updated_at FROM series;
DROP TABLE series;
ALTER TABLE series_old RENAME TO series;
CREATE TABLE series_parts (
    series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    item_id   INTEGER NOT NULL UNIQUE REFERENCES items(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    PRIMARY KEY (series_id, item_id)
);
CREATE INDEX series_parts_order ON series_parts(series_id, position);
-- spin-off parts follow the main parts, so positions stay dense
INSERT INTO series_parts(series_id, item_id, position)
    SELECT k.series_id, k.item_id,
           (SELECT count(*) FROM series_parts_keep o WHERE o.series_id = k.series_id
               AND (o.spin_off < k.spin_off
                    OR (o.spin_off = k.spin_off AND o.position < k.position)
                    OR (o.spin_off = k.spin_off AND o.position = k.position AND o.item_id <= k.item_id)))
    FROM series_parts_keep k;
DROP TABLE series_parts_keep;
