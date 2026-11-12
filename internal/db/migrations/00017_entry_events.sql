-- +goose Up
-- One row per recorded entry change, appended in the same transaction as the
-- change itself. It powers the activity heatmap; the table is append-only and
-- rows cascade with their item.
CREATE TABLE entry_events (
    id INTEGER PRIMARY KEY,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    occurred_at TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('progress','status','rating')),
    progress_key TEXT NOT NULL DEFAULT '',
    old_num REAL,
    new_num REAL,
    old_text TEXT NOT NULL DEFAULT '',
    new_text TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual','import','restore'))
);
CREATE INDEX entry_events_item ON entry_events(item_id);
CREATE INDEX entry_events_time ON entry_events(origin, occurred_at);

-- +goose Down
DROP TABLE entry_events;
