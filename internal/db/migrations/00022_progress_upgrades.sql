-- +goose Up
-- One row per progress upgrade a plugin declares (ProgressSpec.Upgrades)
-- that has been applied to this installation's items. The library applies
-- each upgrade once at startup and records it here, so an item the owner
-- switches back afterwards keeps that choice.
CREATE TABLE progress_upgrades (
    plugin_id  TEXT NOT NULL,
    kind       TEXT NOT NULL,
    upgrade_id TEXT NOT NULL,
    items      INTEGER NOT NULL DEFAULT 0,
    applied_at TEXT NOT NULL,
    PRIMARY KEY (plugin_id, kind, upgrade_id)
);

-- +goose Down
DROP TABLE progress_upgrades;
