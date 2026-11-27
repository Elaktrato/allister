-- +goose Up
-- Source extras of an item: a banner image and a trailer (video link plus
-- thumbnail). banner_path and trailer_thumb hold the remote URL until the
-- image is downloaded, then the local file name (like cover_path); ''
-- means none.
ALTER TABLE items ADD COLUMN banner_path TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN trailer_url TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN trailer_thumb TEXT NOT NULL DEFAULT '';

-- The source's labelled links of an item (streaming sites and the like),
-- in the source's order.
CREATE TABLE item_links (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    seq     INTEGER NOT NULL,
    label   TEXT NOT NULL,
    url     TEXT NOT NULL,
    PRIMARY KEY (item_id, seq)
);

-- One row per bulk "update from source" run of a plugin: its counts, the
-- titles that failed (a JSON array) and why it stopped early ('' = it did
-- not).
CREATE TABLE source_refresh_runs (
    id            INTEGER PRIMARY KEY,
    plugin_id     TEXT NOT NULL,
    started_at    TEXT NOT NULL,
    finished_at   TEXT,
    total         INTEGER NOT NULL,
    updated       INTEGER NOT NULL DEFAULT 0,
    unchanged     INTEGER NOT NULL DEFAULT 0,
    failed        INTEGER NOT NULL DEFAULT 0,
    failed_titles TEXT NOT NULL DEFAULT '[]',
    stopped       TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE source_refresh_runs;
DROP TABLE item_links;
ALTER TABLE items DROP COLUMN trailer_thumb;
ALTER TABLE items DROP COLUMN trailer_url;
ALTER TABLE items DROP COLUMN banner_path;
