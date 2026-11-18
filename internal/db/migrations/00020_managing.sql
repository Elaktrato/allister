-- +goose Up
-- Managing entries: merged-away source identities, dismissed duplicate pairs,
-- the bulk event origin and the origin of a downloaded cover.

-- The remote URL a local cover file was downloaded from ('' when unknown or
-- when the cover is a URL or was set by hand), so a source's cover URL can be
-- compared with a downloaded cover.
ALTER TABLE items ADD COLUMN cover_source TEXT NOT NULL DEFAULT '';

-- A merge keeps the identity of the item that was merged away, so a lookup by
-- that source identity finds the survivor.
CREATE TABLE item_aliases (
    item_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    plugin_id TEXT NOT NULL,
    kind      TEXT NOT NULL,
    source    TEXT NOT NULL,
    source_id TEXT NOT NULL,
    PRIMARY KEY (plugin_id, kind, source, source_id)
);
CREATE INDEX item_aliases_item ON item_aliases(item_id);

-- Pairs the duplicate finder must not offer again (item_a < item_b).
CREATE TABLE dismissed_duplicates (
    item_a       INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    item_b       INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    dismissed_at TEXT NOT NULL,
    PRIMARY KEY (item_a, item_b),
    CHECK (item_a < item_b)
);
CREATE INDEX dismissed_duplicates_b ON dismissed_duplicates(item_b);

-- The origin CHECK cannot be altered in place: rebuild entry_events with
-- 'bulk' added, keeping every column, row and index (run_id keeps ON DELETE
-- SET NULL).
CREATE TABLE entry_events_v19 (
    id INTEGER PRIMARY KEY,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    occurred_at TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('progress','status','rating')),
    progress_key TEXT NOT NULL DEFAULT '',
    old_num REAL,
    new_num REAL,
    old_text TEXT NOT NULL DEFAULT '',
    new_text TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual','import','restore','bulk')),
    run_id INTEGER REFERENCES entry_runs(id) ON DELETE SET NULL
);
INSERT INTO entry_events_v19 (id, item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin, run_id)
    SELECT id, item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin, run_id FROM entry_events;
DROP TABLE entry_events;
ALTER TABLE entry_events_v19 RENAME TO entry_events;
CREATE INDEX entry_events_item ON entry_events(item_id);
CREATE INDEX entry_events_time ON entry_events(origin, occurred_at);
CREATE INDEX entry_events_run ON entry_events(run_id);

-- +goose Down
-- Bulk events are deleted; the aliases, dismissed pairs and cover origins
-- are lost.
ALTER TABLE items DROP COLUMN cover_source;
DROP TABLE dismissed_duplicates;
DROP TABLE item_aliases;

CREATE TABLE entry_events_v20 (
    id INTEGER PRIMARY KEY,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    occurred_at TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('progress','status','rating')),
    progress_key TEXT NOT NULL DEFAULT '',
    old_num REAL,
    new_num REAL,
    old_text TEXT NOT NULL DEFAULT '',
    new_text TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual','import','restore')),
    run_id INTEGER REFERENCES entry_runs(id) ON DELETE SET NULL
);
INSERT INTO entry_events_v20 (id, item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin, run_id)
    SELECT id, item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin, run_id FROM entry_events WHERE origin <> 'bulk';
DROP TABLE entry_events;
ALTER TABLE entry_events_v20 RENAME TO entry_events;
CREATE INDEX entry_events_item ON entry_events(item_id);
CREATE INDEX entry_events_time ON entry_events(origin, occurred_at);
CREATE INDEX entry_events_run ON entry_events(run_id);
