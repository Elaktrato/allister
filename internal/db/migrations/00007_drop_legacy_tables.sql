-- +goose Up
-- The generic library replaces the legacy anime tables. The owner has no real
-- data, so legacy rows are NOT carried over: the tables are simply dropped
-- (this never fails, whether they hold rows or not).
--
-- import_rows.anime_id referenced anime(id); it is rebuilt to reference
-- items(id). Its old values pointed at legacy anime rows that are gone, so
-- they become NULL (what ON DELETE SET NULL would have done).
CREATE TABLE import_rows_new (
    import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
    seq       INTEGER NOT NULL,
    mal_id    TEXT    NOT NULL DEFAULT '',
    title     TEXT    NOT NULL DEFAULT '',
    outcome   TEXT    NOT NULL DEFAULT 'pending',
    anime_id  INTEGER REFERENCES items(id) ON DELETE SET NULL,
    message   TEXT    NOT NULL DEFAULT '',
    data      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (import_id, seq)
);
INSERT INTO import_rows_new (import_id, seq, mal_id, title, outcome, anime_id, message, data)
    SELECT import_id, seq, mal_id, title, outcome, NULL, message, data FROM import_rows;
DROP TABLE import_rows;
ALTER TABLE import_rows_new RENAME TO import_rows;
CREATE INDEX idx_import_rows_outcome ON import_rows(import_id, outcome, seq);

-- children first, so the implicit DELETE of a parent never meets a reference
DROP TABLE anime_cast;
DROP TABLE anime_relations;
DROP TABLE anime_titles;
DROP TABLE list_entries;
DROP TABLE anime;
DROP TABLE people;

-- the generic tables take the final names; SQLite rewrites the foreign keys
-- of credits (person_id, counterpart_person_id) to the new name
ALTER TABLE people_g RENAME TO people;
ALTER TABLE relations_g RENAME TO relations;

-- A free-text qualifier of a credit's role (for example MAIN or SUPPORTING
-- for an anime character).
ALTER TABLE credits ADD COLUMN detail TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Restores the legacy schema EMPTY: the generic rows stay where they are and
-- nothing is copied back into the legacy tables. import_rows.anime_id values
-- (items ids) become NULL because they do not name legacy anime rows.
ALTER TABLE credits DROP COLUMN detail;
ALTER TABLE relations RENAME TO relations_g;
ALTER TABLE people RENAME TO people_g;

CREATE TABLE anime (
    id              INTEGER PRIMARY KEY,
    source          TEXT NOT NULL,
    source_id       TEXT NOT NULL,
    type            TEXT NOT NULL DEFAULT '',
    episodes        INTEGER,
    year            INTEGER,
    studio          TEXT NOT NULL DEFAULT '',
    synopsis        TEXT NOT NULL DEFAULT '',
    cover_path      TEXT NOT NULL DEFAULT '',
    external_url    TEXT NOT NULL DEFAULT '',
    source_material TEXT NOT NULL DEFAULT '',
    started_on      TEXT,
    ended_on        TEXT,
    mal_id          INTEGER,
    sync_state      TEXT NOT NULL DEFAULT '',
    UNIQUE (source, source_id)
);
CREATE INDEX idx_anime_mal_id ON anime(mal_id);

CREATE TABLE anime_titles (
    anime_id INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    lang     TEXT NOT NULL,
    text     TEXT NOT NULL,
    UNIQUE (anime_id, lang, text)
);

CREATE TABLE anime_relations (
    anime_id       INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    rel_source     TEXT NOT NULL,
    rel_source_id  TEXT NOT NULL,
    titles_json    TEXT NOT NULL DEFAULT '[]',
    type           TEXT NOT NULL DEFAULT '',
    UNIQUE (anime_id, kind, rel_source, rel_source_id)
);

CREATE TABLE people (
    id           INTEGER PRIMARY KEY,
    source       TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    image_path   TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    UNIQUE (source, source_id)
);

CREATE TABLE anime_cast (
    anime_id            INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    character_person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    actor_person_id     INTEGER REFERENCES people(id) ON DELETE SET NULL,
    role                TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_anime_cast_anime ON anime_cast(anime_id);
CREATE INDEX idx_anime_cast_actor ON anime_cast(actor_person_id);
CREATE INDEX idx_anime_cast_character ON anime_cast(character_person_id);

CREATE TABLE list_entries (
    anime_id    INTEGER PRIMARY KEY REFERENCES anime(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('watching','plan_to_watch','paused','completed','dropped')),
    rating      INTEGER CHECK (rating IS NULL OR rating BETWEEN 0 AND 10),
    progress    INTEGER NOT NULL DEFAULT 0,
    started_on  TEXT,
    finished_on TEXT,
    comment     TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX idx_list_entries_status ON list_entries(status);

CREATE TABLE import_rows_old (
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
INSERT INTO import_rows_old (import_id, seq, mal_id, title, outcome, anime_id, message, data)
    SELECT import_id, seq, mal_id, title, outcome, NULL, message, data FROM import_rows;
DROP TABLE import_rows;
ALTER TABLE import_rows_old RENAME TO import_rows;
CREATE INDEX idx_import_rows_outcome ON import_rows(import_id, outcome, seq);
