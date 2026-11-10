-- +goose Up
-- The airing schedules of items whose source declares SupportsSchedule: the
-- calendar feed reads these rows, the sync_schedule job replaces them. The
-- marker row keeps an item that answered with no schedule from being asked
-- again on every calendar poll.
CREATE TABLE item_schedules (
    item_id   INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    synced_at TEXT NOT NULL
);
CREATE TABLE airings (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    number  INTEGER NOT NULL,
    airs_at TEXT NOT NULL,
    PRIMARY KEY (item_id, number)
);

-- +goose Down
DROP TABLE airings;
DROP TABLE item_schedules;
