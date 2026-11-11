-- +goose Up
-- TV schedules need a season: the same episode number can occur in two
-- seasons. Existing airings become season 0, which preserves anime UIDs.
CREATE TABLE airings_new (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    season  INTEGER NOT NULL DEFAULT 0,
    number  INTEGER NOT NULL,
    airs_at TEXT NOT NULL,
    PRIMARY KEY (item_id, season, number)
);
INSERT INTO airings_new(item_id, season, number, airs_at)
    SELECT item_id, 0, number, airs_at FROM airings;
DROP TABLE airings;
ALTER TABLE airings_new RENAME TO airings;

-- +goose Down
CREATE TABLE airings_old (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    number  INTEGER NOT NULL,
    airs_at TEXT NOT NULL,
    PRIMARY KEY (item_id, number)
);
INSERT INTO airings_old(item_id, number, airs_at)
    SELECT item_id, number, MIN(airs_at) FROM airings GROUP BY item_id, number;
DROP TABLE airings;
ALTER TABLE airings_old RENAME TO airings;
