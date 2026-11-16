-- +goose Up
-- Runs replace the single entry state. Every viewing / reading / playthrough
-- of an item is a run with its own status, rating, dates, review and
-- progress; the entry only means "in my library" and keeps the notes. Each
-- existing entry becomes run 1 with its status, rating (NULL and 0 kept),
-- dates, every progress value (inactive keys included) and updated_at; its
-- comment becomes the entry's notes and its events point at run 1. Progress
-- rows of items without an entry (none are written by the library) are not
-- carried over.
--
-- goose runs this in a transaction with foreign keys on, so PRAGMA
-- foreign_keys cannot be switched off: the old entries table is renamed
-- away first (nothing references it), the new tables are created under the
-- final names and filled from it, then it is dropped.
DROP INDEX idx_entries_status;
DROP INDEX idx_entries_updated;
ALTER TABLE entries RENAME TO entries_v18;

-- display_run_id names the run the entry presents (see the library's
-- refreshDisplayRun); it is maintained in the transaction of every run write.
CREATE TABLE entries (
    item_id        INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    notes          TEXT NOT NULL DEFAULT '',
    added_at       TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    display_run_id INTEGER REFERENCES entry_runs(id) ON DELETE SET NULL
);

-- number is dense (1..n) per item.
CREATE TABLE entry_runs (
    id          INTEGER PRIMARY KEY,
    item_id     INTEGER NOT NULL REFERENCES entries(item_id) ON DELETE CASCADE,
    number      INTEGER NOT NULL CHECK (number >= 1),
    status      TEXT NOT NULL CHECK (status IN ('planned','active','paused','completed','dropped')),
    rating      INTEGER CHECK (rating IS NULL OR rating BETWEEN 0 AND 10),
    review      TEXT NOT NULL DEFAULT '',
    started_on  TEXT,
    finished_on TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX entry_runs_number ON entry_runs(item_id, number);
CREATE INDEX entry_runs_status ON entry_runs(status);

-- Progress values per run. The active keys stay on the item
-- (items.progress_keys); values of keys that are not active are kept.
CREATE TABLE run_progress (
    run_id INTEGER NOT NULL REFERENCES entry_runs(id) ON DELETE CASCADE,
    key    TEXT NOT NULL,
    value  REAL NOT NULL,
    PRIMARY KEY (run_id, key)
);

INSERT INTO entries (item_id, notes, added_at, updated_at)
    SELECT item_id, comment, updated_at, updated_at FROM entries_v18;
INSERT INTO entry_runs (item_id, number, status, rating, review, started_on, finished_on, created_at, updated_at)
    SELECT item_id, 1, status, rating, '', started_on, finished_on, updated_at, updated_at FROM entries_v18;
UPDATE entries SET display_run_id =
    (SELECT r.id FROM entry_runs r WHERE r.item_id = entries.item_id AND r.number = 1);
INSERT INTO run_progress (run_id, key, value)
    SELECT r.id, p.key, p.value FROM entry_progress p
    JOIN entry_runs r ON r.item_id = p.item_id AND r.number = 1;

-- Deleting a run keeps its events (past activity never changes); removing
-- the item still cascades through item_id.
ALTER TABLE entry_events ADD COLUMN run_id INTEGER REFERENCES entry_runs(id) ON DELETE SET NULL;
UPDATE entry_events SET run_id =
    (SELECT r.id FROM entry_runs r WHERE r.item_id = entry_events.item_id AND r.number = 1);
CREATE INDEX entry_events_run ON entry_events(run_id);

DROP TABLE entry_progress;
DROP TABLE entries_v18;

-- One note per unit (a numbered step) of a count or pages track
-- within a run.
CREATE TABLE unit_notes (
    run_id       INTEGER NOT NULL REFERENCES entry_runs(id) ON DELETE CASCADE,
    progress_key TEXT NOT NULL,
    unit         INTEGER NOT NULL CHECK (unit >= 1),
    body         TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (run_id, progress_key, unit)
);

-- The owner's own tags, shared across all plugins.
CREATE TABLE user_tags (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    created_at TEXT NOT NULL
);
CREATE TABLE entry_user_tags (
    item_id INTEGER NOT NULL REFERENCES entries(item_id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES user_tags(id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, tag_id)
);
CREATE INDEX entry_user_tags_tag ON entry_user_tags(tag_id);

-- Custom, cross-plugin lists of entries. An entry removed from the library
-- leaves every list; deleting a list never touches entries.
CREATE TABLE lists (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    manual_order INTEGER NOT NULL DEFAULT 0 CHECK (manual_order IN (0, 1)),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE TABLE list_items (
    list_id  INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    item_id  INTEGER NOT NULL REFERENCES entries(item_id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    added_at TEXT NOT NULL,
    PRIMARY KEY (list_id, item_id)
);
CREATE INDEX list_items_item ON list_items(item_id);

-- The entry as it presents itself: the display run's state, the newer of the
-- entry's and the run's timestamps, and the number of completed runs.
CREATE VIEW entry_state AS
    SELECT e.item_id AS item_id,
           r.status AS status,
           r.rating AS rating,
           r.started_on AS started_on,
           r.finished_on AS finished_on,
           MAX(e.updated_at, r.updated_at) AS updated_at,
           e.notes AS notes,
           r.id AS run_id,
           r.number AS run_number,
           r.review AS review,
           -- the unary + keeps the planner on the (item_id, number) index
           -- instead of scanning every completed run per row
           (SELECT COUNT(*) FROM entry_runs c WHERE c.item_id = e.item_id AND +c.status = 'completed') AS repeats
    FROM entries e JOIN entry_runs r ON r.id = e.display_run_id;

-- The progress values (every stored key) of each entry's display run.
CREATE VIEW display_progress AS
    SELECT e.item_id AS item_id, p.key AS key, p.value AS value
    FROM entries e JOIN run_progress p ON p.run_id = e.display_run_id;

-- +goose Down
-- Back to one state per entry: the display run's values (notes become the
-- comment, the newer timestamp the entry's updated_at). Other runs, reviews,
-- unit notes, tags and lists are lost.
DROP VIEW display_progress;
DROP VIEW entry_state;
DROP TABLE list_items;
DROP TABLE lists;
DROP TABLE entry_user_tags;
DROP TABLE user_tags;
DROP TABLE unit_notes;

CREATE TABLE entries_v18 (
    item_id     INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('planned','active','paused','completed','dropped')),
    rating      INTEGER CHECK (rating IS NULL OR rating BETWEEN 0 AND 10),
    comment     TEXT NOT NULL DEFAULT '',
    started_on  TEXT,
    finished_on TEXT,
    updated_at  TEXT NOT NULL
);
INSERT INTO entries_v18 (item_id, status, rating, comment, started_on, finished_on, updated_at)
    SELECT e.item_id, r.status, r.rating, e.notes, r.started_on, r.finished_on, MAX(e.updated_at, r.updated_at)
    FROM entries e JOIN entry_runs r ON r.id = e.display_run_id;

CREATE TABLE entry_progress (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    value   REAL NOT NULL,
    PRIMARY KEY (item_id, key)
);
INSERT INTO entry_progress (item_id, key, value)
    SELECT e.item_id, p.key, p.value FROM entries e JOIN run_progress p ON p.run_id = e.display_run_id;

-- a column with a foreign key cannot be dropped: rebuild entry_events
CREATE TABLE entry_events_v18 (
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
INSERT INTO entry_events_v18 (id, item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin)
    SELECT id, item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin FROM entry_events;
DROP TABLE entry_events;
ALTER TABLE entry_events_v18 RENAME TO entry_events;
CREATE INDEX entry_events_item ON entry_events(item_id);
CREATE INDEX entry_events_time ON entry_events(origin, occurred_at);

DROP TABLE run_progress;
DROP TABLE entry_runs;
DROP TABLE entries;
ALTER TABLE entries_v18 RENAME TO entries;
CREATE INDEX idx_entries_status ON entries(status);
CREATE INDEX idx_entries_updated ON entries(updated_at);
