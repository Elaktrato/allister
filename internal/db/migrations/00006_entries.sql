-- +goose Up
-- The user's list: one entry per saved item, with its progress values.
-- Progress values of keys that are no longer active for the item are kept.
CREATE TABLE entries (
    item_id     INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('planned','active','paused','completed','dropped')),
    rating      INTEGER CHECK (rating IS NULL OR rating BETWEEN 0 AND 10),
    comment     TEXT NOT NULL DEFAULT '',
    started_on  TEXT,
    finished_on TEXT,
    updated_at  TEXT NOT NULL
);
CREATE INDEX idx_entries_status ON entries(status);

CREATE TABLE entry_progress (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    value   REAL NOT NULL,
    PRIMARY KEY (item_id, key)
);

-- +goose Down
DROP TABLE entry_progress;
DROP TABLE entries;
