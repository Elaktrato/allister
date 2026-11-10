-- +goose Up
-- Imports become plugin-aware: an import is run by one importer of one
-- plugin (imports.plugin_id, imports.importer_id), and each row names its
-- external id generically (import_rows.external_id_type + external_id,
-- formerly the MyAnimeList-only mal_id). import_rows.anime_id, which already
-- references items(id) since 00007, is renamed item_id. The job kind
-- import_yamtrack becomes the generic import_run.
ALTER TABLE imports ADD COLUMN plugin_id TEXT NOT NULL DEFAULT '';
ALTER TABLE imports ADD COLUMN importer_id TEXT NOT NULL DEFAULT '';
ALTER TABLE import_rows RENAME COLUMN mal_id TO external_id;
ALTER TABLE import_rows ADD COLUMN external_id_type TEXT NOT NULL DEFAULT '';
ALTER TABLE import_rows RENAME COLUMN anime_id TO item_id;

-- Legacy imports (importer_id '') all came from the Yamtrack importer of the
-- anime plugin. They keep importer_id '' as the marker of the old row shape:
-- their row JSON cannot be read by the generic runner, so they can never be
-- resumed. Unfinished ones (pending, running) are failed, and every pending
-- row of a legacy import (also of a failed or cancelled one, which the old
-- version could resume) is failed with the same reason.
UPDATE imports SET plugin_id = 'anime' WHERE importer_id = '';
UPDATE import_rows SET external_id_type = 'mal'
WHERE external_id <> '' AND import_id IN (SELECT id FROM imports WHERE importer_id = '');
UPDATE import_rows
SET outcome = 'failed', message = 'created by an older version; upload the file again'
WHERE outcome = 'pending' AND import_id IN (SELECT id FROM imports WHERE importer_id = '');
UPDATE imports
SET state = 'failed',
    finished_at = COALESCE(finished_at, strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    note = 'created by an older version; upload the file again'
WHERE importer_id = '' AND state IN ('pending', 'running');

-- Queued legacy import jobs (pending, running or failed) get the new kind and
-- a plugin_id ('anime' when absent) so the plugin gate pauses them. Their
-- import is failed now, so the import_run handler finishes them at once
-- without doing anything (it ignores imports in a final state): they end as
-- done jobs instead of failing as an unknown kind. Done rows are history and
-- stay; a row whose payload is not JSON or has no integer import_id is left
-- untouched and fails as an unknown kind.
UPDATE jobs
SET kind = 'import_run',
    payload_json = json_object(
        'import_id', json_extract(payload_json, '$.import_id'),
        'plugin_id', COALESCE(json_extract(payload_json, '$.plugin_id'), 'anime'))
WHERE kind = 'import_yamtrack'
  AND state IN ('pending', 'running', 'failed')
  AND CASE WHEN json_valid(payload_json)
           THEN json_type(payload_json, '$.import_id') = 'integer'
           ELSE 0 END;

-- +goose Down
-- Restores the legacy column names. Down is meant for empty import tables
-- (a database that has just been upgraded). With imports of the newer
-- version present it still works, but their row JSON has the generic shape
-- the older version cannot read: unfinished ones are failed and all their
-- pending rows too, so the older version never resumes them. Queued
-- import_run jobs get the legacy kind back (their payload already has the
-- legacy shape {"import_id","plugin_id"}); done rows stay.
UPDATE import_rows
SET outcome = 'failed', message = 'created by a newer version; upload the file again'
WHERE outcome = 'pending' AND import_id IN (SELECT id FROM imports WHERE importer_id <> '');
UPDATE imports
SET state = 'failed',
    finished_at = COALESCE(finished_at, strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    note = 'created by a newer version; upload the file again'
WHERE importer_id <> '' AND state IN ('pending', 'running');
UPDATE jobs SET kind = 'import_yamtrack'
WHERE kind = 'import_run' AND state IN ('pending', 'running', 'failed');
ALTER TABLE import_rows RENAME COLUMN item_id TO anime_id;
ALTER TABLE import_rows DROP COLUMN external_id_type;
ALTER TABLE import_rows RENAME COLUMN external_id TO mal_id;
ALTER TABLE imports DROP COLUMN importer_id;
ALTER TABLE imports DROP COLUMN plugin_id;
