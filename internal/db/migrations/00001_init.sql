-- +goose Up
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

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

CREATE TABLE jobs (
    id           INTEGER PRIMARY KEY,
    kind         TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    state        TEXT NOT NULL DEFAULT 'pending',
    attempts     INTEGER NOT NULL DEFAULT 0,
    run_after    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    last_error   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_jobs_state_run_after ON jobs(state, run_after);

-- +goose Down
DROP TABLE jobs;
DROP TABLE list_entries;
DROP TABLE anime_cast;
DROP TABLE people;
DROP TABLE anime_relations;
DROP TABLE anime_titles;
DROP TABLE anime;
DROP TABLE settings;
