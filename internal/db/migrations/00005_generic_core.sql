-- +goose Up
-- Generic library next to the legacy anime tables (dropped/renamed in a later
-- migration). people_g and relations_g get their final names then.
CREATE TABLE items (
    id            INTEGER PRIMARY KEY,
    plugin_id     TEXT NOT NULL,
    kind          TEXT NOT NULL,
    source        TEXT NOT NULL,
    source_id     TEXT NOT NULL,
    cover_path    TEXT NOT NULL DEFAULT '',
    external_url  TEXT NOT NULL DEFAULT '',
    sync_state    TEXT NOT NULL DEFAULT 'pending',
    progress_keys TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    UNIQUE (plugin_id, kind, source, source_id)
);

CREATE TABLE item_titles (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    lang    TEXT NOT NULL,
    text    TEXT NOT NULL,
    UNIQUE (item_id, lang, text)
);

CREATE TABLE item_fields (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    vtype   TEXT NOT NULL,
    text    TEXT,
    num     REAL,
    date    TEXT,
    seq     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (item_id, key, seq)
);
CREATE INDEX idx_item_fields_num  ON item_fields(key, num);
CREATE INDEX idx_item_fields_text ON item_fields(key, text);
CREATE INDEX idx_item_fields_date ON item_fields(key, date);

CREATE TABLE external_ids (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    type    TEXT NOT NULL,
    value   TEXT NOT NULL,
    PRIMARY KEY (item_id, type)
);
CREATE INDEX idx_external_ids_lookup ON external_ids(type, value);

CREATE TABLE people_g (
    id           INTEGER PRIMARY KEY,
    plugin_id    TEXT NOT NULL,
    source       TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    image_path   TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    UNIQUE (plugin_id, source, source_id)
);

CREATE TABLE credits (
    item_id               INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    seq                   INTEGER NOT NULL,
    role                  TEXT NOT NULL DEFAULT '',
    person_id             INTEGER NOT NULL REFERENCES people_g(id) ON DELETE CASCADE,
    counterpart_person_id INTEGER REFERENCES people_g(id) ON DELETE CASCADE,
    counterpart_role      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (item_id, seq)
);
CREATE INDEX idx_credits_person ON credits(person_id);
CREATE INDEX idx_credits_counterpart ON credits(counterpart_person_id);

CREATE TABLE relations_g (
    item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    kind        TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    source_id   TEXT NOT NULL DEFAULT '',
    kind_item   TEXT NOT NULL DEFAULT '',
    cover_url   TEXT NOT NULL DEFAULT '',
    url         TEXT NOT NULL DEFAULT '',
    titles_json TEXT NOT NULL DEFAULT '[]',
    fields_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (item_id, seq)
);

CREATE TABLE item_terms (
    item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind     TEXT NOT NULL CHECK (kind IN ('genre','tag')),
    term     TEXT NOT NULL,
    term_key TEXT NOT NULL,
    rank     INTEGER,
    spoiler  INTEGER NOT NULL DEFAULT 0,
    seq      INTEGER NOT NULL DEFAULT 0,
    UNIQUE (item_id, kind, term_key)
);
CREATE INDEX idx_item_terms_key ON item_terms(kind, term_key);

-- +goose Down
DROP TABLE item_terms;
DROP TABLE relations_g;
DROP TABLE credits;
DROP TABLE people_g;
DROP TABLE external_ids;
DROP TABLE item_fields;
DROP TABLE item_titles;
DROP TABLE items;
